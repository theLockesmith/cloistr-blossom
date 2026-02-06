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
  created
) values ($1,$2,$3,$4,$5,$6)
returning *;

-- name: DeleteBlobFromHash :exec
delete
from blobs
where hash = $1;
