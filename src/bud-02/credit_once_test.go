package bud02

import (
	"context"
	"os"
	"sync"
	"testing"

	"github.com/nbd-wtf/go-nostr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"git.aegis-hq.xyz/coldforge/cloistr-blossom/db"
	"git.aegis-hq.xyz/coldforge/cloistr-blossom/src/core"
	"git.aegis-hq.xyz/coldforge/cloistr-blossom/src/pkg/hashing"
	"git.aegis-hq.xyz/coldforge/cloistr-blossom/src/pkg/logging"
	"git.aegis-hq.xyz/coldforge/cloistr-blossom/src/service"
)

// lockedQuota is countingQuota safe for concurrent deletes.
type lockedQuota struct {
	mu sync.Mutex
	countingQuota
}

func (q *lockedQuota) IncrementUsage(ctx context.Context, pk string, n int64) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.countingQuota.IncrementUsage(ctx, pk, n)
}

func (q *lockedQuota) DecrementUsage(ctx context.Context, pk string, n int64) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.countingQuota.DecrementUsage(ctx, pk, n)
}

type lockedServices struct {
	core.Services
	quota *lockedQuota
}

func (s lockedServices) Quota() core.QuotaService { return s.quota }

// Two deletes of the same reference used to both pass the ownership check,
// both credit the key, and both decrement the blob's ref count, which could
// delete the bytes while another key still owned them.
func TestDelete_ConcurrentDeletesCreditOnce(t *testing.T) {
	dbFile := "./db-TestDelete_ConcurrentDeletesCreditOnce.sqlite3"
	t.Cleanup(func() { os.Remove(dbFile) })

	alice, _ := nostr.GetPublicKey(nostr.GeneratePrivateKey())
	bob, _ := nostr.GetPublicKey(nostr.GeneratePrivateKey())
	conf := baseConf(dbFile, alice)
	svc := lockedServices{Services: newTestServices(t, dbFile, conf), quota: &lockedQuota{countingQuota: countingQuota{usage: map[string]int64{}}}}

	ctx := context.Background()
	hash, _ := hashing.Hash(textBytes)
	upload := func(pk string) {
		_, err := UploadBlob(ctx, svc, conf.CdnUrl, hash, pk, textBytes, core.EncryptionModeNone, "")
		require.NoError(t, err)
	}
	upload(bob)

	for round := 0; round < 40; round++ {
		upload(alice)

		var wg sync.WaitGroup
		start := make(chan struct{})
		for i := 0; i < 2; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				_ = DeleteBlob(ctx, svc, alice, hash, hash)
			}()
		}
		close(start)
		wg.Wait()

		require.Equal(t, int64(0), svc.quota.usage[alice], "round %d: alice must be credited exactly once", round)
		has, err := svc.Blob().HasReference(ctx, bob, hash)
		require.NoError(t, err)
		require.True(t, has, "round %d: bob still owns the blob", round)
		exists, err := svc.Blob().Exists(ctx, hash)
		require.NoError(t, err)
		require.True(t, exists, "round %d: bob's blob must not be deleted", round)
	}
	assert.Equal(t, int64(len(textBytes)), svc.quota.usage[bob])
}

// A new blob whose ownership row cannot be written used to be stored and
// charged anyway, with no reference the key could ever delete to get the
// charge back.
func TestUpload_FailedReferenceInsertLeavesUsageUnchanged(t *testing.T) {
	dbFile := "./db-TestUpload_FailedReferenceInsert.sqlite3"
	t.Cleanup(func() { os.Remove(dbFile) })

	pk, _ := nostr.GetPublicKey(nostr.GeneratePrivateKey())
	conf := baseConf(dbFile, pk)
	conf.ApplyDefaults()
	logger, err := logging.NewLog("ERROR")
	require.NoError(t, err)
	database, err := db.NewDB(dbFile, "../../db/migrations")
	require.NoError(t, err)
	_, err = database.Exec(`CREATE TRIGGER fail_reference_insert BEFORE INSERT ON blob_references
		BEGIN SELECT RAISE(ABORT, 'reference insert failed'); END;`)
	require.NoError(t, err)
	inner := service.New(context.Background(), database, db.New(database), conf, nil, logger)
	require.NoError(t, inner.Init(context.Background()))
	svc := countingServices{Services: inner, quota: &countingQuota{usage: map[string]int64{}}}

	hash, _ := hashing.Hash(textBytes)
	_, err = UploadBlob(context.Background(), svc, conf.CdnUrl, hash, pk, textBytes, core.EncryptionModeNone, "")
	assert.Error(t, err, "the upload must fail when its ownership cannot be recorded")
	assert.Equal(t, int64(0), svc.quota.usage[pk], "usage must be unchanged")
}
