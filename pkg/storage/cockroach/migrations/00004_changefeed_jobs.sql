-- +goose Up
-- The changefeed jobs Milo runs, by name, with a hash of the sink and options
-- each was created with, the broker and topic it writes to, and the
-- timestamp that topic holds every change after. CockroachDB redacts a
-- sink's secrets when it lists jobs, so the listing can't show that a CA or
-- password changed.
CREATE TABLE changefeed_jobs (
    name        STRING PRIMARY KEY,
    job_id      INT8   NOT NULL,
    spec_hash   BYTES  NOT NULL,
    destination STRING NOT NULL,
    covers_from STRING NOT NULL
);

-- +goose Down
DROP TABLE changefeed_jobs;
