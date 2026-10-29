package gin

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"git.aegis-hq.xyz/coldforge/cloistr-blossom/src/core"
)

// A finalize that answers 503 keeps the upload so the client can retry, but
// every byte is already stored, so the store's offset is at the end. A client
// retrying its last chunk at the old offset got 409 and could never finish.

// flakyBlocklist fails its failOn'th lookup and answers "not blocked"
// otherwise.
type flakyBlocklist struct {
	core.ModerationService
	calls  int
	failOn int
}

func (m *flakyBlocklist) IsBlocked(context.Context, string) (bool, error) {
	m.calls++
	if m.calls == m.failOn {
		return false, errors.New("blocklist unavailable")
	}
	return false, nil
}

func (s tusShared) replicaWithBlocklist(t *testing.T, mod core.ModerationService) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	h, err := NewTusHandler(s.blobs, nil, mod, nil, nil, TusConfig{
		Store:      s.store,
		CDNBaseURL: "http://test.invalid",
	}, zap.NewNop())
	require.NoError(t, err)
	r := gin.New()
	auth := func(c *gin.Context) { c.Set(ctxPubkey, c.GetHeader("X-Test-Caller")) }
	RegisterTusRoutes(r, h, auth, zap.NewNop())
	return r
}

func TestTusFinalizeRetry_LastChunkRetryCompletes(t *testing.T) {
	shared := newTusShared(t)
	// Calls: create, first chunk, last chunk, then the finalize check fails.
	r := shared.replicaWithBlocklist(t, &flakyBlocklist{failOn: 4})
	data := []byte("hello, world")

	id := tusCreateOn(t, r, len(data))
	w := tusPatchOn(r, id, 0, data[:5])
	require.Equal(t, http.StatusNoContent, w.Code, "first chunk, body %s", w.Body)

	w = tusPatchOn(r, id, 5, data[5:])
	require.Equal(t, http.StatusServiceUnavailable, w.Code, "finalize fails once, body %s", w.Body)
	_, err := shared.blobs.GetFromHash(context.Background(), sha256Hex(data))
	require.Error(t, err, "nothing stored yet")

	w = tusPatchOn(r, id, 5, data[5:])
	require.Equal(t, http.StatusNoContent, w.Code, "retry of the last chunk, body %s", w.Body)
	assert.Equal(t, strconv.Itoa(len(data)), w.Header().Get("Upload-Offset"))

	blob, err := shared.blobs.GetFromHash(context.Background(), sha256Hex(data))
	require.NoError(t, err, "the retried upload is stored")
	assert.Equal(t, int64(len(data)), blob.Size)
	has, err := shared.blobs.HasReference(context.Background(), tusOwnerPK, sha256Hex(data))
	require.NoError(t, err)
	assert.True(t, has, "owned by the key that created the upload")
}

func TestTusFinalizeRetry_EmptyPatchAtEndCompletes(t *testing.T) {
	shared := newTusShared(t)
	r := shared.replicaWithBlocklist(t, &flakyBlocklist{failOn: 3})
	data := []byte("hello, world")

	id := tusCreateOn(t, r, len(data))
	w := tusPatchOn(r, id, 0, data)
	require.Equal(t, http.StatusServiceUnavailable, w.Code, "finalize fails once, body %s", w.Body)

	// A client that asks HEAD first resumes at the end with no bytes.
	w = tusPatchOn(r, id, len(data), nil)
	require.Equal(t, http.StatusNoContent, w.Code, "empty PATCH at the end, body %s", w.Body)

	_, err := shared.blobs.GetFromHash(context.Background(), sha256Hex(data))
	require.NoError(t, err, "the retried upload is stored")
}

func TestTusFinalizeRetry_RefusesDifferentBytes(t *testing.T) {
	shared := newTusShared(t)
	r := shared.replicaWithBlocklist(t, &flakyBlocklist{failOn: 4})
	data := []byte("hello, world")

	id := tusCreateOn(t, r, len(data))
	require.Equal(t, http.StatusNoContent, tusPatchOn(r, id, 0, data[:5]).Code)
	require.Equal(t, http.StatusServiceUnavailable, tusPatchOn(r, id, 5, data[5:]).Code)

	other := []byte(", WORLD")
	w := tusPatchOn(r, id, 5, other)
	assert.Equal(t, http.StatusConflict, w.Code, "a retry must resend the same bytes, body %s", w.Body)

	w = tusPatchOn(r, id, 5, data[5:8])
	assert.Equal(t, http.StatusConflict, w.Code, "a retry must reach the end, body %s", w.Body)

	_, err := shared.blobs.GetFromHash(context.Background(), sha256Hex(data))
	require.Error(t, err, "nothing stored")
}

func TestTusFinalizeRetry_UnfinishedUploadStillRejectsOldOffset(t *testing.T) {
	shared := newTusShared(t)
	r := shared.replica(t)
	data := []byte("hello, world")

	id := tusCreateOn(t, r, len(data))
	require.Equal(t, http.StatusNoContent, tusPatchOn(r, id, 0, data[:5]).Code)

	w := tusPatchOn(r, id, 0, data[:5])
	assert.Equal(t, http.StatusConflict, w.Code, "an upload not yet complete keeps strict offsets")
}
