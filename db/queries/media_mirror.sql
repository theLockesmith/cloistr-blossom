-- Remote media mirror queries.
--
-- Note what is absent: there is no query here that takes a pubkey, an IP, or
-- any other caller identity, because no such column exists. The mirror caches
-- content by URL and cannot answer "who viewed this" even if asked. That is
-- the point of the feature, so keep it that way.

-- name: GetMirroredMedia :one
SELECT url_hash, source_url, status, reason, sha256, size, mime, fetched_at, accessed_at
FROM mirrored_media
WHERE url_hash = $1;

-- name: UpsertMirroredMedia :exec
-- Records the outcome of a fetch, successful or not. Upsert rather than insert
-- because a previously unreachable host coming back, or a refused object whose
-- policy verdict changed, must overwrite the stale verdict in place.
INSERT INTO mirrored_media (
    url_hash, source_url, status, reason, sha256, size, mime, fetched_at, accessed_at
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9
)
ON CONFLICT (url_hash) DO UPDATE SET
    source_url  = excluded.source_url,
    status      = excluded.status,
    reason      = excluded.reason,
    sha256      = excluded.sha256,
    size        = excluded.size,
    mime        = excluded.mime,
    fetched_at  = excluded.fetched_at,
    accessed_at = excluded.accessed_at;

-- name: TouchMirroredMedia :exec
-- Refreshes the LRU timestamp. Guarded by accessed_at < $2 so a hot object
-- costs one write per coarse interval instead of one per request -- which
-- keeps write load off a small server and stops the column from becoming a
-- fine-grained record of when content was viewed.
UPDATE mirrored_media
SET accessed_at = $2
WHERE url_hash = $1
  AND accessed_at < $2;

-- name: SumMirroredMediaSize :one
-- Total bytes held by successfully mirrored objects. Failure rows are excluded
-- because they hold no bytes.
-- CAST(... AS BIGINT), not Postgres's ::BIGINT shorthand: the same generated
-- SQL runs against SQLite on self-hosted deployments, and ::  is a syntax
-- error there. Without a cast at all, Postgres types SUM(BIGINT) as NUMERIC
-- and sqlc emits interface{}.
SELECT CAST(COALESCE(SUM(size), 0) AS BIGINT) AS total
FROM mirrored_media
WHERE status = 'ok';

-- name: CountMirroredMedia :one
SELECT COUNT(*) AS count
FROM mirrored_media
WHERE status = 'ok';

-- name: ListMirroredMediaLRU :many
-- Eviction candidates, least recently accessed first. Bounded by $1 so one
-- pass cannot stall on a huge cache.
SELECT url_hash, sha256, size
FROM mirrored_media
WHERE status = 'ok'
ORDER BY accessed_at ASC, fetched_at ASC
LIMIT $1;

-- name: DeleteMirroredMedia :exec
DELETE FROM mirrored_media
WHERE url_hash = $1;

-- name: CountMirroredMediaForBlob :one
-- How many mirror entries still point at a blob. Distinct URLs can serve
-- byte-identical content, so a blob must not be released until the LAST URL
-- referencing it is gone.
SELECT COUNT(*) AS count
FROM mirrored_media
WHERE sha256 = $1;

-- name: DeleteStaleMirrorFailures :exec
-- Expires negative-cache rows so a host that comes back, or an object whose
-- policy verdict would now differ, is eventually retried rather than being
-- refused forever.
DELETE FROM mirrored_media
WHERE status = $1
  AND fetched_at < $2;
