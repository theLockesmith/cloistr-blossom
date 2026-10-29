package bud04

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"sync/atomic"
	"testing"

	"github.com/nbd-wtf/go-nostr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"git.aegis-hq.xyz/coldforge/cloistr-blossom/db"
	"git.aegis-hq.xyz/coldforge/cloistr-blossom/src/core"
	"git.aegis-hq.xyz/coldforge/cloistr-blossom/src/pkg/config"
	"git.aegis-hq.xyz/coldforge/cloistr-blossom/src/pkg/hashing"
	"git.aegis-hq.xyz/coldforge/cloistr-blossom/src/pkg/logging"
	"git.aegis-hq.xyz/coldforge/cloistr-blossom/src/service"
)

func newMirrorTestServices(t *testing.T, name string, maxUpload int) (core.Services, *config.Config) {
	t.Helper()
	dbFile := "./db-" + name + ".sqlite3"
	t.Cleanup(func() { os.Remove(dbFile) })

	conf := &config.Config{
		DbPath:      dbFile,
		CdnUrl:      "http://localhost:8000",
		AdminPubkey: "0000000000000000000000000000000000000000000000000000000000000000",
		AccessControlRules: []config.AccessControlRule{
			{Action: string(core.ACRActionAllow), Pubkey: "ALL", Resource: string(core.ResourceUpload)},
		},
		AllowedMimeTypes:   []string{"*"},
		MaxUploadSizeBytes: maxUpload,
		Quota:              config.QuotaConfig{Enabled: true, DefaultBytes: 1 << 20, MaxBytes: 1 << 30},
	}
	conf.ApplyDefaults()

	logger, err := logging.NewLog("ERROR")
	require.NoError(t, err)
	database, err := db.NewDB(dbFile, "../../db/migrations")
	require.NoError(t, err)
	svc := service.New(context.Background(), database, db.New(database), conf, nil, logger)
	require.NoError(t, svc.Init(context.Background()))
	return svc, conf
}

// blobSource serves body at any path and counts the requests it receives.
func blobSource(t *testing.T, body []byte) (*url.URL, *atomic.Int32) {
	t.Helper()
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	u, err := url.Parse(srv.URL + "/blob")
	require.NoError(t, err)
	return u, &hits
}

// allowLoopbackSources lets a test mirror from its own httptest server.
func allowLoopbackSources(t *testing.T) {
	t.Helper()
	prev := fetchPolicy
	fetchPolicy.AllowPrivateAddresses = true
	t.Cleanup(func() { fetchPolicy = prev })
}

// A mirror source on a private, loopback or metadata address must be refused
// without being contacted, and the error must not echo the address back.
func TestMirror_RefusesPrivateSource(t *testing.T) {
	svc, conf := newMirrorTestServices(t, "TestMirror_RefusesPrivateSource", 1<<20)
	body := []byte("internal admin page")
	hash, _ := hashing.Hash(body)
	src, hits := blobSource(t, body)
	pk, _ := nostr.GetPublicKey(nostr.GeneratePrivateKey())

	_, err := MirrorBlob(context.Background(), svc, conf.CdnUrl, pk, hash, *src, core.EncryptionModeNone)

	require.ErrorIs(t, err, ErrSourceNotPermitted)
	assert.NotContains(t, err.Error(), src.Host)
	assert.Zero(t, hits.Load(), "the private source must never be contacted")
}

// A mirrored blob counts against the mirroring user's quota like an upload.
func TestMirror_RecordsUsage(t *testing.T) {
	allowLoopbackSources(t)
	svc, conf := newMirrorTestServices(t, "TestMirror_RecordsUsage", 1<<20)
	body := []byte("a blob worth mirroring")
	hash, _ := hashing.Hash(body)
	src, _ := blobSource(t, body)
	pk, _ := nostr.GetPublicKey(nostr.GeneratePrivateKey())

	blob, err := MirrorBlob(context.Background(), svc, conf.CdnUrl, pk, hash, *src, core.EncryptionModeNone)
	require.NoError(t, err)
	assert.Equal(t, hash, blob.Sha256)

	info, err := svc.Quota().GetQuotaInfo(context.Background(), pk)
	require.NoError(t, err)
	assert.Equal(t, int64(len(body)), info.UsedBytes)
}

// A source larger than the upload cap is refused, and the read stops at the
// cap instead of buffering the whole response.
func TestMirror_RefusesOversizedSource(t *testing.T) {
	allowLoopbackSources(t)
	svc, conf := newMirrorTestServices(t, "TestMirror_RefusesOversizedSource", 16)
	body := []byte("this body is longer than sixteen bytes")
	hash, _ := hashing.Hash(body)
	src, _ := blobSource(t, body)
	pk, _ := nostr.GetPublicKey(nostr.GeneratePrivateKey())

	_, err := MirrorBlob(context.Background(), svc, conf.CdnUrl, pk, hash, *src, core.EncryptionModeNone)
	require.Error(t, err)
}
