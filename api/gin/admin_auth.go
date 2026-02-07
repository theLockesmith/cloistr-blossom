package gin

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	goNostr "github.com/nbd-wtf/go-nostr"
	"go.uber.org/zap"
)

// AdminSession represents an authenticated admin session.
type AdminSession struct {
	Pubkey    string    `json:"pubkey"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

// AdminAuthManager handles admin authentication and session management.
type AdminAuthManager struct {
	adminPubkey   string
	secretKey     []byte
	sessions      map[string]*AdminSession
	sessionsMutex sync.RWMutex
	log           *zap.Logger
}

// NewAdminAuthManager creates a new admin auth manager.
func NewAdminAuthManager(adminPubkey string, log *zap.Logger) *AdminAuthManager {
	// Generate a random secret key for signing tokens
	secretKey := make([]byte, 32)
	if _, err := rand.Read(secretKey); err != nil {
		// Fallback to a derived key if random fails
		h := sha256.Sum256([]byte(adminPubkey + time.Now().String()))
		secretKey = h[:]
	}

	return &AdminAuthManager{
		adminPubkey: adminPubkey,
		secretKey:   secretKey,
		sessions:    make(map[string]*AdminSession),
		log:         log,
	}
}

// CreateSession creates a new admin session and returns a token.
func (m *AdminAuthManager) CreateSession(pubkey string) (string, error) {
	session := &AdminSession{
		Pubkey:    pubkey,
		CreatedAt: time.Now(),
		ExpiresAt: time.Now().Add(24 * time.Hour), // 24 hour sessions
	}

	// Create token: base64(pubkey:expiry:signature)
	data := fmt.Sprintf("%s:%d", pubkey, session.ExpiresAt.Unix())
	sig := m.sign(data)
	token := base64.URLEncoding.EncodeToString([]byte(data + ":" + sig))

	m.sessionsMutex.Lock()
	m.sessions[token] = session
	m.sessionsMutex.Unlock()

	return token, nil
}

// ValidateSession validates a session token and returns the session.
func (m *AdminAuthManager) ValidateSession(token string) (*AdminSession, error) {
	// First check in-memory cache
	m.sessionsMutex.RLock()
	session, exists := m.sessions[token]
	m.sessionsMutex.RUnlock()

	if exists {
		if time.Now().After(session.ExpiresAt) {
			m.InvalidateSession(token)
			return nil, fmt.Errorf("session expired")
		}
		return session, nil
	}

	// Validate token signature
	decoded, err := base64.URLEncoding.DecodeString(token)
	if err != nil {
		return nil, fmt.Errorf("invalid token format")
	}

	parts := strings.Split(string(decoded), ":")
	if len(parts) != 3 {
		return nil, fmt.Errorf("invalid token structure")
	}

	pubkey := parts[0]
	expiryStr := parts[1]
	sig := parts[2]

	// Verify signature
	data := fmt.Sprintf("%s:%s", pubkey, expiryStr)
	if !m.verify(data, sig) {
		return nil, fmt.Errorf("invalid token signature")
	}

	// Check expiry
	var expiry int64
	fmt.Sscanf(expiryStr, "%d", &expiry)
	if time.Now().Unix() > expiry {
		return nil, fmt.Errorf("session expired")
	}

	// Verify pubkey is admin
	if pubkey != m.adminPubkey {
		return nil, fmt.Errorf("not an admin")
	}

	session = &AdminSession{
		Pubkey:    pubkey,
		ExpiresAt: time.Unix(expiry, 0),
	}

	// Cache the session
	m.sessionsMutex.Lock()
	m.sessions[token] = session
	m.sessionsMutex.Unlock()

	return session, nil
}

// InvalidateSession removes a session.
func (m *AdminAuthManager) InvalidateSession(token string) {
	m.sessionsMutex.Lock()
	delete(m.sessions, token)
	m.sessionsMutex.Unlock()
}

func (m *AdminAuthManager) sign(data string) string {
	h := hmac.New(sha256.New, m.secretKey)
	h.Write([]byte(data))
	return hex.EncodeToString(h.Sum(nil))
}

func (m *AdminAuthManager) verify(data, signature string) bool {
	expected := m.sign(data)
	return hmac.Equal([]byte(expected), []byte(signature))
}

// VerifyNostrAdminAuth verifies a Nostr auth event for admin access.
func (m *AdminAuthManager) VerifyNostrAdminAuth(eventBase64 string) (string, error) {
	eventBytes, err := base64.StdEncoding.DecodeString(eventBase64)
	if err != nil {
		return "", fmt.Errorf("invalid base64: %w", err)
	}

	ev := &goNostr.Event{}
	if err := json.Unmarshal(eventBytes, ev); err != nil {
		return "", fmt.Errorf("invalid JSON: %w", err)
	}

	// Verify signature
	if ok, err := ev.CheckSignature(); !ok || err != nil {
		return "", fmt.Errorf("invalid signature")
	}

	// Must be kind 24242
	if ev.Kind != 24242 {
		return "", fmt.Errorf("invalid event kind: expected 24242, got %d", ev.Kind)
	}

	// Check created_at is not in the future
	if ev.CreatedAt.Time().After(time.Now()) {
		return "", fmt.Errorf("event created_at is in the future")
	}

	// Find required tags
	var tTag, expirationTag string
	for _, tag := range ev.Tags {
		if len(tag) >= 2 {
			switch tag[0] {
			case "t":
				tTag = tag[1]
			case "expiration":
				expirationTag = tag[1]
			}
		}
	}

	// Verify t tag is "admin"
	if tTag != "admin" {
		return "", fmt.Errorf("invalid action: expected 'admin', got '%s'", tTag)
	}

	// Verify expiration
	if expirationTag == "" {
		return "", fmt.Errorf("missing expiration tag")
	}

	var expiry int64
	fmt.Sscanf(expirationTag, "%d", &expiry)
	if time.Now().Unix() > expiry {
		return "", fmt.Errorf("auth event expired")
	}

	// Verify pubkey is admin
	if ev.PubKey != m.adminPubkey {
		return "", fmt.Errorf("not authorized: pubkey is not admin")
	}

	return ev.PubKey, nil
}

// AdminSessionMiddleware protects routes with session token authentication.
func AdminSessionMiddleware(authManager *AdminAuthManager) gin.HandlerFunc {
	return func(c *gin.Context) {
		// Check for session token in cookie or Authorization header
		var token string

		// Try cookie first
		if cookie, err := c.Cookie("admin_session"); err == nil {
			token = cookie
		}

		// Try Authorization header (Bearer token)
		if token == "" {
			authHeader := c.GetHeader("Authorization")
			if strings.HasPrefix(authHeader, "Bearer ") {
				token = strings.TrimPrefix(authHeader, "Bearer ")
			}
		}

		if token == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, apiError{Message: "authentication required"})
			return
		}

		session, err := authManager.ValidateSession(token)
		if err != nil {
			authManager.log.Debug("admin session validation failed", zap.Error(err))
			c.AbortWithStatusJSON(http.StatusUnauthorized, apiError{Message: "invalid or expired session"})
			return
		}

		c.Set("admin_session", session)
		c.Set("admin_pubkey", session.Pubkey)
		c.Next()
	}
}

// AdminLoginRequest is the request body for admin login.
type AdminLoginRequest struct {
	AuthEvent string `json:"auth_event"` // Base64-encoded signed Nostr event
}

// AdminLoginResponse is the response for successful admin login.
type AdminLoginResponse struct {
	Token     string `json:"token"`
	ExpiresAt int64  `json:"expires_at"`
	Pubkey    string `json:"pubkey"`
}

// adminLogin handles admin login via Nostr auth.
func adminLogin(authManager *AdminAuthManager) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req AdminLoginRequest
		if err := c.BindJSON(&req); err != nil {
			c.AbortWithStatusJSON(http.StatusBadRequest, apiError{Message: "invalid request body"})
			return
		}

		if req.AuthEvent == "" {
			c.AbortWithStatusJSON(http.StatusBadRequest, apiError{Message: "auth_event is required"})
			return
		}

		pubkey, err := authManager.VerifyNostrAdminAuth(req.AuthEvent)
		if err != nil {
			authManager.log.Debug("admin login failed", zap.Error(err))
			c.AbortWithStatusJSON(http.StatusUnauthorized, apiError{Message: err.Error()})
			return
		}

		token, err := authManager.CreateSession(pubkey)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusInternalServerError, apiError{Message: "failed to create session"})
			return
		}

		// Set cookie for browser-based access
		c.SetCookie(
			"admin_session",
			token,
			86400, // 24 hours
			"/admin",
			"",    // domain
			true,  // secure (HTTPS only)
			true,  // httpOnly
		)

		c.JSON(http.StatusOK, AdminLoginResponse{
			Token:     token,
			ExpiresAt: time.Now().Add(24 * time.Hour).Unix(),
			Pubkey:    pubkey,
		})
	}
}

// adminLogout handles admin logout.
func adminLogout(authManager *AdminAuthManager) gin.HandlerFunc {
	return func(c *gin.Context) {
		// Get token from cookie or header
		var token string
		if cookie, err := c.Cookie("admin_session"); err == nil {
			token = cookie
		}

		if token != "" {
			authManager.InvalidateSession(token)
		}

		// Clear cookie
		c.SetCookie(
			"admin_session",
			"",
			-1, // Delete cookie
			"/admin",
			"",
			true,
			true,
		)

		c.JSON(http.StatusOK, gin.H{"message": "logged out"})
	}
}

// adminCheckSession checks if the current session is valid.
func adminCheckSession(authManager *AdminAuthManager) gin.HandlerFunc {
	return func(c *gin.Context) {
		var token string
		if cookie, err := c.Cookie("admin_session"); err == nil {
			token = cookie
		}
		if token == "" {
			authHeader := c.GetHeader("Authorization")
			if strings.HasPrefix(authHeader, "Bearer ") {
				token = strings.TrimPrefix(authHeader, "Bearer ")
			}
		}

		if token == "" {
			c.JSON(http.StatusOK, gin.H{"authenticated": false})
			return
		}

		session, err := authManager.ValidateSession(token)
		if err != nil {
			c.JSON(http.StatusOK, gin.H{"authenticated": false})
			return
		}

		c.JSON(http.StatusOK, gin.H{
			"authenticated": true,
			"pubkey":        session.Pubkey,
			"expires_at":    session.ExpiresAt.Unix(),
		})
	}
}

// adminLoginPageHTML returns the admin login page HTML with NIP-07 support.
func adminLoginPageHTML(adminPubkey string) string {
	return `<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>Blossom Admin Login</title>
    <style>
        * { box-sizing: border-box; margin: 0; padding: 0; }
        body {
            font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif;
            background: linear-gradient(135deg, #667eea 0%, #764ba2 100%);
            min-height: 100vh;
            display: flex;
            align-items: center;
            justify-content: center;
        }
        .login-container {
            background: white;
            padding: 40px;
            border-radius: 12px;
            box-shadow: 0 20px 60px rgba(0,0,0,0.3);
            width: 100%;
            max-width: 420px;
            text-align: center;
        }
        h1 {
            font-size: 24px;
            margin-bottom: 8px;
            color: #333;
        }
        .subtitle {
            color: #666;
            margin-bottom: 30px;
            font-size: 14px;
        }
        .btn {
            display: block;
            width: 100%;
            padding: 14px 20px;
            border: none;
            border-radius: 8px;
            cursor: pointer;
            font-size: 16px;
            font-weight: 500;
            transition: all 0.2s;
            margin-bottom: 12px;
        }
        .btn-primary {
            background: #6366f1;
            color: white;
        }
        .btn-primary:hover { background: #5558e3; }
        .btn-primary:disabled {
            background: #a5a6f6;
            cursor: not-allowed;
        }
        .btn-secondary {
            background: #f3f4f6;
            color: #374151;
        }
        .btn-secondary:hover { background: #e5e7eb; }
        .status {
            margin-top: 20px;
            padding: 12px;
            border-radius: 8px;
            font-size: 14px;
        }
        .status.error {
            background: #fef2f2;
            color: #dc2626;
            border: 1px solid #fecaca;
        }
        .status.success {
            background: #f0fdf4;
            color: #16a34a;
            border: 1px solid #bbf7d0;
        }
        .status.info {
            background: #eff6ff;
            color: #2563eb;
            border: 1px solid #bfdbfe;
        }
        .hidden { display: none; }
        .admin-pubkey {
            font-family: monospace;
            font-size: 11px;
            color: #9ca3af;
            word-break: break-all;
            margin-top: 20px;
            padding: 10px;
            background: #f9fafb;
            border-radius: 6px;
        }
        .divider {
            margin: 20px 0;
            border-top: 1px solid #e5e7eb;
            position: relative;
        }
        .divider span {
            position: absolute;
            top: -10px;
            left: 50%;
            transform: translateX(-50%);
            background: white;
            padding: 0 10px;
            color: #9ca3af;
            font-size: 12px;
        }
    </style>
</head>
<body>
    <div class="login-container">
        <h1>🌸 Blossom Admin</h1>
        <p class="subtitle">Authenticate with your Nostr key</p>

        <button id="nip07-login" class="btn btn-primary" onclick="loginWithNip07()">
            Login with Browser Extension (NIP-07)
        </button>

        <div class="divider"><span>or</span></div>

        <button id="manual-toggle" class="btn btn-secondary" onclick="showManualLogin()">
            Paste Signed Event
        </button>

        <div id="manual-login" class="hidden" style="margin-top: 20px;">
            <textarea id="auth-event" placeholder="Paste base64-encoded signed auth event..."
                style="width: 100%; height: 100px; padding: 10px; border: 1px solid #d1d5db; border-radius: 8px; font-family: monospace; font-size: 12px; resize: vertical;"></textarea>
            <button class="btn btn-primary" onclick="loginWithEvent()" style="margin-top: 10px;">
                Login
            </button>
        </div>

        <div id="status" class="status hidden"></div>

        <div class="admin-pubkey">
            Authorized admin: ` + adminPubkey[:16] + `...` + adminPubkey[len(adminPubkey)-8:] + `
        </div>
    </div>

    <script>
        const ADMIN_PUBKEY = '` + adminPubkey + `';

        function showStatus(message, type) {
            const status = document.getElementById('status');
            status.textContent = message;
            status.className = 'status ' + type;
        }

        function showManualLogin() {
            document.getElementById('manual-login').classList.toggle('hidden');
        }

        async function loginWithNip07() {
            const btn = document.getElementById('nip07-login');
            btn.disabled = true;
            btn.textContent = 'Connecting...';

            try {
                if (!window.nostr) {
                    throw new Error('No NIP-07 extension found. Install nos2x, Alby, or similar.');
                }

                // Get public key
                const pubkey = await window.nostr.getPublicKey();

                if (pubkey !== ADMIN_PUBKEY) {
                    throw new Error('Your pubkey is not authorized as admin.');
                }

                btn.textContent = 'Signing...';

                // Create auth event
                const event = {
                    kind: 24242,
                    created_at: Math.floor(Date.now() / 1000),
                    tags: [
                        ['t', 'admin'],
                        ['expiration', String(Math.floor(Date.now() / 1000) + 300)] // 5 min expiry
                    ],
                    content: ''
                };

                // Sign with extension
                const signedEvent = await window.nostr.signEvent(event);

                // Base64 encode
                const eventBase64 = btoa(JSON.stringify(signedEvent));

                await submitLogin(eventBase64);

            } catch (err) {
                showStatus(err.message, 'error');
                btn.disabled = false;
                btn.textContent = 'Login with Browser Extension (NIP-07)';
            }
        }

        async function loginWithEvent() {
            const eventBase64 = document.getElementById('auth-event').value.trim();
            if (!eventBase64) {
                showStatus('Please paste a signed auth event', 'error');
                return;
            }
            await submitLogin(eventBase64);
        }

        async function submitLogin(eventBase64) {
            try {
                const resp = await fetch('/admin/api/login', {
                    method: 'POST',
                    headers: { 'Content-Type': 'application/json' },
                    body: JSON.stringify({ auth_event: eventBase64 })
                });

                const data = await resp.json();

                if (!resp.ok) {
                    throw new Error(data.message || 'Login failed');
                }

                showStatus('Login successful! Redirecting...', 'success');

                // Redirect to dashboard
                setTimeout(() => {
                    window.location.href = '/admin/';
                }, 1000);

            } catch (err) {
                showStatus(err.message, 'error');
                document.getElementById('nip07-login').disabled = false;
                document.getElementById('nip07-login').textContent = 'Login with Browser Extension (NIP-07)';
            }
        }

        // Check if already logged in
        async function checkSession() {
            try {
                const resp = await fetch('/admin/api/session');
                const data = await resp.json();
                if (data.authenticated) {
                    window.location.href = '/admin/';
                }
            } catch (e) {
                // Not logged in
            }
        }

        checkSession();
    </script>
</body>
</html>`
}
