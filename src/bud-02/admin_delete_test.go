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

func usedBytes(t *testing.T, svc core.Services, pubkey string) int64 {
	t.Helper()
	u, err := svc.Quota().GetUser(context.Background(), pubkey)
	require.NoError(t, err)
	return u.UsedBytes
}

// TestAdminDeleteBlob covers the admin removal path that test cleanup and
// erasure requests need: delete a blob by hash, take it off every owner's
// usage, and do NOT add it to the removed-blobs list (unlike a moderation
// "removed" action, which blocks the hash forever).
func TestAdminDeleteBlob(t *testing.T) {
	dbFile := "./db-TestAdminDeleteBlob.sqlite3"
	t.Cleanup(func() { os.Remove(dbFile) })

	ctx := context.Background()
	admin, _ := nostr.GetPublicKey(nostr.GeneratePrivateKey())
	alice, _ := nostr.GetPublicKey(nostr.GeneratePrivateKey())
	bob, _ := nostr.GetPublicKey(nostr.GeneratePrivateKey())
	conf := baseConf(dbFile, admin)
	svc := newTestServices(t, dbFile, conf)

	blobBytes := []byte("admin delete test blob, owned by two keys")
	hash, _ := hashing.Hash(blobBytes)
	size := int64(len(blobBytes))

	// Same bytes from two keys: one stored blob, two references, both charged.
	_, err := UploadBlob(ctx, svc, conf.CdnUrl, hash, alice, blobBytes, core.EncryptionModeNone, "")
	require.NoError(t, err)
	_, err = UploadBlob(ctx, svc, conf.CdnUrl, hash, bob, blobBytes, core.EncryptionModeNone, "")
	require.NoError(t, err)
	require.Equal(t, size, usedBytes(t, svc, alice))
	require.Equal(t, size, usedBytes(t, svc, bob))

	t.Run("dry run reports and changes nothing", func(t *testing.T) {
		res, err := AdminDeleteBlob(ctx, svc, hash, true)
		require.NoError(t, err)
		assert.True(t, res.DryRun)
		assert.Equal(t, size, res.Size)
		assert.ElementsMatch(t, []string{alice, bob}, res.Owners)
		assert.False(t, res.Deleted)

		exists, err := svc.Blob().Exists(ctx, hash)
		require.NoError(t, err)
		assert.True(t, exists)
		assert.Equal(t, size, usedBytes(t, svc, alice))
		assert.Equal(t, size, usedBytes(t, svc, bob))
	})

	t.Run("confirmed delete removes blob, references and usage", func(t *testing.T) {
		res, err := AdminDeleteBlob(ctx, svc, hash, false)
		require.NoError(t, err)
		assert.False(t, res.DryRun)
		assert.True(t, res.Deleted)
		assert.ElementsMatch(t, []string{alice, bob}, res.Owners)
		assert.ElementsMatch(t, []string{alice, bob}, res.Removed)

		// Exists reports a missing row as (false, sql.ErrNoRows).
		exists, _ := svc.Blob().Exists(ctx, hash)
		assert.False(t, exists, "blob row should be gone")
		for _, pk := range []string{alice, bob} {
			has, err := svc.Blob().HasReference(ctx, pk, hash)
			require.NoError(t, err)
			assert.False(t, has, "reference for %s should be gone", pk)
			assert.Equal(t, int64(0), usedBytes(t, svc, pk))
		}
	})

	t.Run("hash is not blocklisted, so it can be uploaded again", func(t *testing.T) {
		removed, err := svc.Moderation().IsHashRemoved(ctx, hash)
		require.NoError(t, err)
		assert.False(t, removed)

		_, err = UploadBlob(ctx, svc, conf.CdnUrl, hash, alice, blobBytes, core.EncryptionModeNone, "")
		require.NoError(t, err)
	})

	t.Run("unknown hash is not found", func(t *testing.T) {
		missing, _ := hashing.Hash([]byte("never uploaded"))
		_, err := AdminDeleteBlob(ctx, svc, missing, false)
		assert.ErrorIs(t, err, core.ErrBlobNotFound)
	})
}
