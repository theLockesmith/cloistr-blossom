package lightning

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"sync"
	"time"

	"git.coldforge.xyz/coldforge/cloistr-blossom/src/core"
	"git.coldforge.xyz/coldforge/cloistr-blossom/src/pkg/config"
	"go.uber.org/zap"
)

// Client implements the core.LightningClient interface using LND REST API.
// This avoids the heavy LND gRPC dependencies while providing the same functionality.
type Client struct {
	config     *config.LightningConfig
	log        *zap.Logger
	httpClient *http.Client
	baseURL    string
	macaroon   string
	connected  bool
	mu         sync.RWMutex
}

// LND REST API response types

// GetInfoResponse represents the response from /v1/getinfo
type GetInfoResponse struct {
	IdentityPubkey string `json:"identity_pubkey"`
	Alias          string `json:"alias"`
	BlockHeight    uint32 `json:"block_height"`
	SyncedToChain  bool   `json:"synced_to_chain"`
	Version        string `json:"version"`
}

// AddInvoiceRequest represents the request to /v1/invoices
type AddInvoiceRequest struct {
	Value  int64  `json:"value,string"`
	Memo   string `json:"memo,omitempty"`
	Expiry int64  `json:"expiry,string,omitempty"`
}

// AddInvoiceResponse represents the response from /v1/invoices
type AddInvoiceResponse struct {
	RHash          string `json:"r_hash"` // base64 encoded
	PaymentRequest string `json:"payment_request"`
	AddIndex       string `json:"add_index"`
}

// LookupInvoiceResponse represents the response from /v1/invoice/{r_hash}
type LookupInvoiceResponse struct {
	Memo           string `json:"memo"`
	RPreimage      string `json:"r_preimage"` // base64 encoded
	RHash          string `json:"r_hash"`     // base64 encoded
	Value          string `json:"value"`
	Settled        bool   `json:"settled"`
	State          string `json:"state"` // OPEN, SETTLED, CANCELED, ACCEPTED
	PaymentRequest string `json:"payment_request"`
}

// NewClient creates a new LND Lightning client using REST API.
func NewClient(cfg *config.LightningConfig, log *zap.Logger) (*Client, error) {
	if !cfg.Enabled {
		return &Client{config: cfg, log: log}, nil
	}

	c := &Client{
		config: cfg,
		log:    log,
	}

	if err := c.connect(); err != nil {
		return nil, fmt.Errorf("failed to connect to LND: %w", err)
	}

	return c, nil
}

// connect establishes connection to LND REST API.
func (c *Client) connect() error {
	// Build base URL
	scheme := "https"
	if c.config.Insecure {
		scheme = "http"
	}
	c.baseURL = fmt.Sprintf("%s://%s:%d", scheme, c.config.LNDHost, c.config.RESTPort)
	if c.config.RESTPort == 0 {
		// Default REST port is 8080
		c.baseURL = fmt.Sprintf("%s://%s:8080", scheme, c.config.LNDHost)
	}

	// Load TLS certificate if provided
	var tlsConfig *tls.Config
	if c.config.TLSCertPath != "" {
		certBytes, err := os.ReadFile(c.config.TLSCertPath)
		if err != nil {
			return fmt.Errorf("failed to read TLS cert: %w", err)
		}

		certPool := x509.NewCertPool()
		if !certPool.AppendCertsFromPEM(certBytes) {
			return errors.New("failed to parse TLS certificate")
		}

		tlsConfig = &tls.Config{
			RootCAs: certPool,
		}
	} else {
		// Skip verification if no cert provided (not recommended for production)
		tlsConfig = &tls.Config{
			InsecureSkipVerify: true,
		}
	}

	// Load macaroon
	if c.config.MacaroonPath != "" {
		macaroonBytes, err := os.ReadFile(c.config.MacaroonPath)
		if err != nil {
			return fmt.Errorf("failed to read macaroon: %w", err)
		}
		c.macaroon = hex.EncodeToString(macaroonBytes)
	} else if c.config.MacaroonHex != "" {
		c.macaroon = c.config.MacaroonHex
	} else {
		return errors.New("macaroon required: set macaroon_path or macaroon_hex")
	}

	// Create HTTP client
	c.httpClient = &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: tlsConfig,
		},
	}

	// Verify connection by getting node info
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	info, err := c.getInfo(ctx)
	if err != nil {
		return fmt.Errorf("failed to get LND node info: %w", err)
	}

	c.mu.Lock()
	c.connected = true
	c.mu.Unlock()

	c.log.Info("connected to LND node via REST API",
		zap.String("alias", info.Alias),
		zap.String("pubkey", info.IdentityPubkey),
		zap.Uint32("block_height", info.BlockHeight))

	return nil
}

// getInfo retrieves node info from LND.
func (c *Client) getInfo(ctx context.Context) (*GetInfoResponse, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", c.baseURL+"/v1/getinfo", nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Grpc-Metadata-macaroon", c.macaroon)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("LND returned status %d: %s", resp.StatusCode, string(body))
	}

	var info GetInfoResponse
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return nil, err
	}

	return &info, nil
}

// Close closes the HTTP client (no-op for HTTP).
func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.connected = false
	return nil
}

// IsConnected returns true if the client is connected to LND.
func (c *Client) IsConnected() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.connected
}

// CreateInvoice creates a new Lightning invoice.
func (c *Client) CreateInvoice(ctx context.Context, amountSats int64, memo string) (invoice string, paymentHash string, err error) {
	if !c.IsConnected() {
		return "", "", core.ErrLightningDisabled
	}

	if memo == "" {
		memo = c.config.InvoiceMemo
	}

	reqBody := AddInvoiceRequest{
		Value:  amountSats,
		Memo:   memo,
		Expiry: 30 * 60, // 30 minutes
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return "", "", err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/v1/invoices", bytes.NewReader(bodyBytes))
	if err != nil {
		return "", "", err
	}

	req.Header.Set("Grpc-Metadata-macaroon", c.macaroon)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("failed to create invoice: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", "", fmt.Errorf("LND returned status %d: %s", resp.StatusCode, string(body))
	}

	var addResp AddInvoiceResponse
	if err := json.NewDecoder(resp.Body).Decode(&addResp); err != nil {
		return "", "", err
	}

	// Convert r_hash from base64 to hex
	hashBytes, err := base64.StdEncoding.DecodeString(addResp.RHash)
	if err != nil {
		return "", "", fmt.Errorf("failed to decode r_hash: %w", err)
	}

	return addResp.PaymentRequest, hex.EncodeToString(hashBytes), nil
}

// LookupInvoice checks if an invoice has been paid.
func (c *Client) LookupInvoice(ctx context.Context, paymentHash string) (paid bool, preimage string, err error) {
	if !c.IsConnected() {
		return false, "", core.ErrLightningDisabled
	}

	// LND REST API expects the payment hash as a URL-safe base64 string
	hashBytes, err := hex.DecodeString(paymentHash)
	if err != nil {
		return false, "", fmt.Errorf("invalid payment hash: %w", err)
	}
	hashBase64 := base64.URLEncoding.EncodeToString(hashBytes)

	req, err := http.NewRequestWithContext(ctx, "GET", c.baseURL+"/v1/invoice/"+hashBase64, nil)
	if err != nil {
		return false, "", err
	}

	req.Header.Set("Grpc-Metadata-macaroon", c.macaroon)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return false, "", fmt.Errorf("failed to lookup invoice: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return false, "", fmt.Errorf("LND returned status %d: %s", resp.StatusCode, string(body))
	}

	var lookupResp LookupInvoiceResponse
	if err := json.NewDecoder(resp.Body).Decode(&lookupResp); err != nil {
		return false, "", err
	}

	if lookupResp.State == "SETTLED" || lookupResp.Settled {
		// Decode preimage from base64 to hex
		preimageBytes, err := base64.StdEncoding.DecodeString(lookupResp.RPreimage)
		if err != nil {
			return true, "", nil // Paid but couldn't decode preimage
		}
		return true, hex.EncodeToString(preimageBytes), nil
	}

	return false, "", nil
}

// ValidatePreimage validates a preimage against a payment hash.
func (c *Client) ValidatePreimage(paymentHash, preimage string) bool {
	preimageBytes, err := hex.DecodeString(preimage)
	if err != nil {
		return false
	}

	// Hash the preimage
	hash := sha256.Sum256(preimageBytes)
	calculatedHash := hex.EncodeToString(hash[:])

	return calculatedHash == paymentHash
}

// Ensure Client implements core.LightningClient
var _ core.LightningClient = (*Client)(nil)
