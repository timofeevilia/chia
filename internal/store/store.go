// Package store is the PostgreSQL persistence layer.
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"github.com/timofeevilia/chia/migrations"
)

var (
	ErrNotFound        = errors.New("store: not found")
	ErrNameTaken       = errors.New("store: member name already used in this group")
	ErrUnknownMember   = errors.New("store: member does not belong to this group")
	ErrAlreadyReversed = errors.New("store: transaction already reversed")
	ErrNotReversible   = errors.New("store: a reversal cannot be reversed")
)

type Payout struct {
	Method     string `json:"method"`
	BankBIN    string `json:"bank_bin,omitempty"`
	Account    string `json:"account,omitempty"`
	ProxyType  string `json:"proxy_type,omitempty"`
	ProxyValue string `json:"proxy_value,omitempty"`
}

type Member struct {
	ID     uuid.UUID
	Name   string
	Payout *Payout
}

type Group struct {
	ID           uuid.UUID
	Name         string
	BaseCurrency string
	CreatedAt    time.Time
	Members      []Member
}

type Posting struct {
	MemberID uuid.UUID `json:"member_id"`
	Amount   int64     `json:"amount_minor"`
}

type Transaction struct {
	ID               uuid.UUID
	Seq              int64
	Kind             string
	Description      string
	OriginalAmount   int64
	OriginalCurrency string
	FxRate           string
	BaseAmount       int64
	Reverses         *uuid.UUID
	ReversedBy       *uuid.UUID
	Postings         []Posting
	CreatedAt        time.Time
}

// NewTransaction is the input to InsertTransaction.
type NewTransaction struct {
	GroupID          uuid.UUID
	Kind             string
	Description      string
	OriginalAmount   int64
	OriginalCurrency string
	FxRate           string
	BaseAmount       int64
	Reverses         *uuid.UUID
	IdempotencyKey   *string
	Postings         []Posting
}

type Balance struct {
	MemberID uuid.UUID
	Amount   int64
}

type Store struct {
	pool *pgxpool.Pool
}

func Open(ctx context.Context, url string) (*Store, error) {
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		return nil, err
	}
	return &Store{pool: pool}, nil
}

func (s *Store) Close()                         { s.pool.Close() }
func (s *Store) Ping(ctx context.Context) error { return s.pool.Ping(ctx) }

// Migrate applies all pending migrations embedded in the binary.
func (s *Store) Migrate(ctx context.Context) error {
	db := stdlib.OpenDBFromPool(s.pool)
	defer db.Close()
	return migrate(ctx, db, "up")
}

// MigrateDown rolls back the most recent migration.
func (s *Store) MigrateDown(ctx context.Context) error {
	db := stdlib.OpenDBFromPool(s.pool)
	defer db.Close()
	return migrate(ctx, db, "down")
}

func migrate(ctx context.Context, db *sql.DB, direction string) error {
	p, err := goose.NewProvider(goose.DialectPostgres, db, migrations.FS)
	if err != nil {
		return err
	}
	if direction == "down" {
		_, err = p.Down(ctx)
	} else {
		_, err = p.Up(ctx)
	}
	return err
}

func (s *Store) CreateGroup(ctx context.Context, name, currency string, members []string) (*Group, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit

	g := &Group{Name: name, BaseCurrency: currency}
	err = tx.QueryRow(ctx,
		`INSERT INTO groups (name, base_currency) VALUES ($1, $2) RETURNING id, created_at`,
		name, currency).Scan(&g.ID, &g.CreatedAt)
	if err != nil {
		return nil, err
	}
	for _, m := range members {
		mem := Member{Name: m}
		err = tx.QueryRow(ctx,
			`INSERT INTO members (group_id, name) VALUES ($1, $2) RETURNING id`, g.ID, m).Scan(&mem.ID)
		if err != nil {
			return nil, mapErr(err)
		}
		g.Members = append(g.Members, mem)
	}
	return g, tx.Commit(ctx)
}

func (s *Store) GetGroup(ctx context.Context, id uuid.UUID) (*Group, error) {
	g := &Group{ID: id}
	err := s.pool.QueryRow(ctx,
		`SELECT name, base_currency, created_at FROM groups WHERE id = $1`, id).
		Scan(&g.Name, &g.BaseCurrency, &g.CreatedAt)
	if err != nil {
		return nil, mapErr(err)
	}
	rows, err := s.pool.Query(ctx,
		`SELECT id, name, payout FROM members WHERE group_id = $1 ORDER BY seq`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		m, err := scanMember(rows)
		if err != nil {
			return nil, err
		}
		g.Members = append(g.Members, *m)
	}
	return g, rows.Err()
}

func (s *Store) AddMember(ctx context.Context, groupID uuid.UUID, name string) (*Member, error) {
	m := &Member{Name: name}
	err := s.pool.QueryRow(ctx,
		`INSERT INTO members (group_id, name) VALUES ($1, $2) RETURNING id`, groupID, name).Scan(&m.ID)
	if err != nil {
		return nil, mapErr(err)
	}
	return m, nil
}

// UpdateMember changes the fields that are non-nil.
func (s *Store) UpdateMember(ctx context.Context, groupID, memberID uuid.UUID, name *string, payout *Payout) (*Member, error) {
	var payoutJSON []byte
	if payout != nil {
		payoutJSON, _ = json.Marshal(payout)
	}
	row := s.pool.QueryRow(ctx,
		`UPDATE members SET name = COALESCE($3, name), payout = COALESCE($4, payout)
		 WHERE group_id = $1 AND id = $2 RETURNING id, name, payout`,
		groupID, memberID, name, payoutJSON)
	m, err := scanMember(row)
	return m, mapErr(err)
}

func scanMember(row pgx.Row) (*Member, error) {
	var m Member
	var payout []byte
	if err := row.Scan(&m.ID, &m.Name, &payout); err != nil {
		return nil, err
	}
	if payout != nil {
		m.Payout = &Payout{}
		if err := json.Unmarshal(payout, m.Payout); err != nil {
			return nil, err
		}
	}
	return &m, nil
}

// InsertTransaction appends a transaction with its postings atomically.
//
// If the idempotency key was already used in this group, nothing is written and
// the original transaction is returned with created=false. The unique index makes
// this hold even when two identical requests race.
func (s *Store) InsertTransaction(ctx context.Context, n NewTransaction) (t *Transaction, created bool, err error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, false, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit

	var id uuid.UUID
	err = tx.QueryRow(ctx,
		`INSERT INTO transactions
		   (group_id, kind, description, original_amount, original_currency, fx_rate, base_amount, reverses, idempotency_key)
		 VALUES ($1, $2, $3, $4, $5, $6::numeric, $7, $8, $9)
		 ON CONFLICT ON CONSTRAINT transactions_idempotency DO NOTHING
		 RETURNING id`,
		n.GroupID, n.Kind, n.Description, n.OriginalAmount, n.OriginalCurrency, n.FxRate, n.BaseAmount,
		n.Reverses, n.IdempotencyKey).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		// Lost the race (or a plain retry): return what the first request wrote.
		_ = tx.Rollback(ctx)
		err = s.pool.QueryRow(ctx,
			`SELECT id FROM transactions WHERE group_id = $1 AND idempotency_key = $2`,
			n.GroupID, n.IdempotencyKey).Scan(&id)
		if err != nil {
			return nil, false, mapErr(err)
		}
		t, err = s.GetTransaction(ctx, n.GroupID, id)
		return t, false, err
	}
	if err != nil {
		return nil, false, mapErr(err)
	}

	batch := &pgx.Batch{}
	for _, p := range n.Postings {
		batch.Queue(
			`INSERT INTO postings (group_id, transaction_id, member_id, amount) VALUES ($1, $2, $3, $4)`,
			n.GroupID, id, p.MemberID, p.Amount)
	}
	if err = tx.SendBatch(ctx, batch).Close(); err != nil {
		return nil, false, mapErr(err)
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, false, mapErr(err)
	}
	t, err = s.GetTransaction(ctx, n.GroupID, id)
	return t, true, err
}

const selectTx = `
SELECT t.id, t.seq, t.kind, t.description, t.original_amount, t.original_currency,
       trim_scale(t.fx_rate)::text, t.base_amount, t.reverses, r.id, t.created_at,
       (SELECT json_agg(json_build_object('member_id', p.member_id, 'amount_minor', p.amount) ORDER BY p.id)
          FROM postings p WHERE p.transaction_id = t.id)
  FROM transactions t
  LEFT JOIN transactions r ON r.reverses = t.id`

func scanTx(row pgx.Row) (*Transaction, error) {
	var t Transaction
	var postings []byte
	err := row.Scan(&t.ID, &t.Seq, &t.Kind, &t.Description, &t.OriginalAmount, &t.OriginalCurrency,
		&t.FxRate, &t.BaseAmount, &t.Reverses, &t.ReversedBy, &t.CreatedAt, &postings)
	if err != nil {
		return nil, err
	}
	if postings != nil {
		if err := json.Unmarshal(postings, &t.Postings); err != nil {
			return nil, err
		}
	}
	return &t, nil
}

func (s *Store) GetTransaction(ctx context.Context, groupID, id uuid.UUID) (*Transaction, error) {
	t, err := scanTx(s.pool.QueryRow(ctx, selectTx+` WHERE t.group_id = $1 AND t.id = $2`, groupID, id))
	return t, mapErr(err)
}

// ListTransactions returns up to limit transactions older than cursor (newest first)
// and the cursor for the next page, which is empty on the last page.
func (s *Store) ListTransactions(ctx context.Context, groupID uuid.UUID, cursor string, limit int) ([]Transaction, string, error) {
	before := int64(1<<63 - 1)
	if cursor != "" {
		v, err := strconv.ParseInt(cursor, 10, 64)
		if err != nil {
			return nil, "", fmt.Errorf("invalid cursor: %w", err)
		}
		before = v
	}
	rows, err := s.pool.Query(ctx,
		selectTx+` WHERE t.group_id = $1 AND t.seq < $2 ORDER BY t.seq DESC LIMIT $3`,
		groupID, before, limit+1)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	out := []Transaction{}
	for rows.Next() {
		t, err := scanTx(rows)
		if err != nil {
			return nil, "", err
		}
		out = append(out, *t)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	next := ""
	if len(out) > limit {
		out = out[:limit]
		next = strconv.FormatInt(out[limit-1].Seq, 10)
	}
	return out, next, nil
}

// Reverse appends the mirror image of a transaction.
func (s *Store) Reverse(ctx context.Context, groupID, id uuid.UUID) (*Transaction, error) {
	orig, err := s.GetTransaction(ctx, groupID, id)
	if err != nil {
		return nil, err
	}
	if orig.Kind == "reversal" {
		return nil, ErrNotReversible
	}
	n := NewTransaction{
		GroupID:          groupID,
		Kind:             "reversal",
		Description:      orig.Description,
		OriginalAmount:   orig.OriginalAmount,
		OriginalCurrency: orig.OriginalCurrency,
		FxRate:           orig.FxRate,
		BaseAmount:       orig.BaseAmount,
		Reverses:         &orig.ID,
	}
	for _, p := range orig.Postings {
		n.Postings = append(n.Postings, Posting{MemberID: p.MemberID, Amount: -p.Amount})
	}
	t, _, err := s.InsertTransaction(ctx, n)
	return t, err
}

// Balances returns the net position of every member of the group.
func (s *Store) Balances(ctx context.Context, groupID uuid.UUID) ([]Balance, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT m.id, COALESCE(SUM(p.amount), 0)::bigint
		   FROM members m LEFT JOIN postings p ON p.member_id = m.id
		  WHERE m.group_id = $1 GROUP BY m.id, m.seq ORDER BY m.seq`, groupID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Balance{}
	for rows.Next() {
		var b Balance
		if err := rows.Scan(&b.MemberID, &b.Amount); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

func mapErr(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	var pg *pgconn.PgError
	if errors.As(err, &pg) {
		switch {
		case pg.ConstraintName == "members_group_id_name_key":
			return ErrNameTaken
		case pg.ConstraintName == "transactions_reversed_once":
			return ErrAlreadyReversed
		case pg.Code == "23503" && pg.TableName == "postings":
			return ErrUnknownMember
		case pg.Code == "23503":
			return ErrNotFound // unknown group
		}
	}
	return err
}
