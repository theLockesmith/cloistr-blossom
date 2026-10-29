package service

import (
	"bytes"
	"context"
	"database/sql"
	"io/fs"
	"path/filepath"
	"testing"
	"time"

	"go.uber.org/zap"

	"git.aegis-hq.xyz/coldforge/cloistr-blossom/db"
	"git.aegis-hq.xyz/coldforge/cloistr-blossom/internal/storage"
	"git.aegis-hq.xyz/coldforge/cloistr-blossom/src/core"
)

// newTusStoreOn returns a store on database and the directory its chunk
// bytes go to, so a test can count what is left in blob storage.
func newTusStoreOn(t *testing.T, database *sql.DB) (core.TusUploadStore, string) {
	t.Helper()
	dir := t.TempDir()
	objects, err := storage.NewLocalStorage(dir)
	if err != nil {
		t.Fatalf("NewLocalStorage: %v", err)
	}
	return NewTusStore(database, db.New(database), objects, zap.NewNop()), dir
}

func newSQLiteTusStore(t *testing.T) (core.TusUploadStore, string) {
	t.Helper()
	database, err := db.NewDBWithConfig(
		db.DBConfig{Driver: "sqlite", DSN: t.TempDir() + "/tus.sqlite3"},
		"../../db/migrations",
	)
	if err != nil {
		t.Fatalf("open sqlite test database: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	return newTusStoreOn(t, database)
}

func countObjects(t *testing.T, dir string) int {
	t.Helper()
	n := 0
	err := filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			n++
		}
		return err
	})
	if err != nil {
		t.Fatalf("walk %s: %v", dir, err)
	}
	return n
}

func createTusUpload(t *testing.T, store core.TusUploadStore, id string, size int64, expiresAt int64) {
	t.Helper()
	err := store.Create(context.Background(), &core.TusUpload{
		ID: id, Pubkey: "pk", Size: size, Metadata: map[string]string{"filetype": "text/plain"},
		CreatedAt: time.Now().Unix(), ExpiresAt: expiresAt,
	})
	if err != nil {
		t.Fatalf("Create %s: %v", id, err)
	}
}

// expireTusUpload backdates an upload's expiry, as if its last chunk were
// long ago.
func expireTusUpload(t *testing.T, store core.TusUploadStore, id string, at int64) {
	t.Helper()
	_, err := store.(*tusStore).db.Exec("UPDATE tus_uploads SET expires_at = $1 WHERE id = $2", at, id)
	if err != nil {
		t.Fatalf("expire %s: %v", id, err)
	}
}

func TestTusStore_ChunksAssembleInOrder(t *testing.T) {
	store, _ := newSQLiteTusStore(t)
	ctx := context.Background()
	createTusUpload(t, store, "u", 11, time.Now().Add(time.Hour).Unix())

	if off, err := store.AppendChunk(ctx, "u", 0, []byte("hello")); err != nil || off != 5 {
		t.Fatalf("first chunk: offset %d, err %v", off, err)
	}
	if off, err := store.AppendChunk(ctx, "u", 5, []byte(" world")); err != nil || off != 11 {
		t.Fatalf("second chunk: offset %d, err %v", off, err)
	}
	got, err := store.ReadAll(ctx, "u")
	if err != nil || string(got) != "hello world" {
		t.Fatalf("ReadAll = %q, %v", got, err)
	}
	u, err := store.Get(ctx, "u")
	if err != nil || u.Offset != 11 || u.Metadata["filetype"] != "text/plain" {
		t.Fatalf("Get = %+v, %v", u, err)
	}
}

// A chunk written for an offset the upload has moved past is refused, and
// its bytes do not stay behind in blob storage.
func TestTusStore_StaleOffsetIsRefusedAndLeavesNoBytes(t *testing.T) {
	store, dir := newSQLiteTusStore(t)
	ctx := context.Background()
	createTusUpload(t, store, "u", 10, time.Now().Add(time.Hour).Unix())

	if _, err := store.AppendChunk(ctx, "u", 0, []byte("first")); err != nil {
		t.Fatalf("first chunk: %v", err)
	}
	if _, err := store.AppendChunk(ctx, "u", 0, []byte("again")); err != core.ErrTusOffsetConflict {
		t.Fatalf("stale chunk: err %v, want ErrTusOffsetConflict", err)
	}
	if n := countObjects(t, dir); n != 1 {
		t.Fatalf("%d objects in blob storage, want 1 (the refused chunk's bytes must go)", n)
	}
	got, _ := store.ReadAll(ctx, "u")
	if string(got) != "first" {
		t.Fatalf("ReadAll = %q, want the first chunk only", got)
	}
}

func TestTusStore_DeleteRemovesRowAndBytes(t *testing.T) {
	store, dir := newSQLiteTusStore(t)
	ctx := context.Background()
	createTusUpload(t, store, "u", 10, time.Now().Add(time.Hour).Unix())
	_, _ = store.AppendChunk(ctx, "u", 0, []byte("12345"))
	_, _ = store.AppendChunk(ctx, "u", 5, []byte("678"))

	if err := store.Delete(ctx, "u"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := store.Get(ctx, "u"); err != core.ErrTusUploadNotFound {
		t.Fatalf("Get after Delete: err %v", err)
	}
	if n := countObjects(t, dir); n != 0 {
		t.Fatalf("%d chunk objects left after Delete", n)
	}
	if err := store.Delete(ctx, "u"); err != core.ErrTusUploadNotFound {
		t.Fatalf("second Delete: err %v, want ErrTusUploadNotFound", err)
	}
	if _, err := store.AppendChunk(ctx, "u", 8, []byte("9")); err != core.ErrTusOffsetConflict {
		t.Fatalf("chunk for a deleted upload: err %v, want ErrTusOffsetConflict", err)
	}
	if n := countObjects(t, dir); n != 0 {
		t.Fatalf("a chunk for a deleted upload left %d objects", n)
	}
}

func TestTusStore_DeleteExpiredKeepsLiveUploads(t *testing.T) {
	store, dir := newSQLiteTusStore(t)
	ctx := context.Background()
	now := time.Now().Unix()
	createTusUpload(t, store, "old", 5, now+3600)
	createTusUpload(t, store, "live", 5, now+3600)
	_, _ = store.AppendChunk(ctx, "old", 0, []byte("ab"))
	_, _ = store.AppendChunk(ctx, "live", 0, []byte("cd"))
	expireTusUpload(t, store, "old", now-1)

	n, err := store.DeleteExpired(ctx, now)
	if err != nil || n != 1 {
		t.Fatalf("DeleteExpired = %d, %v; want 1", n, err)
	}
	if _, err := store.Get(ctx, "old"); err != core.ErrTusUploadNotFound {
		t.Fatalf("expired upload still there: %v", err)
	}
	if got, err := store.ReadAll(ctx, "live"); err != nil || !bytes.Equal(got, []byte("cd")) {
		t.Fatalf("live upload = %q, %v", got, err)
	}
	if n := countObjects(t, dir); n != 1 {
		t.Fatalf("%d objects left, want only the live upload's chunk", n)
	}
}
