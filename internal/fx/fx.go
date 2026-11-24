// Package fx provides exchange rates, cached in Redis so the upstream API is hit
// at most once per TTL regardless of how many API instances are running.
package fx

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"time"

	"github.com/redis/go-redis/v9"
)

var (
	ErrUnavailable = errors.New("fx: rates unavailable")
	ErrUnknown     = errors.New("fx: no rate for currency")
)

// Table is a set of rates against one pivot currency (units per 1 USD).
type Table struct {
	AsOf  time.Time              `json:"as_of"`
	Rates map[string]json.Number `json:"rates"`
}

// Source fetches a fresh table.
type Source interface {
	Fetch(ctx context.Context) (*Table, error)
}

// Rate returns units of `to` per one unit of `from` as an exact rational.
func (t *Table) Rate(from, to string) (*big.Rat, error) {
	if from == to {
		return big.NewRat(1, 1), nil
	}
	f, okF := new(big.Rat).SetString(t.Rates[from].String())
	g, okT := new(big.Rat).SetString(t.Rates[to].String())
	if !okF || !okT || f.Sign() <= 0 || g.Sign() <= 0 {
		return nil, ErrUnknown
	}
	return g.Quo(g, f), nil
}

// Service serves rates from Redis, refreshing from the source when the cached
// table expires. If the source is down it keeps serving the last known table:
// a slightly stale rate is better than refusing to record an expense.
type Service struct {
	rdb    *redis.Client
	source Source
	ttl    time.Duration
}

const (
	freshKey = "fx:table:fresh"
	staleKey = "fx:table:last"
)

func NewService(rdb *redis.Client, source Source, ttl time.Duration) *Service {
	return &Service{rdb: rdb, source: source, ttl: ttl}
}

func (s *Service) Table(ctx context.Context) (*Table, error) {
	if t, err := s.load(ctx, freshKey); err == nil {
		return t, nil
	}
	t, err := s.source.Fetch(ctx)
	if err != nil {
		if stale, sErr := s.load(ctx, staleKey); sErr == nil {
			return stale, nil
		}
		return nil, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	if raw, err := json.Marshal(t); err == nil {
		// Cache writes are best effort; a Redis outage must not fail the request.
		s.rdb.Set(ctx, freshKey, raw, s.ttl)
		s.rdb.Set(ctx, staleKey, raw, 30*24*time.Hour)
	}
	return t, nil
}

func (s *Service) load(ctx context.Context, key string) (*Table, error) {
	raw, err := s.rdb.Get(ctx, key).Bytes()
	if err != nil {
		return nil, err
	}
	var t Table
	if err := json.Unmarshal(raw, &t); err != nil {
		return nil, err
	}
	return &t, nil
}

// Rate is a convenience wrapper around Table().Rate.
func (s *Service) Rate(ctx context.Context, from, to string) (*big.Rat, time.Time, error) {
	if from == to {
		return big.NewRat(1, 1), time.Now().UTC(), nil
	}
	t, err := s.Table(ctx)
	if err != nil {
		return nil, time.Time{}, err
	}
	r, err := t.Rate(from, to)
	return r, t.AsOf, err
}

// HTTPSource reads the free open.er-api.com feed (USD pivot, updated daily).
type HTTPSource struct {
	URL    string
	Client *http.Client
}

func NewHTTPSource(url string) *HTTPSource {
	return &HTTPSource{URL: url, Client: &http.Client{Timeout: 5 * time.Second}}
}

func (h *HTTPSource) Fetch(ctx context.Context) (*Table, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, h.URL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := h.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fx source returned %s", resp.Status)
	}
	var body struct {
		Result string                 `json:"result"`
		Unix   int64                  `json:"time_last_update_unix"`
		Rates  map[string]json.Number `json:"rates"`
	}
	dec := json.NewDecoder(resp.Body)
	dec.UseNumber()
	if err := dec.Decode(&body); err != nil {
		return nil, err
	}
	if body.Result != "success" || len(body.Rates) == 0 {
		return nil, errors.New("fx source returned no rates")
	}
	return &Table{AsOf: time.Unix(body.Unix, 0).UTC(), Rates: body.Rates}, nil
}

// StaticSource serves a fixed table; used in tests and load tests.
type StaticSource struct{ T Table }

func (s StaticSource) Fetch(context.Context) (*Table, error) {
	t := s.T
	if t.AsOf.IsZero() {
		t.AsOf = time.Now().UTC()
	}
	return &t, nil
}

// DemoRates is an approximate table for running without network access.
var DemoRates = map[string]json.Number{
	"USD": "1", "SGD": "1.3", "VND": "25400", "EUR": "0.92", "GBP": "0.78", "JPY": "150",
	"KRW": "1380", "THB": "34", "MYR": "4.4", "IDR": "16300", "PHP": "58", "CNY": "7.2",
	"AUD": "1.52", "HKD": "7.8",
}
