-- +goose Up
-- Every object, keyed by project and storage key. Each project's row at key ''
-- is its resource-version counter. A deleted object stays as a tombstone
-- until the sweeper removes it, deleted_at after its delete.
CREATE TABLE objects (
    project    STRING      NOT NULL,
    key        STRING      NOT NULL,
    rv         INT8        NOT NULL,
    deleted    BOOL        NOT NULL DEFAULT false,
    value      BYTES       NOT NULL,
    expires_at TIMESTAMPTZ,
    deleted_at TIMESTAMPTZ,
    PRIMARY KEY (project, key),
    -- What the sweeper reads: live objects with a TTL, and tombstones.
    -- Ordinary writes touch neither index.
    INDEX objects_expiring (expires_at) WHERE expires_at IS NOT NULL AND NOT deleted,
    INDEX objects_tombstones (deleted_at) WHERE deleted
);

-- Exact reads and continue tokens read past versions for a few minutes, and
-- a feed resumes from its cursor only while the cursor's versions remain, so
-- GC bounds the outage a feed survives without making every watch relist.
-- Every write also rewrites its project's counter row, so a longer TTL keeps
-- more versions there for reads to skip.
ALTER TABLE objects CONFIGURE ZONE USING gc.ttlseconds = 3600;

-- +goose Down
DROP TABLE objects;
