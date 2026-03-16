package core

import (
	"context"
	"errors"
)

// Federation-related errors.
var (
	ErrFederationDisabled   = errors.New("federation is not enabled")
	ErrFederationNoRelays   = errors.New("no relays configured for federation")
	ErrFederationNoKey      = errors.New("server nsec not configured for federation")
	ErrServerNotFound       = errors.New("federated server not found")
	ErrFederatedBlobNotFound = errors.New("federated blob not found")
	ErrAlreadyFederated     = errors.New("blob is already federated")
)

// FederationMode defines the federation behavior.
type FederationMode string

const (
	FederationModePublish   FederationMode = "publish"   // Only publish blob events
	FederationModeSubscribe FederationMode = "subscribe" // Only subscribe to events
	FederationModeBoth      FederationMode = "both"      // Full federation
)

// FederationEventStatus represents the status of a federation event.
type FederationEventStatus string

const (
	FederationEventPending   FederationEventStatus = "pending"
	FederationEventPublished FederationEventStatus = "published"
	FederationEventFailed    FederationEventStatus = "failed"
	FederationEventReceived  FederationEventStatus = "received"
)

// FederatedBlobStatus represents the status of a federated blob.
type FederatedBlobStatus string

const (
	FederatedBlobDiscovered FederatedBlobStatus = "discovered" // Seen but not mirrored
	FederatedBlobMirroring  FederatedBlobStatus = "mirroring"  // Currently being mirrored
	FederatedBlobMirrored   FederatedBlobStatus = "mirrored"   // Successfully mirrored locally
	FederatedBlobFailed     FederatedBlobStatus = "failed"     // Mirror failed
)

// FederatedBlob represents a blob discovered via federation.
type FederatedBlob struct {
	Hash        string              `json:"hash"`
	Size        int64               `json:"size"`
	MimeType    string              `json:"mime_type"`
	URLs        []string            `json:"urls"` // Remote URLs where blob is available
	RefCount    int                 `json:"ref_count"` // Number of kind 1063 events referencing this blob
	Status      FederatedBlobStatus `json:"status"`
	DiscoveredAt int64              `json:"discovered_at"`
	MirroredAt  int64               `json:"mirrored_at,omitempty"`
	LastSeenAt  int64               `json:"last_seen_at"`
}

// FederatedBlobURL represents a remote URL for a federated blob.
type FederatedBlobURL struct {
	BlobHash  string `json:"blob_hash"`
	URL       string `json:"url"`
	ServerID  string `json:"server_id"` // Server pubkey or identifier
	Priority  int    `json:"priority"`  // Lower = higher priority
	Healthy   bool   `json:"healthy"`   // Whether this URL is reachable
	LastCheck int64  `json:"last_check"`
}

// KnownServer represents a Blossom server discovered via kind 10063 events.
type KnownServer struct {
	URL         string `json:"url"`
	Pubkey      string `json:"pubkey,omitempty"` // Server's Nostr pubkey if known
	UserCount   int    `json:"user_count"`       // Users with this server in kind 10063
	BlobCount   int    `json:"blob_count"`       // Blobs discovered from this server
	Healthy     bool   `json:"healthy"`
	FirstSeen   int64  `json:"first_seen"`
	LastSeen    int64  `json:"last_seen"`
	LastCheck   int64  `json:"last_check"`
}

// FederationEvent represents a published or received federation event.
type FederationEvent struct {
	ID          string                `json:"id"`
	EventID     string                `json:"event_id"` // Nostr event ID
	EventKind   int                   `json:"event_kind"` // 1063 or 10063
	Pubkey      string                `json:"pubkey"`     // Event author
	BlobHash    string                `json:"blob_hash,omitempty"` // For kind 1063
	Direction   string                `json:"direction"`  // "publish" or "receive"
	Status      FederationEventStatus `json:"status"`
	Error       string                `json:"error,omitempty"`
	RelayURL    string                `json:"relay_url"`
	CreatedAt   int64                 `json:"created_at"`
	PublishedAt int64                 `json:"published_at,omitempty"`
	Retries     int                   `json:"retries"`
}

// FederatedUser represents a user who has this server in their kind 10063 server list.
type FederatedUser struct {
	Pubkey      string `json:"pubkey"`
	EventID     string `json:"event_id"` // kind 10063 event ID
	ServerRank  int    `json:"server_rank"` // Position in user's server list (0 = primary)
	CreatedAt   int64  `json:"created_at"`
	UpdatedAt   int64  `json:"updated_at"`
}

// FederationStats contains statistics about federation activity.
type FederationStats struct {
	// Server identity
	ServerPubkey string `json:"server_pubkey"`

	// General stats
	Enabled     bool           `json:"enabled"`
	Mode        FederationMode `json:"mode"`
	RelayCount  int            `json:"relay_count"`

	// Publishing stats
	EventsPublished   int64 `json:"events_published"`
	EventsPending     int64 `json:"events_pending"`
	EventsFailed      int64 `json:"events_failed"`

	// Subscription stats
	EventsReceived    int64 `json:"events_received"`
	BlobsDiscovered   int64 `json:"blobs_discovered"`
	BlobsMirrored     int64 `json:"blobs_mirrored"`
	MirrorsPending    int64 `json:"mirrors_pending"`

	// Server discovery stats
	KnownServers      int64 `json:"known_servers"`
	HealthyServers    int64 `json:"healthy_servers"`
	FederatedUsers    int64 `json:"federated_users"`

	// Recent activity
	LastPublished     int64 `json:"last_published,omitempty"`
	LastReceived      int64 `json:"last_received,omitempty"`
	LastMirror        int64 `json:"last_mirror,omitempty"`

	// Worker status
	WorkersActive     int   `json:"workers_active"`
	QueueSize         int   `json:"queue_size"`
}

// FederationService handles Nostr-based cross-server federation.
type FederationService interface {
	// IsEnabled returns true if federation is enabled.
	IsEnabled() bool

	// GetMode returns the current federation mode.
	GetMode() FederationMode

	// GetServerPubkey returns this server's Nostr pubkey.
	GetServerPubkey() string

	// Publishing (kind 1063)

	// PublishBlob publishes a kind 1063 event for a blob.
	// This is called after a successful upload.
	PublishBlob(ctx context.Context, blob *Blob) error

	// PublishBlobAsync queues a blob for async publishing.
	PublishBlobAsync(ctx context.Context, blob *Blob) error

	// RepublishBlob forces republishing of a blob's kind 1063 event.
	RepublishBlob(ctx context.Context, hash string) error

	// GetPendingPublishes returns blobs waiting to be published.
	GetPendingPublishes(ctx context.Context, limit int) ([]*FederationEvent, error)

	// Subscription (kind 1063/10063)

	// SubscribeToRelays connects to configured relays and subscribes to events.
	// This is called at startup and maintains persistent connections.
	SubscribeToRelays(ctx context.Context) error

	// ProcessEvent handles an incoming Nostr event.
	ProcessEvent(ctx context.Context, eventID string, eventKind int, pubkey string, content []byte) error

	// Federated blob discovery

	// GetFederatedBlob returns info about a federated blob.
	GetFederatedBlob(ctx context.Context, hash string) (*FederatedBlob, error)

	// GetFallbackURLs returns remote URLs for a blob not stored locally.
	// Used to return X-Fallback-URLs header for clients.
	GetFallbackURLs(ctx context.Context, hash string) ([]string, error)

	// ListFederatedBlobs returns discovered federated blobs.
	ListFederatedBlobs(ctx context.Context, status FederatedBlobStatus, limit, offset int) ([]*FederatedBlob, error)

	// Mirroring

	// MirrorBlob mirrors a federated blob to local storage.
	MirrorBlob(ctx context.Context, hash string) error

	// MirrorBlobAsync queues a blob for async mirroring.
	MirrorBlobAsync(ctx context.Context, hash string) error

	// ShouldAutoMirror returns true if a blob should be auto-mirrored.
	// Based on reference count and configuration.
	ShouldAutoMirror(ctx context.Context, hash string) (bool, error)

	// Server discovery (kind 10063)

	// GetKnownServers returns discovered Blossom servers.
	GetKnownServers(ctx context.Context, limit, offset int) ([]*KnownServer, error)

	// GetHealthyServers returns only healthy servers.
	GetHealthyServers(ctx context.Context) ([]*KnownServer, error)

	// CheckServerHealth verifies if a server is reachable.
	CheckServerHealth(ctx context.Context, serverURL string) (bool, error)

	// User tracking

	// GetFederatedUsers returns users who have this server in their server list.
	GetFederatedUsers(ctx context.Context, limit, offset int) ([]*FederatedUser, error)

	// GetUserServerList returns the server list for a user (from kind 10063).
	GetUserServerList(ctx context.Context, pubkey string) ([]string, error)

	// Event history

	// GetEventHistory returns recent federation events.
	GetEventHistory(ctx context.Context, direction string, limit, offset int) ([]*FederationEvent, error)

	// GetEventByID returns a specific federation event.
	GetEventByID(ctx context.Context, id string) (*FederationEvent, error)

	// Lifecycle

	// Start starts the federation workers and relay connections.
	Start(ctx context.Context) error

	// Stop gracefully stops federation.
	Stop() error

	// Stats returns federation statistics.
	Stats(ctx context.Context) (*FederationStats, error)
}
