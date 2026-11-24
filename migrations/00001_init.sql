-- +goose Up
-- +goose StatementBegin
CREATE TABLE groups (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name          text NOT NULL CHECK (char_length(name) BETWEEN 1 AND 100),
    base_currency char(3) NOT NULL,
    created_at    timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE members (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    seq        bigint GENERATED ALWAYS AS IDENTITY,
    group_id   uuid NOT NULL REFERENCES groups (id),
    name       text NOT NULL CHECK (char_length(name) BETWEEN 1 AND 60),
    payout     jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (group_id, name),
    UNIQUE (group_id, id)
);

-- One row per business event. Amounts are integer minor units; the FX rate that
-- applied is stored so balances never move when market rates do.
CREATE TABLE transactions (
    id                uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    seq               bigint GENERATED ALWAYS AS IDENTITY,
    group_id          uuid NOT NULL REFERENCES groups (id),
    kind              text NOT NULL CHECK (kind IN ('expense', 'settlement', 'reversal')),
    description       text NOT NULL CHECK (char_length(description) <= 200),
    original_amount   bigint NOT NULL CHECK (original_amount > 0),
    original_currency char(3) NOT NULL,
    fx_rate           numeric(38, 18) NOT NULL CHECK (fx_rate > 0),
    base_amount       bigint NOT NULL CHECK (base_amount > 0),
    reverses          uuid REFERENCES transactions (id),
    idempotency_key   text,
    created_at        timestamptz NOT NULL DEFAULT now(),
    UNIQUE (group_id, id),
    CONSTRAINT transactions_reversed_once UNIQUE (reverses),
    CONSTRAINT transactions_idempotency UNIQUE (group_id, idempotency_key),
    CHECK ((kind = 'reversal') = (reverses IS NOT NULL))
);
CREATE INDEX transactions_group_seq ON transactions (group_id, seq DESC);

-- Double-entry lines. Positive: the group owes the member. Negative: the member owes.
CREATE TABLE postings (
    id             bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    group_id       uuid NOT NULL,
    transaction_id uuid NOT NULL,
    member_id      uuid NOT NULL,
    amount         bigint NOT NULL CHECK (amount <> 0),
    FOREIGN KEY (group_id, transaction_id) REFERENCES transactions (group_id, id),
    FOREIGN KEY (group_id, member_id) REFERENCES members (group_id, id)
);
CREATE INDEX postings_transaction ON postings (transaction_id);
CREATE INDEX postings_member ON postings (member_id);

-- Invariant 1: the postings of a transaction sum to zero. Checked at commit, so
-- no code path (or manual SQL) can leave the ledger unbalanced.
CREATE FUNCTION assert_transaction_balanced() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
    total numeric;
BEGIN
    SELECT COALESCE(SUM(amount), 0) INTO total FROM postings WHERE transaction_id = NEW.transaction_id;
    IF total <> 0 THEN
        RAISE EXCEPTION 'transaction % is unbalanced by %', NEW.transaction_id, total
            USING ERRCODE = 'check_violation', CONSTRAINT = 'postings_balanced';
    END IF;
    RETURN NULL;
END $$;

CREATE CONSTRAINT TRIGGER postings_balanced
    AFTER INSERT ON postings
    DEFERRABLE INITIALLY DEFERRED
    FOR EACH ROW EXECUTE FUNCTION assert_transaction_balanced();

-- Invariant 2: the ledger is append-only. Corrections are new reversal transactions.
CREATE FUNCTION forbid_ledger_mutation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION '% is append-only', TG_TABLE_NAME USING ERRCODE = 'restrict_violation';
END $$;

CREATE TRIGGER transactions_immutable BEFORE UPDATE OR DELETE ON transactions
    FOR EACH ROW EXECUTE FUNCTION forbid_ledger_mutation();
CREATE TRIGGER postings_immutable BEFORE UPDATE OR DELETE ON postings
    FOR EACH ROW EXECUTE FUNCTION forbid_ledger_mutation();
CREATE TRIGGER transactions_no_truncate BEFORE TRUNCATE ON transactions
    FOR EACH STATEMENT EXECUTE FUNCTION forbid_ledger_mutation();
CREATE TRIGGER postings_no_truncate BEFORE TRUNCATE ON postings
    FOR EACH STATEMENT EXECUTE FUNCTION forbid_ledger_mutation();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE postings;
DROP TABLE transactions;
DROP TABLE members;
DROP TABLE groups;
DROP FUNCTION forbid_ledger_mutation();
DROP FUNCTION assert_transaction_balanced();
-- +goose StatementEnd
