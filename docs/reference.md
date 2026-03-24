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

---

## Completed Features (History)

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

**Last Updated:** 2026-03-23
