-- Garbage-collection / reconciliation queries.
--
-- These surface blobs whose reference bookkeeping has drifted from reality:
--   * zero-ref blobs   -- ref_count has been driven to 0 or below but the
--                         blobs row still exists (e.g. a crash between
--                         DecrementBlobRefCount and DeleteFromHash).
--   * ownerless blobs  -- no blob_references row points at the blob at all.
--                         This is the authoritative "no owner" signal and the
--                         set the manual reconcile delete targets.
--
-- A divergence between the two counts is itself a health signal: it means the
-- ref_count column disagrees with the blob_references join table.

-- name: CountZeroRefBlobs :one
SELECT COUNT(*) AS count
FROM blobs
WHERE ref_count <= 0;

-- name: CountOwnerlessBlobs :one
SELECT COUNT(*) AS count
FROM blobs b
WHERE NOT EXISTS (
    SELECT 1 FROM blob_references br WHERE br.hash = b.hash
);

-- name: ListOwnerlessBlobs :many
-- Blobs with no owner in blob_references, oldest first. Bounded by $1 so a
-- large backlog is drained across multiple reconcile invocations.
SELECT b.hash, b.size
FROM blobs b
WHERE NOT EXISTS (
    SELECT 1 FROM blob_references br WHERE br.hash = b.hash
)
ORDER BY b.created ASC
LIMIT $1;

-- name: DeleteOwnerlessBlob :one
-- Deletes a blob ONLY if it is still ownerless at delete time. The NOT EXISTS
-- is re-evaluated atomically within this single statement, closing the TOCTOU
-- window between ListOwnerlessBlobs (a snapshot) and the delete: if a new owner
-- re-uploaded matching content in between, the row is skipped (0 rows deleted,
-- sql.ErrNoRows) instead of destroying a live blob. Returns the hash on delete.
DELETE FROM blobs
WHERE blobs.hash = $1
  AND NOT EXISTS (
    SELECT 1 FROM blob_references br WHERE br.hash = blobs.hash
)
RETURNING blobs.hash;
