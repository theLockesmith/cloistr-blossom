package gin

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/nbd-wtf/go-nostr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"git.aegis-hq.xyz/coldforge/cloistr-blossom/src/core"
)

// A blossom auth token names the blob it authorizes in its x tag. Batch
// delete and batch upload ignored x, so a delete token signed for one blob
// (clients send these to every server that holds a copy) could be replayed
// to delete all of that key's blobs in one call.

const (
	xHashA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	xHashB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

// signedToken returns an Authorization header value for verb with one x tag
// per hash.
func signedToken(t *testing.T, verb string, hashes ...string) string {
	t.Helper()
	sk := nostr.GeneratePrivateKey()
	pk, _ := nostr.GetPublicKey(sk)
	ev := &nostr.Event{
		PubKey:    pk,
		CreatedAt: nostr.Now(),
		Kind:      24242,
		Tags: nostr.Tags{
			{"t", verb},
			{"expiration", strconv.FormatInt(time.Now().Add(time.Hour).Unix(), 10)},
		},
	}
	for _, h := range hashes {
		ev.Tags = append(ev.Tags, nostr.Tag{"x", h})
	}
	require.NoError(t, ev.Sign(sk))
	enc, err := encodeAuthEvent(ev)
	require.NoError(t, err)
	return "Nostr " + enc
}

// copyPK hands the verified key to the batch handlers under the key they read.
func copyPK(c *gin.Context) { c.Set(handlerPubkeyKey, c.GetString("pk")) }

type deleteRecordingBatch struct {
	core.BatchService
	deleted [][]string
	upload  *core.BatchUploadRequest
}

func (b *deleteRecordingBatch) Delete(_ context.Context, _ string, req *core.BatchDeleteRequest) (*core.BatchDeleteResponse, error) {
	b.deleted = append(b.deleted, req.Hashes)
	return &core.BatchDeleteResponse{TotalRequested: len(req.Hashes)}, nil
}

func (b *deleteRecordingBatch) Upload(_ context.Context, req *core.BatchUploadRequest) (*core.BatchUploadResponse, error) {
	b.upload = req
	return &core.BatchUploadResponse{TotalFiles: len(req.Files)}, nil
}

func TestBatchDelete_TokenMustNameEveryHash(t *testing.T) {
	gin.SetMode(gin.TestMode)
	log := zap.NewNop()

	cases := []struct {
		name    string
		tokenX  []string
		status  int
		deletes bool
	}{
		{"token for one blob cannot delete two", []string{xHashA}, http.StatusForbidden, false},
		{"token naming both deletes both", []string{xHashA, xHashB}, http.StatusOK, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			batch := &deleteRecordingBatch{}
			r := gin.New()
			r.DELETE("/batch", nostrAuthMiddleware("delete", log), copyPK, NewBatchController(batch, nil, nil).BatchDelete)

			body, _ := json.Marshal(core.BatchDeleteRequest{Hashes: []string{xHashA, xHashB}})
			req := httptest.NewRequest(http.MethodDelete, "/batch", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", signedToken(t, "delete", tc.tokenX...))
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			assert.Equal(t, tc.status, w.Code, "body %s", w.Body)
			if tc.deletes {
				assert.Equal(t, [][]string{{xHashA, xHashB}}, batch.deleted)
			} else {
				assert.Empty(t, batch.deleted, "nothing may be deleted when the token does not cover every hash")
			}
		})
	}
}

func TestBatchUpload_PassesTokenHashesToService(t *testing.T) {
	gin.SetMode(gin.TestMode)
	batch := &deleteRecordingBatch{}
	r := gin.New()
	r.POST("/batch/upload", nostrAuthMiddleware("upload", zap.NewNop()), copyPK, NewBatchController(batch, nil, nil).BatchUpload)

	body, ct := batchUploadBody(t)
	req := httptest.NewRequest(http.MethodPost, "/batch/upload", body)
	req.Header.Set("Content-Type", ct)
	req.Header.Set("Authorization", signedToken(t, "upload", xHashA, xHashB))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code, "body %s", w.Body)
	require.NotNil(t, batch.upload)
	assert.ElementsMatch(t, []string{xHashA, xHashB}, batch.upload.AllowedHashes)
}
