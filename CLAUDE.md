# CLAUDE.md - coldforge-blossom

**Nostr-native blob storage server (Go) - Blossom protocol implementation**

**Domain:** files.cloistr.xyz, blossom.cloistr.xyz (Cloistr is the consumer-facing brand for Coldforge Nostr services)

## REQUIRED READING (Before ANY Action)

**Claude MUST read this file at the start of every session:**
- `~/claude/coldforge/cloistr/CLAUDE.md` - Cloistr project rules (contains further required reading)

## Upstream

This is a fork of [sebdeveloper6952/blossom-server](https://github.com/sebdeveloper6952/blossom-server).

- **Upstream remote:** `upstream` (github.com/sebdeveloper6952/blossom-server)
- **Origin remote:** `origin` (git.coldforge.xyz/coldforge/coldforge-blossom)

To sync with upstream:
```bash
git fetch upstream
git merge upstream/master
```

## Autonomous Work Mode (CRITICAL)

**Work autonomously. Do NOT stop to ask what to do next.**

- Keep working until the task is complete or you hit a genuine blocker
- Use the "Next Steps" section below to know what to work on
- Make reasonable decisions - don't ask for permission on obvious choices
- Only stop to ask if there's a true ambiguity that affects architecture
- If tests fail, fix them. If code needs review, use the reviewer agent. Keep going.
- Update documentation as you make progress

## Agent Usage (IMPORTANT)

**Use agents proactively. Do not wait for explicit instructions.**

| When... | Use agent... |
|---------|-------------|
| Starting new work or need context | `explore` |
| Need to research NIPs or protocols | `explore` |
| Writing or modifying code | `reviewer` after significant changes |
| Writing tests | `test-writer` |
| Running tests | `tester` |
| Investigating bugs | `debugger` |
| Updating documentation | `documenter` |
| Creating Dockerfiles | `docker` |
| Setting up Kubernetes deployment | `atlas-deploy` |
| Security-sensitive code (auth, crypto) | `security` |

## Current Status

**Version:** v1.1.0
**Deployment:** ArgoCD GitOps via coldforge-config
**Image:** oci.coldforge.xyz/coldforge/coldforge-blossom:v1.1.0

### Implemented Features

| Feature | Status | Notes |
|---------|--------|-------|
| BUD-01 Server Info | ✅ | |
| BUD-02 Blob Upload | ✅ | With encryption support |
| BUD-04 Mirroring | ✅ | |
| BUD-05 Media Optimization | ✅ | /media endpoint with resize/compress |
| Thumbnail Generation | ✅ | /:hash/thumb endpoint (tested) |
| Video Transcoding | ✅ | HLS streaming with multi-bitrate support |
| Enhanced Blob Listing | ✅ | /list/:pubkey with filters & pagination |
| BUD-06 URL Upload | ✅ | |
| BUD-08 Negentropy | ✅ | Basic |
| S3 Storage Backend | ✅ | Ceph RGW via s3.coldforge.xyz |
| PostgreSQL Support | ✅ | postgres-rw.db.coldforge.xyz |
| Storage Quotas | ✅ | Per-pubkey limits |
| Server-side Encryption | ✅ | AES-256-GCM at rest |
| Prometheus Metrics | ✅ | /metrics endpoint |
| Grafana Dashboard | ✅ | coldforge-blossom dashboard |
| Content Moderation | ✅ | Reporting, blocklist, transparency |
| Admin Dashboard | ✅ | NIP-86 auth, /admin routes |
| Redis/Dragonfly Cache | ✅ | Optional shared cache |
| CDN Integration | ✅ | Presigned URLs, redirect support |
| Rate Limiting | ✅ | Per-IP/pubkey throttling, bandwidth limits |
| DASH Streaming | ✅ | Multi-bitrate DASH alongside HLS |
| WebVTT Subtitles | ✅ | Add/manage subtitle tracks for videos |
| IPFS Pinning | ✅ | Pin blobs to IPFS via pinning services |
| Drive Integration | ✅ | Tested with cloistr-drive web UI |
| GPU Transcoding | ✅ | NVENC, QSV, VAAPI hardware acceleration |
| Torrent Seeds | ✅ | Generate .torrent files with WebSeeds (BEP 19) |
| Deduplication | ✅ | Content-addressable dedup across users |
| BUD-09 Reporting | ✅ | NIP-56 signed reports, re-upload prevention |

## Project Structure

```
coldforge-blossom/
├── api/gin/              # Gin HTTP handlers
│   ├── auth_middleware.go
│   ├── admin_*.go        # Admin dashboard
│   ├── bud0*_controller.go
│   ├── metrics_middleware.go
│   ├── moderation_controller.go
│   ├── stats_controller.go
│   └── routes.go
├── cmd/api/              # Entry point
├── db/                   # Database (sqlc)
│   ├── migrations/
│   └── queries/
├── internal/
│   ├── cache/            # Redis/memory cache
│   ├── metrics/          # Prometheus metrics
│   └── storage/          # S3/local storage backends
├── src/
│   ├── bud-*/            # BUD implementations
│   ├── core/             # Domain types
│   ├── pkg/              # Utilities
│   └── service/          # Business logic
├── config.example.yml
├── Dockerfile
└── go.mod
```

## Quick Commands

```bash
# Run locally
cp config.example.yml config.yml
# Edit config.yml with your settings
go run ./cmd/api

# Run tests
go test ./...

# Build Docker image
docker build -t coldforge-blossom .

# Push to Harbor
docker tag coldforge-blossom oci.coldforge.xyz/coldforge/coldforge-blossom:v1.x.x
docker push oci.coldforge.xyz/coldforge/coldforge-blossom:v1.x.x
```

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

### Media Processing (BUD-05)

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| PUT | `/media` | Yes | Upload and optimize media |
| HEAD | `/media` | Yes | Get media upload requirements |
| GET | `/:hash/thumb` | No | Get thumbnail (w, h query params) |

### Video Streaming (HLS & DASH)

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| POST | `/:hash/transcode` | Yes | Start video transcoding |
| GET | `/:hash/transcode` | No | Get transcoding status |
| GET | `/:hash/hls/master.m3u8` | No | Get HLS master playlist |
| GET | `/:hash/hls/:quality/stream.m3u8` | No | Get HLS quality variant playlist |
| GET | `/:hash/hls/:quality/:segment` | No | Get HLS segment (.ts file) |
| GET | `/:hash/dash/manifest.mpd` | No | Get DASH manifest (MPD) |
| GET | `/:hash/dash/:segment` | No | Get DASH segment (.m4s file) |

### Subtitles (WebVTT)

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| PUT | `/:hash/subtitles/:lang` | Yes | Add/update subtitle track |
| GET | `/:hash/subtitles/:lang` | No | Get subtitle track (VTT) |
| GET | `/:hash/subtitles` | No | List all subtitle tracks |
| DELETE | `/:hash/subtitles/:lang` | Yes | Remove subtitle track |

**Query parameters for PUT:**
- `label` - Display name (defaults to language code)
- `default=true` - Set as default subtitle
- `forced=true` - Mark as forced (for foreign language parts)

**Subtitle workflow:**
1. Upload WebVTT file: `PUT /:hash/subtitles/en` with VTT content in body
2. Subtitles are automatically included in HLS/DASH manifests
3. Players supporting WebVTT will display subtitle options

**Supported languages:** en, es, fr, de, it, pt, ru, ja, ko, zh, ar, hi, nl, pl, tr, vi, th, id, sv, da, no, fi

### BUD-09 Content Reporting

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| PUT | `/report` | No | Submit BUD-09 NIP-56 signed report |
| POST | `/report` | No | Submit legacy JSON report |
| GET | `/transparency` | No | Get moderation stats and privacy statement |

**BUD-09 Report Format (PUT /report):**

The request body must be a signed NIP-56 report event (kind 1984):

```json
{
  "kind": 1984,
  "pubkey": "<reporter_pubkey>",
  "created_at": 1234567890,
  "content": "Human readable report details",
  "tags": [
    ["x", "<blob_sha256>", "<report_type>"],
    ["x", "<another_blob_sha256>", "illegal"]
  ],
  "id": "<event_id>",
  "sig": "<signature>"
}
```

**Report types (mapped from NIP-56):**
- `csam` - Child safety (highest priority)
- `illegal` - Illegal content
- `copyright` - Copyright violation
- `abuse` - Harassment/abuse (includes nudity, spam, impersonation)
- `other` - Other violations

**Re-upload Prevention:**

When content is removed due to a report, the blob hash is added to a blocklist. Attempts to re-upload the same content will fail with `ErrHashRemoved`.

**Legacy JSON Report (POST /report):**

```json
{
  "blob_hash": "<sha256>",
  "reason": "csam|illegal|copyright|abuse|other",
  "details": "Optional description",
  "reporter_pubkey": "Optional nostr pubkey"
}
```

### IPFS Pinning

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| POST | `/:hash/pin` | Yes | Pin blob to IPFS |
| DELETE | `/:hash/pin` | Yes | Unpin blob from IPFS |
| GET | `/:hash/pin` | No | Get pin status |
| GET | `/pins` | No | List all pins |

**Query parameters for POST:**
- `name` - Optional name for the pin

**Query parameters for GET /pins:**
- `status` - Filter by status (queued, pinning, pinned, failed)
- `limit` - Max results (1-1000, default 100)

**Supported pinning services:**
- Pinata (https://api.pinata.cloud/psa)
- web3.storage
- Filebase
- Any IPFS Pinning Service API compatible endpoint

### Torrent Seeds

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| POST | `/:hash/torrent` | Yes | Generate .torrent file for blob |
| GET | `/:hash/torrent` | No | Get cached .torrent file |
| DELETE | `/:hash/torrent` | Yes | Delete cached .torrent file |

**Query parameters for POST:**
- `tracker` - Tracker URL (can specify multiple)
- `webseed` - WebSeed URL (can specify multiple, defaults to server URL)
- `dht` - Enable DHT bootstrap nodes (default: true)
- `comment` - Optional comment in torrent file
- `created_by` - Creator identifier (default: coldforge-blossom)

**Response formats:**
- `Accept: application/json` - Returns torrent metadata (info_hash, magnet_uri, etc.)
- Default - Returns .torrent file with Content-Disposition header

**Features:**
- BEP 3: Standard BitTorrent metainfo
- BEP 5: DHT bootstrap nodes for tracker-less operation
- BEP 12: Multi-tracker support
- BEP 19: WebSeeds for HTTP fallback (points to Blossom server)
- Automatic piece length calculation based on file size
- Torrent files cached for 1 week

**Quality presets:** 720p (2500kbps), 480p (1000kbps), 360p (600kbps)

### Content Deduplication

Cloistr-blossom implements content-addressable deduplication, allowing multiple users to reference the same blob without storing duplicate data.

**How it works:**

1. When User A uploads a blob, it's stored and User A gets a reference
2. When User B uploads the same blob (same SHA-256 hash):
   - The server detects the blob already exists
   - Instead of re-storing, it creates a reference for User B
   - Storage space is saved (only one copy on disk)
3. When either user deletes their reference:
   - Only their reference is removed
   - The other user's access is unaffected
   - The actual blob is only deleted when the last reference is removed

**Database schema:**

```
blob_references:
  pubkey  TEXT NOT NULL  -- User who has access
  hash    TEXT NOT NULL  -- Blob hash (FK to blobs)
  created BIGINT NOT NULL
  PRIMARY KEY (pubkey, hash)

blobs:
  ... existing columns ...
  ref_count INTEGER NOT NULL DEFAULT 1  -- Number of references
```

**Quota behavior:**

- Each user's quota reflects their blob references, not actual storage
- If User A and B both reference a 10MB blob, both have 10MB counted against their quota
- This is fair: users pay for what they "own", while the server benefits from dedup

**Benefits:**

- Storage efficiency: Popular files are only stored once
- Instant uploads: Duplicate detection happens at upload time
- User isolation: Deleting a reference doesn't affect other users
- Transparent: Users don't need to know about deduplication

**Transcoding workflow:**
1. Upload video via `/upload`
2. Start transcoding: `POST /:hash/transcode`
3. Poll status: `GET /:hash/transcode` (returns progress %)
4. When complete, stream via:
   - HLS: `GET /:hash/hls/master.m3u8`
   - DASH: `GET /:hash/dash/manifest.mpd`

**Requirements:** FFmpeg must be installed on the server.

**Format Support:**
- **HLS** (HTTP Live Streaming): Best for Apple devices and Safari
- **DASH** (Dynamic Adaptive Streaming over HTTP): Best for cross-platform and modern browsers

### List Endpoint Filters

The `/list/:pubkey` endpoint supports the following query parameters:

| Parameter | Type | Description |
|-----------|------|-------------|
| `type` | string | MIME type prefix filter (e.g., `image/`, `video/mp4`) |
| `since` | int64 | Unix timestamp - return blobs created after this time |
| `until` | int64 | Unix timestamp - return blobs created before this time |
| `limit` | int | Max results (1-1000) |
| `offset` | int | Pagination offset |
| `sort` | string | `desc` for newest first, default is oldest first |

Example: `/list/abc123?type=image/&limit=20&sort=desc`

When filters are used, response includes pagination info:
```json
{
  "blobs": [...],
  "total": 150
}
```

## Deployment

### ArgoCD GitOps

The service is deployed via ArgoCD from the coldforge-config repo:

- **App:** `blossom-production` in argocd namespace
- **Source:** `overlays/production/blossom` in coldforge-config
- **Image updates:** Manual tag updates in kustomization.yaml

### Infrastructure Dependencies

| Service | Endpoint | Purpose |
|---------|----------|---------|
| PostgreSQL | postgres-rw.db.coldforge.xyz:5432 | Database |
| Ceph RGW | s3.coldforge.xyz | S3 storage |
| Dragonfly | dragonfly.dragonfly.svc.cluster.local:6379 | Cache |
| Prometheus | Via ServiceMonitor | Metrics |

### Cloudflare Tunnel Routes

- files.cloistr.xyz → coldforge-blossom:80
- blossom.cloistr.xyz → coldforge-blossom:80

## Configuration

Key environment variables / config options:

```yaml
database:
  driver: postgres  # or sqlite
  postgres:
    host: postgres-rw.db.coldforge.xyz
    port: 5432
    user: cloistr_blossom
    password: ${DB_PASSWORD}
    database: cloistr_blossom

storage:
  backend: s3  # or local
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
  public_url: https://cdn.example.com  # or use presigned_urls for private buckets
  presigned_urls: false
  presigned_expiry: 1h
  redirect: true  # 302 redirect to CDN instead of proxying

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
  endpoint: https://api.pinata.cloud/psa  # IPFS Pinning Service API endpoint
  bearer_token: ${IPFS_BEARER_TOKEN}       # API token
  gateway_url: https://gateway.pinata.cloud/ipfs/  # For accessing pinned content
  auto_pin: false                          # Auto-pin new uploads

transcoding:
  work_dir: /tmp/blossom-transcode  # Temporary directory for transcoding
  ffmpeg_path: ""                   # Auto-detect if empty
  hwaccel:
    type: auto                      # none, nvenc, qsv, vaapi, auto
    device: /dev/dri/renderD128     # VAAPI device path (optional)
    preset: ""                      # Encoder-specific preset (optional)
    look_ahead: 0                   # NVENC look-ahead frames (optional)
```

## Next Steps (Roadmap)

### P1 - High Priority

(All P1 items completed)

### P2 - Medium Priority

1. **AV1/HEVC Support** - Modern codec support for better compression

### Completed

- ~~BUD-09 Reporting~~ - NIP-56 signed reports with re-upload prevention (2026-02-23)
- ~~Deduplication~~ - Content-addressable dedup across users (2026-02-23)
- ~~Torrent Seeds~~ - BEP 3/5/12/19 compliant .torrent generation (2026-02-21)
- ~~GPU Transcoding~~ - NVENC, QSV, VAAPI hardware acceleration (2026-02-20)
- ~~Drive Frontend Integration~~ - Tested with cloistr-drive (2026-02-20)

## Monitoring

### Prometheus Metrics

- `cloistr_blossom_requests_total` - HTTP requests by method/path/status
- `cloistr_blossom_uploads_total` - Uploads by status/encryption
- `cloistr_blossom_downloads_total` - Downloads by status
- `cloistr_blossom_storage_bytes` - Total storage used
- `cloistr_blossom_stored_blobs` - Total blob count
- `cloistr_blossom_active_users` - Users with stored blobs
- `cloistr_blossom_errors_total` - Errors by type
- `cloistr_blossom_reports_total` - Content reports by reason

### Grafana Dashboard

Dashboard: "Cloistr Blossom" (uid: coldforge-blossom)

Panels: Overview stats, traffic, uploads/downloads, moderation, errors

## See Also

- Blossom Spec: https://github.com/hzrd149/blossom
- Upstream: https://github.com/sebdeveloper6952/blossom-server
- Atlas Role: ~/Atlas/roles/kube/coldforge-blossom
- Config Repo: ~/Development/coldforge-config/overlays/production/blossom
