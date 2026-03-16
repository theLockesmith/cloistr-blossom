package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/nbd-wtf/go-nostr"
	"github.com/nbd-wtf/go-nostr/nip19"
	"go.uber.org/zap"

	"git.coldforge.xyz/coldforge/cloistr-blossom/db"
	"git.coldforge.xyz/coldforge/cloistr-blossom/internal/storage"
	"git.coldforge.xyz/coldforge/cloistr-blossom/src/core"
	"git.coldforge.xyz/coldforge/cloistr-blossom/src/pkg/config"
)

const (
	// Nostr event kinds for federation
	KindFileMetadata   = 1063  // NIP-94: File metadata
	KindBlossomServers = 10063 // BUD-03: User's server list
)

type federationService struct {
	config     config.FederationConfig
	cdnBaseURL string
	queries    *db.Queries
	storage    storage.StorageBackend
	log        *zap.Logger

	// Server identity
	serverSk    string // Private key (hex)
	serverPk    string // Public key (hex)

	// Relay connections
	relayPool   *nostr.SimplePool
	relayMu     sync.RWMutex
	relayConns  map[string]*nostr.Relay

	// Workers
	publishQueue chan *publishJob
	mirrorQueue  chan string // blob hashes to mirror
	stopCh       chan struct{}
	wg           sync.WaitGroup
}

type publishJob struct {
	blob      *core.Blob
	eventID   string // Internal event ID for tracking
	retries   int
}

// NewFederationService creates a new federation service.
func NewFederationService(
	cfg config.FederationConfig,
	cdnBaseURL string,
	queries *db.Queries,
	storage storage.StorageBackend,
	log *zap.Logger,
) (core.FederationService, error) {
	if !cfg.Enabled {
		return &federationService{
			config: cfg,
			log:    log,
		}, nil
	}

	// Parse server private key
	var sk string
	if strings.HasPrefix(cfg.ServerNsec, "nsec") {
		// Decode nsec format
		_, decoded, err := nip19.Decode(cfg.ServerNsec)
		if err != nil {
			return nil, fmt.Errorf("invalid server_nsec: %w", err)
		}
		sk = decoded.(string)
	} else {
		// Assume hex format
		sk = cfg.ServerNsec
	}

	if len(sk) != 64 {
		return nil, fmt.Errorf("invalid server_nsec: must be 64 hex chars or nsec format")
	}

	// Derive public key
	pk, err := nostr.GetPublicKey(sk)
	if err != nil {
		return nil, fmt.Errorf("failed to derive public key: %w", err)
	}

	svc := &federationService{
		config:       cfg,
		cdnBaseURL:   cdnBaseURL,
		queries:      queries,
		storage:      storage,
		log:          log,
		serverSk:     sk,
		serverPk:     pk,
		relayPool:    nostr.NewSimplePool(context.Background()),
		relayConns:   make(map[string]*nostr.Relay),
		publishQueue: make(chan *publishJob, cfg.BatchSize*2),
		mirrorQueue:  make(chan string, cfg.BatchSize*2),
		stopCh:       make(chan struct{}),
	}

	log.Info("federation service initialized",
		zap.String("server_pubkey", pk),
		zap.String("mode", cfg.Mode),
		zap.Int("relay_count", len(cfg.RelayURLs)))

	return svc, nil
}

// IsEnabled returns true if federation is enabled.
func (s *federationService) IsEnabled() bool {
	return s.config.Enabled
}

// GetMode returns the current federation mode.
func (s *federationService) GetMode() core.FederationMode {
	switch s.config.Mode {
	case "publish":
		return core.FederationModePublish
	case "subscribe":
		return core.FederationModeSubscribe
	default:
		return core.FederationModeBoth
	}
}

// GetServerPubkey returns this server's Nostr pubkey.
func (s *federationService) GetServerPubkey() string {
	return s.serverPk
}

// PublishBlob publishes a kind 1063 event for a blob synchronously.
func (s *federationService) PublishBlob(ctx context.Context, blob *core.Blob) error {
	if !s.config.Enabled {
		return core.ErrFederationDisabled
	}

	mode := s.GetMode()
	if mode == core.FederationModeSubscribe {
		return nil // Not in publish mode
	}

	if len(s.config.RelayURLs) == 0 {
		return core.ErrFederationNoRelays
	}

	// Create kind 1063 event
	event := s.createFileMetadataEvent(blob)

	// Sign the event
	if err := event.Sign(s.serverSk); err != nil {
		return fmt.Errorf("failed to sign event: %w", err)
	}

	// Publish to all relays
	var lastErr error
	published := 0
	for _, relayURL := range s.config.RelayURLs {
		relay, err := s.getOrConnectRelay(ctx, relayURL)
		if err != nil {
			s.log.Warn("failed to connect to relay",
				zap.String("relay", relayURL),
				zap.Error(err))
			lastErr = err
			continue
		}

		if err := relay.Publish(ctx, event); err != nil {
			s.log.Warn("failed to publish to relay",
				zap.String("relay", relayURL),
				zap.Error(err))
			lastErr = err
			continue
		}

		published++
	}

	if published == 0 && lastErr != nil {
		return fmt.Errorf("failed to publish to any relay: %w", lastErr)
	}

	// Record the event
	_, err := s.queries.CreateFederationEvent(ctx, db.CreateFederationEventParams{
		ID:        uuid.New().String(),
		EventKind: KindFileMetadata,
		Pubkey:    s.serverPk,
		BlobHash:  sql.NullString{String: blob.Sha256, Valid: true},
		Direction: "publish",
		CreatedAt: time.Now().Unix(),
	})
	if err != nil {
		s.log.Warn("failed to record federation event", zap.Error(err))
	}

	s.log.Info("blob published to federation",
		zap.String("hash", blob.Sha256),
		zap.Int("relays", published))

	return nil
}

// PublishBlobAsync queues a blob for async publishing.
func (s *federationService) PublishBlobAsync(ctx context.Context, blob *core.Blob) error {
	if !s.config.Enabled {
		return core.ErrFederationDisabled
	}

	// Create tracking event
	eventID := uuid.New().String()
	_, err := s.queries.CreateFederationEvent(ctx, db.CreateFederationEventParams{
		ID:        eventID,
		EventKind: KindFileMetadata,
		Pubkey:    s.serverPk,
		BlobHash:  sql.NullString{String: blob.Sha256, Valid: true},
		Direction: "publish",
		CreatedAt: time.Now().Unix(),
	})
	if err != nil {
		return fmt.Errorf("failed to create federation event: %w", err)
	}

	// Queue for async processing
	select {
	case s.publishQueue <- &publishJob{blob: blob, eventID: eventID}:
		return nil
	default:
		return fmt.Errorf("publish queue full")
	}
}

// RepublishBlob forces republishing of a blob's kind 1063 event.
func (s *federationService) RepublishBlob(ctx context.Context, hash string) error {
	// TODO: Fetch blob from storage and republish
	s.log.Info("republish requested", zap.String("hash", hash))
	return nil
}

// GetPendingPublishes returns blobs waiting to be published.
func (s *federationService) GetPendingPublishes(ctx context.Context, limit int) ([]*core.FederationEvent, error) {
	events, err := s.queries.ListPendingPublishes(ctx, int32(limit))
	if err != nil {
		return nil, err
	}

	result := make([]*core.FederationEvent, len(events))
	for i, e := range events {
		result[i] = dbEventToCore(e)
	}
	return result, nil
}

// SubscribeToRelays connects to configured relays and subscribes to events.
func (s *federationService) SubscribeToRelays(ctx context.Context) error {
	if !s.config.Enabled {
		return core.ErrFederationDisabled
	}

	mode := s.GetMode()
	if mode == core.FederationModePublish {
		return nil // Not in subscribe mode
	}

	// Subscribe to kind 1063 and 10063 events
	filters := []nostr.Filter{
		{
			Kinds: []int{KindFileMetadata, KindBlossomServers},
			Limit: 100,
		},
	}

	// Subscribe to all relays
	for _, relayURL := range s.config.RelayURLs {
		go s.subscribeToRelay(ctx, relayURL, filters)
	}

	return nil
}

func (s *federationService) subscribeToRelay(ctx context.Context, relayURL string, filters []nostr.Filter) {
	relay, err := s.getOrConnectRelay(ctx, relayURL)
	if err != nil {
		s.log.Error("failed to connect for subscription",
			zap.String("relay", relayURL),
			zap.Error(err))
		return
	}

	sub, err := relay.Subscribe(ctx, filters)
	if err != nil {
		s.log.Error("failed to subscribe",
			zap.String("relay", relayURL),
			zap.Error(err))
		return
	}

	s.log.Info("subscribed to relay", zap.String("relay", relayURL))

	for {
		select {
		case <-ctx.Done():
			return
		case <-s.stopCh:
			return
		case event := <-sub.Events:
			if event == nil {
				continue
			}
			s.handleIncomingEvent(ctx, event)
		}
	}
}

func (s *federationService) handleIncomingEvent(ctx context.Context, event *nostr.Event) {
	switch event.Kind {
	case KindFileMetadata:
		s.handleFileMetadataEvent(ctx, event)
	case KindBlossomServers:
		s.handleServerListEvent(ctx, event)
	}
}

func (s *federationService) handleFileMetadataEvent(ctx context.Context, event *nostr.Event) {
	// Extract file metadata from tags
	var hash, url, mimeType string
	var size int64

	for _, tag := range event.Tags {
		if len(tag) < 2 {
			continue
		}
		switch tag[0] {
		case "x":
			hash = tag[1]
		case "url":
			url = tag[1]
		case "m":
			mimeType = tag[1]
		case "size":
			fmt.Sscanf(tag[1], "%d", &size)
		}
	}

	if hash == "" || url == "" {
		return // Invalid event
	}

	// Upsert federated blob
	_, err := s.queries.UpsertFederatedBlob(ctx, db.UpsertFederatedBlobParams{
		Hash:         hash,
		Size:         size,
		MimeType:     mimeType,
		DiscoveredAt: time.Now().Unix(),
	})
	if err != nil {
		s.log.Warn("failed to upsert federated blob",
			zap.String("hash", hash),
			zap.Error(err))
		return
	}

	// Add URL
	serverID := extractServerID(url)
	_, err = s.queries.AddFederatedBlobURL(ctx, db.AddFederatedBlobURLParams{
		BlobHash:  hash,
		Url:       url,
		ServerID:  sql.NullString{String: serverID, Valid: serverID != ""},
		Priority:  0,
		CreatedAt: time.Now().Unix(),
	})
	if err != nil {
		s.log.Warn("failed to add federated blob URL",
			zap.String("hash", hash),
			zap.Error(err))
	}

	// Record server
	if serverID != "" {
		_, _ = s.queries.UpsertKnownServer(ctx, db.UpsertKnownServerParams{
			Url:       serverID,
			FirstSeen: time.Now().Unix(),
		})
		_ = s.queries.IncrementServerBlobCount(ctx, db.IncrementServerBlobCountParams{
			Url:      serverID,
			LastSeen: time.Now().Unix(),
		})
	}

	// Check if we should auto-mirror
	if s.config.AutoMirror {
		shouldMirror, _ := s.ShouldAutoMirror(ctx, hash)
		if shouldMirror {
			s.MirrorBlobAsync(ctx, hash)
		}
	}

	s.log.Debug("processed file metadata event",
		zap.String("hash", hash),
		zap.String("url", url))
}

func (s *federationService) handleServerListEvent(ctx context.Context, event *nostr.Event) {
	// kind 10063 contains a list of Blossom server URLs in "r" tags
	var rank int
	for _, tag := range event.Tags {
		if len(tag) < 2 || tag[0] != "r" {
			continue
		}
		serverURL := tag[1]

		// Check if this is our server
		if strings.Contains(serverURL, s.cdnBaseURL) {
			// This user has us in their server list
			_, err := s.queries.UpsertFederatedUser(ctx, db.UpsertFederatedUserParams{
				Pubkey:     event.PubKey,
				EventID:    event.ID,
				ServerRank: int32(rank),
				CreatedAt:  time.Now().Unix(),
			})
			if err != nil {
				s.log.Warn("failed to upsert federated user",
					zap.String("pubkey", event.PubKey),
					zap.Error(err))
			}
		}

		// Record the server
		_, _ = s.queries.UpsertKnownServer(ctx, db.UpsertKnownServerParams{
			Url:       serverURL,
			FirstSeen: time.Now().Unix(),
		})

		rank++
	}
}

// ProcessEvent handles an incoming Nostr event (for manual processing).
func (s *federationService) ProcessEvent(ctx context.Context, eventID string, eventKind int, pubkey string, content []byte) error {
	var event nostr.Event
	if err := json.Unmarshal(content, &event); err != nil {
		return fmt.Errorf("invalid event JSON: %w", err)
	}

	s.handleIncomingEvent(ctx, &event)
	return nil
}

// GetFederatedBlob returns info about a federated blob.
func (s *federationService) GetFederatedBlob(ctx context.Context, hash string) (*core.FederatedBlob, error) {
	blob, err := s.queries.GetFederatedBlob(ctx, hash)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, core.ErrFederatedBlobNotFound
		}
		return nil, err
	}

	urls, err := s.queries.GetFederatedBlobURLs(ctx, hash)
	if err != nil {
		return nil, err
	}

	urlList := make([]string, len(urls))
	for i, u := range urls {
		urlList[i] = u.Url
	}

	return &core.FederatedBlob{
		Hash:         blob.Hash,
		Size:         blob.Size,
		MimeType:     blob.MimeType,
		URLs:         urlList,
		RefCount:     int(blob.RefCount),
		Status:       core.FederatedBlobStatus(blob.Status),
		DiscoveredAt: blob.DiscoveredAt,
		MirroredAt:   nullInt64ToInt64(blob.MirroredAt),
		LastSeenAt:   blob.LastSeenAt,
	}, nil
}

// GetFallbackURLs returns remote URLs for a blob not stored locally.
func (s *federationService) GetFallbackURLs(ctx context.Context, hash string) ([]string, error) {
	if !s.config.Enabled {
		return nil, nil
	}

	urls, err := s.queries.GetFederatedBlobURLs(ctx, hash)
	if err != nil {
		return nil, err
	}

	result := make([]string, 0, len(urls))
	for _, u := range urls {
		if u.Healthy {
			result = append(result, u.Url)
		}
	}

	return result, nil
}

// ListFederatedBlobs returns discovered federated blobs.
func (s *federationService) ListFederatedBlobs(ctx context.Context, status core.FederatedBlobStatus, limit, offset int) ([]*core.FederatedBlob, error) {
	blobs, err := s.queries.ListFederatedBlobsByStatus(ctx, db.ListFederatedBlobsByStatusParams{
		Status: string(status),
		Limit:  int32(limit),
		Offset: int32(offset),
	})
	if err != nil {
		return nil, err
	}

	result := make([]*core.FederatedBlob, len(blobs))
	for i, b := range blobs {
		result[i] = &core.FederatedBlob{
			Hash:         b.Hash,
			Size:         b.Size,
			MimeType:     b.MimeType,
			RefCount:     int(b.RefCount),
			Status:       core.FederatedBlobStatus(b.Status),
			DiscoveredAt: b.DiscoveredAt,
			MirroredAt:   nullInt64ToInt64(b.MirroredAt),
			LastSeenAt:   b.LastSeenAt,
		}
	}

	return result, nil
}

// MirrorBlob mirrors a federated blob to local storage.
func (s *federationService) MirrorBlob(ctx context.Context, hash string) error {
	// Get blob info
	blob, err := s.queries.GetFederatedBlob(ctx, hash)
	if err != nil {
		return fmt.Errorf("blob not found: %w", err)
	}

	// Get URLs
	urls, err := s.queries.GetFederatedBlobURLs(ctx, hash)
	if err != nil || len(urls) == 0 {
		return fmt.Errorf("no URLs for blob: %s", hash)
	}

	// Update status to mirroring
	_ = s.queries.UpdateFederatedBlobStatus(ctx, db.UpdateFederatedBlobStatusParams{
		Hash:   hash,
		Status: "mirroring",
	})

	// Try each URL
	var lastErr error
	for _, u := range urls {
		if err := s.downloadAndStore(ctx, hash, u.Url, blob.Size); err != nil {
			lastErr = err
			s.log.Warn("mirror attempt failed",
				zap.String("hash", hash),
				zap.String("url", u.Url),
				zap.Error(err))
			continue
		}

		// Success
		_ = s.queries.UpdateFederatedBlobStatus(ctx, db.UpdateFederatedBlobStatusParams{
			Hash:       hash,
			Status:     "mirrored",
			MirroredAt: sql.NullInt64{Int64: time.Now().Unix(), Valid: true},
		})

		s.log.Info("blob mirrored",
			zap.String("hash", hash),
			zap.String("from", u.Url))

		return nil
	}

	// All URLs failed
	_ = s.queries.UpdateFederatedBlobStatus(ctx, db.UpdateFederatedBlobStatusParams{
		Hash:   hash,
		Status: "failed",
	})

	return fmt.Errorf("all mirror attempts failed: %w", lastErr)
}

func (s *federationService) downloadAndStore(ctx context.Context, hash, url string, expectedSize int64) error {
	// Download
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	// Read body
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	// Verify hash
	h := sha256.Sum256(data)
	actualHash := hex.EncodeToString(h[:])
	if actualHash != hash {
		return fmt.Errorf("hash mismatch: expected %s, got %s", hash, actualHash)
	}

	// Store
	reader := bytes.NewReader(data)
	return s.storage.Put(ctx, hash, reader, int64(len(data)))
}

// MirrorBlobAsync queues a blob for async mirroring.
func (s *federationService) MirrorBlobAsync(ctx context.Context, hash string) error {
	select {
	case s.mirrorQueue <- hash:
		return nil
	default:
		return fmt.Errorf("mirror queue full")
	}
}

// ShouldAutoMirror returns true if a blob should be auto-mirrored.
func (s *federationService) ShouldAutoMirror(ctx context.Context, hash string) (bool, error) {
	if !s.config.AutoMirror {
		return false, nil
	}

	blob, err := s.queries.GetFederatedBlob(ctx, hash)
	if err != nil {
		return false, err
	}

	// Only mirror discovered blobs with enough references
	if blob.Status != "discovered" {
		return false, nil
	}

	return int(blob.RefCount) >= s.config.MirrorMinRefs, nil
}

// GetKnownServers returns discovered Blossom servers.
func (s *federationService) GetKnownServers(ctx context.Context, limit, offset int) ([]*core.KnownServer, error) {
	servers, err := s.queries.ListKnownServers(ctx, db.ListKnownServersParams{
		Limit:  int32(limit),
		Offset: int32(offset),
	})
	if err != nil {
		return nil, err
	}

	result := make([]*core.KnownServer, len(servers))
	for i, s := range servers {
		result[i] = dbServerToCore(s)
	}

	return result, nil
}

// GetHealthyServers returns only healthy servers.
func (s *federationService) GetHealthyServers(ctx context.Context) ([]*core.KnownServer, error) {
	servers, err := s.queries.ListHealthyServers(ctx)
	if err != nil {
		return nil, err
	}

	result := make([]*core.KnownServer, len(servers))
	for i, s := range servers {
		result[i] = dbServerToCore(s)
	}

	return result, nil
}

// CheckServerHealth verifies if a server is reachable.
func (s *federationService) CheckServerHealth(ctx context.Context, serverURL string) (bool, error) {
	req, err := http.NewRequestWithContext(ctx, "HEAD", serverURL, nil)
	if err != nil {
		return false, err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false, nil // Not healthy
	}
	defer resp.Body.Close()

	healthy := resp.StatusCode >= 200 && resp.StatusCode < 500

	_ = s.queries.UpdateServerHealth(ctx, db.UpdateServerHealthParams{
		Url:       serverURL,
		Healthy:   healthy,
		LastCheck: sql.NullInt64{Int64: time.Now().Unix(), Valid: true},
	})

	return healthy, nil
}

// GetFederatedUsers returns users who have this server in their server list.
func (s *federationService) GetFederatedUsers(ctx context.Context, limit, offset int) ([]*core.FederatedUser, error) {
	users, err := s.queries.ListFederatedUsers(ctx, db.ListFederatedUsersParams{
		Limit:  int32(limit),
		Offset: int32(offset),
	})
	if err != nil {
		return nil, err
	}

	result := make([]*core.FederatedUser, len(users))
	for i, u := range users {
		result[i] = &core.FederatedUser{
			Pubkey:     u.Pubkey,
			EventID:    u.EventID,
			ServerRank: int(u.ServerRank),
			CreatedAt:  u.CreatedAt,
			UpdatedAt:  u.UpdatedAt,
		}
	}

	return result, nil
}

// GetUserServerList returns the server list for a user.
func (s *federationService) GetUserServerList(ctx context.Context, pubkey string) ([]string, error) {
	// This would require querying relays for the user's kind 10063 event
	// For now, return empty
	return nil, nil
}

// GetEventHistory returns recent federation events.
func (s *federationService) GetEventHistory(ctx context.Context, direction string, limit, offset int) ([]*core.FederationEvent, error) {
	var events []db.FederationEvent
	var err error

	if direction == "" {
		events, err = s.queries.ListAllFederationEvents(ctx, db.ListAllFederationEventsParams{
			Limit:  int32(limit),
			Offset: int32(offset),
		})
	} else {
		events, err = s.queries.ListFederationEvents(ctx, db.ListFederationEventsParams{
			Direction: direction,
			Limit:     int32(limit),
			Offset:    int32(offset),
		})
	}

	if err != nil {
		return nil, err
	}

	result := make([]*core.FederationEvent, len(events))
	for i, e := range events {
		result[i] = dbEventToCore(e)
	}

	return result, nil
}

// GetEventByID returns a specific federation event.
func (s *federationService) GetEventByID(ctx context.Context, id string) (*core.FederationEvent, error) {
	event, err := s.queries.GetFederationEvent(ctx, id)
	if err != nil {
		return nil, err
	}

	return dbEventToCore(event), nil
}

// Start starts the federation workers and relay connections.
func (s *federationService) Start(ctx context.Context) error {
	if !s.config.Enabled {
		s.log.Info("federation service disabled")
		return nil
	}

	// Start publish workers
	mode := s.GetMode()
	if mode == core.FederationModePublish || mode == core.FederationModeBoth {
		for i := 0; i < s.config.WorkerCount; i++ {
			s.wg.Add(1)
			go s.publishWorker(ctx, i)
		}
	}

	// Start mirror workers
	if mode == core.FederationModeSubscribe || mode == core.FederationModeBoth {
		for i := 0; i < s.config.WorkerCount; i++ {
			s.wg.Add(1)
			go s.mirrorWorker(ctx, i)
		}

		// Start subscriptions
		if err := s.SubscribeToRelays(ctx); err != nil {
			s.log.Warn("failed to subscribe to relays", zap.Error(err))
		}
	}

	s.log.Info("federation service started",
		zap.String("mode", s.config.Mode),
		zap.Int("workers", s.config.WorkerCount),
		zap.Int("relays", len(s.config.RelayURLs)))

	return nil
}

func (s *federationService) publishWorker(ctx context.Context, id int) {
	defer s.wg.Done()

	s.log.Debug("publish worker started", zap.Int("worker_id", id))

	for {
		select {
		case <-ctx.Done():
			return
		case <-s.stopCh:
			return
		case job := <-s.publishQueue:
			if err := s.PublishBlob(ctx, job.blob); err != nil {
				s.log.Error("publish failed",
					zap.String("hash", job.blob.Sha256),
					zap.Error(err))

				// Update event status
				_ = s.queries.UpdateFederationEventStatus(ctx, db.UpdateFederationEventStatusParams{
					ID:     job.eventID,
					Status: "failed",
					Error:  sql.NullString{String: err.Error(), Valid: true},
				})

				// Retry if allowed
				if job.retries < s.config.RetryAttempts {
					job.retries++
					retryDelay, _ := time.ParseDuration(s.config.RetryDelay)
					time.Sleep(retryDelay)
					select {
					case s.publishQueue <- job:
					default:
					}
				}
			} else {
				// Update event status
				_ = s.queries.UpdateFederationEventStatus(ctx, db.UpdateFederationEventStatusParams{
					ID:          job.eventID,
					Status:      "published",
					PublishedAt: sql.NullInt64{Int64: time.Now().Unix(), Valid: true},
				})
			}
		}
	}
}

func (s *federationService) mirrorWorker(ctx context.Context, id int) {
	defer s.wg.Done()

	s.log.Debug("mirror worker started", zap.Int("worker_id", id))

	for {
		select {
		case <-ctx.Done():
			return
		case <-s.stopCh:
			return
		case hash := <-s.mirrorQueue:
			if err := s.MirrorBlob(ctx, hash); err != nil {
				s.log.Error("mirror failed",
					zap.String("hash", hash),
					zap.Error(err))
			}
		}
	}
}

// Stop gracefully stops federation.
func (s *federationService) Stop() error {
	close(s.stopCh)
	s.wg.Wait()

	// Close relay connections
	s.relayMu.Lock()
	for _, relay := range s.relayConns {
		relay.Close()
	}
	s.relayMu.Unlock()

	s.log.Info("federation service stopped")
	return nil
}

// Stats returns federation statistics.
func (s *federationService) Stats(ctx context.Context) (*core.FederationStats, error) {
	stats := &core.FederationStats{
		ServerPubkey: s.serverPk,
		Enabled:      s.config.Enabled,
		Mode:         s.GetMode(),
		RelayCount:   len(s.config.RelayURLs),
	}

	if !s.config.Enabled {
		return stats, nil
	}

	// Count events
	published, _ := s.queries.CountFederationEventsByStatus(ctx, db.CountFederationEventsByStatusParams{
		Direction: "publish",
		Status:    "published",
	})
	pending, _ := s.queries.CountFederationEventsByStatus(ctx, db.CountFederationEventsByStatusParams{
		Direction: "publish",
		Status:    "pending",
	})
	failed, _ := s.queries.CountFederationEventsByStatus(ctx, db.CountFederationEventsByStatusParams{
		Direction: "publish",
		Status:    "failed",
	})
	received, _ := s.queries.CountFederationEventsByStatus(ctx, db.CountFederationEventsByStatusParams{
		Direction: "receive",
		Status:    "received",
	})

	stats.EventsPublished = published
	stats.EventsPending = pending
	stats.EventsFailed = failed
	stats.EventsReceived = received

	// Count blobs
	discovered, _ := s.queries.CountFederatedBlobsByStatus(ctx, "discovered")
	mirrored, _ := s.queries.CountFederatedBlobsByStatus(ctx, "mirrored")
	mirroring, _ := s.queries.CountFederatedBlobsByStatus(ctx, "mirroring")

	stats.BlobsDiscovered = discovered
	stats.BlobsMirrored = mirrored
	stats.MirrorsPending = mirroring

	// Count servers
	totalServers, _ := s.queries.CountKnownServers(ctx)
	healthyServers, _ := s.queries.CountHealthyServers(ctx)

	stats.KnownServers = totalServers
	stats.HealthyServers = healthyServers

	// Count users
	users, _ := s.queries.CountFederatedUsers(ctx)
	stats.FederatedUsers = users

	// Worker status
	stats.WorkersActive = s.config.WorkerCount
	stats.QueueSize = len(s.publishQueue) + len(s.mirrorQueue)

	return stats, nil
}

// Helper functions

func (s *federationService) createFileMetadataEvent(blob *core.Blob) nostr.Event {
	// Build URL
	blobURL := fmt.Sprintf("%s/%s", s.cdnBaseURL, blob.Sha256)

	// Create tags
	tags := nostr.Tags{
		{"url", blobURL},
		{"x", blob.Sha256},
		{"m", blob.Type},
		{"size", fmt.Sprintf("%d", blob.Size)},
	}

	return nostr.Event{
		Kind:      KindFileMetadata,
		CreatedAt: nostr.Timestamp(time.Now().Unix()),
		Tags:      tags,
		Content:   "", // No content for file metadata
	}
}

func (s *federationService) getOrConnectRelay(ctx context.Context, url string) (*nostr.Relay, error) {
	s.relayMu.RLock()
	if relay, ok := s.relayConns[url]; ok {
		s.relayMu.RUnlock()
		return relay, nil
	}
	s.relayMu.RUnlock()

	s.relayMu.Lock()
	defer s.relayMu.Unlock()

	// Double-check after acquiring write lock
	if relay, ok := s.relayConns[url]; ok {
		return relay, nil
	}

	relay, err := nostr.RelayConnect(ctx, url)
	if err != nil {
		return nil, err
	}

	s.relayConns[url] = relay
	return relay, nil
}

func extractServerID(url string) string {
	// Extract base URL from full blob URL
	// e.g., "https://files.example.com/abc123" -> "https://files.example.com"
	for i := len("https://"); i < len(url); i++ {
		if url[i] == '/' {
			return url[:i]
		}
	}
	return url
}

func dbEventToCore(e db.FederationEvent) *core.FederationEvent {
	return &core.FederationEvent{
		ID:          e.ID,
		EventID:     nullStringToString(e.EventID),
		EventKind:   int(e.EventKind),
		Pubkey:      e.Pubkey,
		BlobHash:    nullStringToString(e.BlobHash),
		Direction:   e.Direction,
		Status:      core.FederationEventStatus(e.Status),
		Error:       nullStringToString(e.Error),
		RelayURL:    nullStringToString(e.RelayUrl),
		CreatedAt:   e.CreatedAt,
		PublishedAt: nullInt64ToInt64(e.PublishedAt),
		Retries:     int(e.Retries),
	}
}

func dbServerToCore(s db.KnownServer) *core.KnownServer {
	return &core.KnownServer{
		URL:       s.Url,
		Pubkey:    nullStringToString(s.Pubkey),
		UserCount: int(s.UserCount),
		BlobCount: int(s.BlobCount),
		Healthy:   s.Healthy,
		FirstSeen: s.FirstSeen,
		LastSeen:  s.LastSeen,
		LastCheck: nullInt64ToInt64(s.LastCheck),
	}
}

func nullStringToString(ns sql.NullString) string {
	if ns.Valid {
		return ns.String
	}
	return ""
}

func nullInt64ToInt64(ni sql.NullInt64) int64 {
	if ni.Valid {
		return ni.Int64
	}
	return 0
}

// Ensure interface compliance
var _ core.FederationService = (*federationService)(nil)
