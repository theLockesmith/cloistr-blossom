package gin

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// A token's expiration tag had no ceiling, so a token signed to expire years
// out could be replayed until then, on every verb. The cap matches
// blossom-client-sdk's default (one hour, also what cloistr-pages signs) plus
// the clock-skew tolerance.
func TestNostrAuth_ExpirationCap(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cases := []struct {
		name     string
		lifetime time.Duration
		status   int
	}{
		{"five minutes (stash, email signer)", 5 * time.Minute, http.StatusOK},
		{"one hour (sdk default, pages)", time.Hour, http.StatusOK},
		{"beyond the cap", time.Hour + clockSkewTolerance + time.Minute, http.StatusUnauthorized},
		{"a year out", 365 * 24 * time.Hour, http.StatusUnauthorized},
	}
	for _, verb := range []string{"upload", "delete", wsAuthAction} {
		for _, tc := range cases {
			t.Run(verb+"/"+tc.name, func(t *testing.T) {
				r := gin.New()
				r.GET("/", nostrAuthMiddleware(verb, zap.NewNop()), func(c *gin.Context) { c.Status(http.StatusOK) })

				x := ""
				if verb != wsAuthAction {
					x = xHashA
				}
				auth, err := encodeAuthEvent(createValidAuthEvent(verb, x, tc.lifetime))
				require.NoError(t, err)
				req := httptest.NewRequest(http.MethodGet, "/", nil)
				req.Header.Set("Authorization", "Nostr "+auth)
				w := httptest.NewRecorder()
				r.ServeHTTP(w, req)
				assert.Equal(t, tc.status, w.Code)
			})
		}
	}
}
