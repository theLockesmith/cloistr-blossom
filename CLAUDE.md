# CLAUDE.md - coldforge-blossom

**Nostr-native blob storage server (Go) - Blossom protocol implementation**

**Domain:** files.cloistr.xyz (Cloistr is the consumer-facing brand for Coldforge Nostr services)

## Documentation

Full documentation is maintained at:
`~/claude/coldforge/services/files/CLAUDE.md`

This file exists to help Claude Code find context when working in this repository.

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
- Use the "Next Steps" section in the service docs to know what to work on
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

## Workflow

1. **Before coding:** Read the service docs at `~/claude/coldforge/services/files/CLAUDE.md`
2. **While coding:** Write code, then use `reviewer` to check it
3. **Testing:** Use `test-writer` to create tests, `tester` to run them
4. **Before committing:** Use `security` for auth/crypto code
5. **Deployment:** Use `docker` for containers, `atlas-deploy` for Kubernetes

## Project Structure (Current Upstream)

```
coldforge-blossom/
├── api/gin/              # Gin HTTP handlers
│   ├── auth_middleware.go
│   ├── bud01_controller.go
│   ├── bud02_controller.go
│   ├── bud04_controller.go
│   ├── bud06_controller.go
│   ├── stats_controller.go
│   └── routes.go
├── client/               # Client library
├── cmd/                  # Entry points
├── db/                   # Database (sqlc)
│   ├── migrations/
│   ├── queries/
│   └── *.sql.go
├── src/                  # Core source
├── config.example.yml    # Config template
├── Dockerfile
└── go.mod
```

## Quick Commands

```bash
# Run locally
cp config.example.yml config.yml
# Edit config.yml
go run ./cmd/blossom

# Run tests
go test ./...

# Build Docker image
docker build -t coldforge-blossom .

# Database migrations
sql-migrate up
```

## Key Features (Current)

- BUD-01, BUD-02, BUD-04, BUD-06, BUD-08 (basic)
- Nostr auth (kind 24242)
- SQLite database
- Per-pubkey ACLs
- MIME type filtering
- Max upload size limits

## Features to Add

See `~/claude/coldforge/services/files/CLAUDE.md` for full roadmap:

1. S3 storage backend (P0)
2. PostgreSQL support (P0)
3. Storage quotas (P0)
4. Image optimization (P1)
5. Admin dashboard (P1)
6. BUD-05 user search (P1)
7. Video transcoding (P2)

## NIPs Referenced

- Kind 24242 - Blossom auth events

## BUDs Implemented

| BUD | Description | Status |
|-----|-------------|--------|
| BUD-01 | Server info | ✅ |
| BUD-02 | Blob upload | ✅ |
| BUD-04 | Mirroring | ✅ |
| BUD-05 | User search | ❌ TODO |
| BUD-06 | URL upload | ✅ |
| BUD-08 | Negentropy | ✅ (basic) |

## See Also

- Service Documentation: `~/claude/coldforge/services/files/CLAUDE.md`
- Coldforge Overview: `~/claude/coldforge/CLAUDE.md`
- Blossom Spec: https://github.com/hzrd149/blossom
- Upstream: https://github.com/sebdeveloper6952/blossom-server
