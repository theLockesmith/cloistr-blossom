package bud02

import (
	"context"
	"os"
	"testing"

	"github.com/nbd-wtf/go-nostr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"git.aegis-hq.xyz/coldforge/cloistr-blossom/src/core"
	"git.aegis-hq.xyz/coldforge/cloistr-blossom/src/pkg/hashing"
)

// A key was charged every time it uploaded a blob, including a blob it
// already owned, but credited only once when it deleted it. Usage only grew.

type countingQuota struct {
	core.QuotaService
	usage map[string]int64
}

func (q *countingQuota) IsEnabled() bool                                 { return true }
func (q *countingQuota) CheckQuota(context.Context, string, int64) error { return nil }
func (q *countingQuota) IncrementUsage(_ context.Context, pk string, n int64) error {
	q.usage[pk] += n
	return nil
}
func (q *countingQuota) DecrementUsage(_ context.Context, pk string, n int64) error {
	q.usage[pk] -= n
	return nil
}

type countingServices struct {
	core.Services
	quota *countingQuota
}

func (s countingServices) Quota() core.QuotaService { return s.quota }

func TestUpload_ChargesOncePerOwnedBlob(t *testing.T) {
	dbFile := "./db-TestUpload_ChargesOncePerOwnedBlob.sqlite3"
	t.Cleanup(func() { os.Remove(dbFile) })

	alice, _ := nostr.GetPublicKey(nostr.GeneratePrivateKey())
	bob, _ := nostr.GetPublicKey(nostr.GeneratePrivateKey())
	conf := baseConf(dbFile, alice)
	svc := countingServices{Services: newTestServices(t, dbFile, conf), quota: &countingQuota{usage: map[string]int64{}}}

	ctx := context.Background()
	hash, _ := hashing.Hash(textBytes)
	size := int64(len(textBytes))
	upload := func(pk string) {
		_, err := UploadBlob(ctx, svc, conf.CdnUrl, hash, pk, textBytes, core.EncryptionModeNone, "")
		require.NoError(t, err)
	}

	upload(alice)
	upload(alice)
	assert.Equal(t, size, svc.quota.usage[alice], "re-uploading a blob the key owns is not charged again")

	upload(bob)
	assert.Equal(t, size, svc.quota.usage[bob], "another key's first copy of the same blob is charged")

	require.NoError(t, DeleteBlob(ctx, svc, alice, hash, hash))
	assert.Equal(t, int64(0), svc.quota.usage[alice], "upload twice, delete once: usage is back to the start")
	assert.Equal(t, size, svc.quota.usage[bob], "the other owner is unaffected")
}
