package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"git.aegis-hq.xyz/coldforge/cloistr-blossom/internal/storage"
)

// Federated blob URLs come from other people's nostr events. A URL on a
// private or loopback address must be refused without being contacted.
func TestFederationDownload_RefusesPrivateSource(t *testing.T) {
	body := []byte("internal admin page")
	sum := sha256.Sum256(body)
	hash := hex.EncodeToString(sum[:])
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	store, err := storage.NewLocalStorage(t.TempDir())
	require.NoError(t, err)
	s := &federationService{storage: store, log: zap.NewNop()}

	err = s.downloadAndStore(context.Background(), hash, srv.URL+"/"+hash, int64(len(body)))

	require.Error(t, err)
	assert.Zero(t, hits.Load(), "the private source must never be contacted")
}

// The server health probe takes its URL from the same events.
func TestFederationHealthCheck_RefusesPrivateServer(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
	}))
	t.Cleanup(srv.Close)
	s := &federationService{log: zap.NewNop()}

	healthy, _ := s.CheckServerHealth(context.Background(), srv.URL)

	assert.False(t, healthy)
	assert.Zero(t, hits.Load(), "the private server must never be contacted")
}
