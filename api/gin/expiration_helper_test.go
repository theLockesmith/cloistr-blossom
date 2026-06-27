package gin

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func newTestContextWithHeader(header, value string) (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPut, "/upload", nil)
	if header != "" {
		c.Request.Header.Set(header, value)
	}
	return c, w
}

func TestParseUploadExpiration(t *testing.T) {
	future := time.Now().Add(time.Hour).Unix()
	past := time.Now().Add(-time.Hour).Unix()

	tests := []struct {
		name          string
		headerValue   string
		setHeader     bool
		wantOK        bool
		wantHas       bool
		wantStatus    int // expected response status when wantOK is false
		wantExpiresTS int64
	}{
		{name: "no header is a no-op", setHeader: false, wantOK: true, wantHas: false},
		{name: "empty header is a no-op", setHeader: true, headerValue: "", wantOK: true, wantHas: false},
		{
			name: "valid future timestamp", setHeader: true,
			headerValue: strconv.FormatInt(future, 10),
			wantOK:      true, wantHas: true, wantExpiresTS: future,
		},
		{
			name: "non-numeric is rejected", setHeader: true, headerValue: "not-a-number",
			wantOK: false, wantStatus: http.StatusBadRequest,
		},
		{
			name: "zero is rejected", setHeader: true, headerValue: "0",
			wantOK: false, wantStatus: http.StatusBadRequest,
		},
		{
			name: "past timestamp is rejected", setHeader: true, headerValue: strconv.FormatInt(past, 10),
			wantOK: false, wantStatus: http.StatusBadRequest,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var c *gin.Context
			var w *httptest.ResponseRecorder
			if tc.setHeader {
				c, w = newTestContextWithHeader(expirationHeader, tc.headerValue)
			} else {
				c, w = newTestContextWithHeader("", "")
			}

			expiresAt, has, ok := parseUploadExpiration(c)

			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if has != tc.wantHas {
				t.Fatalf("hasExpiration = %v, want %v", has, tc.wantHas)
			}
			if !tc.wantOK {
				if w.Code != tc.wantStatus {
					t.Fatalf("status = %d, want %d", w.Code, tc.wantStatus)
				}
				return
			}
			if tc.wantHas && expiresAt.Unix() != tc.wantExpiresTS {
				t.Fatalf("expiresAt = %d, want %d", expiresAt.Unix(), tc.wantExpiresTS)
			}
		})
	}
}
