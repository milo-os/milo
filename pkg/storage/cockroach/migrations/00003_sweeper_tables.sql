-- +goose Up
-- The sweeper no longer reads objects through secondary indexes. Each process
-- removes the tombstones it wrote by key, and a rare scan of the table finds
-- the rest. Objects with a TTL are recorded in expirations instead. With no
-- secondary indexes left, a write to objects is a blind write that reads
-- nothing first, and a delete writes one range, like any other write.
DROP INDEX objects@objects_tombstones;
DROP INDEX objects@objects_expiring;
ALTER TABLE objects DROP COLUMN expires_at;

-- When objects written with a TTL expire, recorded in the same transaction as
-- the write, in the order the sweeper reads them. A record outlives a later
-- write or delete of its object, and the sweeper's revision check skips it.
CREATE TABLE expirations (
    expires_at TIMESTAMPTZ NOT NULL,
    project    STRING      NOT NULL,
    key        STRING      NOT NULL,
    rv         INT8        NOT NULL,
    PRIMARY KEY (expires_at, project, key)
);

-- Only the sweeper reads expirations, and only its current rows, so its
-- history goes quickly rather than piling up ahead of the next sweep.
ALTER TABLE expirations CONFIGURE ZONE USING gc.ttlseconds = 300;

-- +goose Down
DROP TABLE expirations;
ALTER TABLE objects ADD COLUMN expires_at TIMESTAMPTZ;
CREATE INDEX objects_expiring ON objects (expires_at) WHERE expires_at IS NOT NULL AND NOT deleted;
CREATE INDEX objects_tombstones ON objects (deleted_at) WHERE deleted;
