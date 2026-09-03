-- FundKit transactional outbox
-- Engineered by Dhanush C N (github.com/dhanush-cn)
--
-- This file used to be documentation: the table was really created by
-- AutoMigrate at boot and the partial index by raw DDL in Go, with this SQL
-- kept alongside as "the canonical definition". Two sources of truth that had
-- to be manually kept in agreement, which is not a definition at all.
--
-- It is now the only source. golang-migrate applies it, exactly once, in
-- order, before the service starts.

CREATE TABLE IF NOT EXISTS outbox (
    id              BIGSERIAL     PRIMARY KEY,
    aggregate_type  VARCHAR(64)   NOT NULL,
    aggregate_id    VARCHAR(64)   NOT NULL,
    event_type      VARCHAR(128)  NOT NULL,
    payload         JSONB         NOT NULL,
    request_id      VARCHAR(64),
    status          VARCHAR(16)   NOT NULL DEFAULT 'PENDING',
    attempts        BIGINT        NOT NULL DEFAULT 0,
    last_error      TEXT,
    created_at      TIMESTAMPTZ   NOT NULL DEFAULT now(),
    processed_at    TIMESTAMPTZ
);

-- Postgres has no CREATE CONSTRAINT IF NOT EXISTS, so the drop-then-add keeps
-- this file safe to re-run against a database that predates the migration
-- tooling.
ALTER TABLE outbox DROP CONSTRAINT IF EXISTS outbox_status_check;
ALTER TABLE outbox ADD CONSTRAINT outbox_status_check
    CHECK (status IN ('PENDING', 'PROCESSED', 'FAILED'));

-- The relay's only hot query:
--   SELECT ... WHERE status = 'PENDING' ORDER BY id LIMIT n FOR UPDATE SKIP LOCKED
--
-- This is a PARTIAL index, and that is the whole point. A plain index on
-- (status, id) grows with the table's entire history -- millions of PROCESSED
-- rows that will never be polled for. The partial index contains only the live
-- backlog, which in a healthy system is close to zero rows, so the poll stays
-- O(backlog) rather than O(table) no matter how long the service has run.
-- Postgres also removes each row from the index automatically the moment the
-- relay flips its status to 'PROCESSED'.
CREATE INDEX IF NOT EXISTS idx_outbox_pending
    ON outbox (id)
    WHERE status = 'PENDING';

-- Debugging and replay: "show me every event for order X, in order".
CREATE INDEX IF NOT EXISTS idx_outbox_aggregate
    ON outbox (aggregate_type, aggregate_id, id);

-- Supports the nightly retention sweep:
--   DELETE FROM outbox WHERE status = 'PROCESSED'
--     AND processed_at < now() - INTERVAL '7 days';
CREATE INDEX IF NOT EXISTS idx_outbox_processed_at
    ON outbox (processed_at)
    WHERE status = 'PROCESSED';
