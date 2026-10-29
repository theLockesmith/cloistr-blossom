package gin

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"path"
	"strconv"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"git.aegis-hq.xyz/coldforge/cloistr-blossom/db"
	"git.aegis-hq.xyz/coldforge/cloistr-blossom/internal/storage"
	"git.aegis-hq.xyz/coldforge/cloistr-blossom/src/core"
	"git.aegis-hq.xyz/coldforge/cloistr-blossom/src/service"
)

// Production runs two replicas and does not pin a client to one. Each
// replica kept tus uploads in its own temp dir, so an upload created on one
// was 404 on the other: PATCH, HEAD and DELETE failed whenever they landed
// on the replica that had not created it.

type tusShared struct {
	store core.TusUploadStore
	blobs core.BlobStorage
}

// newTusShared is the state both replicas share: one database, one blob store.
func newTusShared(t *testing.T) tusShared {
	t.Helper()
	log := zap.NewNop()
	database, err := db.NewDBWithConfig(
		db.DBConfig{Driver: "sqlite", DSN: t.TempDir() + "/tus.sqlite3"},
		"../../db/migrations",
	)
	require.NoError(t, err)
	t.Cleanup(func() { database.Close() })
	queries := db.New(database)
	objects, err := storage.NewLocalStorage(t.TempDir())
	require.NoError(t, err)
	blobs, err := service.NewBlobService(database, queries, objects, nil, "http://test.invalid", log)
	require.NoError(t, err)
	return tusShared{store: service.NewTusStore(database, queries, objects, log), blobs: blobs}
}

// newTusTestStore is a store for tests that run a single replica.
func newTusTestStore(t *testing.T) core.TusUploadStore {
	t.Helper()
	return newTusShared(t).store
}

// seedTusUpload stores an upload holding data, as if its chunks had arrived.
func seedTusUpload(t *testing.T, store core.TusUploadStore, id string, data []byte) {
	t.Helper()
	ctx := context.Background()
	require.NoError(t, store.Create(ctx, &core.TusUpload{ID: id, Size: int64(len(data))}))
	_, err := store.AppendChunk(ctx, id, 0, data)
	require.NoError(t, err)
}

// countingTusStore counts the uploads created through it.
type countingTusStore struct {
	core.TusUploadStore
	creates int
}

func (s *countingTusStore) Create(ctx context.Context, u *core.TusUpload) error {
	s.creates++
	return s.TusUploadStore.Create(ctx, u)
}

// replica is one server instance: its own handler and its own local disk.
func (s tusShared) replica(t *testing.T) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	h, err := NewTusHandler(s.blobs, nil, nil, nil, nil, TusConfig{
		Store:      s.store,
		CDNBaseURL: "http://test.invalid",
	}, zap.NewNop())
	require.NoError(t, err)
	r := gin.New()
	auth := func(c *gin.Context) { c.Set(ctxPubkey, c.GetHeader("X-Test-Caller")) }
	RegisterTusRoutes(r, h, auth, zap.NewNop())
	return r
}

func tusCreateOn(t *testing.T, r *gin.Engine, length int) string {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/files", nil)
	req.Header.Set("X-Test-Caller", tusOwnerPK)
	req.Header.Set("Upload-Length", strconv.Itoa(length))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusCreated, w.Code, "create, body %s", w.Body)
	return path.Base(w.Header().Get("Location"))
}

func tusPatchOn(r *gin.Engine, id string, offset int, data []byte) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPatch, "/files/"+id, bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/offset+octet-stream")
	req.Header.Set("Upload-Offset", strconv.Itoa(offset))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func tusHeadOn(r *gin.Engine, id string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodHead, "/files/"+id, nil))
	return w
}

func TestTusReplicas_UploadContinuesOnTheOtherReplica(t *testing.T) {
	shared := newTusShared(t)
	a, b := shared.replica(t), shared.replica(t)
	data := []byte("hello, world")

	id := tusCreateOn(t, a, len(data))

	w := tusHeadOn(b, id)
	require.Equal(t, http.StatusOK, w.Code, "HEAD on B for an upload created on A")
	assert.Equal(t, "0", w.Header().Get("Upload-Offset"))

	w = tusPatchOn(b, id, 0, data[:5])
	require.Equal(t, http.StatusNoContent, w.Code, "PATCH on B, body %s", w.Body)
	assert.Equal(t, "5", w.Header().Get("Upload-Offset"))

	w = tusHeadOn(a, id)
	require.Equal(t, http.StatusOK, w.Code, "HEAD on A after a chunk on B")
	assert.Equal(t, "5", w.Header().Get("Upload-Offset"), "A sees B's chunk")

	w = tusPatchOn(a, id, 5, data[5:])
	require.Equal(t, http.StatusNoContent, w.Code, "final PATCH on A, body %s", w.Body)
	assert.Equal(t, strconv.Itoa(len(data)), w.Header().Get("Upload-Offset"))

	blob, err := shared.blobs.GetFromHash(context.Background(), sha256Hex(data))
	require.NoError(t, err, "the finished upload is stored")
	assert.Equal(t, int64(len(data)), blob.Size)
	has, err := shared.blobs.HasReference(context.Background(), tusOwnerPK, sha256Hex(data))
	require.NoError(t, err)
	assert.True(t, has, "owned by the key that created the upload")
}

func TestTusReplicas_DeleteOnTheOtherReplica(t *testing.T) {
	shared := newTusShared(t)
	a, b := shared.replica(t), shared.replica(t)

	id := tusCreateOn(t, a, 10)
	require.Equal(t, http.StatusNoContent, tusPatchOn(a, id, 0, []byte("12345")).Code)

	other := httptest.NewRequest(http.MethodDelete, "/files/"+id, nil)
	other.Header.Set("X-Test-Caller", tusOtherPK)
	w := httptest.NewRecorder()
	b.ServeHTTP(w, other)
	assert.Equal(t, http.StatusForbidden, w.Code, "another key's DELETE on B")

	owner := httptest.NewRequest(http.MethodDelete, "/files/"+id, nil)
	owner.Header.Set("X-Test-Caller", tusOwnerPK)
	w = httptest.NewRecorder()
	b.ServeHTTP(w, owner)
	require.Equal(t, http.StatusNoContent, w.Code, "owner's DELETE on B, body %s", w.Body)

	assert.Equal(t, http.StatusNotFound, tusHeadOn(a, id).Code, "gone on A too")
	_, err := shared.store.Get(context.Background(), id)
	assert.ErrorIs(t, err, core.ErrTusUploadNotFound)
}
