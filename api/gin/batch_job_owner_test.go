package gin

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/nbd-wtf/go-nostr"
	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"

	"git.aegis-hq.xyz/coldforge/cloistr-blossom/src/core"
)

// GET /batch/jobs/:id had no auth and DELETE /batch/jobs/:id checked no
// owner: anyone could read any job, and any signed key could cancel it.

type jobBatch struct {
	core.BatchService
	jobs      map[string]*core.BatchJob
	cancelled []string
}

func (b *jobBatch) GetJob(_ context.Context, id string) (*core.BatchJob, error) {
	if j, ok := b.jobs[id]; ok {
		return j, nil
	}
	return nil, fmt.Errorf("job not found: %s", id)
}

func (b *jobBatch) CancelJob(_ context.Context, id string) error {
	b.cancelled = append(b.cancelled, id)
	return nil
}

func TestBatchJobs_OnlyTheOwner(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ownerSK, otherSK := nostr.GeneratePrivateKey(), nostr.GeneratePrivateKey()
	owner, _ := nostr.GetPublicKey(ownerSK)

	setup := func() (*gin.Engine, *jobBatch) {
		b := &jobBatch{jobs: map[string]*core.BatchJob{"job-1": {ID: "job-1", Pubkey: owner, Status: core.BatchStatusProcessing}}}
		r := gin.New()
		RegisterBatchRoutes(r, NewBatchController(b, nil, nil), zap.NewNop())
		return r, b
	}
	do := func(r *gin.Engine, method, id, auth string) int {
		req := httptest.NewRequest(method, "/batch/jobs/"+id, nil)
		if auth != "" {
			req.Header.Set("Authorization", auth)
		}
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w.Code
	}
	get := func(sk string) string { return signedTokenAs(t, sk, "get") }
	del := func(sk string) string { return signedTokenAs(t, sk, "delete", xHashA) }

	t.Run("read", func(t *testing.T) {
		r, _ := setup()
		assert.Equal(t, http.StatusUnauthorized, do(r, http.MethodGet, "job-1", ""), "unsigned")
		assert.Equal(t, http.StatusForbidden, do(r, http.MethodGet, "job-1", get(otherSK)), "another key")
		assert.Equal(t, http.StatusNotFound, do(r, http.MethodGet, "nope", get(ownerSK)), "unknown job")
		assert.Equal(t, http.StatusOK, do(r, http.MethodGet, "job-1", get(ownerSK)), "owner")
	})

	t.Run("cancel", func(t *testing.T) {
		r, b := setup()
		assert.Equal(t, http.StatusUnauthorized, do(r, http.MethodDelete, "job-1", ""), "unsigned")
		assert.Equal(t, http.StatusForbidden, do(r, http.MethodDelete, "job-1", del(otherSK)), "another key")
		assert.Empty(t, b.cancelled, "another key must not cancel the job")
		assert.Equal(t, http.StatusNotFound, do(r, http.MethodDelete, "nope", del(ownerSK)), "unknown job")
		assert.Equal(t, http.StatusOK, do(r, http.MethodDelete, "job-1", del(ownerSK)), "owner")
		assert.Equal(t, []string{"job-1"}, b.cancelled)
	})
}
