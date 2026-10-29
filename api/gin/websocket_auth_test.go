package gin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gobwas/ws"
	"github.com/gobwas/ws/wsutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"git.aegis-hq.xyz/coldforge/cloistr-blossom/src/core"
	"git.aegis-hq.xyz/coldforge/cloistr-blossom/src/service"
)

// GET /ws used to take the key to subscribe to from ?pubkey= with no auth, so
// anyone could follow any key's notifications. These run through the real
// route registration and auth middleware over a real WebSocket handshake.

func startWSTestServer(t *testing.T) (*httptest.Server, core.NotificationService) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	log := zap.NewNop()
	notifications := service.NewNotificationService(log)

	r := gin.New()
	RegisterWebSocketRoutes(r, NewWebSocketHandler(notifications, log), nostrAuthMiddleware(wsAuthAction, log))
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return srv, notifications
}

// dialWS opens /ws with an optional auth event and query. It returns 101 and a
// reader on success, or the refused status code.
func dialWS(t *testing.T, srv *httptest.Server, authEvent string, query string) (status int, read func() (*core.Notification, error)) {
	t.Helper()
	header := http.Header{}
	if authEvent != "" {
		header.Set("Authorization", "Nostr "+authEvent)
	}
	url := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws" + query

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, _, err := ws.Dialer{Header: ws.HandshakeHeaderHTTP(header)}.Dial(ctx, url)
	if err != nil {
		var se ws.StatusError
		require.True(t, errors.As(err, &se), "dial failed without a status: %v", err)
		return int(se), nil
	}
	t.Cleanup(func() { conn.Close() })

	read = func() (*core.Notification, error) {
		for {
			_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
			msg, op, err := wsutil.ReadServerData(conn)
			if err != nil {
				return nil, err
			}
			if op != ws.OpText {
				continue
			}
			var n core.Notification
			if err := json.Unmarshal(msg, &n); err != nil {
				return nil, err
			}
			return &n, nil
		}
	}
	return http.StatusSwitchingProtocols, read
}

func TestWebSocketAuth_UnsignedIsRefused(t *testing.T) {
	srv, _ := startWSTestServer(t)

	status, _ := dialWS(t, srv, "", "?pubkey="+strings.Repeat("b", 64))
	assert.Equal(t, http.StatusUnauthorized, status)
}

func TestWebSocketAuth_TokenForAnotherVerbIsRefused(t *testing.T) {
	srv, _ := startWSTestServer(t)

	// An upload token must not open a subscription.
	ev := createValidAuthEvent("upload", strings.Repeat("0", 64), time.Hour)
	auth, err := encodeAuthEvent(ev)
	require.NoError(t, err)

	status, _ := dialWS(t, srv, auth, "")
	assert.Equal(t, http.StatusUnauthorized, status)
}

func TestWebSocketAuth_SignedAsAAskingForBIsRefused(t *testing.T) {
	srv, _ := startWSTestServer(t)

	ev := createValidAuthEvent(wsAuthAction, "", time.Hour)
	auth, err := encodeAuthEvent(ev)
	require.NoError(t, err)

	other := strings.Repeat("b", 64)
	require.NotEqual(t, ev.PubKey, other)

	status, _ := dialWS(t, srv, auth, "?pubkey="+other)
	assert.Equal(t, http.StatusForbidden, status)
}

func TestWebSocketAuth_SignedKeyGetsOnlyItsOwnNotifications(t *testing.T) {
	srv, notifications := startWSTestServer(t)

	for _, withQuery := range []bool{false, true} {
		// A fresh key per subtest: the previous subtest's connection may still
		// be unsubscribing, and would satisfy the wait below for a shared key.
		ev := createValidAuthEvent(wsAuthAction, "", time.Hour)
		auth, err := encodeAuthEvent(ev)
		require.NoError(t, err)
		query := ""
		if withQuery {
			query = "?pubkey=" + ev.PubKey
		}
		t.Run("query="+query, func(t *testing.T) {
			status, read := dialWS(t, srv, auth, query)
			require.Equal(t, http.StatusSwitchingProtocols, status)

			// Wait for the subscription to register before publishing.
			require.Eventually(t, func() bool {
				return notifications.GetClientCount(ev.PubKey) > 0
			}, 2*time.Second, 10*time.Millisecond)

			other := strings.Repeat("b", 64)
			notifications.Publish(context.Background(), core.NewErrorNotification(other, "for b", ""))
			notifications.Publish(context.Background(), core.NewErrorNotification(ev.PubKey, "for a", ""))

			n, err := read()
			require.NoError(t, err)
			assert.Equal(t, ev.PubKey, n.Pubkey)
			assert.Equal(t, "for a", n.Error)
		})
	}
}
