#!/usr/bin/env bash
# ci-verify.sh — reproduce the GitLab CI build environment locally.
#
# Why: a go.work workspace (e.g. ~/Development/go.work) can include sibling
# modules like cloistr-common locally, so `go build ./...` resolves them from
# your working tree regardless of the version pinned in go.mod. CI has no
# workspace and pulls the *tagged* dependency, so code using an unreleased API
# builds locally but fails in CI. Running with GOWORK=off resolves dependencies
# exactly the way CI does and catches that mismatch before you push.
#
# Usage:
#   scripts/ci-verify.sh          # build + vet (fast; default)
#   scripts/ci-verify.sh --test   # also run the full test suite (slower)
set -euo pipefail

cd "$(git rev-parse --show-toplevel)"

export GOWORK=off
export GOPRIVATE="${GOPRIVATE:-git.aegis-hq.xyz}"

echo "==> ci-verify: GOWORK=off (CI-equivalent dependency resolution)"

echo "==> go build ./..."
go build ./...

echo "==> go vet ./..."
go vet ./...

if [ "${1:-}" = "--test" ]; then
  echo "==> go test ./..."
  go test ./...
fi

echo "==> ci-verify OK"
