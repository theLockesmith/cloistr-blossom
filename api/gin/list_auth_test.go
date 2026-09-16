package gin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func setupListTestRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	logger, _ := zap.NewDevelopment()

	r := gin.New()
	r.GET("/list/:pubkey",
		nostrAuthMiddleware("list", logger),
		func(c *gin.Context) {
			pubkey := c.Param("pubkey")
			authenticatedPK, _ := c.Get("pk")
			if authenticatedPK != pubkey {
				c.AbortWithStatus(http.StatusForbidden)
				return
			}
			c.JSON(http.StatusOK, gin.H{"pubkey": pubkey})
		},
	)

	return r
}

func TestListAuth_OwnerCanListOwnBlobs(t *testing.T) {
	r := setupListTestRouter()

	ev := createValidAuthEvent("list", "", 1*time.Hour)
	authHeader, err := encodeAuthEvent(ev)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodGet, "/list/"+ev.PubKey, nil)
	req.Header.Set("Authorization", "Nostr "+authHeader)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]interface{}
	err = json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)
	assert.Equal(t, ev.PubKey, response["pubkey"])
}

func TestListAuth_CannotListOtherPubkey(t *testing.T) {
	r := setupListTestRouter()

	ev := createValidAuthEvent("list", "", 1*time.Hour)
	authHeader, err := encodeAuthEvent(ev)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodGet, "/list/someotherpubkey", nil)
	req.Header.Set("Authorization", "Nostr "+authHeader)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestListAuth_UnauthenticatedReturns401(t *testing.T) {
	r := setupListTestRouter()

	req := httptest.NewRequest(http.MethodGet, "/list/anypubkey", nil)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestListAuth_WrongVerbReturns401(t *testing.T) {
	r := setupListTestRouter()

	ev := createValidAuthEvent("upload", "", 1*time.Hour)
	authHeader, err := encodeAuthEvent(ev)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodGet, "/list/"+ev.PubKey, nil)
	req.Header.Set("Authorization", "Nostr "+authHeader)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestListAuth_ExpiredTokenReturns401(t *testing.T) {
	r := setupListTestRouter()

	ev := createValidAuthEvent("list", "", -1*time.Hour)
	authHeader, err := encodeAuthEvent(ev)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodGet, "/list/"+ev.PubKey, nil)
	req.Header.Set("Authorization", "Nostr "+authHeader)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestListAuth_FilterWithWrongPubkeyReturns403(t *testing.T) {
	r := setupListTestRouter()

	ev := createValidAuthEvent("list", "", 1*time.Hour)
	authHeader, err := encodeAuthEvent(ev)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodGet, "/list/someotherpubkey?type=image/", nil)
	req.Header.Set("Authorization", "Nostr "+authHeader)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
}
