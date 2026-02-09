-- name: GetBlobsFromPubkey :many
select *
from blobs
where pubkey = $1;

-- name: GetBlobFromHash :one
select *
from blobs
where hash = $1
limit 1;

-- name: InsertBlob :one
insert into blobs(
  pubkey,
  hash,
  type,
  size,
  blob,
  created,
  encryption_mode,
  encrypted_dek,
  encryption_nonce,
  original_size
) values ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
returning *;

-- name: DeleteBlobFromHash :exec
delete
from blobs
where hash = $1;
