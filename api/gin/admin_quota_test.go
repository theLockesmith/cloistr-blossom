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

// quotaAdminMock fails every admin write with err (nil means success).
type quotaAdminMock struct {
	core.QuotaService
	err error
}

func (m quotaAdminMock) SetQuota(context.Context, string, int64) error  { return m.err }
func (m quotaAdminMock) BanUser(context.Context, string) error          { return m.err }
func (m quotaAdminMock) UnbanUser(context.Context, string) error        { return m.err }
func (m quotaAdminMock) RecalculateUsage(context.Context, string) error { return m.err }

type quotaAdminServices struct {
	core.Services
	quota core.QuotaService
}

func (s quotaAdminServices) Quota() core.QuotaService { return s.quota }

func runQuotaAdmin(h func(core.Services) gin.HandlerFunc, svc core.Services, method, body string) *httptest.ResponseRecorder {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(method, "/admin/api/users/abc", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Params = gin.Params{{Key: "pubkey", Value: "abc"}}
	h(svc)(c)
	return w
}

func TestQuotaAdminHandlersInPlatformMode(t *testing.T) {
	cases := []struct {
		name    string
		handler func(core.Services) gin.HandlerFunc
		method  string
		body    string
		err     error
		mention []string // the body must point at where this is really done
	}{
		{"set quota", setUserQuota, http.MethodPut, `{"quota_bytes":1073741824}`, core.ErrQuotaManagedCentrally, []string{"cloistr-me", "POST /admin/v1/quotas"}},
		{"ban", banUser, http.MethodPost, "", core.ErrBanNotInPlatformMode, []string{"POST /admin/api/blocklist"}},
		{"unban", unbanUser, http.MethodPost, "", core.ErrBanNotInPlatformMode, []string{"DELETE /admin/api/blocklist/:pubkey"}},
		{"recalculate", recalculateUserUsage, http.MethodPost, "", core.ErrRecalculateNotInPlatformMode, []string{"cannot be recalculated"}},
	}
	for _, tc := range cases {
		t.Run(tc.name+" refuses with 501", func(t *testing.T) {
			w := runQuotaAdmin(tc.handler, quotaAdminServices{quota: quotaAdminMock{err: tc.err}}, tc.method, tc.body)
			if w.Code != http.StatusNotImplemented {
				t.Fatalf("status = %d, want 501; body %s", w.Code, w.Body)
			}
			for _, m := range tc.mention {
				if !strings.Contains(w.Body.String(), m) {
					t.Fatalf("body does not mention %q: %s", m, w.Body)
				}
			}
		})
		t.Run(tc.name+" still succeeds in local mode", func(t *testing.T) {
			w := runQuotaAdmin(tc.handler, quotaAdminServices{quota: quotaAdminMock{}}, tc.method, tc.body)
			if w.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200; body %s", w.Code, w.Body)
			}
		})
	}
}
