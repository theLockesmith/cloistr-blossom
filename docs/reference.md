# coldforge-blossom Reference

**Comprehensive reference documentation for the Blossom blob storage server.**

For quick start and essential info, see [CLAUDE.md](../CLAUDE.md).

---

## API Endpoints

### Core Blossom (BUD) Endpoints

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| GET | `/:hash` | No | Retrieve blob by hash |
| HEAD | `/:hash` | No | Check if blob exists |
| PUT | `/upload` | Yes | Upload a blob |
| HEAD | `/upload` | Yes | Get upload requirements |
| DELETE | `/:hash` | Yes | Delete a blob |
| PUT | `/mirror` | Yes | Mirror a blob from URL |
| GET | `/list/:pubkey` | No | List blobs by pubkey |

**Upload headers (`PUT /upload`, `PUT /media`):**

| Header | Description |
|--------|-------------|
| `X-Encryption` | Encryption mode: `none`, `server`, or `e2e` |
| `X-Expiration` | Optional. Absolute Unix timestamp (seconds, NIP-40 style) at which the blob auto-expires. Must be in the future; a malformed value returns `400` before the blob is stored. On success the applied value is echoed back in the `X-Expiration` response header. Requires `expiration.enabled` for the blob to actually be deleted (the timestamp is recorded either way). |

### Media Processing (BUD-05)

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| PUT | `/media` | Yes | Upload and optimize media |
| HEAD | `/media` | Yes | Get media upload requirements |
| GET | `/:hash/thumb` | No | Get thumbnail (w, h query params) |

### Remote Media Mirror

Mirrors third-party images (custom emoji, NIP-30) so clients never contact the
hosts that serve them.

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| POST | `/media/mirror/sign` | Yes (`t=mirror`) | Mint signed mirror links for a batch of remote URLs |
| GET | `/media/mirror?u=&e=&s=` | No | Serve the mirrored image |

Enabled by `media_mirror.enabled`; advertised as `features.media_mirror` in
`/.well-known/blossom`.

#### Feature detection, and what a client must treat as "unavailable"

**Both routes always exist and answer `501 MIRROR_DISABLED` when the feature is
off.** They are registered unconditionally. A route that answers "this server
has the feature and it is switched off" is diagnosable; an absent route is not.

That said, **a client MUST treat `404` exactly as it treats `501`.** This is not
optional, because 404 is what you legitimately get from:

- a server built before this feature existed,
- a deploy window where the client is ahead of the server,
- any other Blossom implementation, which has no obligation to have this at all.

A client that handles 501 but not 404 will show error states in precisely the
situations where graceful degradation matters most. Both statuses mean the same
thing to a client: *this server will not mirror for me; fall back to not
rendering remote images.*

The **preferred** signal is neither: read `features.media_mirror` from
`/.well-known/blossom` once at startup and skip the mirror entirely when it is
false. The status codes are the backstop for when that check is stale.

#### Why two endpoints with different auth

The signing key cannot live in a browser — our apps are static SPAs, so anything
they hold is readable in devtools, and a readable signing key is an open proxy.
So minting is authenticated and serving is not:

- **Signing is authenticated** so only our users can enqueue remote URLs.
- **Serving is anonymous** because it is the request a browser makes for every
  `<img src>`. Requiring auth there would attach an identity to every image
  view, which is the exact tracking the mirror exists to remove.

The signature covers the URL and an expiry and **nothing else**, so it is
identical for every user. It identifies content, never a person. Everyone who
mirrors `:blobcatpeek:` gets the same link, it caches once for all of them, and
the link discloses nothing about who requested it. The consequence, stated
plainly: signed links are shareable bearer tokens for one URL. That is an
accepted trade — the exposure is one already-mirrored image, and the alternative
costs the user's privacy. Revoke by rotating `signing_key`.

#### Client flow

```
POST /media/mirror/sign
Authorization: Nostr <base64 kind-24242 event, t=mirror>
{"urls": ["https://host.example/blobcatpeek.png", "..."]}

200 OK
{
  "signed":   [{"source": "https://host.example/blobcatpeek.png",
                "url": "/media/mirror?u=<base64url>&s=<sig>",
                "expires_at": 0}],
  "rejected": [{"source": "file:///etc/passwd", "reason": "invalid_url"}]
}
```

Then render `<img src="https://blossom.cloistr.xyz{url}">`.

A bad URL rejects that URL, not the batch: an emoji set published by a stranger
routinely contains one broken entry, and failing the whole request over it would
leave the user with no images — the problem this feature exists to solve.

#### Failure responses

Three classes, deliberately distinguishable. An unfetched image, a refused
image, and an unreachable host must not render identically, or the next person
debugging it learns nothing.

| Status | Code | Meaning | Client should |
|--------|------|---------|---------------|
| 415 | `MIRROR_REFUSED` | We fetched and said no (too large, wrong type, too many pixels, blocked address) | Show a "not available" placeholder. **Permanent** — do not retry |
| 502 | `MIRROR_UNREACHABLE` | The remote host did not answer (DNS, timeout, HTTP error) | **Transient** — retry after `Retry-After` |
| 403 | `MIRROR_UNSIGNED` | Link is not ours, or expired | Re-sign; do not retry the same link |
| 501 | `MIRROR_DISABLED` | Mirroring is off on this server | Fall back to not rendering custom emoji |

The body carries a machine-readable `reason` (`too_large`, `type_not_allowed`,
`too_many_pixels`, `not_an_image`, `blocked_address`, `http_status`,
`transport`, …). The failure *detail* is logged server-side only: it can contain
the remote host's response, and returning it would let anyone with a signed link
use the mirror as a network probe.

Success responses carry `X-Blossom-Sha256` (the blob hash — a client can go
straight to `GET /<hash>` from then on) and `X-Mirror-Status: hit|miss`.

#### What it refuses, and why

- **SSRF.** Destinations are validated at **dial time**, after DNS resolution,
  on every redirect hop. Checking the URL before fetching does not work: DNS
  rebinding answers differently for the check and the fetch, and a public URL
  can redirect to `169.254.169.254`. Private, loopback, link-local, CGNAT,
  broadcast, multicast, IPv4-mapped and NAT64 ranges are refused. `HTTP_PROXY`
  is ignored — a proxy would terminate every connection at the proxy, so only
  the proxy's address would ever be checked.
- **Open-proxy use.** Unsigned requests are refused before any outbound fetch.
- **Oversized objects.** A declared `Content-Length` over the cap hangs up
  early; the read is limited regardless, because the header can lie.
- **Decompression bombs.** Dimensions are read from the image header
  (`DecodeConfig`, which never allocates a pixel buffer) and checked before any
  decode.
- **Type confusion.** The MIME type comes from the bytes, never the remote
  `Content-Type`. A host serving HTML labelled `image/png` is refused — that is
  how a media proxy becomes an XSS vector. Responses also carry `nosniff`, a
  `sandbox` CSP, and `no-referrer`.

#### What it does not record

There is no per-user request log, by construction:

- The `mirrored_media` table has no pubkey, IP, or per-request column. It is one
  row per remote URL.
- The fetch route is **excluded from the access log**. `ginzap` logs path,
  query, and client IP, and the mirror carries the remote URL in its query — so
  logging it would write "this IP fetched this emoji at this time" for every
  image view, rebuilding the tracking in-house. That is worse than the leak,
  because then we would be the ones doing it.
- `accessed_at` (which drives LRU) is rounded to the hour, making it a
  popularity signal rather than a viewing record.
- Prometheus labels carry outcomes only — no URL, host, or pubkey.

Failures are still diagnosable: the service logs status, reason, and detail with
no identifiers, and `media_mirror_*` metrics carry outcome counts.

#### Relationship to BUD-04

BUD-04 (`PUT /mirror`) is a different thing and does not cover this case. It
mirrors a blob whose **sha256 the client already knows** — the hash comes from
the `x` tag of the authorization event and the server verifies the downloaded
bytes against it (409 on mismatch). A client rendering a NIP-30 emoji has a URL
and no idea what its hash is, so there is nothing to put in the `x` tag.

This is therefore a separate endpoint, not a reinterpretation of BUD-04. What it
does reuse is the substance: mirrored bytes are stored as ordinary
content-addressed blobs, deduplicated against existing uploads, and retrievable
at `GET /<sha256>` like anything else.

### Video Streaming (HLS & DASH)

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| POST | `/:hash/transcode` | Yes | Start video transcoding |
| GET | `/:hash/transcode` | No | Get transcoding status |
| GET | `/:hash/hls/master.m3u8` | No | Get HLS master playlist |
| GET | `/:hash/hls/:quality/stream.m3u8` | No | Get HLS quality variant |
| GET | `/:hash/hls/:quality/:segment` | No | Get HLS segment (.ts) |
| GET | `/:hash/dash/manifest.mpd` | No | Get DASH manifest |
| GET | `/:hash/dash/:segment` | No | Get DASH segment (.m4s) |

**Quality presets:**
- H.264: 720p (2500kbps), 480p (1000kbps), 360p (600kbps)
- HEVC: 720p (1750kbps), 480p (700kbps), 360p (420kbps) - ~30% more efficient
- AV1: 720p (1500kbps), 480p (600kbps), 360p (360kbps) - ~40% more efficient

**Transcoding workflow:**
1. Upload video via `/upload`
2. Start transcoding: `POST /:hash/transcode`
3. Poll status: `GET /:hash/transcode`
4. Stream via HLS or DASH

### Subtitles (WebVTT)

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| PUT | `/:hash/subtitles/:lang` | Yes | Add/update subtitle track |
| GET | `/:hash/subtitles/:lang` | No | Get subtitle track (VTT) |
| GET | `/:hash/subtitles` | No | List all subtitle tracks |
| DELETE | `/:hash/subtitles/:lang` | Yes | Remove subtitle track |

**Query parameters for PUT:**
- `label` - Display name
- `default=true` - Set as default
- `forced=true` - Mark as forced

### BUD-03 User Server List

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| GET | `/servers/:pubkey` | No | Get user's Blossom server list |

Returns the server URLs from a user's kind 10063 event, ordered by preference.

**Response:**
```json
{
  "pubkey": "abc123...",
  "servers": [
    "https://blossom.example.com",
    "https://backup.example.com"
  ]
}
```

**Note:** Requires federation to be enabled. Server lists are cached from kind 10063 events received via Nostr relays.

### BUD-10 Blossom URI Schema

The server supports BUD-10 `blossom:` URIs for blob references.

**URI Format:**
```
blossom:hash.ext?xs=server&as=author&sz=size
```

| Component | Description |
|-----------|-------------|
| `hash` | 64-char hex SHA256 blob hash |
| `ext` | File extension (e.g., `jpg`, `mp4`) |
| `xs` | Server hint URL (can have multiple) |
| `as` | Author pubkey |
| `sz` | File size in bytes |

**Server-side support:**

1. **Mirror endpoint accepts blossom: URIs** - The `/mirror` endpoint can accept a `blossom:` URI in the `url` field. Server hints are used to fetch the blob.

2. **Blob responses include blossom_uri** - All blob descriptor responses include a `blossom_uri` field for easy sharing.

**Example response:**
```json
{
  "url": "https://files.cloistr.xyz/abc123...",
  "sha256": "abc123...",
  "size": 12345,
  "type": "image/jpeg",
  "blossom_uri": "blossom:abc123...jpg?xs=files.cloistr.xyz&sz=12345"
}
```

**Example mirror with blossom: URI:**
```bash
curl -X PUT https://files.cloistr.xyz/mirror \
  -H "Authorization: Nostr base64event" \
  -H "Content-Type: application/json" \
  -d '{"url": "blossom:abc123...jpg?xs=other-server.com"}'
```

### Server Capabilities

Server discovery and feature advertisement at `/.well-known/blossom`.

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| GET | `/.well-known/blossom` | No | Get server capabilities |
| GET | `/.well-known/health` | No | Health check |

**Response:**
```json
{
  "name": "Cloistr Blossom",
  "version": "1.2.0",
  "pubkey": "admin_pubkey_here",
  "buds": ["BUD-01", "BUD-02", "BUD-03", "BUD-04", "BUD-05", "BUD-06", "BUD-07", "BUD-08", "BUD-09", "BUD-10", "BUD-11"],
  "features": {
    "encryption": true,
    "cdn": true,
    "media_optimization": true,
    "transcoding": true,
    "thumbnails": true,
    "subtitles": true,
    "chunked_upload": true,
    "tus_upload": true,
    "batch_operations": true,
    "websocket_notify": true,
    "federation": true,
    "content_moderation": true,
    "ipfs": true,
    "torrent": true
  },
  "limits": {
    "max_upload_size": 104857600,
    "default_quota": 1073741824,
    "max_quota": 10737418240,
    "rate_limit_enabled": true
  },
  "payment": {
    "required": false,
    "free_tier_bytes": 10485760,
    "satoshis_per_byte": 0.001,
    "min_payment_sats": 10,
    "methods": ["lightning", "cashu"]
  }
}
```

### BUD-07 Payments

Paid uploads via Lightning Network (BOLT-11) or Cashu (ecash).

**Headers for payment request (402 response):**
| Header | Description |
|--------|-------------|
| `X-Lightning` | BOLT-11 Lightning invoice |
| `X-Cashu` | Cashu payment request |
| `X-Payment-Request` | Payment request ID for tracking |
| `X-Payment-Amount` | Amount in satoshis |

**Headers for payment proof (upload request):**
| Header | Description |
|--------|-------------|
| `X-Lightning` | Lightning preimage (hex) |
| `X-Cashu` | Cashu token (cashuA... format) |
| `X-Payment-Request` | Payment request ID |

**Flow:**
1. Upload request without payment → 402 Payment Required + payment headers
2. Client pays via Lightning or Cashu
3. Retry upload with proof headers → 200 OK

**Free tier:** Configurable free bytes per pubkey before payment required.

**Pricing:** Per-byte pricing (e.g., 0.000001 sats/byte = 1 sat/MB).

### BUD-09 Content Reporting

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| PUT | `/report` | No | Submit NIP-56 signed report |
| POST | `/report` | No | Submit legacy JSON report |
| GET | `/transparency` | No | Get moderation stats |

**NIP-56 Report (kind 1984):**
```json
{
  "kind": 1984,
  "tags": [["x", "<blob_sha256>", "<report_type>"]],
  "content": "Report details"
}
```

**Report types:** csam, illegal, copyright, abuse, other

### IPFS Pinning

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| POST | `/:hash/pin` | Yes | Pin blob to IPFS |
| DELETE | `/:hash/pin` | Yes | Unpin |
| GET | `/:hash/pin` | No | Get pin status |
| GET | `/pins` | No | List all pins |

**Supported services:** Pinata, web3.storage, Filebase, any IPFS PSA compatible

### Torrent Seeds

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| POST | `/:hash/torrent` | Yes | Generate .torrent file |
| GET | `/:hash/torrent` | No | Get cached .torrent |
| DELETE | `/:hash/torrent` | Yes | Delete cached .torrent |

**Features:** BEP 3/5/12/19, DHT bootstrap, WebSeeds, multi-tracker

### Chunked Uploads

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| POST | `/upload/chunked` | Yes | Start session |
| PUT | `/upload/chunked/:session/:chunk` | No | Upload chunk |
| POST | `/upload/chunked/:session/complete` | No | Finalize |
| DELETE | `/upload/chunked/:session` | No | Abort |
| GET | `/upload/chunked/:session` | No | Get status |

### Resumable Uploads (tus)

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| OPTIONS | `/files` | No | Get capabilities |
| POST | `/files` | Yes | Create upload |
| HEAD | `/files/:id` | No | Get progress |
| PATCH | `/files/:id` | No | Resume upload |
| DELETE | `/files/:id` | Yes | Terminate |

**Extensions:** creation, creation-with-upload, termination, concatenation

### WebSocket Notifications

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| GET | `/ws` | No | WebSocket (pubkey query param) |
| GET | `/ws/status` | No | Connection stats |

**Event types:** upload_progress, upload_complete, upload_failed, transcode_progress, transcode_complete, quota_warning

### Batch Operations

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| POST | `/batch/upload` | Yes | Upload multiple files |
| POST | `/batch/download` | No | Download as archive |
| DELETE | `/batch` | Yes | Delete multiple |
| POST | `/batch/status` | No | Check multiple statuses |
| GET | `/batch/jobs/:job_id` | No | Get job status |

**Limits:** 50 upload, 100 download/delete, 500MB total upload

### AI Content Moderation (Admin)

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| GET | `/admin/ai-moderation/stats` | Admin | Stats |
| GET | `/admin/ai-moderation/providers` | Admin | List providers |
| GET | `/admin/ai-moderation/quarantine` | Admin | List quarantined |
| POST | `/admin/ai-moderation/quarantine/:hash/review` | Admin | Approve/reject |
| POST | `/admin/ai-moderation/scan/:hash` | Admin | Manual scan |

**Providers:** HashBlocklistProvider, AWSRekognitionProvider (stub), GoogleVisionProvider (stub), CustomAPIProvider

**Actions:** allow, block, quarantine, flag

### Federation (Admin)

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| GET | `/admin/federation/status` | Admin | Status and config |
| GET | `/admin/federation/blobs` | Admin | List federated blobs |
| POST | `/admin/federation/blobs/:hash/mirror` | Admin | Trigger mirror |
| GET | `/admin/federation/servers` | Admin | List known servers |

**Nostr events:** kind 1063 (file metadata), kind 10063 (server list)

**Modes:** publish, subscribe, both

### Analytics Dashboard (Admin)

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| GET | `/admin/analytics` | Admin | Analytics dashboard page |
| GET | `/admin/api/analytics/overview` | Admin | Dashboard summary stats |
| GET | `/admin/api/analytics/storage` | Admin | Storage trends |
| GET | `/admin/api/analytics/activity` | Admin | Upload/download activity |
| GET | `/admin/api/analytics/users` | Admin | User growth and top users |
| GET | `/admin/api/analytics/content` | Admin | Content type breakdown |

**Query parameters for time-series endpoints:**

| Parameter | Type | Description |
|-----------|------|-------------|
| `start_time` | int64 | Unix timestamp (period start) |
| `end_time` | int64 | Unix timestamp (period end) |
| `bucket` | string | Time bucket: `hourly`, `daily`, `weekly`, `monthly` |
| `limit` | int | Max results for top-N queries (1-100) |

**Overview response fields:**
- `total_storage`, `total_blobs`, `total_users` - Current totals
- `storage_growth`, `blob_growth`, `user_growth` - Week-over-week % change
- `uploads_last_24h`, `bytes_in_last_24h`, `new_users_last_24h` - Recent activity

**Storage analytics:**
- `bytes_over_time`, `blobs_over_time` - Time series with cumulative values
- `deduplication_pct` - Percentage storage saved via deduplication

**Content analytics:**
- `by_mime_type` - Breakdown by MIME type (blob count, total size)
- `by_category` - Breakdown by category (image, video, audio, text, document, archive, other)
- `encryption_pct` - Percentage of blobs encrypted

### Blob Search (Admin)

Server-wide blob search for moderation/ops. Admin session required (this is
intentionally not a public endpoint — public content discovery is better served
by Nostr relay queries).

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| GET | `/admin/api/blobs/search` | Admin | Search all blobs by criteria |

**Query parameters:**

| Parameter | Type | Description |
|-----------|------|-------------|
| `type` | string | MIME type prefix (e.g. `image/`, `video/mp4`) |
| `pubkey` | string | Restrict to a single uploader (hex) |
| `since` / `until` | int64 | Created-time range (Unix timestamps) |
| `min_size` / `max_size` | int64 | Size bounds in bytes |
| `limit` | int | Page size (default 50, max 200) |
| `offset` | int | Pagination offset |
| `sort` | string | `desc` for newest-first (default ascending) |

Response: `{ "blobs": [...], "total": <int> }` (`total` is the full match count, ignoring pagination).

### List Endpoint Filters

`/list/:pubkey` query parameters:

| Parameter | Type | Description |
|-----------|------|-------------|
| `type` | string | MIME prefix (e.g., `image/`) |
| `since` | int64 | Unix timestamp (after) |
| `until` | int64 | Unix timestamp (before) |
| `limit` | int | Max results (1-1000) |
| `offset` | int | Pagination offset |
| `sort` | string | `desc` for newest first |

---

## Content Deduplication

Multiple users can reference the same blob without duplicate storage.

**How it works:**
1. User A uploads blob → stored with reference
2. User B uploads same hash → creates reference, no re-storage
3. Delete removes reference only; blob deleted when last reference removed

**Quota behavior:** Each user's quota counts their references, not actual storage.

---

## Hardware Transcoding

| Codec | Hardware Encoders | Software |
|-------|-------------------|----------|
| H.264 | h264_nvenc, h264_qsv, h264_vaapi | libx264 |
| HEVC | hevc_nvenc, hevc_qsv, hevc_vaapi | libx265 |
| AV1 | av1_nvenc (RTX 40+), av1_qsv (Arc/12th+), av1_vaapi | libsvtav1 |

**Requirements:**
- NVENC: GTX 600+ / Quadro K+
- NVENC AV1: RTX 4000+
- QSV: Intel 4th gen+ (AV1: 12th gen+)
- VAAPI: AMD/Intel Linux drivers

---

## Configuration

```yaml
database:
  driver: postgres
  postgres:
    host: postgres-rw.db.coldforge.xyz
    port: 5432
    user: cloistr_blossom
    password: ${DB_PASSWORD}
    database: cloistr_blossom

storage:
  backend: s3
  s3:
    endpoint: https://s3.coldforge.xyz
    bucket: coldforge-blossom
    region: us-east-1
    access_key: ${S3_ACCESS_KEY}
    secret_key: ${S3_SECRET_KEY}
    path_style: true

encryption:
  enabled: true
  master_key: ${ENCRYPTION_MASTER_KEY}

quota:
  enabled: true
  default_bytes: 1073741824  # 1 GB
  max_bytes: 107374182400    # 100 GB

cdn:
  enabled: true
  public_url: https://cdn.example.com
  presigned_urls: false
  presigned_expiry: 1h
  redirect: true

rate_limiting:
  enabled: true
  ip:
    download: { requests: 100, window: "1m" }
    upload: { requests: 10, window: "1m" }
  pubkey:
    download: { requests: 200, window: "1m" }
    upload: { requests: 30, window: "1m" }
  bandwidth:
    download_mb_per_minute: 100
    upload_mb_per_minute: 50

ipfs:
  enabled: true
  endpoint: https://api.pinata.cloud/psa
  bearer_token: ${IPFS_BEARER_TOKEN}
  gateway_url: https://gateway.pinata.cloud/ipfs/
  auto_pin: false

transcoding:
  work_dir: /tmp/blossom-transcode
  ffmpeg_path: ""
  hwaccel:
    type: auto  # none, nvenc, qsv, vaapi, auto
    codec: h264  # h264, hevc, av1
    device: /dev/dri/renderD128

ai_moderation:
  enabled: true
  scan_timeout: 30s
  max_file_size: 104857600
  scan_images: true
  scan_videos: true
  action_thresholds:
    csam: 0.001
    illegal: 0.5
    explicit_adult: 0.8
  providers:
    hash_blocklist:
      enabled: true
    custom_api:
      enabled: true
      endpoint: "https://my-ml-service.local/scan"

payment:
  enabled: true
  satoshis_per_byte: 0.000001  # 1 sat per MB
  min_payment_sats: 1
  free_bytes_limit: 104857600   # 100 MB free per user
  request_expiry_mins: 30
  lightning:
    enabled: true
    lnd_host: localhost
    rest_port: 8080
    tls_cert_path: /path/to/tls.cert
    macaroon_path: /path/to/invoice.macaroon
    invoice_memo: "Blossom Upload"
  cashu:
    enabled: true
    mint_urls:
      - "https://mint.minibits.cash/Bitcoin"

expiration:
  enabled: true          # Honor X-Expiration + run the cleanup worker
  cleanup_interval: 1h   # How often to scan for expired blobs
  batch_size: 1000       # Max blobs deleted per cleanup run
  grace_period: 0s       # Delay after expiry before deletion
```

---

## Deployment

### ArgoCD GitOps

- **App:** `blossom-production` in argocd namespace
- **Source:** `overlays/production/blossom` in coldforge-config
- **Image updates:** Manual tag updates in kustomization.yaml

### Cloudflare Tunnel Routes

- files.cloistr.xyz → coldforge-blossom:80
- blossom.cloistr.xyz → coldforge-blossom:80

---

## Prometheus Metrics

- `cloistr_blossom_requests_total{method,path,status}`
- `cloistr_blossom_uploads_total{status,encryption}`
- `cloistr_blossom_downloads_total{status}`
- `cloistr_blossom_storage_bytes`
- `cloistr_blossom_stored_blobs`
- `cloistr_blossom_active_users`
- `cloistr_blossom_errors_total{type}`
- `cloistr_blossom_reports_total{reason}`
- `cloistr_blossom_payment_requests_total{method}` - Payment requests by method (lightning, cashu)
- `cloistr_blossom_payments_verified_total{method}` - Verified payments by method
- `cloistr_blossom_payment_required_total` - 402 responses issued
- `cloistr_blossom_payment_sats_received_total` - Total satoshis received
- `cloistr_blossom_free_tier_uploads_total` - Uploads within free tier
- `cloistr_blossom_free_tier_bytes_used_total` - Bytes consumed from free tier

---

## Completed Features (History)

- BUD-10 Blossom URI Schema (2026-03-25)
- BUD-03 User Server List (2026-03-25)
- BUD-07 Payments - Lightning/Cashu (2026-03-25)
- Analytics Dashboard (2026-03-23)
- Federation - Nostr cross-server discovery (2026-03-07)
- E2E Encryption UI (2026-03-06)
- AI Content Moderation (2026-03-05)
- Batch Operations (2026-03-05)
- Chunked/Resumable Uploads (2026-03-01)
- WebSocket Notifications (2026-03-01)
- Blob Expiration (2026-03-01)
- Multi-region Replication (2026-03-01)
- AV1/HEVC Support (2026-02-23)
- BUD-09 Reporting (2026-02-23)
- Deduplication (2026-02-23)
- Torrent Seeds (2026-02-21)
- GPU Transcoding (2026-02-20)

---

**Last Updated:** 2026-03-30
