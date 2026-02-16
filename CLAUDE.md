# CLAUDE.md - coldforge-blossom

**Nostr-native blob storage server (Go) - Blossom protocol implementation**

**Domain:** files.cloistr.xyz, blossom.cloistr.xyz (Cloistr is the consumer-facing brand for Coldforge Nostr services)

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
| BUD-05 User Search | ❌ | TODO |
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
```

## Next Steps (Roadmap)

### P1 - High Priority

1. **Image Optimization** - Resize/compress images on upload
2. **BUD-05 User Search** - List blobs by pubkey with filters
3. **Drive Frontend Integration** - Test with Drive web UI

### P2 - Medium Priority

4. **Video Transcoding** - HLS/DASH streaming support
5. **CDN Integration** - Cloudflare R2 or similar
6. **Bandwidth Throttling** - Rate limiting per pubkey

### P3 - Nice to Have

7. **IPFS Pinning** - Pin blobs to IPFS
8. **Torrent Seeds** - Generate .torrent files
9. **Deduplication** - Content-addressable dedup

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
