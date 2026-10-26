package gin

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"git.aegis-hq.xyz/coldforge/cloistr-blossom/src/core"
)

type adminDeleteBlobMock struct {
	core.BlobStorage
	blob          *core.Blob
	owners        []string
	deletedRefs   []string
	deletedBlob   bool
	descriptorErr error
	gone          map[string]bool // owners whose reference is already gone
}

func (m *adminDeleteBlobMock) GetDescriptor(_ context.Context, _ string) (*core.Blob, error) {
	if m.descriptorErr != nil {
		return nil, m.descriptorErr
	}
	if m.blob == nil {
		return nil, sql.ErrNoRows
	}
	return m.blob, nil
}

func (m *adminDeleteBlobMock) HasReference(_ context.Context, pk, _ string) (bool, error) {
	return !m.gone[pk], nil
}

func (m *adminDeleteBlobMock) ListReferenceOwners(_ context.Context, _ string) ([]string, error) {
	return m.owners, nil
}

func (m *adminDeleteBlobMock) DeleteReference(_ context.Context, pk, _ string) (bool, error) {
	m.deletedRefs = append(m.deletedRefs, pk)
	if len(m.deletedRefs) == len(m.owners)-len(m.gone) {
		m.owners, m.blob = nil, nil // last reference: the blob goes too
	}
	return true, nil
}

func (m *adminDeleteBlobMock) DeleteFromHash(_ context.Context, _ string) error {
	m.deletedBlob = true
	return nil
}

type adminDeleteQuotaMock struct {
	core.QuotaService
	decremented map[string]int64
}

func (m *adminDeleteQuotaMock) DecrementUsage(_ context.Context, pk string, n int64) error {
	m.decremented[pk] += n
	return nil
}

type adminDeleteServices struct {
	core.Services
	blob  core.BlobStorage
	quota core.QuotaService
}

func (s adminDeleteServices) Blob() core.BlobStorage   { return s.blob }
func (s adminDeleteServices) Quota() core.QuotaService { return s.quota }

const adminDeleteTestHash = "4c2e48bb020b0e6359e25788892f2f09e0c2f06651478c6e3f808f8b79860f49"

func runAdminDelete(svc core.Services, hash, rawQuery string) *httptest.ResponseRecorder {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodDelete, "/admin/api/blobs/"+hash+"?"+rawQuery, nil)
	c.Params = gin.Params{{Key: "hash", Value: hash}}
	adminDeleteBlob(svc)(c)
	return w
}

func TestAdminDeleteBlobHandler(t *testing.T) {
	t.Run("malformed hash is 400 and touches nothing", func(t *testing.T) {
		w := runAdminDelete(adminDeleteServices{}, "not-a-hash", "confirm=true")
		if w.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", w.Code)
		}
	})

	t.Run("unknown hash is 404", func(t *testing.T) {
		blob := &adminDeleteBlobMock{descriptorErr: sql.ErrNoRows}
		w := runAdminDelete(adminDeleteServices{blob: blob}, adminDeleteTestHash, "confirm=true")
		if w.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", w.Code)
		}
	})

	t.Run("a lookup failure is 500, not 404", func(t *testing.T) {
		blob := &adminDeleteBlobMock{descriptorErr: errors.New("connection reset")}
		w := runAdminDelete(adminDeleteServices{blob: blob}, adminDeleteTestHash, "confirm=true")
		if w.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d, want 500", w.Code)
		}
	})

	t.Run("an owner who already deleted their copy is skipped", func(t *testing.T) {
		blob := &adminDeleteBlobMock{
			blob:   &core.Blob{Sha256: adminDeleteTestHash, Size: 300},
			owners: []string{"alice", "carol"},
			gone:   map[string]bool{"carol": true},
		}
		quota := &adminDeleteQuotaMock{decremented: map[string]int64{}}
		w := runAdminDelete(adminDeleteServices{blob: blob, quota: quota}, adminDeleteTestHash, "confirm=true")
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
		}
		if _, refunded := quota.decremented["carol"]; refunded || quota.decremented["alice"] != 300 {
			t.Fatalf("usage = %v, want only alice refunded", quota.decremented)
		}
		var body struct {
			Removed []string `json:"removed"`
			Deleted bool     `json:"deleted"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if len(body.Removed) != 1 || body.Removed[0] != "alice" || !body.Deleted {
			t.Fatalf("body = %+v, want removed [alice] and deleted", body)
		}
	})

	t.Run("without confirm=true it is a dry run", func(t *testing.T) {
		for _, q := range []string{"", "confirm=1", "confirm=TRUE"} {
			blob := &adminDeleteBlobMock{blob: &core.Blob{Sha256: adminDeleteTestHash, Size: 300}, owners: []string{"alice"}}
			quota := &adminDeleteQuotaMock{decremented: map[string]int64{}}
			w := runAdminDelete(adminDeleteServices{blob: blob, quota: quota}, adminDeleteTestHash, q)
			if w.Code != http.StatusOK {
				t.Fatalf("%q: status = %d, want 200", q, w.Code)
			}
			if len(blob.deletedRefs) != 0 || blob.deletedBlob || len(quota.decremented) != 0 {
				t.Fatalf("%q: dry run changed state: refs=%v blob=%v usage=%v", q, blob.deletedRefs, blob.deletedBlob, quota.decremented)
			}
			if !strings.Contains(w.Body.String(), `"dry_run":true`) {
				t.Fatalf("%q: body %s does not say dry_run", q, w.Body.String())
			}
		}
	})

	t.Run("confirm=true deletes every reference and refunds usage", func(t *testing.T) {
		blob := &adminDeleteBlobMock{blob: &core.Blob{Sha256: adminDeleteTestHash, Size: 300}, owners: []string{"alice", "bob"}}
		quota := &adminDeleteQuotaMock{decremented: map[string]int64{}}
		w := runAdminDelete(adminDeleteServices{blob: blob, quota: quota}, strings.ToUpper(adminDeleteTestHash), "confirm=true")
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
		}
		if len(blob.deletedRefs) != 2 || quota.decremented["alice"] != 300 || quota.decremented["bob"] != 300 {
			t.Fatalf("refs=%v usage=%v", blob.deletedRefs, quota.decremented)
		}
		var body struct {
			Hash    string `json:"hash"`
			Deleted bool   `json:"deleted"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if !body.Deleted || body.Hash != adminDeleteTestHash {
			t.Fatalf("body = %+v, want deleted with lowercased hash", body)
		}
	})
}
