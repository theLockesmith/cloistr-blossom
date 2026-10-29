package service

import (
	"context"
	"sync"
	"testing"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"git.aegis-hq.xyz/coldforge/cloistr-blossom/db"
	"git.aegis-hq.xyz/coldforge/cloistr-blossom/internal/storage"
	"git.aegis-hq.xyz/coldforge/cloistr-blossom/src/core"
)

type lockedQuotaService struct {
	mu sync.Mutex
	*mockQuotaService
}

func (q *lockedQuotaService) DecrementUsage(ctx context.Context, pk string, n int64) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.mockQuotaService.DecrementUsage(ctx, pk, n)
}

// Two concurrent batch deletes of the same reference must credit it once and
// must not delete a blob another key still owns.
func TestBatchDelete_ConcurrentDeletesCreditOnce(t *testing.T) {
	database, err := db.NewDBWithConfig(
		db.DBConfig{Driver: "sqlite", DSN: t.TempDir() + "/batchdel.sqlite3"},
		"../../db/migrations",
	)
	require.NoError(t, err)
	t.Cleanup(func() { database.Close() })
	store, err := storage.NewLocalStorage(t.TempDir())
	require.NoError(t, err)
	blobs, err := NewBlobService(database, db.New(database), store, nil, "http://test.invalid", zap.NewNop())
	require.NoError(t, err)
	quota := &lockedQuotaService{mockQuotaService: newMockQuotaService(true)}
	svc, err := NewBatchService(blobs, store, quota, nil, nil, core.DefaultBatchConfig(), "http://test.invalid", zap.NewNop())
	require.NoError(t, err)

	ctx := context.Background()
	const (
		alice = "00000000000000000000000000000000000000000000000000000000000000a1"
		bob   = "00000000000000000000000000000000000000000000000000000000000000b2"
		hash  = "00000000000000000000000000000000000000000000000000000000000000c3"
	)
	data := []byte("shared bytes")
	_, _, err = blobs.SaveWithDedup(ctx, bob, hash, "", int64(len(data)), "text/plain", data, 1, core.EncryptionModeNone)
	require.NoError(t, err)

	for round := 0; round < 40; round++ {
		_, _, err = blobs.SaveWithDedup(ctx, alice, hash, "", int64(len(data)), "text/plain", data, 1, core.EncryptionModeNone)
		require.NoError(t, err)
		quota.quotaUsage[alice] = int64(len(data))

		var wg sync.WaitGroup
		start := make(chan struct{})
		for i := 0; i < 2; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				_, _ = svc.Delete(ctx, alice, &core.BatchDeleteRequest{Hashes: []string{hash}})
			}()
		}
		close(start)
		wg.Wait()

		require.Equal(t, int64(0), quota.quotaUsage[alice], "round %d: credited exactly once", round)
		has, err := blobs.HasReference(ctx, bob, hash)
		require.NoError(t, err)
		require.True(t, has, "round %d: bob still owns the blob", round)
		exists, err := blobs.Exists(ctx, hash)
		require.NoError(t, err)
		require.True(t, exists, "round %d: bob's blob must not be deleted", round)
	}
}
