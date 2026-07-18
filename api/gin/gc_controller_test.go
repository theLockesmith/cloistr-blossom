package gin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"git.aegis-hq.xyz/coldforge/cloistr-blossom/src/core"
)

// mockGCService implements core.GCService by embedding the interface (so only
// the methods a test exercises need bodies) and recording the arguments the
// handler passed through.
type mockGCService struct {
	core.GCService
	reportFn      func(ctx context.Context) (*core.GCReport, error)
	reconcileFn   func(ctx context.Context, dryRun bool, limit int) (*core.GCReconcileResult, error)
	gotDryRun     bool
	gotLimit      int
	reconcileSeen bool
}

func (m *mockGCService) Report(ctx context.Context) (*core.GCReport, error) {
	return m.reportFn(ctx)
}

func (m *mockGCService) Reconcile(ctx context.Context, dryRun bool, limit int) (*core.GCReconcileResult, error) {
	m.reconcileSeen = true
	m.gotDryRun = dryRun
	m.gotLimit = limit
	return m.reconcileFn(ctx, dryRun, limit)
}

// mockServices implements core.Services by embedding the interface and
// overriding only GC(), the sole method these handlers call.
type mockServices struct {
	core.Services
	gc core.GCService
}

func (m mockServices) GC() core.GCService { return m.gc }

// newTestRequestContext builds a gin context whose recorder captures the
// handler's response, for a request to path with the given raw query.
func newTestRequestContext(method, rawQuery string) (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(method, "/admin/api/gc/x?"+rawQuery, nil)
	return c, w
}

func newTestPostContext(rawQuery string) *gin.Context {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/admin/api/gc/reconcile?"+rawQuery, nil)
	return c
}

func TestParseReconcileParams(t *testing.T) {
	t.Run("defaults to dry run with default limit", func(t *testing.T) {
		dryRun, limit := parseReconcileParams(newTestPostContext(""))
		if !dryRun {
			t.Fatal("expected dry run by default")
		}
		if limit != gcReconcileDefaultLimit {
			t.Fatalf("limit = %d, want %d", limit, gcReconcileDefaultLimit)
		}
	})

	t.Run("confirm=true enables deletion", func(t *testing.T) {
		dryRun, _ := parseReconcileParams(newTestPostContext("confirm=true"))
		if dryRun {
			t.Fatal("confirm=true should disable dry run")
		}
	})

	t.Run("any non-true confirm value stays a dry run", func(t *testing.T) {
		for _, q := range []string{"confirm=1", "confirm=yes", "confirm=TRUE", "confirm="} {
			if dryRun, _ := parseReconcileParams(newTestPostContext(q)); !dryRun {
				t.Fatalf("%q should remain a dry run", q)
			}
		}
	})

	t.Run("explicit limit parses", func(t *testing.T) {
		_, limit := parseReconcileParams(newTestPostContext("limit=42"))
		if limit != 42 {
			t.Fatalf("limit = %d, want 42", limit)
		}
	})

	t.Run("garbage or non-positive limit falls back to default", func(t *testing.T) {
		for _, q := range []string{"limit=xyz", "limit=-5", "limit=0"} {
			if _, limit := parseReconcileParams(newTestPostContext(q)); limit != gcReconcileDefaultLimit {
				t.Fatalf("%q: limit = %d, want default %d", q, limit, gcReconcileDefaultLimit)
			}
		}
	})
}

func TestGCReconcileHandler(t *testing.T) {
	t.Run("no confirm passes dryRun=true to the service", func(t *testing.T) {
		mock := &mockGCService{
			reconcileFn: func(_ context.Context, dryRun bool, _ int) (*core.GCReconcileResult, error) {
				return &core.GCReconcileResult{DryRun: dryRun, OwnerlessFound: 3}, nil
			},
		}
		c, w := newTestRequestContext(http.MethodPost, "")
		gcReconcile(mockServices{gc: mock})(c)

		if !mock.reconcileSeen {
			t.Fatal("Reconcile was not called")
		}
		if !mock.gotDryRun {
			t.Fatal("expected dryRun=true when confirm is absent")
		}
		if mock.gotLimit != gcReconcileDefaultLimit {
			t.Fatalf("limit = %d, want default %d", mock.gotLimit, gcReconcileDefaultLimit)
		}
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", w.Code)
		}
	})

	t.Run("confirm=true passes dryRun=false and honors limit", func(t *testing.T) {
		mock := &mockGCService{
			reconcileFn: func(_ context.Context, dryRun bool, _ int) (*core.GCReconcileResult, error) {
				return &core.GCReconcileResult{DryRun: dryRun, Deleted: 2}, nil
			},
		}
		c, w := newTestRequestContext(http.MethodPost, "confirm=true&limit=42")
		gcReconcile(mockServices{gc: mock})(c)

		if mock.gotDryRun {
			t.Fatal("expected dryRun=false when confirm=true")
		}
		if mock.gotLimit != 42 {
			t.Fatalf("limit = %d, want 42", mock.gotLimit)
		}
		var body core.GCReconcileResult
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatalf("unmarshal response: %v", err)
		}
		if body.Deleted != 2 {
			t.Fatalf("deleted = %d, want 2", body.Deleted)
		}
	})

	t.Run("service error maps to 500", func(t *testing.T) {
		mock := &mockGCService{
			reconcileFn: func(_ context.Context, _ bool, _ int) (*core.GCReconcileResult, error) {
				return nil, errors.New("boom")
			},
		}
		c, w := newTestRequestContext(http.MethodPost, "confirm=true")
		gcReconcile(mockServices{gc: mock})(c)

		if w.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d, want 500", w.Code)
		}
	})
}

func TestGCReportHandler(t *testing.T) {
	t.Run("returns the service report as JSON", func(t *testing.T) {
		mock := &mockGCService{
			reportFn: func(_ context.Context) (*core.GCReport, error) {
				return &core.GCReport{ZeroRefBlobs: 5, OwnerlessBlobs: 7}, nil
			},
		}
		c, w := newTestRequestContext(http.MethodGet, "")
		gcReport(mockServices{gc: mock})(c)

		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", w.Code)
		}
		var body core.GCReport
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatalf("unmarshal response: %v", err)
		}
		if body.ZeroRefBlobs != 5 || body.OwnerlessBlobs != 7 {
			t.Fatalf("report = %+v, want {5 7}", body)
		}
	})

	t.Run("service error maps to 500", func(t *testing.T) {
		mock := &mockGCService{
			reportFn: func(_ context.Context) (*core.GCReport, error) {
				return nil, errors.New("db down")
			},
		}
		c, w := newTestRequestContext(http.MethodGet, "")
		gcReport(mockServices{gc: mock})(c)

		if w.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d, want 500", w.Code)
		}
	})
}
