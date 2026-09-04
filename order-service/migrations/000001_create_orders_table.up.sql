-- FundKit — orders table
-- Engineered by Dhanush C N (github.com/dhanush-cn)
--
-- This replaces GORM's AutoMigrate. AutoMigrate is a convenience for a
-- prototype and a liability for anything else: it derives DDL from whatever the
-- struct happens to look like at boot, it never drops or narrows a column, it
-- cannot express a CHECK constraint or a partial index, and it leaves no record
-- of what the schema is supposed to be. A reviewer cannot diff it, a rollback
-- cannot undo it, and two services booting against one database race each other
-- to apply it.
--
-- A versioned migration is the opposite on every count: it is a reviewable
-- artefact, it runs exactly once, it is ordered, and it has an inverse.
--
-- Amount is BIGINT and holds paise. This is the schema-level half of the
-- integer-money change: DOUBLE PRECISION would let a value that is exact in Go
-- become inexact the moment it is stored, which would make the application's
-- guarantee worthless. The column type is what actually enforces it.

CREATE TABLE IF NOT EXISTS orders (
    id              VARCHAR(36)   PRIMARY KEY,
    user_id         VARCHAR(64)   NOT NULL,
    user_name       VARCHAR(128),
    user_email      VARCHAR(255),
    user_phone      VARCHAR(32),
    fund_id         VARCHAR(64)   NOT NULL,

    -- Paise, not rupees. ₹100.50 is stored as 10050.
    amount          BIGINT        NOT NULL,

    type            VARCHAR(16)   NOT NULL,
    status          VARCHAR(16)   NOT NULL DEFAULT 'PENDING',
    idempotency_key VARCHAR(128)  NOT NULL,
    created_at      TIMESTAMPTZ   NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ   NOT NULL DEFAULT now()
);

-- Constraints AutoMigrate could not express.
--
-- These are not belt-and-braces duplication of the Go validation. The Go layer
-- protects the API path; the database protects every path, including a
-- migration script, an operator's psql session, and a future service that
-- writes to this table without going through order-service. A rule that only
-- exists in application code is a rule that holds until the first time someone
-- does not go through the application.
ALTER TABLE orders DROP CONSTRAINT IF EXISTS orders_amount_positive;
ALTER TABLE orders ADD CONSTRAINT orders_amount_positive
    CHECK (amount > 0);

ALTER TABLE orders DROP CONSTRAINT IF EXISTS orders_type_check;
ALTER TABLE orders ADD CONSTRAINT orders_type_check
    CHECK (type IN ('SIP', 'LUMPSUM'));

ALTER TABLE orders DROP CONSTRAINT IF EXISTS orders_status_check;
ALTER TABLE orders ADD CONSTRAINT orders_status_check
    CHECK (status IN ('PENDING', 'PROCESSING', 'EXECUTED', 'FAILED'));

-- The index names match what GORM's `gorm:"index"` and `gorm:"uniqueIndex"`
-- tags would have generated, so a database built by the old AutoMigrate path
-- and one built from this file are indistinguishable.

-- The idempotency guarantee. Redis holds the fast reservation, but this unique
-- index is the authority: it is what makes a duplicate POST fail even if Redis
-- has just been flushed, and the repository translates its violation into
-- domain.ErrDuplicateOrder.
CREATE UNIQUE INDEX IF NOT EXISTS idx_orders_idempotency_key
    ON orders (idempotency_key);

CREATE INDEX IF NOT EXISTS idx_orders_user_id
    ON orders (user_id);

-- Supports the list endpoint's only query: ORDER BY created_at DESC LIMIT n.
-- Unindexed, that is a full scan and a sort of the entire table to return 100
-- rows; DESC in the index definition lets Postgres walk it and stop.
CREATE INDEX IF NOT EXISTS idx_orders_created_at
    ON orders (created_at DESC);
