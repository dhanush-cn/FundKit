-- FundKit transactional outbox
-- Engineered by Dhanush C N (github.com/dhanush-cn)
--
-- Applied automatically at boot by repository.OpenPostgres (AutoMigrate for the
-- table, raw DDL for the partial index). Kept here as the canonical, reviewable
-- definition and for environments where migrations are run out-of-band.

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

-- Applied separately by repository.createOutboxIndexes so that a database
-- created by AutoMigrate ends up with the same constraint as one created from
-- this file. Postgres has no CREATE CONSTRAINT IF NOT EXISTS, hence the guard.
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
