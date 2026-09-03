-- FundKit — transactional outbox, reversed.
-- Engineered by Dhanush C N (github.com/dhanush-cn)
--
-- Rolling this back discards any events that have been committed but not yet
-- relayed to Kafka. That is worth stating plainly rather than discovering: the
-- outbox is a durable queue, and dropping a queue drops what is in it. Drain it
-- first —
--
--   SELECT count(*) FROM outbox WHERE status = 'PENDING';
--
-- should read 0 before this runs.

DROP TABLE IF EXISTS outbox;
