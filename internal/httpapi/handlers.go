package httpapi

//go:generate go tool oapi-codegen -config oapi.yaml ../../api/openapi.yaml

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"github.com/timofeevilia/chia/internal/fx"
	"github.com/timofeevilia/chia/internal/ledger"
	"github.com/timofeevilia/chia/internal/money"
	"github.com/timofeevilia/chia/internal/qr"
	"github.com/timofeevilia/chia/internal/store"
)

// apiError is returned by handlers and rendered as the Error schema.
type apiError struct {
	Status  int
	Code    string
	Message string
}

func (e *apiError) Error() string { return e.Message }

func fail(status int, code, format string, args ...any) *apiError {
	return &apiError{Status: status, Code: code, Message: fmt.Sprintf(format, args...)}
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(Error{Code: code, Message: message})
}

// Server implements StrictServerInterface.
type Server struct {
	store *store.Store
	rdb   *redis.Client
	fx    *fx.Service
	log   *slog.Logger
}

var _ StrictServerInterface = (*Server)(nil)

// mapStoreErr translates persistence errors into API errors.
func mapStoreErr(err error) error {
	switch {
	case errors.Is(err, store.ErrNotFound):
		return fail(http.StatusNotFound, "not_found", "group, member or transaction not found")
	case errors.Is(err, store.ErrNameTaken):
		return fail(http.StatusConflict, "name_taken", "a member with this name already exists in the group")
	case errors.Is(err, store.ErrUnknownMember):
		return fail(http.StatusUnprocessableEntity, "unknown_member", "a referenced member is not part of this group")
	case errors.Is(err, store.ErrAlreadyReversed):
		return fail(http.StatusConflict, "already_reversed", "this transaction has already been reversed")
	case errors.Is(err, store.ErrNotReversible):
		return fail(http.StatusConflict, "not_reversible", "a reversal cannot be reversed")
	}
	return err
}

func mapFxErr(err error) error {
	if errors.Is(err, fx.ErrUnknown) {
		return fail(http.StatusBadRequest, "unsupported_currency", "no exchange rate for this currency pair")
	}
	return fail(http.StatusServiceUnavailable, "fx_unavailable", "exchange rates are temporarily unavailable; pass fx_rate explicitly")
}

func lookupCurrency(code string) (money.Currency, error) {
	c, ok := money.Lookup(code)
	if !ok {
		return c, fail(http.StatusBadRequest, "unsupported_currency", "currency %q is not supported", code)
	}
	return c, nil
}

func cleanName(s string) (string, error) {
	s = strings.Join(strings.Fields(s), " ")
	if s == "" {
		return "", fail(http.StatusBadRequest, "validation_failed", "name must not be blank")
	}
	return s, nil
}

// publish notifies live subscribers (SSE) that a group changed. Best effort.
func (s *Server) publish(ctx context.Context, groupID uuid.UUID, kind string) {
	msg, _ := json.Marshal(map[string]string{"type": kind})
	if err := s.rdb.Publish(ctx, eventChannel(groupID), msg).Err(); err != nil {
		s.log.Warn("publish failed", "err", err)
	}
}

func eventChannel(groupID uuid.UUID) string { return "group:" + groupID.String() }

// ---- conversions ----

func toMember(m store.Member) Member {
	out := Member{Id: m.ID, Name: m.Name}
	if p := m.Payout; p != nil {
		out.Payout = &Payout{Method: PayoutMethod(p.Method)}
		if p.BankBIN != "" {
			out.Payout.BankBin = &p.BankBIN
			out.Payout.Account = &p.Account
		}
		if p.ProxyType != "" {
			pt := PayoutProxyType(p.ProxyType)
			out.Payout.ProxyType = &pt
			out.Payout.ProxyValue = &p.ProxyValue
		}
	}
	return out
}

func toGroup(g *store.Group) Group {
	out := Group{Id: g.ID, Name: g.Name, BaseCurrency: g.BaseCurrency, CreatedAt: g.CreatedAt, Members: []Member{}}
	for _, m := range g.Members {
		out.Members = append(out.Members, toMember(m))
	}
	return out
}

func toTransaction(t *store.Transaction) Transaction {
	out := Transaction{
		Id: t.ID, Kind: TransactionKind(t.Kind), Description: t.Description,
		OriginalAmountMinor: t.OriginalAmount, OriginalCurrency: t.OriginalCurrency,
		FxRate: t.FxRate, BaseAmountMinor: t.BaseAmount,
		Reverses: t.Reverses, ReversedBy: t.ReversedBy,
		CreatedAt: t.CreatedAt, Postings: []Posting{},
	}
	for _, p := range t.Postings {
		out.Postings = append(out.Postings, Posting{MemberId: p.MemberID, AmountMinor: p.Amount})
	}
	return out
}

// ---- reference & ops ----

func (s *Server) GetHealth(ctx context.Context, _ GetHealthRequestObject) (GetHealthResponseObject, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	h := Health{
		Postgres: s.store.Ping(ctx) == nil,
		Redis:    s.rdb.Ping(ctx).Err() == nil,
		Status:   Ok,
	}
	if !h.Postgres || !h.Redis {
		h.Status = Degraded
	}
	// Postgres is the only hard dependency: Redis-backed features fail open.
	if !h.Postgres {
		return GetHealth503JSONResponse(h), nil
	}
	return GetHealth200JSONResponse(h), nil
}

func (s *Server) ListCurrencies(context.Context, ListCurrenciesRequestObject) (ListCurrenciesResponseObject, error) {
	out := ListCurrencies200JSONResponse{}
	for _, c := range money.Currencies() {
		out = append(out, Currency{Code: c.Code, Exponent: c.Exponent, Name: c.Name})
	}
	return out, nil
}

func (s *Server) GetFxRate(ctx context.Context, req GetFxRateRequestObject) (GetFxRateResponseObject, error) {
	for _, code := range []string{req.Params.From, req.Params.To} {
		if _, err := lookupCurrency(code); err != nil {
			return nil, err
		}
	}
	rate, asOf, err := s.fx.Rate(ctx, req.Params.From, req.Params.To)
	if err != nil {
		return nil, mapFxErr(err)
	}
	return GetFxRate200JSONResponse{From: req.Params.From, To: req.Params.To, Rate: money.FormatRate(rate), AsOf: asOf}, nil
}

// ---- groups ----

func (s *Server) CreateGroup(ctx context.Context, req CreateGroupRequestObject) (CreateGroupResponseObject, error) {
	if _, err := lookupCurrency(req.Body.BaseCurrency); err != nil {
		return nil, err
	}
	name, err := cleanName(req.Body.Name)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	members := make([]string, 0, len(req.Body.Members))
	for _, m := range req.Body.Members {
		m, err := cleanName(m)
		if err != nil {
			return nil, err
		}
		if seen[m] {
			return nil, fail(http.StatusBadRequest, "validation_failed", "member name %q is repeated", m)
		}
		seen[m] = true
		members = append(members, m)
	}
	g, err := s.store.CreateGroup(ctx, name, req.Body.BaseCurrency, members)
	if err != nil {
		return nil, mapStoreErr(err)
	}
	return CreateGroup201JSONResponse(toGroup(g)), nil
}

func (s *Server) GetGroup(ctx context.Context, req GetGroupRequestObject) (GetGroupResponseObject, error) {
	g, err := s.store.GetGroup(ctx, req.GroupId)
	if err != nil {
		return nil, mapStoreErr(err)
	}
	return GetGroup200JSONResponse(toGroup(g)), nil
}

func (s *Server) AddMember(ctx context.Context, req AddMemberRequestObject) (AddMemberResponseObject, error) {
	name, err := cleanName(req.Body.Name)
	if err != nil {
		return nil, err
	}
	m, err := s.store.AddMember(ctx, req.GroupId, name)
	if err != nil {
		return nil, mapStoreErr(err)
	}
	s.publish(ctx, req.GroupId, "member")
	return AddMember201JSONResponse(toMember(*m)), nil
}

func (s *Server) UpdateMember(ctx context.Context, req UpdateMemberRequestObject) (UpdateMemberResponseObject, error) {
	var name *string
	if req.Body.Name != nil {
		n, err := cleanName(*req.Body.Name)
		if err != nil {
			return nil, err
		}
		name = &n
	}
	var payout *store.Payout
	if p := req.Body.Payout; p != nil {
		payout = &store.Payout{Method: string(p.Method)}
		var err error
		switch p.Method {
		case PayoutMethodVietqr:
			if p.BankBin == nil || p.Account == nil {
				return nil, fail(http.StatusBadRequest, "validation_failed", "vietqr needs bank_bin and account")
			}
			payout.BankBIN, payout.Account = *p.BankBin, *p.Account
			_, err = qr.VietQR(payout.BankBIN, payout.Account, "1", "")
		case PayoutMethodPaynow:
			if p.ProxyType == nil || p.ProxyValue == nil {
				return nil, fail(http.StatusBadRequest, "validation_failed", "paynow needs proxy_type and proxy_value")
			}
			payout.ProxyType, payout.ProxyValue = string(*p.ProxyType), *p.ProxyValue
			_, err = qr.PayNow(payout.ProxyType, payout.ProxyValue, "1.00", "", time.Now())
		}
		if err != nil {
			return nil, fail(http.StatusBadRequest, "validation_failed",
				"payout details are not valid (PayNow mobile numbers look like +6591234567)")
		}
	}
	m, err := s.store.UpdateMember(ctx, req.GroupId, req.MemberId, name, payout)
	if err != nil {
		return nil, mapStoreErr(err)
	}
	s.publish(ctx, req.GroupId, "member")
	return UpdateMember200JSONResponse(toMember(*m)), nil
}

// ---- ledger ----

func (s *Server) CreateExpense(ctx context.Context, req CreateExpenseRequestObject) (CreateExpenseResponseObject, error) {
	b := req.Body
	g, err := s.store.GetGroup(ctx, req.GroupId)
	if err != nil {
		return nil, mapStoreErr(err)
	}
	from, err := lookupCurrency(b.Currency)
	if err != nil {
		return nil, err
	}
	base, _ := money.Lookup(g.BaseCurrency)

	// Lock in the exchange rate: the caller's, or today's market rate.
	var rate *big.Rat
	switch {
	case b.FxRate != nil:
		if rate, err = money.ParseRate(*b.FxRate); err != nil {
			return nil, fail(http.StatusBadRequest, "validation_failed", "fx_rate must be a positive decimal string")
		}
	default:
		if rate, _, err = s.fx.Rate(ctx, from.Code, base.Code); err != nil {
			return nil, mapFxErr(err)
		}
	}
	if rate, err = money.QuantizeRate(rate); err != nil {
		return nil, fail(http.StatusBadRequest, "validation_failed", "fx_rate is too small to represent")
	}
	baseAmount, err := money.Convert(b.AmountMinor, from, base, rate)
	if err != nil {
		return nil, fail(http.StatusUnprocessableEntity, "amount_out_of_range", "converted amount is out of range")
	}
	if baseAmount == 0 {
		return nil, fail(http.StatusUnprocessableEntity, "amount_too_small",
			"amount is less than one minor unit of %s", base.Code)
	}

	weights := make([]int64, len(b.Shares))
	seen := map[uuid.UUID]bool{}
	for i, sh := range b.Shares {
		if seen[sh.MemberId] {
			return nil, fail(http.StatusBadRequest, "validation_failed", "member %s appears twice in shares", sh.MemberId)
		}
		seen[sh.MemberId] = true
		weights[i] = 1
		if sh.Weight != nil {
			weights[i] = int64(*sh.Weight)
		}
	}
	parts, err := money.Allocate(baseAmount, weights)
	if err != nil {
		return nil, fail(http.StatusBadRequest, "validation_failed", "share weights must be positive")
	}

	// The payer is owed the full amount; each participant owes their part.
	postings := []store.Posting{{MemberID: b.PaidBy, Amount: baseAmount}}
	for i, sh := range b.Shares {
		if parts[i] != 0 {
			postings = append(postings, store.Posting{MemberID: sh.MemberId, Amount: -parts[i]})
		}
	}
	desc := strings.TrimSpace(b.Description)
	if desc == "" {
		return nil, fail(http.StatusBadRequest, "validation_failed", "description must not be blank")
	}
	t, _, err := s.store.InsertTransaction(ctx, store.NewTransaction{
		GroupID: g.ID, Kind: "expense", Description: desc,
		OriginalAmount: b.AmountMinor, OriginalCurrency: from.Code,
		FxRate: money.FormatRate(rate), BaseAmount: baseAmount,
		IdempotencyKey: req.Params.IdempotencyKey, Postings: postings,
	})
	if err != nil {
		return nil, mapStoreErr(err)
	}
	s.publish(ctx, g.ID, "transaction")
	return CreateExpense201JSONResponse(toTransaction(t)), nil
}

func (s *Server) CreateSettlement(ctx context.Context, req CreateSettlementRequestObject) (CreateSettlementResponseObject, error) {
	b := req.Body
	if b.From == b.To {
		return nil, fail(http.StatusBadRequest, "validation_failed", "from and to must be different members")
	}
	g, err := s.store.GetGroup(ctx, req.GroupId)
	if err != nil {
		return nil, mapStoreErr(err)
	}
	t, _, err := s.store.InsertTransaction(ctx, store.NewTransaction{
		GroupID: g.ID, Kind: "settlement", Description: "Settlement",
		OriginalAmount: b.AmountMinor, OriginalCurrency: g.BaseCurrency,
		FxRate: "1", BaseAmount: b.AmountMinor,
		IdempotencyKey: req.Params.IdempotencyKey,
		// Paying reduces what the payer owes and what the receiver is owed.
		Postings: []store.Posting{{MemberID: b.From, Amount: b.AmountMinor}, {MemberID: b.To, Amount: -b.AmountMinor}},
	})
	if err != nil {
		return nil, mapStoreErr(err)
	}
	s.publish(ctx, g.ID, "transaction")
	return CreateSettlement201JSONResponse(toTransaction(t)), nil
}

func (s *Server) ReverseTransaction(ctx context.Context, req ReverseTransactionRequestObject) (ReverseTransactionResponseObject, error) {
	t, err := s.store.Reverse(ctx, req.GroupId, req.TransactionId)
	if err != nil {
		return nil, mapStoreErr(err)
	}
	s.publish(ctx, req.GroupId, "transaction")
	return ReverseTransaction201JSONResponse(toTransaction(t)), nil
}

func (s *Server) ListTransactions(ctx context.Context, req ListTransactionsRequestObject) (ListTransactionsResponseObject, error) {
	if _, err := s.store.GetGroup(ctx, req.GroupId); err != nil {
		return nil, mapStoreErr(err)
	}
	limit, cursor := 50, ""
	if req.Params.Limit != nil {
		limit = *req.Params.Limit
	}
	if req.Params.Cursor != nil {
		cursor = *req.Params.Cursor
	}
	items, next, err := s.store.ListTransactions(ctx, req.GroupId, cursor, limit)
	if err != nil {
		if strings.Contains(err.Error(), "invalid cursor") {
			return nil, fail(http.StatusBadRequest, "validation_failed", "cursor is not valid")
		}
		return nil, err
	}
	page := TransactionPage{Items: []Transaction{}}
	for i := range items {
		page.Items = append(page.Items, toTransaction(&items[i]))
	}
	if next != "" {
		page.NextCursor = &next
	}
	return ListTransactions200JSONResponse(page), nil
}

func (s *Server) GetBalances(ctx context.Context, req GetBalancesRequestObject) (GetBalancesResponseObject, error) {
	g, err := s.store.GetGroup(ctx, req.GroupId)
	if err != nil {
		return nil, mapStoreErr(err)
	}
	rows, err := s.store.Balances(ctx, req.GroupId)
	if err != nil {
		return nil, err
	}
	out := Balances{Currency: g.BaseCurrency, Balances: []Balance{}, Transfers: []Transfer{}}
	in := make([]ledger.Balance, 0, len(rows))
	for _, r := range rows {
		out.Balances = append(out.Balances, Balance{MemberId: r.MemberID, AmountMinor: r.Amount})
		in = append(in, ledger.Balance{Member: r.MemberID.String(), Amount: r.Amount})
	}
	for _, t := range ledger.Simplify(in) {
		out.Transfers = append(out.Transfers, Transfer{
			From: uuid.MustParse(t.From), To: uuid.MustParse(t.To), AmountMinor: t.Amount,
		})
	}
	return GetBalances200JSONResponse(out), nil
}

// ---- payments ----

func (s *Server) GetPaymentQr(ctx context.Context, req GetPaymentQrRequestObject) (GetPaymentQrResponseObject, error) {
	g, err := s.store.GetGroup(ctx, req.GroupId)
	if err != nil {
		return nil, mapStoreErr(err)
	}
	var payee *store.Member
	for i := range g.Members {
		if g.Members[i].ID == req.MemberId {
			payee = &g.Members[i]
		}
	}
	if payee == nil {
		return nil, mapStoreErr(store.ErrNotFound)
	}
	if payee.Payout == nil {
		return nil, fail(http.StatusUnprocessableEntity, "payout_not_set", "%s has not added payment details yet", payee.Name)
	}
	target := "VND"
	if payee.Payout.Method == "paynow" {
		target = "SGD"
	}
	base, _ := money.Lookup(g.BaseCurrency)
	to, _ := money.Lookup(target)

	out := PaymentQr{Scheme: PaymentQrScheme(payee.Payout.Method), Currency: target, AmountMinor: req.Params.AmountMinor}
	if base.Code != to.Code {
		rate, _, err := s.fx.Rate(ctx, base.Code, to.Code)
		if err != nil {
			return nil, mapFxErr(err)
		}
		if out.AmountMinor, err = money.Convert(req.Params.AmountMinor, base, to, rate); err != nil || out.AmountMinor == 0 {
			return nil, fail(http.StatusUnprocessableEntity, "amount_too_small", "amount cannot be expressed in %s", to.Code)
		}
		r := money.FormatRate(rate)
		out.FxRate = &r
	}
	amount := money.Format(out.AmountMinor, to)
	if payee.Payout.Method == "paynow" {
		out.Payload, err = qr.PayNow(payee.Payout.ProxyType, payee.Payout.ProxyValue, amount, "CHIA", time.Now().AddDate(1, 0, 0))
	} else {
		out.Payload, err = qr.VietQR(payee.Payout.BankBIN, payee.Payout.Account, amount, "CHIA")
	}
	if err != nil {
		return nil, fail(http.StatusUnprocessableEntity, "amount_out_of_range", "amount is too large for a QR payment")
	}
	return GetPaymentQr200JSONResponse(out), nil
}
