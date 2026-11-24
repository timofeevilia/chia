// Package httpapi is the HTTP layer. Request/response types and routing are
// generated from api/openapi.yaml (api.gen.go); this file wires middleware.
package httpapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/google/uuid"
	nethttpmw "github.com/oapi-codegen/nethttp-middleware"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/redis/go-redis/v9"

	"github.com/timofeevilia/chia/internal/fx"
	"github.com/timofeevilia/chia/internal/store"
)

type Config struct {
	// RateLimitPerMinute caps requests per client IP; 0 disables the limiter.
	RateLimitPerMinute int
	// CORSOrigins lists allowed browser origins; "*" allows any.
	CORSOrigins []string
	// TrustProxy makes the limiter use X-Forwarded-For (set behind a reverse proxy).
	TrustProxy bool
}

const maxBodyBytes = 64 << 10

// NewHandler builds the complete HTTP handler.
func NewHandler(st *store.Store, rdb *redis.Client, fxs *fx.Service, log *slog.Logger, cfg Config) (http.Handler, error) {
	srv := &Server{store: st, rdb: rdb, fx: fxs, log: log}

	spec, err := GetSpec()
	if err != nil {
		return nil, err
	}
	spec.Servers = nil // validate paths regardless of the host the request came to
	specJSON, err := json.Marshal(spec)
	if err != nil {
		return nil, err
	}

	// Requests are checked against the OpenAPI document, so the published
	// contract and the enforced one cannot drift apart.
	validate := nethttpmw.OapiRequestValidatorWithOptions(spec, &nethttpmw.Options{
		Options: openapi3filter.Options{AuthenticationFunc: openapi3filter.NoopAuthenticationFunc},
		ErrorHandler: func(w http.ResponseWriter, message string, status int) {
			code := "validation_failed"
			if status == http.StatusNotFound {
				code = "not_found"
			}
			writeError(w, status, code, message)
		},
	})

	strict := NewStrictHandlerWithOptions(srv, nil, StrictHTTPServerOptions{
		RequestErrorHandlerFunc: func(w http.ResponseWriter, _ *http.Request, err error) {
			writeError(w, http.StatusBadRequest, "validation_failed", err.Error())
		},
		ResponseErrorHandlerFunc: func(w http.ResponseWriter, r *http.Request, err error) {
			var ae *apiError
			if errors.As(err, &ae) {
				writeError(w, ae.Status, ae.Code, ae.Message)
				return
			}
			log.Error("request failed", "path", r.URL.Path, "err", err)
			writeError(w, http.StatusInternalServerError, "internal", "internal error")
		},
	})

	api := http.NewServeMux()
	HandlerWithOptions(strict, StdHTTPServerOptions{
		BaseRouter: api,
		ErrorHandlerFunc: func(w http.ResponseWriter, _ *http.Request, err error) {
			writeError(w, http.StatusBadRequest, "validation_failed", err.Error())
		},
	})

	reg := prometheus.NewRegistry()
	metrics := newMetrics(reg)

	root := http.NewServeMux()
	root.Handle("GET /metrics", promhttp.HandlerFor(reg, promhttp.HandlerOpts{}))
	// Liveness only: touches no dependency, so platform health checks and
	// keep-alive pings never wake the database. /healthz is the deep check.
	root.HandleFunc("GET /livez", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "ok")
	})
	root.HandleFunc("GET /openapi.json", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(specJSON)
	})
	root.HandleFunc("GET /docs", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, docsHTML)
	})
	root.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/docs", http.StatusFound)
	})
	root.HandleFunc("GET /v1/groups/{groupId}/events", srv.events)
	// Everything else is the generated API behind its middleware chain.
	root.Handle("/", chain(api,
		rateLimit(rdb, cfg, log),
		limitBody,
		validate,
		idempotency(rdb, log),
	))

	return chain(root, recoverPanic(log), accessLog(log, metrics), cors(cfg.CORSOrigins)), nil
}

// chain applies middleware so that the first one listed runs first.
func chain(h http.Handler, mws ...func(http.Handler) http.Handler) http.Handler {
	for i := len(mws) - 1; i >= 0; i-- {
		h = mws[i](h)
	}
	return h
}

const docsHTML = `<!doctype html>
<html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>Chia API</title></head>
<body><script id="api-reference" data-url="/openapi.json"></script>
<script src="https://cdn.jsdelivr.net/npm/@scalar/api-reference"></script></body></html>`

// ---- observability ----

type metrics struct {
	requests *prometheus.CounterVec
	duration *prometheus.HistogramVec
}

func newMetrics(reg *prometheus.Registry) *metrics {
	m := &metrics{
		requests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "chia_http_requests_total", Help: "HTTP requests by route and status.",
		}, []string{"method", "route", "status"}),
		duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name: "chia_http_request_duration_seconds", Help: "HTTP request latency by route.",
			Buckets: []float64{.005, .01, .025, .05, .1, .25, .5, 1, 2.5},
		}, []string{"method", "route"}),
	}
	reg.MustRegister(m.requests, m.duration)
	return m
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.ResponseWriter.Write(b)
}

// Unwrap lets http.ResponseController reach Flush for server-sent events.
func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func accessLog(log *slog.Logger, m *metrics) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			id := r.Header.Get("X-Request-Id")
			if id == "" || len(id) > 64 {
				id = uuid.NewString()
			}
			w.Header().Set("X-Request-Id", id)
			sw := &statusWriter{ResponseWriter: w}
			next.ServeHTTP(sw, r)

			// The mux records the matched pattern on the request; using it (not the
			// raw path) keeps metric cardinality bounded.
			route := r.Pattern
			if i := strings.IndexByte(route, ' '); i >= 0 {
				route = route[i+1:]
			}
			if route == "" || route == "/" {
				route = "unmatched"
			}
			elapsed := time.Since(start)
			m.requests.WithLabelValues(r.Method, route, strconv.Itoa(sw.status)).Inc()
			m.duration.WithLabelValues(r.Method, route).Observe(elapsed.Seconds())
			if r.URL.Path != "/healthz" && r.URL.Path != "/livez" && r.URL.Path != "/metrics" {
				log.Info("request", "method", r.Method, "path", r.URL.Path, "status", sw.status,
					"ms", elapsed.Milliseconds(), "request_id", id)
			}
		})
	}
}

func recoverPanic(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if v := recover(); v != nil && v != http.ErrAbortHandler {
					log.Error("panic", "value", v, "path", r.URL.Path)
					writeError(w, http.StatusInternalServerError, "internal", "internal error")
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

// ---- cross-cutting middleware ----

func cors(origins []string) func(http.Handler) http.Handler {
	allowed := map[string]bool{}
	for _, o := range origins {
		allowed[strings.TrimRight(strings.TrimSpace(o), "/")] = true
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if origin := r.Header.Get("Origin"); origin != "" && (allowed["*"] || allowed[origin]) {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Add("Vary", "Origin")
				w.Header().Set("Access-Control-Expose-Headers", "Idempotent-Replayed, Retry-After, X-Request-Id")
				if r.Method == http.MethodOptions {
					w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PATCH, OPTIONS")
					w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Idempotency-Key")
					w.Header().Set("Access-Control-Max-Age", "86400")
					w.WriteHeader(http.StatusNoContent)
					return
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}

func limitBody(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
		next.ServeHTTP(w, r)
	})
}

func clientIP(r *http.Request, trustProxy bool) string {
	if trustProxy {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			first, _, _ := strings.Cut(xff, ",")
			return strings.TrimSpace(first)
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// The counter and its expiry are set in one script so a crash between the two
// commands cannot leave a key that never expires.
var rateScript = redis.NewScript(`
local n = redis.call('INCR', KEYS[1])
if n == 1 then redis.call('EXPIRE', KEYS[1], ARGV[1]) end
return n`)

// rateLimit is a fixed-window limiter shared by all API instances through Redis.
// It fails open: if Redis is unreachable, requests are allowed.
func rateLimit(rdb *redis.Client, cfg Config, log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		if cfg.RateLimitPerMinute <= 0 {
			return next
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/healthz" {
				next.ServeHTTP(w, r)
				return
			}
			now := time.Now()
			key := fmt.Sprintf("rl:%s:%d", clientIP(r, cfg.TrustProxy), now.Unix()/60)
			n, err := rateScript.Run(r.Context(), rdb, []string{key}, 60).Int()
			if err != nil {
				log.Warn("rate limiter unavailable", "err", err)
			} else if n > cfg.RateLimitPerMinute {
				w.Header().Set("Retry-After", strconv.FormatInt(60-now.Unix()%60, 10))
				writeError(w, http.StatusTooManyRequests, "rate_limited", "too many requests, slow down")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

type idemRecord struct {
	Fingerprint string `json:"fp"`
	Status      int    `json:"status,omitempty"` // 0 while the first request is still running
	Body        []byte `json:"body,omitempty"`
}

type captureWriter struct {
	http.ResponseWriter
	status int
	buf    bytes.Buffer
}

func (w *captureWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

func (w *captureWriter) Write(b []byte) (int, error) {
	w.buf.Write(b)
	return w.ResponseWriter.Write(b)
}

// idempotency makes POSTs carrying an Idempotency-Key safe to retry.
//
// The first request claims the key in Redis (SET NX) and its response is stored
// for 24h; retries get that response replayed. A retry that arrives while the
// first is still running gets 409, and a reused key with a different body gets
// 422. If Redis is down the request proceeds: the unique index on
// (group_id, idempotency_key) in Postgres still prevents a double write.
func idempotency(rdb *redis.Client, log *slog.Logger) func(http.Handler) http.Handler {
	const (
		pendingTTL = 30 * time.Second
		storedTTL  = 24 * time.Hour
	)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := r.Header.Get("Idempotency-Key")
			if r.Method != http.MethodPost || key == "" {
				next.ServeHTTP(w, r)
				return
			}
			body, err := io.ReadAll(r.Body)
			if err != nil {
				writeError(w, http.StatusRequestEntityTooLarge, "validation_failed", "request body too large")
				return
			}
			r.Body = io.NopCloser(bytes.NewReader(body))

			sum := sha256.Sum256(body)
			fp := hex.EncodeToString(sum[:])
			scope := sha256.Sum256([]byte(r.URL.Path + "\x00" + key))
			rkey := "idem:" + hex.EncodeToString(scope[:])
			ctx := r.Context()

			pending, _ := json.Marshal(idemRecord{Fingerprint: fp})
			claimed, err := rdb.SetNX(ctx, rkey, pending, pendingTTL).Result()
			if err != nil {
				log.Warn("idempotency store unavailable", "err", err)
				next.ServeHTTP(w, r)
				return
			}
			if !claimed {
				var rec idemRecord
				raw, err := rdb.Get(ctx, rkey).Bytes()
				if err != nil || json.Unmarshal(raw, &rec) != nil {
					// The key expired between the two commands; let the database decide.
					next.ServeHTTP(w, r)
					return
				}
				switch {
				case rec.Fingerprint != fp:
					writeError(w, http.StatusUnprocessableEntity, "idempotency_key_reused",
						"this Idempotency-Key was already used with a different request body")
				case rec.Status == 0:
					w.Header().Set("Retry-After", "1")
					writeError(w, http.StatusConflict, "request_in_progress",
						"a request with this Idempotency-Key is still being processed")
				default:
					w.Header().Set("Content-Type", "application/json")
					w.Header().Set("Idempotent-Replayed", "true")
					w.WriteHeader(rec.Status)
					_, _ = w.Write(rec.Body)
				}
				return
			}

			cw := &captureWriter{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(cw, r)

			// The client may have gone away; the bookkeeping must still complete.
			bg, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
			defer cancel()
			if cw.status >= 500 {
				rdb.Del(bg, rkey) // nothing was committed; allow a clean retry
				return
			}
			done, _ := json.Marshal(idemRecord{Fingerprint: fp, Status: cw.status, Body: cw.buf.Bytes()})
			rdb.Set(bg, rkey, done, storedTTL)
		})
	}
}

// ---- live updates ----

// events streams change notifications for one group as server-sent events.
// Writers publish to a Redis channel, so every API instance can serve any
// subscriber.
func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	groupID, err := uuid.Parse(r.PathValue("groupId"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "validation_failed", "invalid group id")
		return
	}
	if _, err := s.store.GetGroup(r.Context(), groupID); err != nil {
		writeError(w, http.StatusNotFound, "not_found", "group not found")
		return
	}
	sub := s.rdb.Subscribe(r.Context(), eventChannel(groupID))
	defer sub.Close()

	rc := http.NewResponseController(w)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, ": connected\n\n")
	_ = rc.Flush()

	heartbeat := time.NewTicker(25 * time.Second)
	defer heartbeat.Stop()
	ch := sub.Channel()
	for {
		select {
		case <-r.Context().Done():
			return
		case msg, ok := <-ch:
			if !ok {
				return
			}
			fmt.Fprintf(w, "event: change\ndata: %s\n\n", msg.Payload)
		case <-heartbeat.C:
			fmt.Fprint(w, ": ping\n\n")
		}
		if rc.Flush() != nil {
			return
		}
	}
}
