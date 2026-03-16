# CLAUDE.md - coldforge-blossom

**Nostr-native blob storage server (Go) - Blossom protocol implementation**

**Version:** v1.1.0 | **Domain:** files.cloistr.xyz, blossom.cloistr.xyz

## Required Reading

| Document | Purpose |
|----------|---------|
| `~/claude/coldforge/cloistr/CLAUDE.md` | Cloistr project rules |
| [docs/reference.md](docs/reference.md) | Full API, config, deployment details |

## Upstream

Fork of [sebdeveloper6952/blossom-server](https://github.com/sebdeveloper6952/blossom-server).

```bash
git fetch upstream && git merge upstream/master  # Sync with upstream
```

## Autonomous Work Mode

**Work autonomously. Do NOT stop to ask what to do next.**

- Keep working until task complete or genuine blocker
- Make reasonable decisions - don't ask permission on obvious choices
- If tests fail, fix them. Use reviewer agent. Keep going.

## Agent Usage

| When | Agent |
|------|-------|
| Starting work / need context | `explore` |
| After significant code changes | `reviewer` |
| Writing/running tests | `test-writer` / `tester` |
| Security-sensitive code | `security` |

## Implemented Features

| Feature | Status |
|---------|--------|
| BUD-01/02/04/05/06/08 | Done |
| Video Transcoding (HLS/DASH) | Done |
| GPU Transcoding (NVENC/QSV/VAAPI) | Done |
| S3 Storage + PostgreSQL | Done |
| Server-side Encryption | Done |
| E2E Encryption UI | Done |
| Content Moderation + AI | Done |
| Rate Limiting + Quotas | Done |
| IPFS Pinning | Done |
| Torrent Seeds (BEP 19) | Done |
| Chunked/Resumable Uploads (tus) | Done |
| WebSocket Notifications | Done |
| Federation (kind 1063/10063) | Done |
| Deduplication | Done |

## Project Structure

```
cmd/api/              Entry point
api/gin/              HTTP handlers (admin, BUD controllers, moderation)
db/                   Database (sqlc, migrations)
internal/
  cache/              Redis/memory cache
  metrics/            Prometheus metrics
  storage/            S3/local backends
src/
  bud-*/              BUD implementations
  core/               Domain types
  service/            Business logic
```

## Quick Commands

```bash
cp config.example.yml config.yml && go run ./cmd/api  # Run locally
go test ./...                                          # Run tests

# Docker
docker build -t coldforge-blossom .
docker tag coldforge-blossom oci.coldforge.xyz/coldforge/coldforge-blossom:v1.x.x
docker push oci.coldforge.xyz/coldforge/coldforge-blossom:v1.x.x
```

## Core Endpoints

| Method | Path | Description |
|--------|------|-------------|
| GET/HEAD | `/:hash` | Retrieve/check blob |
| PUT | `/upload` | Upload blob |
| DELETE | `/:hash` | Delete blob |
| PUT | `/mirror` | Mirror from URL |
| GET | `/list/:pubkey` | List blobs |
| PUT | `/media` | Upload + optimize media |
| POST | `/:hash/transcode` | Start video transcoding |
| GET | `/:hash/hls/master.m3u8` | HLS stream |
| GET | `/:hash/dash/manifest.mpd` | DASH stream |

**Full API:** See [docs/reference.md](docs/reference.md) for all endpoints.

## Infrastructure

| Service | Endpoint |
|---------|----------|
| PostgreSQL | postgres-rw.db.coldforge.xyz:5432 |
| Ceph RGW | s3.coldforge.xyz |
| Dragonfly | dragonfly.dragonfly.svc.cluster.local:6379 |

## Roadmap

| Item | Priority |
|------|----------|
| Analytics Dashboard | P1 |

## Monitoring

- Metrics: `/metrics` (Prometheus)
- Dashboard: "Cloistr Blossom" (uid: coldforge-blossom)

## See Also

- [Blossom Spec](https://github.com/hzrd149/blossom)
- Atlas Role: `~/Atlas/roles/kube/coldforge-blossom`
- Config: `~/Development/coldforge-config/overlays/production/blossom`
