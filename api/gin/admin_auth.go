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

// adminLoginPageHTML returns the admin login page HTML with NIP-07 and NIP-46 support.
func adminLoginPageHTML(adminPubkey string) string {
	return `<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>Blossom Admin Login</title>
    <script src="https://unpkg.com/@noble/hashes@1.3.3/sha256.js"></script>
    <script src="https://unpkg.com/@noble/secp256k1@2.0.0/index.js"></script>
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
            max-width: 480px;
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
        .btn-bunker {
            background: #8b5cf6;
            color: white;
        }
        .btn-bunker:hover { background: #7c3aed; }
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
        .input-group {
            margin-top: 15px;
            text-align: left;
        }
        .input-group label {
            display: block;
            font-size: 13px;
            color: #374151;
            margin-bottom: 6px;
            font-weight: 500;
        }
        .input-group input {
            width: 100%;
            padding: 12px;
            border: 1px solid #d1d5db;
            border-radius: 8px;
            font-size: 14px;
            font-family: monospace;
        }
        .input-group input:focus {
            outline: none;
            border-color: #6366f1;
            box-shadow: 0 0 0 3px rgba(99, 102, 241, 0.1);
        }
        .input-group .hint {
            font-size: 11px;
            color: #9ca3af;
            margin-top: 4px;
        }
        .tabs {
            display: flex;
            margin-bottom: 20px;
            border-radius: 8px;
            background: #f3f4f6;
            padding: 4px;
        }
        .tab {
            flex: 1;
            padding: 10px;
            border: none;
            background: transparent;
            cursor: pointer;
            border-radius: 6px;
            font-size: 14px;
            font-weight: 500;
            color: #6b7280;
            transition: all 0.2s;
        }
        .tab.active {
            background: white;
            color: #374151;
            box-shadow: 0 1px 3px rgba(0,0,0,0.1);
        }
        .tab-content { display: none; }
        .tab-content.active { display: block; }
    </style>
</head>
<body>
    <div class="login-container">
        <h1>Blossom Admin</h1>
        <p class="subtitle">Authenticate with your Nostr key</p>

        <div class="tabs">
            <button class="tab active" onclick="switchTab('nip07')">Browser Extension</button>
            <button class="tab" onclick="switchTab('nip46')">Remote Signer</button>
            <button class="tab" onclick="switchTab('manual')">Manual</button>
        </div>

        <!-- NIP-07 Tab -->
        <div id="tab-nip07" class="tab-content active">
            <button id="nip07-login" class="btn btn-primary" onclick="loginWithNip07()">
                Login with NIP-07 Extension
            </button>
            <p class="hint" style="text-align: center; margin-top: 10px;">
                Works with nos2x, Alby, Nostr Connect, etc.
            </p>
        </div>

        <!-- NIP-46 Tab -->
        <div id="tab-nip46" class="tab-content">
            <div class="input-group">
                <label for="bunker-uri">Bunker URI or Nostr Connect URI</label>
                <input type="text" id="bunker-uri" placeholder="bunker://... or nostr+connect://...">
                <p class="hint">Paste your connection URI from nsecBunker, Amber, or another NIP-46 signer</p>
            </div>
            <button id="nip46-login" class="btn btn-bunker" onclick="loginWithNip46()" style="margin-top: 15px;">
                Connect to Signer
            </button>
        </div>

        <!-- Manual Tab -->
        <div id="tab-manual" class="tab-content">
            <div class="input-group">
                <label for="auth-event">Signed Auth Event (Base64)</label>
                <textarea id="auth-event" placeholder="Paste base64-encoded signed kind 24242 event..."
                    style="width: 100%; height: 100px; padding: 10px; border: 1px solid #d1d5db; border-radius: 8px; font-family: monospace; font-size: 12px; resize: vertical;"></textarea>
                <p class="hint">Generate with: t=admin, expiration=5min from now</p>
            </div>
            <button class="btn btn-primary" onclick="loginWithEvent()" style="margin-top: 15px;">
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
        let nip46State = null;

        function switchTab(tabName) {
            document.querySelectorAll('.tab').forEach(t => t.classList.remove('active'));
            document.querySelectorAll('.tab-content').forEach(t => t.classList.remove('active'));
            document.querySelector('.tab:nth-child(' + ({'nip07': 1, 'nip46': 2, 'manual': 3}[tabName]) + ')').classList.add('active');
            document.getElementById('tab-' + tabName).classList.add('active');
            hideStatus();
        }

        function showStatus(message, type) {
            const status = document.getElementById('status');
            status.textContent = message;
            status.className = 'status ' + type;
        }

        function hideStatus() {
            document.getElementById('status').className = 'status hidden';
        }

        // ============ NIP-07 Login ============
        async function loginWithNip07() {
            const btn = document.getElementById('nip07-login');
            btn.disabled = true;
            btn.textContent = 'Connecting...';

            try {
                if (!window.nostr) {
                    throw new Error('No NIP-07 extension found. Install nos2x, Alby, or similar.');
                }

                const pubkey = await window.nostr.getPublicKey();
                if (pubkey !== ADMIN_PUBKEY) {
                    throw new Error('Your pubkey is not authorized as admin.');
                }

                btn.textContent = 'Signing...';
                const signedEvent = await window.nostr.signEvent(createAuthEvent());
                const eventBase64 = btoa(JSON.stringify(signedEvent));
                await submitLogin(eventBase64);

            } catch (err) {
                showStatus(err.message, 'error');
                btn.disabled = false;
                btn.textContent = 'Login with NIP-07 Extension';
            }
        }

        // ============ NIP-46 Login ============
        async function loginWithNip46() {
            const btn = document.getElementById('nip46-login');
            const uriInput = document.getElementById('bunker-uri').value.trim();

            if (!uriInput) {
                showStatus('Please enter a bunker or nostr+connect URI', 'error');
                return;
            }

            btn.disabled = true;
            btn.textContent = 'Connecting...';

            try {
                const connInfo = parseNip46Uri(uriInput);
                showStatus('Connecting to relay: ' + connInfo.relay, 'info');

                // Generate ephemeral keypair for this session
                const clientPrivkey = generatePrivateKey();
                const clientPubkey = getPublicKey(clientPrivkey);

                // Connect to relay
                const ws = new WebSocket(connInfo.relay);

                await new Promise((resolve, reject) => {
                    ws.onopen = resolve;
                    ws.onerror = () => reject(new Error('Failed to connect to relay'));
                    setTimeout(() => reject(new Error('Connection timeout')), 10000);
                });

                showStatus('Connected! Requesting signature...', 'info');

                // Subscribe to responses
                const subId = generateSubId();
                ws.send(JSON.stringify(['REQ', subId, {
                    kinds: [24133],
                    '#p': [clientPubkey],
                    since: Math.floor(Date.now() / 1000) - 10
                }]));

                // Create the auth event to be signed
                const authEvent = createAuthEvent();
                authEvent.pubkey = connInfo.remotePubkey;

                // Send sign_event request
                const requestId = crypto.randomUUID();
                const request = {
                    id: requestId,
                    method: 'sign_event',
                    params: [JSON.stringify(authEvent)]
                };

                const encryptedContent = await nip04Encrypt(
                    clientPrivkey,
                    connInfo.remotePubkey,
                    JSON.stringify(request)
                );

                const requestEvent = {
                    kind: 24133,
                    pubkey: clientPubkey,
                    created_at: Math.floor(Date.now() / 1000),
                    tags: [['p', connInfo.remotePubkey]],
                    content: encryptedContent
                };

                const signedRequest = await signEvent(requestEvent, clientPrivkey);
                ws.send(JSON.stringify(['EVENT', signedRequest]));

                btn.textContent = 'Waiting for signer...';

                // Wait for response
                const response = await new Promise((resolve, reject) => {
                    const timeout = setTimeout(() => {
                        reject(new Error('Signer timeout - check your signer app'));
                    }, 60000);

                    ws.onmessage = async (msg) => {
                        try {
                            const data = JSON.parse(msg.data);
                            if (data[0] === 'EVENT' && data[2]?.kind === 24133) {
                                const evt = data[2];
                                if (evt.pubkey === connInfo.remotePubkey) {
                                    const decrypted = await nip04Decrypt(
                                        clientPrivkey,
                                        connInfo.remotePubkey,
                                        evt.content
                                    );
                                    const resp = JSON.parse(decrypted);
                                    if (resp.id === requestId) {
                                        clearTimeout(timeout);
                                        ws.close();
                                        resolve(resp);
                                    }
                                }
                            }
                        } catch (e) {
                            console.error('Parse error:', e);
                        }
                    };
                });

                if (response.error) {
                    throw new Error(response.error.message || 'Signer rejected request');
                }

                const signedEvent = typeof response.result === 'string'
                    ? JSON.parse(response.result)
                    : response.result;

                const eventBase64 = btoa(JSON.stringify(signedEvent));
                await submitLogin(eventBase64);

            } catch (err) {
                showStatus(err.message, 'error');
                btn.disabled = false;
                btn.textContent = 'Connect to Signer';
            }
        }

        function parseNip46Uri(uri) {
            // Handle both bunker:// and nostr+connect:// formats
            let url;
            if (uri.startsWith('bunker://')) {
                url = new URL(uri.replace('bunker://', 'https://'));
            } else if (uri.startsWith('nostr+connect://')) {
                url = new URL(uri.replace('nostr+connect://', 'https://'));
            } else {
                throw new Error('Invalid URI format. Use bunker:// or nostr+connect://');
            }

            const remotePubkey = url.hostname || url.pathname.replace(/^\/+/, '');
            const relay = url.searchParams.get('relay');
            const secret = url.searchParams.get('secret');

            if (!remotePubkey || remotePubkey.length !== 64) {
                throw new Error('Invalid pubkey in URI');
            }
            if (!relay) {
                throw new Error('No relay specified in URI');
            }

            return { remotePubkey, relay, secret };
        }

        // ============ Manual Login ============
        async function loginWithEvent() {
            const eventBase64 = document.getElementById('auth-event').value.trim();
            if (!eventBase64) {
                showStatus('Please paste a signed auth event', 'error');
                return;
            }
            await submitLogin(eventBase64);
        }

        // ============ Common Functions ============
        function createAuthEvent() {
            return {
                kind: 24242,
                created_at: Math.floor(Date.now() / 1000),
                tags: [
                    ['t', 'admin'],
                    ['expiration', String(Math.floor(Date.now() / 1000) + 300)]
                ],
                content: ''
            };
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
                setTimeout(() => { window.location.href = '/admin/'; }, 1000);

            } catch (err) {
                showStatus(err.message, 'error');
                resetButtons();
            }
        }

        function resetButtons() {
            const nip07Btn = document.getElementById('nip07-login');
            const nip46Btn = document.getElementById('nip46-login');
            if (nip07Btn) {
                nip07Btn.disabled = false;
                nip07Btn.textContent = 'Login with NIP-07 Extension';
            }
            if (nip46Btn) {
                nip46Btn.disabled = false;
                nip46Btn.textContent = 'Connect to Signer';
            }
        }

        // ============ Crypto Helpers ============
        function generatePrivateKey() {
            const bytes = new Uint8Array(32);
            crypto.getRandomValues(bytes);
            return bytesToHex(bytes);
        }

        function getPublicKey(privkey) {
            const privBytes = hexToBytes(privkey);
            const pubBytes = nobleSecp256k1.getPublicKey(privBytes, true).slice(1);
            return bytesToHex(pubBytes);
        }

        function generateSubId() {
            return Math.random().toString(36).substring(2, 10);
        }

        async function signEvent(event, privkey) {
            event.id = await getEventHash(event);
            const sig = await nobleSecp256k1.sign(
                hexToBytes(event.id),
                hexToBytes(privkey)
            );
            event.sig = bytesToHex(sig.toCompactRawBytes());
            return event;
        }

        async function getEventHash(event) {
            const serialized = JSON.stringify([
                0,
                event.pubkey,
                event.created_at,
                event.kind,
                event.tags,
                event.content
            ]);
            const hash = await crypto.subtle.digest('SHA-256', new TextEncoder().encode(serialized));
            return bytesToHex(new Uint8Array(hash));
        }

        // NIP-04 encryption (simplified - uses Web Crypto)
        async function nip04Encrypt(privkey, pubkey, text) {
            const sharedSecret = nobleSecp256k1.getSharedSecret(hexToBytes(privkey), '02' + pubkey);
            const key = await crypto.subtle.importKey(
                'raw',
                sharedSecret.slice(1, 33),
                { name: 'AES-CBC' },
                false,
                ['encrypt']
            );
            const iv = crypto.getRandomValues(new Uint8Array(16));
            const encrypted = await crypto.subtle.encrypt(
                { name: 'AES-CBC', iv },
                key,
                new TextEncoder().encode(text)
            );
            return btoa(String.fromCharCode(...new Uint8Array(encrypted))) + '?iv=' + btoa(String.fromCharCode(...iv));
        }

        async function nip04Decrypt(privkey, pubkey, data) {
            const [encryptedB64, ivB64] = data.split('?iv=');
            const sharedSecret = nobleSecp256k1.getSharedSecret(hexToBytes(privkey), '02' + pubkey);
            const key = await crypto.subtle.importKey(
                'raw',
                sharedSecret.slice(1, 33),
                { name: 'AES-CBC' },
                false,
                ['decrypt']
            );
            const encrypted = Uint8Array.from(atob(encryptedB64), c => c.charCodeAt(0));
            const iv = Uint8Array.from(atob(ivB64), c => c.charCodeAt(0));
            const decrypted = await crypto.subtle.decrypt(
                { name: 'AES-CBC', iv },
                key,
                encrypted
            );
            return new TextDecoder().decode(decrypted);
        }

        function hexToBytes(hex) {
            const bytes = new Uint8Array(hex.length / 2);
            for (let i = 0; i < bytes.length; i++) {
                bytes[i] = parseInt(hex.substr(i * 2, 2), 16);
            }
            return bytes;
        }

        function bytesToHex(bytes) {
            return Array.from(bytes).map(b => b.toString(16).padStart(2, '0')).join('');
        }

        // Check if already logged in
        async function checkSession() {
            try {
                const resp = await fetch('/admin/api/session');
                const data = await resp.json();
                if (data.authenticated) {
                    window.location.href = '/admin/';
                }
            } catch (e) {}
        }

        checkSession();
    </script>
</body>
</html>`
}
