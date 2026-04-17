-- Federated blobs queries

-- name: UpsertFederatedBlob :one
INSERT INTO federated_blobs (hash, size, mime_type, ref_count, status, discovered_at, last_seen_at)
VALUES ($1, $2, $3, 1, 'discovered', $4, $4)
ON CONFLICT (hash) DO UPDATE SET
    ref_count = federated_blobs.ref_count + 1,
    last_seen_at = excluded.last_seen_at
RETURNING *;

-- name: GetFederatedBlob :one
SELECT *
FROM federated_blobs
WHERE hash = $1
LIMIT 1;

-- name: ListFederatedBlobsByStatus :many
SELECT *
FROM federated_blobs
WHERE status = $1
ORDER BY discovered_at DESC
LIMIT $2 OFFSET $3;

-- name: ListFederatedBlobsForMirroring :many
SELECT *
FROM federated_blobs
WHERE status = 'discovered' AND ref_count >= $1
ORDER BY ref_count DESC, discovered_at ASC
LIMIT $2;

-- name: UpdateFederatedBlobStatus :exec
UPDATE federated_blobs
SET status = $2, mirrored_at = $3
WHERE hash = $1;

-- name: CountFederatedBlobsByStatus :one
SELECT COUNT(*) as count
FROM federated_blobs
WHERE status = $1;

-- name: CountFederatedBlobs :one
SELECT COUNT(*) as count
FROM federated_blobs;

-- Federated blob URLs queries

-- name: AddFederatedBlobURL :one
INSERT INTO federated_blob_urls (blob_hash, url, server_id, priority, healthy, created_at)
VALUES ($1, $2, $3, $4, TRUE, $5)
ON CONFLICT (blob_hash, url) DO UPDATE SET
    server_id = COALESCE(excluded.server_id, federated_blob_urls.server_id),
    healthy = federated_blob_urls.healthy
RETURNING *;

-- name: GetFederatedBlobURLs :many
SELECT *
FROM federated_blob_urls
WHERE blob_hash = $1 AND healthy = TRUE
ORDER BY priority ASC;

-- name: GetAllFederatedBlobURLs :many
SELECT *
FROM federated_blob_urls
WHERE blob_hash = $1
ORDER BY priority ASC;

-- name: UpdateFederatedBlobURLHealth :exec
UPDATE federated_blob_urls
SET healthy = $2, last_check = $3
WHERE id = $1;

-- name: DeleteFederatedBlobURLs :exec
DELETE FROM federated_blob_urls
WHERE blob_hash = $1;

-- Known servers queries

-- name: UpsertKnownServer :one
INSERT INTO known_servers (url, pubkey, user_count, blob_count, healthy, first_seen, last_seen)
VALUES ($1, $2, 1, 0, TRUE, $3, $3)
ON CONFLICT (url) DO UPDATE SET
    pubkey = COALESCE(excluded.pubkey, known_servers.pubkey),
    user_count = known_servers.user_count + 1,
    last_seen = excluded.last_seen
RETURNING *;

-- name: IncrementServerBlobCount :exec
UPDATE known_servers
SET blob_count = blob_count + 1, last_seen = $2
WHERE url = $1;

-- name: GetKnownServer :one
SELECT *
FROM known_servers
WHERE url = $1
LIMIT 1;

-- name: ListKnownServers :many
SELECT *
FROM known_servers
ORDER BY user_count DESC, last_seen DESC
LIMIT $1 OFFSET $2;

-- name: ListHealthyServers :many
SELECT *
FROM known_servers
WHERE healthy = TRUE
ORDER BY user_count DESC, last_seen DESC;

-- name: UpdateServerHealth :exec
UPDATE known_servers
SET healthy = $2, last_check = $3
WHERE url = $1;

-- name: CountKnownServers :one
SELECT COUNT(*) as count
FROM known_servers;

-- name: CountHealthyServers :one
SELECT COUNT(*) as count
FROM known_servers
WHERE healthy = TRUE;

-- Federation events queries

-- name: CreateFederationEvent :one
INSERT INTO federation_events (id, event_kind, pubkey, blob_hash, direction, status, relay_url, created_at)
VALUES ($1, $2, $3, $4, $5, 'pending', $6, $7)
RETURNING *;

-- name: GetFederationEvent :one
SELECT *
FROM federation_events
WHERE id = $1
LIMIT 1;

-- name: GetFederationEventByNostrID :one
SELECT *
FROM federation_events
WHERE event_id = $1
LIMIT 1;

-- name: ListPendingPublishes :many
SELECT *
FROM federation_events
WHERE direction = 'publish' AND status = 'pending'
ORDER BY created_at ASC
LIMIT $1;

-- name: ListFederationEvents :many
SELECT *
FROM federation_events
WHERE direction = $1
ORDER BY created_at DESC
LIMIT $2 OFFSET $3;

-- name: ListAllFederationEvents :many
SELECT *
FROM federation_events
ORDER BY created_at DESC
LIMIT $1 OFFSET $2;

-- name: UpdateFederationEventStatus :exec
UPDATE federation_events
SET status = $2, event_id = $3, published_at = $4, error = $5, retries = retries + 1
WHERE id = $1;

-- name: CountFederationEventsByStatus :one
SELECT COUNT(*) as count
FROM federation_events
WHERE direction = $1 AND status = $2;

-- Federated users queries

-- name: UpsertFederatedUser :one
INSERT INTO federated_users (pubkey, event_id, server_rank, created_at, updated_at)
VALUES ($1, $2, $3, $4, $4)
ON CONFLICT (pubkey) DO UPDATE SET
    event_id = excluded.event_id,
    server_rank = excluded.server_rank,
    updated_at = excluded.updated_at
RETURNING *;

-- name: GetFederatedUser :one
SELECT *
FROM federated_users
WHERE pubkey = $1
LIMIT 1;

-- name: ListFederatedUsers :many
SELECT *
FROM federated_users
ORDER BY server_rank ASC, updated_at DESC
LIMIT $1 OFFSET $2;

-- name: CountFederatedUsers :one
SELECT COUNT(*) as count
FROM federated_users;

-- name: DeleteFederatedUser :exec
DELETE FROM federated_users
WHERE pubkey = $1;

-- BUD-03: User server list queries

-- name: UpsertUserServerList :exec
INSERT INTO user_server_lists (pubkey, server_url, rank, event_id, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $5)
ON CONFLICT (pubkey, server_url) DO UPDATE SET
    rank = excluded.rank,
    event_id = excluded.event_id,
    updated_at = excluded.updated_at;

-- name: GetUserServerList :many
SELECT server_url
FROM user_server_lists
WHERE pubkey = $1
ORDER BY rank ASC;

-- name: GetUserServerListFull :many
SELECT *
FROM user_server_lists
WHERE pubkey = $1
ORDER BY rank ASC;

-- name: DeleteUserServerList :exec
DELETE FROM user_server_lists
WHERE pubkey = $1;

-- name: CountUsersWithServer :one
SELECT COUNT(DISTINCT pubkey) as count
FROM user_server_lists
WHERE server_url = $1;
