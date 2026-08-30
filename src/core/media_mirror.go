package core

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// MirrorStatus is the outcome of trying to mirror one remote URL.
//
// These three are kept distinct all the way to the HTTP response because the
// client has to be able to tell them apart. An emoji that we refused to mirror
// and an emoji whose host is down are different situations: the first will
// never work and should be shown as rejected, the second is transient and may
// work on reload. Collapsing them into one "broken image" teaches the next
// person debugging it nothing at all.
type MirrorStatus string

const (
	// MirrorStatusOK means the blob is mirrored and servable.
	MirrorStatusOK MirrorStatus = "ok"
	// MirrorStatusRefused means we reached a decision about the content and
	// said no — too large, wrong type, too many pixels, a denied address.
	// Permanent: retrying without the content changing will refuse again.
	MirrorStatusRefused MirrorStatus = "refused"
	// MirrorStatusUnreachable means we never got a usable answer from the
	// remote host — DNS failure, timeout, connection refused, an HTTP error
	// status. Transient: worth retrying later.
	MirrorStatusUnreachable MirrorStatus = "unreachable"
)

// Machine-readable reasons, carried in the error body so a client can act on
// the specific cause without parsing prose.
const (
	MirrorReasonTooLarge         = "too_large"
	MirrorReasonTypeNotAllowed   = "type_not_allowed"
	MirrorReasonTooManyPixels    = "too_many_pixels"
	MirrorReasonNotAnImage       = "not_an_image"
	MirrorReasonBlockedAddress   = "blocked_address"
	MirrorReasonInvalidURL       = "invalid_url"
	MirrorReasonTooManyRedirects = "too_many_redirects"
	MirrorReasonHTTPStatus       = "http_status"
	MirrorReasonTransport        = "transport"
	MirrorReasonStorage          = "storage"
)

// MirrorError carries both halves of a failure: the class (which decides the
// HTTP status) and the specific reason (which the client can branch on).
type MirrorError struct {
	Status MirrorStatus
	Reason string
	// Detail is for server-side logs and admin inspection. It is NOT returned
	// to unauthenticated callers: it can contain the remote host's response,
	// and echoing that back turns the mirror into a probe for whoever holds a
	// signed link.
	Detail string
}

func (e *MirrorError) Error() string {
	if e.Detail == "" {
		return fmt.Sprintf("%s: %s", e.Status, e.Reason)
	}
	return fmt.Sprintf("%s: %s: %s", e.Status, e.Reason, e.Detail)
}

// NewMirrorError builds a MirrorError.
func NewMirrorError(status MirrorStatus, reason, detail string) *MirrorError {
	return &MirrorError{Status: status, Reason: reason, Detail: detail}
}

// AsMirrorError extracts a *MirrorError from err, if there is one.
func AsMirrorError(err error) (*MirrorError, bool) {
	var me *MirrorError
	if errors.As(err, &me) {
		return me, true
	}
	return nil, false
}

// ErrMirrorDisabled is returned when the mirror is asked to do work while
// disabled in config.
var ErrMirrorDisabled = errors.New("media mirror is not enabled")

// MirroredMedia is a mirrored remote image, ready to serve.
type MirroredMedia struct {
	// Sha256 is the content hash. The mirrored bytes are a normal blob, so
	// this hash also addresses it at GET /<sha256> — the mirror is a way of
	// ACQUIRING a blob, not a separate store.
	Sha256 string
	Size   int64
	Mime   string
	Data   []byte
	// FromCache distinguishes a hit from a first-sight fetch. Surfaced as a
	// response header for debugging; it says nothing about who asked.
	FromCache bool
}

// SignedMirrorURL is one entry of a signing response.
type SignedMirrorURL struct {
	// Source is the canonicalized remote URL that was signed.
	Source string `json:"source"`
	// URL is the mirror link to put in an <img src>.
	URL string `json:"url"`
	// ExpiresAt is the Unix time the signature stops being valid, or 0 if it
	// does not expire.
	ExpiresAt int64 `json:"expires_at,omitempty"`
}

// RejectedMirrorURL is a URL the signer would not sign, with the reason.
// Returned alongside the successes so a partially-bad batch still yields the
// good half instead of failing whole.
type RejectedMirrorURL struct {
	Source string `json:"source"`
	Reason string `json:"reason"`
}

// MirrorEvictResult reports what an eviction pass did.
type MirrorEvictResult struct {
	// BytesBefore is the cache size at the start of the pass.
	BytesBefore int64 `json:"bytes_before"`
	// BytesAfter is the cache size once eviction finished.
	BytesAfter int64 `json:"bytes_after"`
	// Evicted is how many mirrored objects were removed.
	Evicted int64 `json:"evicted"`
}

// MediaMirrorConfig configures the remote-media mirror.
//
// Defaults are sized for custom emoji on a small self-hosted server, per the
// project's potato-grade rule: a Raspberry Pi must be able to run this without
// filling its card. Every cap is configurable upward for bigger deployments.
type MediaMirrorConfig struct {
	// Enabled turns the mirror on. Off by default: it makes outbound requests
	// on behalf of callers, which is not something to auto-arm on an upgrade.
	Enabled bool `yaml:"enabled"`

	// SigningKey is the HMAC key for mirror links. Required when Enabled —
	// without it the mirror would be an open proxy, so startup fails rather
	// than running unsigned. MUST NOT be shipped to a browser; see the
	// urlsign package.
	SigningKey string `yaml:"signing_key"`

	// SignatureTTL is how long a minted link stays valid. Zero (the default)
	// means links do not expire, which suits immutable content; rotate
	// SigningKey to revoke.
	SignatureTTL time.Duration `yaml:"signature_ttl"`

	// MaxObjectBytes is the largest single remote object accepted.
	MaxObjectBytes int64 `yaml:"max_object_bytes"`

	// MaxPixels caps width*height. This is the decompression-bomb guard: a
	// 40KB PNG can declare 50000x50000 and cost gigabytes to decode, so the
	// dimensions are read from the image header and checked BEFORE any decode.
	MaxPixels int64 `yaml:"max_pixels"`

	// MaxDimension caps width and height individually, catching the long-thin
	// images that slip under a pixel-count cap.
	MaxDimension int `yaml:"max_dimension"`

	// AllowedMimeTypes are the content types that may be mirrored, matched
	// against the type detected from the bytes — never the Content-Type the
	// remote host claims.
	AllowedMimeTypes []string `yaml:"allowed_mime_types"`

	// FetchTimeout bounds one remote fetch end to end.
	FetchTimeout time.Duration `yaml:"fetch_timeout"`

	// MaxRedirects bounds the redirect chain per fetch.
	MaxRedirects int `yaml:"max_redirects"`

	// MaxSignBatch caps how many URLs one signing request may submit.
	MaxSignBatch int `yaml:"max_sign_batch"`

	// CacheMaxBytes is the total size the mirror may occupy. When exceeded,
	// least-recently-accessed entries are evicted. Zero means unbounded, which
	// is not recommended on a small server.
	CacheMaxBytes int64 `yaml:"cache_max_bytes"`

	// CacheLowWaterPercent is how far below CacheMaxBytes an eviction pass
	// drains. Evicting down to exactly the cap would re-trigger on the next
	// insert and evict one entry per request forever.
	CacheLowWaterPercent int `yaml:"cache_low_water_percent"`

	// EvictInterval is how often the eviction worker runs.
	EvictInterval time.Duration `yaml:"evict_interval"`

	// NegativeTTL is how long an "unreachable" result is remembered before the
	// remote host is tried again. Without it, a dead host is re-fetched on
	// every single view of a page that references it.
	NegativeTTL time.Duration `yaml:"negative_ttl"`

	// RefusedTTL is how long a "refused" result is remembered. Longer than
	// NegativeTTL because the verdict depends on the content and our policy,
	// neither of which usually changes; non-zero so a policy change eventually
	// takes effect without a manual purge.
	RefusedTTL time.Duration `yaml:"refused_ttl"`

	// AllowPrivateAddresses disables SSRF protection. Only for a self-hoster
	// deliberately mirroring from their own LAN. Dangerous on a public server.
	AllowPrivateAddresses bool `yaml:"allow_private_addresses"`

	// ExtraDeniedCIDRs are additional ranges to refuse.
	ExtraDeniedCIDRs []string `yaml:"extra_denied_cidrs"`

	// OwnerPubkey owns mirrored blobs in the blob table. Mirrored content
	// belongs to the server, not to whoever first caused it to be fetched —
	// attributing it to a user would both misreport their quota and record
	// that they viewed it. A blob with no owner at all would be swept by the
	// GC worker, so it needs one; this is that one.
	OwnerPubkey string `yaml:"owner_pubkey"`
}

// DefaultMediaMirrorOwnerPubkey is the synthetic owner for mirrored blobs when
// none is configured. It is not a real key and no one holds a private key for
// it: it is a 32-byte marker chosen so it can never collide with a real pubkey
// and is obvious in the database.
const DefaultMediaMirrorOwnerPubkey = "0000000000000000000000000000000000000000000000000000006d6972726f72"

// DefaultMediaMirrorConfig returns the defaults applied when a config file
// omits the section or leaves fields zero.
func DefaultMediaMirrorConfig() MediaMirrorConfig {
	return MediaMirrorConfig{
		Enabled:        false,
		SignatureTTL:   0,
		MaxObjectBytes: 1 << 20, // 1 MiB — an emoji that needs more is not an emoji
		MaxPixels:      4 << 20, // 4 Mpx, e.g. 2048x2048
		MaxDimension:   1024,
		AllowedMimeTypes: []string{
			"image/png",
			"image/jpeg",
			"image/gif",
			"image/webp",
		},
		FetchTimeout:         10 * time.Second,
		MaxRedirects:         3,
		MaxSignBatch:         256,
		CacheMaxBytes:        1 << 30, // 1 GiB
		CacheLowWaterPercent: 90,
		EvictInterval:        10 * time.Minute,
		NegativeTTL:          15 * time.Minute,
		RefusedTTL:           24 * time.Hour,
		OwnerPubkey:          DefaultMediaMirrorOwnerPubkey,
	}
}

// MediaMirrorService mirrors remote media so clients never contact third-party
// hosts directly.
//
// PRIVACY CONTRACT, which the implementation is required to keep: no method
// here accepts a caller identity, and none records one. The mirror knows which
// URLs it has fetched — it must, to cache them — and deliberately does not know
// who asked for them. Adding a pubkey or an IP to any of these signatures would
// move the tracking in-house rather than remove it, which is worse than the
// leak this feature exists to close.
type MediaMirrorService interface {
	// IsEnabled reports whether mirroring is configured and on.
	IsEnabled() bool

	// SignURLs mints mirror links for remote URLs. Returns the signed links
	// and, separately, the inputs it would not sign and why.
	SignURLs(ctx context.Context, rawURLs []string) ([]SignedMirrorURL, []RejectedMirrorURL, error)

	// VerifyAndGet checks a signature and returns the mirrored bytes, fetching
	// them on first sight. Errors are *MirrorError so the caller can map the
	// status to a response code.
	VerifyAndGet(ctx context.Context, encodedURL string, expiresAt int64, signature string) (*MirroredMedia, error)

	// Evict drops least-recently-accessed entries until the cache is under its
	// low-water mark. Safe to call concurrently; passes serialize.
	Evict(ctx context.Context) (*MirrorEvictResult, error)

	// StartWorker starts the background eviction worker. No-op when disabled.
	StartWorker(ctx context.Context)

	// StopWorker stops the background eviction worker.
	StopWorker()
}
