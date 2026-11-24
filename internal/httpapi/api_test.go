package httpapi

// Integration tests: the real handler against real PostgreSQL and Redis.
// They run when TEST_DATABASE_URL and TEST_REDIS_URL are set (CI sets both;
// locally use `make test`) and are skipped otherwise.

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"github.com/timofeevilia/chia/internal/fx"
	"github.com/timofeevilia/chia/internal/store"
)

type env struct {
	t    *testing.T
	url  string
	pool *pgxpool.Pool
}

func setup(t *testing.T, cfg Config) *env {
	t.Helper()
	dbURL, redisURL := os.Getenv("TEST_DATABASE_URL"), os.Getenv("TEST_REDIS_URL")
	if dbURL == "" || redisURL == "" {
		t.Skip("TEST_DATABASE_URL and TEST_REDIS_URL not set")
	}
	ctx := context.Background()
	st, err := store.Open(ctx, dbURL)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	opts, err := redis.ParseURL(redisURL)
	if err != nil {
		t.Fatal(err)
	}
	rdb := redis.NewClient(opts)
	if err := rdb.FlushDB(ctx).Err(); err != nil {
		t.Fatal(err)
	}
	fxs := fx.NewService(rdb, fx.StaticSource{T: fx.Table{Rates: fx.DemoRates}}, time.Hour)
	h, err := NewHandler(st, rdb, fxs, slog.New(slog.NewTextHandler(io.Discard, nil)), cfg)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(h)
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { srv.Close(); pool.Close(); rdb.Close(); st.Close() })
	return &env{t: t, url: srv.URL, pool: pool}
}

// call sends a JSON request and decodes the response into out (if non-nil).
func (e *env) call(method, path string, body any, headers map[string]string, out any) (int, http.Header) {
	e.t.Helper()
	var rd io.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		rd = bytes.NewReader(raw)
	}
	req, _ := http.NewRequest(method, e.url+path, rd)
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if out != nil {
		if err := json.Unmarshal(raw, out); err != nil {
			e.t.Fatalf("%s %s: decode %q: %v", method, path, raw, err)
		}
	}
	return resp.StatusCode, resp.Header
}

func (e *env) newGroup(currency string, names ...string) Group {
	e.t.Helper()
	var g Group
	status, _ := e.call("POST", "/v1/groups",
		CreateGroupRequest{Name: "Test", BaseCurrency: currency, Members: names}, nil, &g)
	if status != 201 {
		e.t.Fatalf("create group: status %d", status)
	}
	return g
}

func (e *env) balances(g Group) map[uuid.UUID]int64 {
	e.t.Helper()
	var b Balances
	if status, _ := e.call("GET", "/v1/groups/"+g.Id.String()+"/balances", nil, nil, &b); status != 200 {
		e.t.Fatalf("balances: status %d", status)
	}
	out := map[uuid.UUID]int64{}
	var sum int64
	for _, x := range b.Balances {
		out[x.MemberId] = x.AmountMinor
		sum += x.AmountMinor
	}
	if sum != 0 {
		e.t.Fatalf("balances do not sum to zero: %v", out)
	}
	return out
}

func shares(ms ...Member) []Share {
	out := make([]Share, len(ms))
	for i, m := range ms {
		out[i] = Share{MemberId: m.Id}
	}
	return out
}

func TestExpenseSettleAndReverse(t *testing.T) {
	e := setup(t, Config{})
	g := e.newGroup("SGD", "An", "Binh", "Chi")
	an, binh, chi := g.Members[0], g.Members[1], g.Members[2]
	base := "/v1/groups/" + g.Id.String()

	// An pays 1 000 000 VND at an agreed rate: exactly 50.00 SGD, split three ways.
	rate := "0.00005"
	var tx Transaction
	status, _ := e.call("POST", base+"/expenses", CreateExpenseRequest{
		Description: "Seafood dinner", AmountMinor: 1_000_000, Currency: "VND", FxRate: &rate,
		PaidBy: an.Id, Shares: shares(an, binh, chi),
	}, nil, &tx)
	if status != 201 || tx.BaseAmountMinor != 5000 || tx.FxRate != rate {
		t.Fatalf("expense: status %d, %+v", status, tx)
	}
	b := e.balances(g)
	// 5000 / 3 = 1667 + 1667 + 1666: the spare cent goes to the first shares.
	if b[an.Id] != 5000-1667 || b[binh.Id] != -1667 || b[chi.Id] != -1666 {
		t.Fatalf("unexpected balances %v", b)
	}

	// Binh pays An back in full.
	status, _ = e.call("POST", base+"/settlements",
		CreateSettlementRequest{From: binh.Id, To: an.Id, AmountMinor: 1667}, nil, nil)
	if status != 201 {
		t.Fatalf("settlement: status %d", status)
	}
	if b = e.balances(g); b[binh.Id] != 0 || b[an.Id] != 1666 {
		t.Fatalf("after settlement: %v", b)
	}

	// Reversing the expense leaves only the settlement on the books.
	var rev Transaction
	status, _ = e.call("POST", base+"/transactions/"+tx.Id.String()+"/reversal", nil, nil, &rev)
	if status != 201 || rev.Kind != Reversal || rev.Reverses == nil || *rev.Reverses != tx.Id {
		t.Fatalf("reversal: status %d, %+v", status, rev)
	}
	if b = e.balances(g); b[binh.Id] != 1667 || b[an.Id] != -1667 || b[chi.Id] != 0 {
		t.Fatalf("after reversal: %v", b)
	}
	if status, _ = e.call("POST", base+"/transactions/"+tx.Id.String()+"/reversal", nil, nil, nil); status != 409 {
		t.Fatalf("second reversal: status %d, want 409", status)
	}
	if status, _ = e.call("POST", base+"/transactions/"+rev.Id.String()+"/reversal", nil, nil, nil); status != 409 {
		t.Fatalf("reversing a reversal: status %d, want 409", status)
	}

	// Paging walks all three transactions, newest first.
	var page TransactionPage
	e.call("GET", base+"/transactions?limit=2", nil, nil, &page)
	if len(page.Items) != 2 || page.NextCursor == nil || page.Items[0].Id != rev.Id {
		t.Fatalf("page 1: %+v", page)
	}
	var page2 TransactionPage
	e.call("GET", base+"/transactions?limit=2&cursor="+*page.NextCursor, nil, nil, &page2)
	if len(page2.Items) != 1 || page2.NextCursor != nil || page2.Items[0].ReversedBy == nil {
		t.Fatalf("page 2: %+v", page2)
	}
}

func TestValidation(t *testing.T) {
	e := setup(t, Config{})
	g := e.newGroup("VND", "An", "Binh")
	other := e.newGroup("VND", "Stranger")
	base := "/v1/groups/" + g.Id.String()
	cases := []struct {
		name string
		body any
		want int
	}{
		{"negative amount", map[string]any{"description": "x", "amount_minor": -1, "currency": "VND", "paid_by": g.Members[0].Id, "shares": shares(g.Members[0])}, 400},
		{"float amount", map[string]any{"description": "x", "amount_minor": 10.5, "currency": "VND", "paid_by": g.Members[0].Id, "shares": shares(g.Members[0])}, 400},
		{"unknown currency", map[string]any{"description": "x", "amount_minor": 10, "currency": "XXX", "paid_by": g.Members[0].Id, "shares": shares(g.Members[0])}, 400},
		{"no shares", map[string]any{"description": "x", "amount_minor": 10, "currency": "VND", "paid_by": g.Members[0].Id, "shares": []Share{}}, 400},
		{"member of another group", map[string]any{"description": "x", "amount_minor": 10, "currency": "VND", "paid_by": other.Members[0].Id, "shares": shares(g.Members[0])}, 422},
		{"below one minor unit", map[string]any{"description": "x", "amount_minor": 1, "currency": "SGD", "fx_rate": "0.001", "paid_by": g.Members[0].Id, "shares": shares(g.Members[0])}, 422},
	}
	for _, c := range cases {
		var errBody Error
		status, _ := e.call("POST", base+"/expenses", c.body, nil, &errBody)
		if status != c.want || errBody.Code == "" {
			t.Errorf("%s: status %d (%+v), want %d", c.name, status, errBody, c.want)
		}
	}
	if len(e.balances(g)) != 2 {
		t.Fatal("expected two members")
	}
	if status, _ := e.call("POST", base+"/members", AddMemberRequest{Name: "An"}, nil, nil); status != 409 {
		t.Errorf("duplicate member: status %d, want 409", status)
	}
}

// Many identical requests racing with one Idempotency-Key must write exactly once.
func TestIdempotencyUnderConcurrency(t *testing.T) {
	e := setup(t, Config{})
	g := e.newGroup("VND", "An", "Binh")
	path := "/v1/groups/" + g.Id.String() + "/expenses"
	body := CreateExpenseRequest{
		Description: "Taxi", AmountMinor: 90_000, Currency: "VND",
		PaidBy: g.Members[0].Id, Shares: shares(g.Members[0], g.Members[1]),
	}
	hdr := map[string]string{"Idempotency-Key": uuid.NewString()}

	var wg sync.WaitGroup
	statuses := make([]int, 30)
	for i := range statuses {
		wg.Add(1)
		go func() {
			defer wg.Done()
			statuses[i], _ = e.call("POST", path, body, hdr, nil)
		}()
	}
	wg.Wait()
	created := 0
	for _, s := range statuses {
		switch s {
		case 201:
			created++
		case 409: // arrived while the first was still in flight
		default:
			t.Fatalf("unexpected status %d", s)
		}
	}
	if created == 0 {
		t.Fatal("no request succeeded")
	}
	var n int
	if err := e.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM transactions WHERE group_id = $1`, g.Id).Scan(&n); err != nil || n != 1 {
		t.Fatalf("transactions written: %d (err %v), want exactly 1", n, err)
	}

	// A later retry is replayed; the same key with another body is refused.
	var replay Transaction
	status, h := e.call("POST", path, body, hdr, &replay)
	if status != 201 || h.Get("Idempotent-Replayed") != "true" {
		t.Fatalf("replay: status %d, header %q", status, h.Get("Idempotent-Replayed"))
	}
	body.AmountMinor = 1
	if status, _ = e.call("POST", path, body, hdr, nil); status != 422 {
		t.Fatalf("reused key with new body: status %d, want 422", status)
	}
	if b := e.balances(g); b[g.Members[0].Id] != 45_000 {
		t.Fatalf("balances after retries: %v", b)
	}
}

// With Redis out of the picture the database constraint alone must dedupe.
func TestIdempotencyFallsBackToDatabase(t *testing.T) {
	e := setup(t, Config{})
	ctx := context.Background()
	g := e.newGroup("VND", "An", "Binh")
	st, _ := store.Open(ctx, os.Getenv("TEST_DATABASE_URL"))
	defer st.Close()
	key := "db-level-key"
	n := store.NewTransaction{
		GroupID: g.Id, Kind: "settlement", Description: "Settlement",
		OriginalAmount: 500, OriginalCurrency: "VND", FxRate: "1", BaseAmount: 500, IdempotencyKey: &key,
		Postings: []store.Posting{{MemberID: g.Members[0].Id, Amount: 500}, {MemberID: g.Members[1].Id, Amount: -500}},
	}
	var wg sync.WaitGroup
	ids := make([]uuid.UUID, 16)
	createdCount := make([]bool, 16)
	for i := range ids {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tx, created, err := st.InsertTransaction(ctx, n)
			if err != nil {
				t.Error(err)
				return
			}
			ids[i], createdCount[i] = tx.ID, created
		}()
	}
	wg.Wait()
	created := 0
	for i := range ids {
		if ids[i] != ids[0] {
			t.Fatal("racing inserts returned different transactions")
		}
		if createdCount[i] {
			created++
		}
	}
	if created != 1 {
		t.Fatalf("%d inserts reported created, want 1", created)
	}
}

// The database itself refuses unbalanced or edited ledger rows.
func TestLedgerInvariantsEnforcedByDatabase(t *testing.T) {
	e := setup(t, Config{})
	ctx := context.Background()
	g := e.newGroup("VND", "An", "Binh")

	tx, err := e.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var id uuid.UUID
	err = tx.QueryRow(ctx, `INSERT INTO transactions
		(group_id, kind, description, original_amount, original_currency, fx_rate, base_amount)
		VALUES ($1, 'expense', 'bad', 100, 'VND', 1, 100) RETURNING id`, g.Id).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO postings (group_id, transaction_id, member_id, amount)
		VALUES ($1, $2, $3, 100)`, g.Id, id, g.Members[0].Id); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err == nil || !strings.Contains(err.Error(), "unbalanced") {
		t.Fatalf("unbalanced transaction committed (err %v)", err)
	}

	status, _ := e.call("POST", "/v1/groups/"+g.Id.String()+"/settlements",
		CreateSettlementRequest{From: g.Members[0].Id, To: g.Members[1].Id, AmountMinor: 10}, nil, nil)
	if status != 201 {
		t.Fatalf("settlement: %d", status)
	}
	for _, q := range []string{
		`UPDATE postings SET amount = amount * 2 WHERE group_id = $1`,
		`DELETE FROM postings WHERE group_id = $1`,
		`DELETE FROM transactions WHERE group_id = $1`,
	} {
		if _, err := e.pool.Exec(ctx, q, g.Id); err == nil || !strings.Contains(err.Error(), "append-only") {
			t.Errorf("%q was allowed (err %v)", q, err)
		}
	}
}

// Concurrent writers to one group never lose or invent money.
func TestConcurrentWritesKeepLedgerBalanced(t *testing.T) {
	e := setup(t, Config{})
	g := e.newGroup("SGD", "An", "Binh", "Chi", "Dung")
	path := "/v1/groups/" + g.Id.String() + "/expenses"
	const workers, perWorker = 8, 25
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				status, _ := e.call("POST", path, CreateExpenseRequest{
					Description: fmt.Sprintf("w%d-%d", w, i), AmountMinor: int64(1000 + 7*i + w), Currency: "USD",
					PaidBy: g.Members[(w+i)%4].Id, Shares: shares(g.Members...),
				}, nil, nil)
				if status != 201 {
					t.Errorf("status %d", status)
				}
			}
		}()
	}
	wg.Wait()
	e.balances(g) // asserts the zero-sum invariant
	var n int
	_ = e.pool.QueryRow(context.Background(), `SELECT count(*) FROM transactions WHERE group_id = $1`, g.Id).Scan(&n)
	if n != workers*perWorker {
		t.Fatalf("%d transactions, want %d", n, workers*perWorker)
	}
}

func TestRateLimit(t *testing.T) {
	e := setup(t, Config{RateLimitPerMinute: 5})
	var last int
	var hdr http.Header
	for i := 0; i < 6; i++ {
		last, hdr = e.call("GET", "/v1/currencies", nil, nil, nil)
	}
	if last != 429 || hdr.Get("Retry-After") == "" {
		t.Fatalf("6th request: status %d, Retry-After %q", last, hdr.Get("Retry-After"))
	}
}

func TestPaymentQR(t *testing.T) {
	e := setup(t, Config{})
	g := e.newGroup("SGD", "An", "Mei")
	base := "/v1/groups/" + g.Id.String() + "/members/"

	if status, _ := e.call("GET", base+g.Members[0].Id.String()+"/payment-qr?amount_minor=100", nil, nil, nil); status != 422 {
		t.Fatalf("QR without payout details: status %d, want 422", status)
	}
	bin, acct := "970436", "1234567890"
	e.call("PATCH", base+g.Members[0].Id.String(), UpdateMemberRequest{
		Payout: &Payout{Method: PayoutMethodVietqr, BankBin: &bin, Account: &acct}}, nil, nil)
	pt, pv := Mobile, "+6591234567"
	e.call("PATCH", base+g.Members[1].Id.String(), UpdateMemberRequest{
		Payout: &Payout{Method: PayoutMethodPaynow, ProxyType: &pt, ProxyValue: &pv}}, nil, nil)

	var vn, sg PaymentQr
	e.call("GET", base+g.Members[0].Id.String()+"/payment-qr?amount_minor=1300", nil, nil, &vn)
	// 13.00 SGD at the demo rate (25400 / 1.3 VND per SGD) is 254 000 VND.
	if vn.Currency != "VND" || vn.AmountMinor != 254_000 || vn.FxRate == nil || !strings.Contains(vn.Payload, "5406254000") {
		t.Fatalf("VietQR: %+v", vn)
	}
	e.call("GET", base+g.Members[1].Id.String()+"/payment-qr?amount_minor=1300", nil, nil, &sg)
	if sg.Currency != "SGD" || sg.AmountMinor != 1300 || sg.FxRate != nil || !strings.Contains(sg.Payload, "540513.00") {
		t.Fatalf("PayNow: %+v", sg)
	}
	bad := "91234567"
	if status, _ := e.call("PATCH", base+g.Members[1].Id.String(), UpdateMemberRequest{
		Payout: &Payout{Method: PayoutMethodPaynow, ProxyType: &pt, ProxyValue: &bad}}, nil, nil); status != 400 {
		t.Fatalf("invalid PayNow number: status %d, want 400", status)
	}
}

func TestEventsStream(t *testing.T) {
	e := setup(t, Config{})
	g := e.newGroup("VND", "An", "Binh")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", e.url+"/v1/groups/"+g.Id.String()+"/events", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	rd := bufio.NewReader(resp.Body)
	if line, _ := rd.ReadString('\n'); !strings.HasPrefix(line, ": connected") {
		t.Fatalf("unexpected first line %q", line)
	}
	e.call("POST", "/v1/groups/"+g.Id.String()+"/settlements",
		CreateSettlementRequest{From: g.Members[0].Id, To: g.Members[1].Id, AmountMinor: 10}, nil, nil)
	for {
		line, err := rd.ReadString('\n')
		if err != nil {
			t.Fatalf("stream ended before the event arrived: %v", err)
		}
		if strings.HasPrefix(line, "data:") {
			if !strings.Contains(line, "transaction") {
				t.Fatalf("unexpected event %q", line)
			}
			return
		}
	}
}
