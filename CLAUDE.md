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

**Quality presets:** 720p (2500kbps), 480p (1000kbps), 360p (600kbps)

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
    user: coldforge_blossom
    password: ${DB_PASSWORD}
    database: coldforge_blossom

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
```

## Next Steps (Roadmap)

### P1 - High Priority

1. **Drive Frontend Integration** - Test with Drive web UI

### P2 - Medium Priority

2. **WebVTT Subtitles** - Support for subtitle tracks in streams

### P3 - Nice to Have

3. **IPFS Pinning** - Pin blobs to IPFS
4. **Torrent Seeds** - Generate .torrent files
5. **Deduplication** - Content-addressable dedup
6. **GPU Transcoding** - Hardware acceleration for video

## Monitoring

### Prometheus Metrics

- `coldforge_blossom_requests_total` - HTTP requests by method/path/status
- `coldforge_blossom_uploads_total` - Uploads by status/encryption
- `coldforge_blossom_downloads_total` - Downloads by status
- `coldforge_blossom_storage_bytes` - Total storage used
- `coldforge_blossom_stored_blobs` - Total blob count
- `coldforge_blossom_active_users` - Users with stored blobs
- `coldforge_blossom_errors_total` - Errors by type
- `coldforge_blossom_reports_total` - Content reports by reason

### Grafana Dashboard

Dashboard: "Coldforge Blossom" (uid: coldforge-blossom)

Panels: Overview stats, traffic, uploads/downloads, moderation, errors

## See Also

- Blossom Spec: https://github.com/hzrd149/blossom
- Upstream: https://github.com/sebdeveloper6952/blossom-server
- Atlas Role: ~/Atlas/roles/kube/coldforge-blossom
- Config Repo: ~/Development/coldforge-config/overlays/production/blossom
