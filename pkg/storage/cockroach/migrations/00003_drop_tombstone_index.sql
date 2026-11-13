-- +goose Up
-- Each process removes the tombstones it wrote by key, and a rare scan of the
-- table finds the rest, so nothing reads this index. Without it a delete no
-- longer writes an index entry in another range, which kept deletes from
-- committing in one phase. Binaries before this migration find tombstones
-- through the index, and scan the table every second instead until they're
-- replaced.
DROP INDEX objects@objects_tombstones;

-- +goose Down
CREATE INDEX objects_tombstones ON objects (deleted_at) WHERE deleted;
