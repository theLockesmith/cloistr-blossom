package bud04

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"sync/atomic"
	"testing"
	"time"

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

func newMirrorTestServices(t *testing.T, name string, maxUpload int, extraRules ...config.AccessControlRule) (core.Services, *config.Config) {
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
	conf.AccessControlRules = append(conf.AccessControlRules, extraRules...)
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

	require.ErrorIs(t, err, ErrSourceFetch)
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

// A refused private address and a name that does not resolve must look the
// same to the caller, or the error tells them which internal names exist.
func TestMirror_BlockedAndUnreachableLookTheSame(t *testing.T) {
	svc, conf := newMirrorTestServices(t, "TestMirror_BlockedAndUnreachableLookTheSame", 1<<20)
	body := []byte("internal admin page")
	hash, _ := hashing.Hash(body)
	private, _ := blobSource(t, body)
	missing, err := url.Parse("http://no-such-host.invalid/blob")
	require.NoError(t, err)
	pk, _ := nostr.GetPublicKey(nostr.GeneratePrivateKey())

	_, blocked := MirrorBlob(context.Background(), svc, conf.CdnUrl, pk, hash, *private, core.EncryptionModeNone)
	_, unreachable := MirrorBlob(context.Background(), svc, conf.CdnUrl, pk, hash, *missing, core.EncryptionModeNone)

	require.Error(t, blocked)
	require.Error(t, unreachable)
	assert.Equal(t, unreachable.Error(), blocked.Error())
}

// A key the access rules refuse must not make the server contact the source.
func TestMirror_ChecksAccessBeforeFetch(t *testing.T) {
	allowLoopbackSources(t)
	pk, _ := nostr.GetPublicKey(nostr.GeneratePrivateKey())
	svc, conf := newMirrorTestServices(t, "TestMirror_ChecksAccessBeforeFetch", 1<<20,
		config.AccessControlRule{Action: string(core.ACRActionDeny), Pubkey: pk, Resource: string(core.ResourceUpload)})
	body := []byte("a blob worth mirroring")
	hash, _ := hashing.Hash(body)
	src, hits := blobSource(t, body)

	_, err := MirrorBlob(context.Background(), svc, conf.CdnUrl, pk, hash, *src, core.EncryptionModeNone)

	require.Error(t, err)
	assert.Zero(t, hits.Load(), "a refused key must not trigger a fetch")
}

// A key whose quota is already full must not make the server contact the
// source.
func TestMirror_ChecksQuotaBeforeFetch(t *testing.T) {
	allowLoopbackSources(t)
	svc, conf := newMirrorTestServices(t, "TestMirror_ChecksQuotaBeforeFetch", 1<<20)
	pk, _ := nostr.GetPublicKey(nostr.GeneratePrivateKey())
	require.NoError(t, svc.Quota().IncrementUsage(context.Background(), pk, 1<<20))
	body := []byte("a blob worth mirroring")
	hash, _ := hashing.Hash(body)
	src, hits := blobSource(t, body)

	_, err := MirrorBlob(context.Background(), svc, conf.CdnUrl, pk, hash, *src, core.EncryptionModeNone)

	require.ErrorIs(t, err, core.ErrQuotaExceeded)
	assert.Zero(t, hits.Load(), "a key over quota must not trigger a fetch")
}

// A max upload size of 0 admits no non-empty upload. It must bound the read,
// not lift the bound: an endless source has to come back at once.
func TestMirror_ZeroMaxUploadBoundsTheRead(t *testing.T) {
	allowLoopbackSources(t)
	fetchPolicy.Timeout = 2 * time.Second
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		chunk := make([]byte, 32<<10)
		for r.Context().Err() == nil {
			if _, err := w.Write(chunk); err != nil {
				return
			}
		}
	}))
	t.Cleanup(srv.Close)
	src, err := url.Parse(srv.URL + "/endless")
	require.NoError(t, err)

	body, err := fetchSource(context.Background(), *src, 0)

	require.NoError(t, err, "the read must stop at the cap, not run into the timeout")
	assert.LessOrEqual(t, len(body), 1)
}
