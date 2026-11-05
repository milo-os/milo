-- +goose NO TRANSACTION
-- +goose Up
-- The cluster is Milo's own, so the storage sets up what it relies on here,
-- once, and assumes it everywhere else. SET CLUSTER SETTING cannot run inside
-- a multi-statement transaction.

-- Watches read a changefeed, which needs rangefeeds.
SET CLUSTER SETTING kv.rangefeed.enabled = true;

-- +goose Down
RESET CLUSTER SETTING kv.rangefeed.enabled;
