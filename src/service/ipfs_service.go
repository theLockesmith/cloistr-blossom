package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	pinclient "github.com/ipfs/boxo/pinning/remote/client"
	"github.com/ipfs/go-cid"
	"github.com/multiformats/go-multihash"
	"go.uber.org/zap"

	"git.coldforge.xyz/coldforge/cloistr-blossom/internal/cache"
	"git.coldforge.xyz/coldforge/cloistr-blossom/internal/storage"
	"git.coldforge.xyz/coldforge/cloistr-blossom/src/core"
	"git.coldforge.xyz/coldforge/cloistr-blossom/src/pkg/config"
)

const (
	ipfsPinCachePrefix = "ipfs_pin:"
	ipfsPinCacheTTL    = 24 * time.Hour
)

type ipfsService struct {
	storage    storage.StorageBackend
	cache      cache.Cache
	client     *pinclient.Client
	gatewayURL string
	autoPin    bool
	log        *zap.Logger
	configured bool
}

// NewIPFSService creates a new IPFS pinning service.
func NewIPFSService(
	storageBackend storage.StorageBackend,
	appCache cache.Cache,
	conf *config.IPFSConfig,
	log *zap.Logger,
) (core.IPFSService, error) {
	svc := &ipfsService{
		storage:    storageBackend,
		cache:      appCache,
		gatewayURL: conf.GatewayURL,
		autoPin:    conf.AutoPin,
		log:        log,
		configured: false,
	}

	if !conf.Enabled || conf.Endpoint == "" || conf.BearerToken == "" {
		log.Info("IPFS pinning not configured")
		return svc, nil
	}

	// Create pinning service client
	svc.client = pinclient.NewClient(conf.Endpoint, conf.BearerToken)
	svc.configured = true

	log.Info("IPFS pinning service initialized",
		zap.String("endpoint", conf.Endpoint),
		zap.String("gateway", conf.GatewayURL),
		zap.Bool("auto_pin", conf.AutoPin))

	return svc, nil
}

// IsConfigured returns true if IPFS pinning is configured.
func (s *ipfsService) IsConfigured() bool {
	return s.configured
}

// PinBlob pins a blob to IPFS via a pinning service.
func (s *ipfsService) PinBlob(ctx context.Context, blobHash string, name string) (*core.IPFSPin, error) {
	if !s.configured {
		return nil, core.ErrIPFSNotConfigured
	}

	// Check if already pinned
	if existing, err := s.GetPinStatus(ctx, blobHash); err == nil && existing.Status == core.IPFSPinStatusPinned {
		return existing, nil
	}

	// Get blob content from storage
	reader, err := s.storage.Get(ctx, blobHash)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", core.ErrBlobNotFound, err)
	}
	defer reader.Close()

	// Read content to calculate CID
	content, err := io.ReadAll(reader)
	if err != nil {
		return nil, fmt.Errorf("read blob: %w", err)
	}

	// Calculate IPFS CID (using raw leaves, CIDv1)
	contentCID, err := calculateCID(content)
	if err != nil {
		return nil, fmt.Errorf("calculate CID: %w", err)
	}

	// Use blob hash as name if not provided
	if name == "" {
		name = blobHash[:16] // Use first 16 chars of hash
	}

	// Pin to IPFS service
	pinStatus, err := s.client.Add(ctx, contentCID,
		pinclient.PinOpts.WithName(name),
		pinclient.PinOpts.AddMeta(map[string]string{
			"blob_hash": blobHash,
			"source":    "cloistr-blossom",
		}),
	)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", core.ErrIPFSPinFailed, err)
	}

	// Create pin record
	pin := &core.IPFSPin{
		BlobHash:  blobHash,
		CID:       contentCID.String(),
		Name:      name,
		Status:    convertStatus(pinStatus.GetStatus()),
		RequestID: pinStatus.GetRequestId(),
		Meta: map[string]string{
			"blob_hash": blobHash,
		},
		CreatedAt: time.Now().Unix(),
	}

	if pin.Status == core.IPFSPinStatusPinned {
		pin.PinnedAt = time.Now().Unix()
	}

	// Cache pin info
	s.cachePin(ctx, pin)

	s.log.Info("blob pinned to IPFS",
		zap.String("blob_hash", blobHash),
		zap.String("cid", pin.CID),
		zap.String("status", string(pin.Status)))

	return pin, nil
}

// UnpinBlob removes a blob from IPFS pinning.
func (s *ipfsService) UnpinBlob(ctx context.Context, blobHash string) error {
	if !s.configured {
		return core.ErrIPFSNotConfigured
	}

	// Get pin info to find the request ID
	pin, err := s.GetPinStatus(ctx, blobHash)
	if err != nil {
		return err
	}

	// Delete pin by request ID
	if err := s.client.DeleteByID(ctx, pin.RequestID); err != nil {
		return fmt.Errorf("unpin failed: %w", err)
	}

	// Remove from cache
	if s.cache != nil {
		s.cache.Delete(ctx, ipfsPinCachePrefix+blobHash)
	}

	s.log.Info("blob unpinned from IPFS",
		zap.String("blob_hash", blobHash),
		zap.String("cid", pin.CID))

	return nil
}

// GetPinStatus returns the current pin status for a blob.
func (s *ipfsService) GetPinStatus(ctx context.Context, blobHash string) (*core.IPFSPin, error) {
	if !s.configured {
		return nil, core.ErrIPFSNotConfigured
	}

	// Check cache first
	if s.cache != nil {
		if data, ok := s.cache.Get(ctx, ipfsPinCachePrefix+blobHash); ok {
			var pin core.IPFSPin
			if err := json.Unmarshal(data, &pin); err == nil {
				// If not yet pinned, refresh status
				if pin.Status != core.IPFSPinStatusPinned && pin.Status != core.IPFSPinStatusFailed {
					return s.refreshPinStatus(ctx, &pin)
				}
				return &pin, nil
			}
		}
	}

	// Search for pin by blob hash in metadata
	pins, err := s.client.LsSync(ctx,
		pinclient.PinOpts.LsMeta(map[string]string{"blob_hash": blobHash}),
	)
	if err != nil {
		return nil, fmt.Errorf("query pins: %w", err)
	}

	if len(pins) == 0 {
		return nil, core.ErrIPFSPinNotFound
	}

	// Convert to our pin type
	pinResult := pins[0]
	pin := &core.IPFSPin{
		BlobHash:  blobHash,
		CID:       pinResult.GetPin().GetCid().String(),
		Name:      pinResult.GetPin().GetName(),
		Status:    convertStatus(pinResult.GetStatus()),
		RequestID: pinResult.GetRequestId(),
		Meta:      pinResult.GetPin().GetMeta(),
		CreatedAt: pinResult.GetCreated().Unix(),
	}

	if pin.Status == core.IPFSPinStatusPinned {
		pin.PinnedAt = pinResult.GetCreated().Unix()
	}

	// Cache the result
	s.cachePin(ctx, pin)

	return pin, nil
}

// refreshPinStatus updates the status of a pin from the remote service.
func (s *ipfsService) refreshPinStatus(ctx context.Context, pin *core.IPFSPin) (*core.IPFSPin, error) {
	pinResult, err := s.client.GetStatusByID(ctx, pin.RequestID)
	if err != nil {
		return pin, nil // Return cached version on error
	}

	pin.Status = convertStatus(pinResult.GetStatus())
	if pin.Status == core.IPFSPinStatusPinned && pin.PinnedAt == 0 {
		pin.PinnedAt = time.Now().Unix()
	}

	// Update cache
	s.cachePin(ctx, pin)

	return pin, nil
}

// ListPins returns all pins for the configured service.
func (s *ipfsService) ListPins(ctx context.Context, status core.IPFSPinStatus, limit int) ([]core.IPFSPin, error) {
	if !s.configured {
		return nil, core.ErrIPFSNotConfigured
	}

	var opts []pinclient.LsOption
	opts = append(opts, pinclient.PinOpts.LsMeta(map[string]string{"source": "cloistr-blossom"}))

	if status != "" {
		opts = append(opts, pinclient.PinOpts.FilterStatus(convertToClientStatus(status)))
	}

	if limit > 0 {
		opts = append(opts, pinclient.PinOpts.Limit(limit))
	}

	pinStatuses, err := s.client.LsSync(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("list pins: %w", err)
	}

	var pins []core.IPFSPin
	for _, ps := range pinStatuses {
		meta := ps.GetPin().GetMeta()
		pin := core.IPFSPin{
			BlobHash:  meta["blob_hash"],
			CID:       ps.GetPin().GetCid().String(),
			Name:      ps.GetPin().GetName(),
			Status:    convertStatus(ps.GetStatus()),
			RequestID: ps.GetRequestId(),
			Meta:      meta,
			CreatedAt: ps.GetCreated().Unix(),
		}
		if pin.Status == core.IPFSPinStatusPinned {
			pin.PinnedAt = ps.GetCreated().Unix()
		}
		pins = append(pins, pin)
	}

	return pins, nil
}

// GetIPFSGatewayURL returns the gateway URL for accessing a pinned blob.
func (s *ipfsService) GetIPFSGatewayURL(cidStr string) string {
	// Ensure gateway URL ends with /
	gateway := strings.TrimSuffix(s.gatewayURL, "/") + "/"
	return gateway + cidStr
}

// cachePin stores pin info in cache.
func (s *ipfsService) cachePin(ctx context.Context, pin *core.IPFSPin) {
	if s.cache == nil {
		return
	}
	data, _ := json.Marshal(pin)
	s.cache.Set(ctx, ipfsPinCachePrefix+pin.BlobHash, data, ipfsPinCacheTTL)
}

// calculateCID generates an IPFS CID from content.
// Uses CIDv1 with raw codec and SHA-256 hash (standard for IPFS).
func calculateCID(data []byte) (cid.Cid, error) {
	// Create multihash of the content using SHA-256
	mh, err := multihash.Sum(data, multihash.SHA2_256, -1)
	if err != nil {
		return cid.Cid{}, fmt.Errorf("create multihash: %w", err)
	}

	// Create CIDv1 with raw codec (0x55)
	// Raw codec is used for arbitrary binary data
	return cid.NewCidV1(cid.Raw, mh), nil
}

// convertStatus converts pinclient status to our status type.
func convertStatus(status pinclient.Status) core.IPFSPinStatus {
	switch status {
	case pinclient.StatusQueued:
		return core.IPFSPinStatusQueued
	case pinclient.StatusPinning:
		return core.IPFSPinStatusPinning
	case pinclient.StatusPinned:
		return core.IPFSPinStatusPinned
	case pinclient.StatusFailed:
		return core.IPFSPinStatusFailed
	default:
		return core.IPFSPinStatusQueued
	}
}

// convertToClientStatus converts our status to pinclient status.
func convertToClientStatus(status core.IPFSPinStatus) pinclient.Status {
	switch status {
	case core.IPFSPinStatusQueued:
		return pinclient.StatusQueued
	case core.IPFSPinStatusPinning:
		return pinclient.StatusPinning
	case core.IPFSPinStatusPinned:
		return pinclient.StatusPinned
	case core.IPFSPinStatusFailed:
		return pinclient.StatusFailed
	default:
		return pinclient.StatusQueued
	}
}

// noopIPFSService is a no-op implementation when IPFS is not configured.
type noopIPFSService struct{}

func (s *noopIPFSService) IsConfigured() bool {
	return false
}

func (s *noopIPFSService) PinBlob(ctx context.Context, blobHash string, name string) (*core.IPFSPin, error) {
	return nil, core.ErrIPFSNotConfigured
}

func (s *noopIPFSService) UnpinBlob(ctx context.Context, blobHash string) error {
	return core.ErrIPFSNotConfigured
}

func (s *noopIPFSService) GetPinStatus(ctx context.Context, blobHash string) (*core.IPFSPin, error) {
	return nil, core.ErrIPFSNotConfigured
}

func (s *noopIPFSService) ListPins(ctx context.Context, status core.IPFSPinStatus, limit int) ([]core.IPFSPin, error) {
	return nil, core.ErrIPFSNotConfigured
}

func (s *noopIPFSService) GetIPFSGatewayURL(cidStr string) string {
	return ""
}

// Ensure we satisfy the interface
var _ core.IPFSService = (*ipfsService)(nil)
var _ core.IPFSService = (*noopIPFSService)(nil)

// Helper to read blob content (not used since we read directly)
func readAll(r io.Reader) (*bytes.Buffer, error) {
	buf := new(bytes.Buffer)
	_, err := buf.ReadFrom(r)
	return buf, err
}
