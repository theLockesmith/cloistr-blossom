package gin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"git.aegis-hq.xyz/coldforge/cloistr-blossom/src/core"
)

type setQuotaMock struct {
	core.QuotaService
	err error
}

func (m setQuotaMock) SetQuota(context.Context, string, int64) error { return m.err }

type setQuotaServices struct {
	core.Services
	quota core.QuotaService
}

func (s setQuotaServices) Quota() core.QuotaService { return s.quota }

func runSetQuota(svc core.Services) *httptest.ResponseRecorder {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPut, "/admin/api/users/abc/quota", strings.NewReader(`{"quota_bytes":1073741824}`))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Params = gin.Params{{Key: "pubkey", Value: "abc"}}
	setUserQuota(svc)(c)
	return w
}

func TestSetUserQuotaHandler(t *testing.T) {
	t.Run("platform mode refuses and names cloistr-me", func(t *testing.T) {
		w := runSetQuota(setQuotaServices{quota: setQuotaMock{err: core.ErrQuotaManagedCentrally}})
		if w.Code != http.StatusNotImplemented {
			t.Fatalf("status = %d, want 501; body %s", w.Code, w.Body)
		}
		if !strings.Contains(w.Body.String(), "cloistr-me") || !strings.Contains(w.Body.String(), "POST /admin/v1/quotas") {
			t.Fatalf("body does not point at cloistr-me: %s", w.Body)
		}
	})

	t.Run("local mode still succeeds", func(t *testing.T) {
		w := runSetQuota(setQuotaServices{quota: setQuotaMock{}})
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200; body %s", w.Code, w.Body)
		}
	})
}
