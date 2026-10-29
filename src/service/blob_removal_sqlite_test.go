package service

import (
	"context"
	"database/sql"
	"sync"
	"testing"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"git.aegis-hq.xyz/coldforge/cloistr-blossom/db"
	"git.aegis-hq.xyz/coldforge/cloistr-blossom/internal/storage"
	"git.aegis-hq.xyz/coldforge/cloistr-blossom/src/core"
)

// The bytes of a blob may be removed only when no reference row remains.
// The ref_count counter used to decide, and it can be wrong: a failed
// decrement left it at 0, and a dedup add (reference insert, then increment,
// as separate statements) racing the last removal let it reach 0 while the
// new owner's reference existed. Either way another owner's file was deleted.

const (
	rmAlice = "00000000000000000000000000000000000000000000000000000000000000a1"
	rmBob   = "00000000000000000000000000000000000000000000000000000000000000b2"
	rmHash  = "00000000000000000000000000000000000000000000000000000000000000d4"
)

var rmData = []byte("bytes two keys own")

func removalEnv(t *testing.T) (*sql.DB, *db.Queries, storage.StorageBackend, core.BlobStorage) {
	t.Helper()
	database, err := db.NewDBWithConfig(
		db.DBConfig{Driver: "sqlite", DSN: t.TempDir() + "/removal.sqlite3"},
		"../../db/migrations",
	)
	require.NoError(t, err)
	t.Cleanup(func() { database.Close() })
	store, err := storage.NewLocalStorage(t.TempDir())
	require.NoError(t, err)
	queries := db.New(database)
	blobs, err := NewBlobService(database, queries, store, nil, "http://test.invalid", zap.NewNop())
	require.NoError(t, err)
	return database, queries, store, blobs
}

func requireStillOwned(t *testing.T, store storage.StorageBackend, blobs core.BlobStorage, owner string) {
	t.Helper()
	ctx := context.Background()
	has, err := blobs.HasReference(ctx, owner, rmHash)
	require.NoError(t, err)
	require.True(t, has, "the other owner's reference is intact")
	exists, err := blobs.Exists(ctx, rmHash)
	require.NoError(t, err)
	require.True(t, exists, "the blob row must remain while someone owns it")
	r, err := store.Get(ctx, rmHash)
	require.NoError(t, err, "the bytes must remain while someone owns them")
	r.Close()
}

func TestDeleteReference_DecrementErrorNeverRemovesBytes(t *testing.T) {
	database, _, store, blobs := removalEnv(t)
	ctx := context.Background()
	for _, pk := range []string{rmAlice, rmBob} {
		_, _, err := blobs.SaveWithDedup(ctx, pk, rmHash, "", int64(len(rmData)), "text/plain", rmData, 1, core.EncryptionModeNone)
		require.NoError(t, err)
	}

	_, err := database.Exec(`CREATE TRIGGER fail_decrement BEFORE UPDATE OF ref_count ON blobs
		WHEN NEW.ref_count < OLD.ref_count
		BEGIN SELECT RAISE(ABORT, 'decrement failed'); END;`)
	require.NoError(t, err)

	_, _ = blobs.DeleteReference(ctx, rmAlice, rmHash)
	requireStillOwned(t, store, blobs, rmBob)
}

func TestDeleteReference_ReferenceAddedBeforeCountUpdateKeepsBytes(t *testing.T) {
	_, queries, store, blobs := removalEnv(t)
	ctx := context.Background()
	_, _, err := blobs.SaveWithDedup(ctx, rmAlice, rmHash, "", int64(len(rmData)), "text/plain", rmData, 1, core.EncryptionModeNone)
	require.NoError(t, err)

	// Bob's dedup add has inserted his reference but not yet incremented
	// ref_count when Alice removes hers.
	_, err = queries.CreateBlobReference(ctx, db.CreateBlobReferenceParams{Pubkey: rmBob, Hash: rmHash, Created: 1})
	require.NoError(t, err)

	_, err = blobs.DeleteReference(ctx, rmAlice, rmHash)
	require.NoError(t, err)
	requireStillOwned(t, store, blobs, rmBob)
}

func TestDeleteReference_RacingDedupAddNeverLosesTheNewOwnersBytes(t *testing.T) {
	_, _, store, blobs := removalEnv(t)
	ctx := context.Background()

	for round := 0; round < 40; round++ {
		_, _, err := blobs.SaveWithDedup(ctx, rmAlice, rmHash, "", int64(len(rmData)), "text/plain", rmData, 1, core.EncryptionModeNone)
		require.NoError(t, err)

		var wg sync.WaitGroup
		var bobErr error
		start := make(chan struct{})
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			_, _ = blobs.DeleteReference(ctx, rmAlice, rmHash)
		}()
		go func() {
			defer wg.Done()
			<-start
			_, _, bobErr = blobs.SaveWithDedup(ctx, rmBob, rmHash, "", int64(len(rmData)), "text/plain", rmData, 1, core.EncryptionModeNone)
		}()
		close(start)
		wg.Wait()

		if bobErr == nil {
			requireStillOwned(t, store, blobs, rmBob)
		}
		// Reset for the next round.
		_, _ = blobs.DeleteReference(ctx, rmBob, rmHash)
		_, _ = blobs.DeleteReference(ctx, rmAlice, rmHash)
	}
	assert.True(t, true)
}
