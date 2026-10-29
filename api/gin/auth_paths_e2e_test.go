package gin

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"path"
	"testing"

	"github.com/gin-gonic/gin"
	_ "github.com/mattn/go-sqlite3"
	"github.com/nbd-wtf/go-nostr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"git.aegis-hq.xyz/coldforge/cloistr-blossom/db"
	"git.aegis-hq.xyz/coldforge/cloistr-blossom/internal/storage"
	"git.aegis-hq.xyz/coldforge/cloistr-blossom/src/core"
	"git.aegis-hq.xyz/coldforge/cloistr-blossom/src/service"
)

// The tus and batch handlers read the caller's key from a context key the
// auth middleware never set, so every request answered 401. These run the
// real route registration, the real auth middleware, and (for batch) the real
// batch and blob services on SQLite.

func sha256Hex(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

type e2eEnv struct {
	router *gin.Engine
	blobs  core.BlobStorage
}

func newE2EEnv(t *testing.T) e2eEnv {
	t.Helper()
	gin.SetMode(gin.TestMode)
	log := zap.NewNop()

	database, err := db.NewDBWithConfig(
		db.DBConfig{Driver: "sqlite", DSN: t.TempDir() + "/e2e.sqlite3"},
		"../../db/migrations",
	)
	require.NoError(t, err)
	t.Cleanup(func() { database.Close() })
	queries := db.New(database)
	store, err := storage.NewLocalStorage(t.TempDir())
	require.NoError(t, err)
	blobs, err := service.NewBlobService(database, queries, store, nil, "http://test.invalid", log)
	require.NoError(t, err)
	batch, err := service.NewBatchService(blobs, store, nil, nil, nil, core.DefaultBatchConfig(), "http://test.invalid", log)
	require.NoError(t, err)
	tus, err := NewTusHandler(blobs, nil, &fakeBlocklist{}, &fakeACR{}, nil, TusConfig{Store: service.NewTusStore(database, queries, store, log), CDNBaseURL: "http://test.invalid"}, log)
	require.NoError(t, err)

	r := gin.New()
	RegisterTusRoutes(r, tus, nostrAuthMiddleware("upload", log), log)
	RegisterBatchRoutes(r, NewBatchController(batch, &fakeBlocklist{}, &fakeACR{}), log)
	return e2eEnv{router: r, blobs: blobs}
}

func (e e2eEnv) do(req *http.Request) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	e.router.ServeHTTP(w, req)
	return w
}

func TestTusPaths_RealAuth(t *testing.T) {
	env := newE2EEnv(t)
	data := []byte("resumable bytes")

	create := func(auth string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/files", nil)
		req.Header.Set("Upload-Length", "15")
		if auth != "" {
			req.Header.Set("Authorization", auth)
		}
		return env.do(req)
	}

	assert.Equal(t, http.StatusUnauthorized, create("").Code, "unsigned create")

	sk := nostr.GeneratePrivateKey()
	pk, _ := nostr.GetPublicKey(sk)
	created := create(signedTokenAs(t, sk, "upload", sha256Hex(data)))
	require.Equal(t, http.StatusCreated, created.Code, "signed create, body %s", created.Body)
	id := path.Base(created.Header().Get("Location"))

	// PATCH and HEAD need the creator's signature, as DELETE does: holding
	// the upload id is not enough.
	patch := func(auth string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPatch, "/files/"+id, bytes.NewReader(data))
		req.Header.Set("Content-Type", "application/offset+octet-stream")
		req.Header.Set("Upload-Offset", "0")
		if auth != "" {
			req.Header.Set("Authorization", auth)
		}
		return env.do(req)
	}
	head := func(auth string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodHead, "/files/"+id, nil)
		if auth != "" {
			req.Header.Set("Authorization", auth)
		}
		return env.do(req)
	}
	other := signedTokenAs(t, nostr.GeneratePrivateKey(), "upload", sha256Hex(data))
	owner := signedTokenAs(t, sk, "upload", sha256Hex(data))

	assert.Equal(t, http.StatusUnauthorized, head("").Code, "unsigned HEAD")
	assert.Equal(t, http.StatusForbidden, head(other).Code, "HEAD by another key")
	assert.Equal(t, http.StatusOK, head(owner).Code, "HEAD by the creator")
	assert.Equal(t, http.StatusUnauthorized, patch("").Code, "unsigned PATCH")
	assert.Equal(t, http.StatusForbidden, patch(other).Code, "PATCH by another key")

	w := patch(owner)
	require.Equal(t, http.StatusNoContent, w.Code, "body %s", w.Body)

	has, err := env.blobs.HasReference(context.Background(), pk, sha256Hex(data))
	require.NoError(t, err)
	assert.True(t, has, "the finished upload is stored and owned by the signer")
}

func TestBatchUpload_RealAuth(t *testing.T) {
	env := newE2EEnv(t)
	data := []byte("batch bytes")

	upload := func(auth string) *httptest.ResponseRecorder {
		var body bytes.Buffer
		mw := multipart.NewWriter(&body)
		fw, _ := mw.CreateFormFile("files", "a.txt")
		_, _ = fw.Write(data)
		_ = mw.Close()
		req := httptest.NewRequest(http.MethodPost, "/batch/upload", &body)
		req.Header.Set("Content-Type", mw.FormDataContentType())
		if auth != "" {
			req.Header.Set("Authorization", auth)
		}
		return env.do(req)
	}

	assert.Equal(t, http.StatusUnauthorized, upload("").Code, "unsigned batch upload")

	sk := nostr.GeneratePrivateKey()
	pk, _ := nostr.GetPublicKey(sk)
	w := upload(signedTokenAs(t, sk, "upload", sha256Hex(data)))
	require.Equal(t, http.StatusOK, w.Code, "signed batch upload, body %s", w.Body)

	var resp core.BatchUploadResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, 1, resp.SuccessCount, "body %s", w.Body)
	has, err := env.blobs.HasReference(context.Background(), pk, sha256Hex(data))
	require.NoError(t, err)
	assert.True(t, has, "the file is stored and owned by the signer")
}

// Batch DELETE must remove only the caller's own references. Bob owns X,
// Alice owns Y, both own Z. Alice's batch names all three.
func TestBatchDelete_RealAuthOnlyRemovesCallersBlobs(t *testing.T) {
	env := newE2EEnv(t)
	ctx := context.Background()

	aliceSK, bobSK := nostr.GeneratePrivateKey(), nostr.GeneratePrivateKey()
	alice, _ := nostr.GetPublicKey(aliceSK)
	bob, _ := nostr.GetPublicKey(bobSK)

	x, y, z := []byte("bob's blob"), []byte("alice's blob"), []byte("shared blob")
	save := func(pk string, data []byte) {
		_, _, err := env.blobs.SaveWithDedup(ctx, pk, sha256Hex(data), "", int64(len(data)), "text/plain", data, 1, core.EncryptionModeNone)
		require.NoError(t, err)
	}
	save(bob, x)
	save(alice, y)
	save(alice, z)
	save(bob, z)

	hashes := []string{sha256Hex(x), sha256Hex(y), sha256Hex(z)}
	del := func(auth string) *httptest.ResponseRecorder {
		body, _ := json.Marshal(core.BatchDeleteRequest{Hashes: hashes})
		req := httptest.NewRequest(http.MethodDelete, "/batch", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if auth != "" {
			req.Header.Set("Authorization", auth)
		}
		return env.do(req)
	}

	assert.Equal(t, http.StatusUnauthorized, del("").Code, "unsigned batch delete")

	w := del(signedTokenAs(t, aliceSK, "delete", hashes...))
	require.Equal(t, http.StatusOK, w.Code, "body %s", w.Body)

	var resp core.BatchDeleteResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Len(t, resp.Results, 3)
	assert.False(t, resp.Results[0].Success, "Bob's blob is reported as not deleted")
	assert.NotEmpty(t, resp.Results[0].Error, "and says why")
	assert.True(t, resp.Results[1].Success, "Alice's own blob is deleted")
	assert.True(t, resp.Results[2].Success, "Alice's reference to the shared blob is deleted")

	ref := func(pk string, data []byte) bool {
		has, err := env.blobs.HasReference(ctx, pk, sha256Hex(data))
		require.NoError(t, err)
		return has
	}
	assert.True(t, ref(bob, x), "Bob still owns X")
	exists, err := env.blobs.Exists(ctx, sha256Hex(x))
	require.NoError(t, err)
	assert.True(t, exists, "X is still stored")
	assert.False(t, ref(alice, y), "Alice no longer owns Y")
	assert.False(t, ref(alice, z), "Alice no longer owns Z")
	assert.True(t, ref(bob, z), "Bob still owns Z")
	exists, err = env.blobs.Exists(ctx, sha256Hex(z))
	require.NoError(t, err)
	assert.True(t, exists, "Z is still stored for Bob")
}
