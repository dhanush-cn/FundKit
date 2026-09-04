-- FundKit — orders table, reversed.
-- Engineered by Dhanush C N (github.com/dhanush-cn)
--
-- Dropping the table takes its indexes and constraints with it, so they are not
-- listed individually.
--
-- This is destructive, which is the honest thing for a down migration on a
-- CREATE TABLE to be. It exists so that `migrate down` in development is a real
-- operation rather than a comment saying "restore from backup"; in production
-- the rollback for a released schema change is a new forward migration, not
-- this file.

DROP TABLE IF EXISTS orders;
