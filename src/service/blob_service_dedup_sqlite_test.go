package service

import (
	"context"
	"testing"

	_ "github.com/mattn/go-sqlite3"
	"go.uber.org/zap"

	"git.aegis-hq.xyz/coldforge/cloistr-blossom/db"
	"git.aegis-hq.xyz/coldforge/cloistr-blossom/internal/storage"
	"git.aegis-hq.xyz/coldforge/cloistr-blossom/src/core"
)

// Regression test for a SQLite-only dedup failure.
//
// HasBlobReference aliased its result column "exists", which is a reserved word
// in SQLite: the query failed with `near "exists": syntax error`, SaveWithDedup
// discarded that error, and the false it got back sent every re-save of an
// already-owned blob into an insert that conflicted and came back as
// "create reference: sql: no rows in result set".
//
// Postgres accepts the alias, so production never saw it and only self-hosted
// SQLite deployments did -- the deployment least likely to have someone able to
// diagnose it. Both halves are covered here: the reference check must WORK, and
// a repeat save must succeed rather than error.
func TestSaveWithDedupIsIdempotentOnSQLite(t *testing.T) {
	database, err := db.NewDBWithConfig(
		db.DBConfig{Driver: "sqlite", DSN: t.TempDir() + "/dedup.sqlite3"},
		"../../db/migrations",
	)
	if err != nil {
		t.Fatalf("open sqlite test database: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	queries := db.New(database)
	store, err := storage.NewLocalStorage(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocalStorage: %v", err)
	}
	blobs, err := NewBlobService(database, queries, store, nil, "http://test.invalid", zap.NewNop())
	if err != nil {
		t.Fatalf("NewBlobService: %v", err)
	}

	ctx := context.Background()
	const pubkey = "0000000000000000000000000000000000000000000000000000000000000001"
	const hash = "0000000000000000000000000000000000000000000000000000000000000002"
	data := []byte("some blob bytes")

	if _, isNew, err := blobs.SaveWithDedup(ctx, pubkey, hash, "", int64(len(data)), "text/plain", data, 1, core.EncryptionModeNone); err != nil {
		t.Fatalf("first save: %v", err)
	} else if !isNew {
		t.Error("first save reported the blob as pre-existing")
	}

	// This is the half that was silently broken: it returned (false, error) on
	// SQLite for a reference that plainly exists.
	has, err := blobs.HasReference(ctx, pubkey, hash)
	if err != nil {
		t.Fatalf("HasReference returned an error on SQLite: %v", err)
	}
	if !has {
		t.Fatal("HasReference says the uploader has no reference to a blob they just saved")
	}

	// And this is how the broken half surfaced: a repeat save failing outright.
	if _, isNew, err := blobs.SaveWithDedup(ctx, pubkey, hash, "", int64(len(data)), "text/plain", data, 1, core.EncryptionModeNone); err != nil {
		t.Fatalf("re-saving a blob the user already owns must be a no-op, got: %v", err)
	} else if isNew {
		t.Error("second save reported the blob as new")
	}

	// A second owner must get their own reference rather than an error.
	const otherPubkey = "0000000000000000000000000000000000000000000000000000000000000003"
	if _, isNew, err := blobs.SaveWithDedup(ctx, otherPubkey, hash, "", int64(len(data)), "text/plain", data, 1, core.EncryptionModeNone); err != nil {
		t.Fatalf("second owner save: %v", err)
	} else if isNew {
		t.Error("second owner's save reported the blob as new; it should have deduplicated")
	}
	if has, err := blobs.HasReference(ctx, otherPubkey, hash); err != nil || !has {
		t.Errorf("second owner has no reference (has=%v err=%v)", has, err)
	}
}
