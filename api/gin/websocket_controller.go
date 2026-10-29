package gin

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gobwas/ws"
	"github.com/gobwas/ws/wsutil"
	"go.uber.org/zap"

	"git.aegis-hq.xyz/coldforge/cloistr-blossom/src/core"
	clerrors "git.aegis-hq.xyz/coldforge/cloistr-common/errors"
)

// WebSocketHandler handles WebSocket connections for real-time notifications.
type WebSocketHandler struct {
	notificationService core.NotificationService
	log                 *zap.Logger
}

// NewWebSocketHandler creates a new WebSocket handler.
func NewWebSocketHandler(notificationSvc core.NotificationService, log *zap.Logger) *WebSocketHandler {
	return &WebSocketHandler{
		notificationService: notificationSvc,
		log:                 log,
	}
}

// wsConnect handles GET /ws
// Upgrades HTTP connection to WebSocket for real-time notifications.
func (h *WebSocketHandler) wsConnect() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		// Subscribe only the key that signed the auth event. ?pubkey= used to
		// pick the key with no auth at all, so anyone could follow anyone.
		pubkey := ctx.GetString("pk")
		if pubkey == "" {
			clerrors.Unauthorized(clerrors.CodeAuthRequired, "authentication required").Abort(ctx)
			return
		}
		if q := ctx.Query("pubkey"); q != "" && q != pubkey {
			clerrors.Forbidden(clerrors.CodeAccessDenied, "can only subscribe to your own notifications").Abort(ctx)
			return
		}

		// Upgrade to WebSocket
		conn, _, _, err := ws.UpgradeHTTP(ctx.Request, ctx.Writer)
		if err != nil {
			h.log.Error("websocket upgrade failed", zap.Error(err))
			return
		}

		h.log.Info("websocket connection established",
			zap.String("pubkey", pubkey),
			zap.String("remote_addr", ctx.ClientIP()))

		// Subscribe to notifications
		notifyCh, unsubscribe := h.notificationService.Subscribe(ctx, pubkey)
		defer unsubscribe()

		// Handle connection in separate goroutines
		done := make(chan struct{})
		errCh := make(chan error, 2)

		// Writer goroutine - sends notifications to client
		go func() {
			defer close(done)
			for {
				select {
				case notification, ok := <-notifyCh:
					if !ok {
						return
					}

					// Marshal notification to JSON
					data, err := json.Marshal(notification)
					if err != nil {
						h.log.Error("marshal notification failed", zap.Error(err))
						continue
					}

					// Send to client
					if err := wsutil.WriteServerText(conn, data); err != nil {
						h.log.Debug("write to websocket failed", zap.Error(err))
						errCh <- err
						return
					}

				case <-ctx.Done():
					return
				}
			}
		}()

		// Reader goroutine - handles client messages (ping/pong, close)
		go func() {
			for {
				msg, op, err := wsutil.ReadClientData(conn)
				if err != nil {
					errCh <- err
					return
				}

				// Handle ping
				if op == ws.OpPing {
					if err := wsutil.WriteServerMessage(conn, ws.OpPong, msg); err != nil {
						errCh <- err
						return
					}
					continue
				}

				// Handle close
				if op == ws.OpClose {
					return
				}

				// Handle text messages (commands from client)
				if op == ws.OpText {
					h.handleClientMessage(ctx, pubkey, msg)
				}
			}
		}()

		// Ping ticker to keep connection alive
		pingTicker := time.NewTicker(30 * time.Second)
		defer pingTicker.Stop()

		// Wait for completion
		for {
			select {
			case <-done:
				conn.Close()
				h.log.Info("websocket connection closed by server",
					zap.String("pubkey", pubkey))
				return

			case err := <-errCh:
				conn.Close()
				h.log.Debug("websocket connection closed",
					zap.String("pubkey", pubkey),
					zap.Error(err))
				return

			case <-pingTicker.C:
				if err := wsutil.WriteServerMessage(conn, ws.OpPing, nil); err != nil {
					conn.Close()
					h.log.Debug("websocket ping failed",
						zap.String("pubkey", pubkey),
						zap.Error(err))
					return
				}

			case <-ctx.Done():
				conn.Close()
				h.log.Info("websocket connection closed by context",
					zap.String("pubkey", pubkey))
				return
			}
		}
	}
}

// handleClientMessage processes messages from the client.
func (h *WebSocketHandler) handleClientMessage(ctx *gin.Context, pubkey string, msg []byte) {
	var cmd struct {
		Action string `json:"action"`
		Data   string `json:"data,omitempty"`
	}

	if err := json.Unmarshal(msg, &cmd); err != nil {
		return
	}

	switch cmd.Action {
	case "ping":
		// Client ping - no action needed
	case "subscribe":
		// Additional subscription handling could go here
		h.log.Debug("client subscribe request",
			zap.String("pubkey", pubkey),
			zap.String("data", cmd.Data))
	default:
		h.log.Debug("unknown client command",
			zap.String("pubkey", pubkey),
			zap.String("action", cmd.Action))
	}
}

// wsStatus handles GET /ws/status
// Returns WebSocket connection statistics.
func (h *WebSocketHandler) wsStatus() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		ctx.JSON(http.StatusOK, gin.H{
			"connected_clients": h.notificationService.GetConnectedClients(),
		})
	}
}

// wsAuthAction is the t-tag verb a /ws auth event must carry. It is not one of
// the BUD verbs, so a token signed for another endpoint cannot open a feed.
const wsAuthAction = "subscribe"

// RegisterWebSocketRoutes registers WebSocket routes. authMiddleware must be
// nostrAuthMiddleware(wsAuthAction, ...). Browsers cannot set headers on a
// WebSocket handshake, so a browser client will need another way to present
// the auth event; nothing publishes notifications yet, so none exists.
func RegisterWebSocketRoutes(r *gin.Engine, handler *WebSocketHandler, authMiddleware gin.HandlerFunc) {
	r.GET("/ws", authMiddleware, handler.wsConnect())

	// Status endpoint
	r.GET("/ws/status", handler.wsStatus())
}
