package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	// Registers the image formats we are willing to mirror, so
	// image.DecodeConfig can read their headers. DecodeConfig reads
	// DIMENSIONS ONLY -- it never allocates the pixel buffer -- which is what
	// makes it safe to run on untrusted bytes and is the whole basis of the
	// decompression-bomb check below.
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"

	_ "golang.org/x/image/webp"

	"github.com/gabriel-vasile/mimetype"
	"go.uber.org/zap"

	"git.aegis-hq.xyz/coldforge/cloistr-blossom/db"
	"git.aegis-hq.xyz/coldforge/cloistr-blossom/internal/metrics"
	"git.aegis-hq.xyz/coldforge/cloistr-blossom/internal/safefetch"
	"git.aegis-hq.xyz/coldforge/cloistr-blossom/src/core"
	"git.aegis-hq.xyz/coldforge/cloistr-blossom/src/pkg/urlsign"
)

// lruGranularity is how coarsely accessed_at is recorded.
//
// Two reasons, and the privacy one is the reason it is not merely an
// optimization. Practically: a popular emoji served a thousand times an hour
// would otherwise cause a thousand database writes an hour on a machine that
// may be running off an SD card. Substantively: an exact access timestamp on a
// specific image is a viewing record. Rounded to the hour it is a popularity
// signal good enough to drive LRU and useless for reconstructing who looked at
// what and when.
const lruGranularity = time.Hour

// evictScanBatch bounds how many candidates one eviction pass examines.
const evictScanBatch = 512

type mediaMirrorService struct {
	queries *db.Queries
	blobs   core.BlobStorage
	config  core.MediaMirrorConfig
	signer  *urlsign.Signer
	client  *http.Client
	log     *zap.Logger

	// inflight collapses concurrent first-sight fetches of the same URL.
	// Opening a picker fires one request per emoji at once, and several
	// viewers may open the same set simultaneously; without this, N viewers
	// produce N outbound fetches of the same image -- exactly the load we
	// promised the remote host we would not generate.
	inflightMu sync.Mutex
	inflight   map[string]*inflightFetch

	// evictMu serializes eviction passes. Two concurrent passes would read the
	// same LRU candidates and race to delete them, double-counting the freed
	// bytes and each concluding it had not freed enough.
	evictMu sync.Mutex

	stopCh   chan struct{}
	stopOnce sync.Once
}

type inflightFetch struct {
	done   chan struct{}
	result *core.MirroredMedia
	err    error
}

// NewMediaMirrorService builds the mirror.
//
// It returns an error rather than a disabled service when the configuration is
// unusable (no signing key, bad CIDR). A mirror that silently starts without a
// signature check is an open proxy, so this is one of the places where failing
// to boot is the correct outcome.
func NewMediaMirrorService(
	queries *db.Queries,
	blobs core.BlobStorage,
	config core.MediaMirrorConfig,
	log *zap.Logger,
) (core.MediaMirrorService, error) {
	s := &mediaMirrorService{
		queries:  queries,
		blobs:    blobs,
		config:   config,
		log:      log,
		inflight: make(map[string]*inflightFetch),
		stopCh:   make(chan struct{}),
	}

	if !config.Enabled {
		return s, nil
	}

	signer, err := urlsign.NewSigner(config.SigningKey, config.SignatureTTL)
	if err != nil {
		return nil, fmt.Errorf("media mirror signing key: %w", err)
	}
	s.signer = signer

	client, err := safefetch.NewClient(safefetch.Policy{
		AllowPrivateAddresses: config.AllowPrivateAddresses,
		ExtraDeniedCIDRs:      config.ExtraDeniedCIDRs,
		MaxRedirects:          config.MaxRedirects,
		Timeout:               config.FetchTimeout,
	})
	if err != nil {
		return nil, fmt.Errorf("media mirror fetch client: %w", err)
	}
	s.client = client

	if config.AllowPrivateAddresses {
		log.Warn("media mirror: allow_private_addresses is ON — the mirror can be " +
			"pointed at internal services and cloud metadata endpoints by anyone " +
			"holding a signed link. Only correct on an isolated deployment.")
	}

	return s, nil
}

func (s *mediaMirrorService) IsEnabled() bool { return s.config.Enabled && s.signer != nil }

// urlHash is the primary key for a cached entry: the hash of the canonical
// URL, not the URL itself, so the key is fixed-width.
func urlHash(canonicalURL string) string {
	sum := sha256.Sum256([]byte(canonicalURL))
	return hex.EncodeToString(sum[:])
}

// SignURLs mints mirror links.
//
// A bad URL in the batch rejects that URL, not the batch. A picker resolving
// thirty emoji from sets published by strangers will routinely contain one
// broken entry, and failing the whole request over it would leave the user
// with no images at all -- reintroducing the problem this feature exists to
// solve.
func (s *mediaMirrorService) SignURLs(
	ctx context.Context,
	rawURLs []string,
) ([]core.SignedMirrorURL, []core.RejectedMirrorURL, error) {
	if !s.IsEnabled() {
		return nil, nil, core.ErrMirrorDisabled
	}
	if len(rawURLs) > s.config.MaxSignBatch {
		return nil, nil, core.NewMirrorError(
			core.MirrorStatusRefused,
			core.MirrorReasonTooLarge,
			fmt.Sprintf("batch of %d exceeds max_sign_batch %d", len(rawURLs), s.config.MaxSignBatch),
		)
	}

	signed := make([]core.SignedMirrorURL, 0, len(rawURLs))
	rejected := make([]core.RejectedMirrorURL, 0)
	seen := make(map[string]struct{}, len(rawURLs))

	for _, raw := range rawURLs {
		canonical, err := safefetch.CanonicalURL(raw)
		if err != nil {
			rejected = append(rejected, core.RejectedMirrorURL{
				Source: raw,
				Reason: core.MirrorReasonInvalidURL,
			})
			continue
		}
		// Distinct inputs can canonicalize to one URL (differing case, an
		// explicit default port). Emitting it twice would make the caller
		// render the same image twice.
		if _, dup := seen[canonical]; dup {
			continue
		}
		seen[canonical] = struct{}{}

		expiresAt, sig := s.signer.Sign(canonical)
		signed = append(signed, core.SignedMirrorURL{
			Source:    canonical,
			URL:       MirrorPath(canonical, expiresAt, sig),
			ExpiresAt: expiresAt,
		})
	}

	return signed, rejected, nil
}

// MirrorPath builds the path-and-query half of a mirror link. The caller
// prefixes the origin, so the same signature works from any hostname the
// server answers on.
func MirrorPath(canonicalURL string, expiresAt int64, signature string) string {
	var b strings.Builder
	b.WriteString(MirrorRoutePath)
	b.WriteString("?u=")
	b.WriteString(urlsign.EncodeURL(canonicalURL))
	if expiresAt != 0 {
		b.WriteString("&e=")
		b.WriteString(fmt.Sprintf("%d", expiresAt))
	}
	b.WriteString("&s=")
	b.WriteString(signature)
	return b.String()
}

// MirrorRoutePath is the route the mirror serves on. Declared here rather than
// only in the router so link minting and route registration cannot drift.
const MirrorRoutePath = "/media/mirror"

// VerifyAndGet is the read path: check the signature, then serve from cache or
// fetch on first sight.
func (s *mediaMirrorService) VerifyAndGet(
	ctx context.Context,
	encodedURL string,
	expiresAt int64,
	signature string,
) (*core.MirroredMedia, error) {
	if !s.IsEnabled() {
		return nil, core.ErrMirrorDisabled
	}

	canonical, err := urlsign.DecodeURL(encodedURL)
	if err != nil {
		return nil, core.NewMirrorError(core.MirrorStatusRefused, core.MirrorReasonInvalidURL, err.Error())
	}

	// Signature first, before anything that touches the network or the
	// database. An unsigned caller must not be able to make us do work.
	if err := s.signer.Verify(canonical, expiresAt, signature); err != nil {
		metrics.ErrorsTotal.WithLabelValues("mirror_bad_signature").Inc()
		return nil, err
	}

	// Defence in depth: a signature proves our key signed these bytes, not
	// that the bytes are a fetchable http(s) URL. If a future caller of Sign
	// forgets to validate, this stops the mirror from trying to dial it.
	if err := safefetch.PreCheckURL(canonical); err != nil {
		return nil, core.NewMirrorError(core.MirrorStatusRefused, core.MirrorReasonInvalidURL, err.Error())
	}

	key := urlHash(canonical)

	if media, err, handled := s.fromCache(ctx, key); handled {
		return media, err
	}

	return s.fetchOnce(ctx, key, canonical)
}

// fromCache answers from the mirrored_media table when it can. The third
// return value reports whether the cache had a verdict at all; a false there
// means "go fetch", which is different from a cached failure.
func (s *mediaMirrorService) fromCache(ctx context.Context, key string) (*core.MirroredMedia, error, bool) {
	row, err := s.queries.GetMirroredMedia(ctx, key)
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			// A cache lookup failure is not a reason to refuse the request;
			// fall through to a fetch. Logged without the URL.
			s.log.Warn("media mirror: cache lookup failed", zap.Error(err))
		}
		return nil, nil, false
	}

	switch core.MirrorStatus(row.Status) {
	case core.MirrorStatusOK:
		if !row.Sha256.Valid {
			return nil, nil, false
		}
		blob, err := s.blobs.GetFromHash(ctx, row.Sha256.String)
		if err != nil {
			// The row says mirrored but the blob is gone -- evicted, or GC'd
			// after its last reference went away. Re-fetch rather than serving
			// a 404 for something we claim to have.
			s.log.Info("media mirror: cached entry lost its blob, refetching")
			return nil, nil, false
		}
		s.touch(ctx, key, row.AccessedAt)
		metrics.MirrorRequests.WithLabelValues("hit").Inc()
		return &core.MirroredMedia{
			Sha256:    row.Sha256.String,
			Size:      row.Size,
			Mime:      row.Mime.String,
			Data:      blob.Blob,
			FromCache: true,
		}, nil, true

	case core.MirrorStatusRefused:
		if s.negativeExpired(row.FetchedAt, s.config.RefusedTTL) {
			return nil, nil, false
		}
		metrics.MirrorRequests.WithLabelValues("refused_cached").Inc()
		return nil, core.NewMirrorError(core.MirrorStatusRefused, row.Reason.String, ""), true

	case core.MirrorStatusUnreachable:
		if s.negativeExpired(row.FetchedAt, s.config.NegativeTTL) {
			return nil, nil, false
		}
		metrics.MirrorRequests.WithLabelValues("unreachable_cached").Inc()
		return nil, core.NewMirrorError(core.MirrorStatusUnreachable, row.Reason.String, ""), true
	}

	return nil, nil, false
}

// negativeExpired reports whether a cached failure is old enough to retry. A
// zero TTL means never retry automatically.
func (s *mediaMirrorService) negativeExpired(fetchedAt int64, ttl time.Duration) bool {
	if ttl <= 0 {
		return false
	}
	return time.Now().After(time.Unix(fetchedAt, 0).Add(ttl))
}

// touch refreshes the LRU stamp, rounded to lruGranularity, and only when the
// rounded value actually moved.
func (s *mediaMirrorService) touch(ctx context.Context, key string, currentAccessedAt int64) {
	now := time.Now().Truncate(lruGranularity).Unix()
	if now <= currentAccessedAt {
		return
	}
	if err := s.queries.TouchMirroredMedia(ctx, db.TouchMirroredMediaParams{
		UrlHash:    key,
		AccessedAt: now,
	}); err != nil {
		// Losing an LRU update costs a slightly wrong eviction order later. It
		// is never worth failing a request the user can otherwise be served.
		s.log.Debug("media mirror: touch failed", zap.Error(err))
	}
}

// fetchOnce collapses concurrent fetches of the same URL into one.
func (s *mediaMirrorService) fetchOnce(ctx context.Context, key, canonical string) (*core.MirroredMedia, error) {
	s.inflightMu.Lock()
	if existing, ok := s.inflight[key]; ok {
		s.inflightMu.Unlock()
		select {
		case <-existing.done:
			return existing.result, existing.err
		case <-ctx.Done():
			// This caller gave up; the fetch continues for whoever is left.
			return nil, ctx.Err()
		}
	}
	f := &inflightFetch{done: make(chan struct{})}
	s.inflight[key] = f
	s.inflightMu.Unlock()

	// The fetch deliberately does NOT inherit the caller's context deadline
	// beyond the configured timeout: the result is shared with every other
	// waiter, so one caller disconnecting must not cancel the work the others
	// are waiting on. Cancellation of the whole server still propagates via
	// the fetch timeout.
	fetchCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), s.config.FetchTimeout)
	defer cancel()

	f.result, f.err = s.fetchAndStore(fetchCtx, key, canonical)
	close(f.done)

	s.inflightMu.Lock()
	delete(s.inflight, key)
	s.inflightMu.Unlock()

	return f.result, f.err
}

// fetchAndStore performs the outbound request, validates the content, stores
// it, and records the verdict either way.
func (s *mediaMirrorService) fetchAndStore(ctx context.Context, key, canonical string) (*core.MirroredMedia, error) {
	media, mirrorErr := s.doFetch(ctx, canonical)

	now := time.Now()
	record := db.UpsertMirroredMediaParams{
		UrlHash:    key,
		SourceUrl:  canonical,
		FetchedAt:  now.Unix(),
		AccessedAt: now.Truncate(lruGranularity).Unix(),
	}

	if mirrorErr != nil {
		me, ok := core.AsMirrorError(mirrorErr)
		if !ok {
			me = core.NewMirrorError(core.MirrorStatusUnreachable, core.MirrorReasonTransport, mirrorErr.Error())
		}
		record.Status = string(me.Status)
		record.Reason = sql.NullString{String: me.Reason, Valid: me.Reason != ""}
		if err := s.queries.UpsertMirroredMedia(ctx, record); err != nil {
			s.log.Warn("media mirror: could not record failure", zap.Error(err))
		}
		metrics.MirrorRequests.WithLabelValues(string(me.Status)).Inc()
		// The detail is logged here and not returned to the caller: it can
		// contain the remote host's response, and echoing that to anyone with
		// a signed link turns the mirror into a network probe.
		s.log.Info("media mirror: fetch failed",
			zap.String("status", string(me.Status)),
			zap.String("reason", me.Reason),
			zap.String("detail", me.Detail))
		return nil, core.NewMirrorError(me.Status, me.Reason, "")
	}

	record.Status = string(core.MirrorStatusOK)
	record.Sha256 = sql.NullString{String: media.Sha256, Valid: true}
	record.Size = media.Size
	record.Mime = sql.NullString{String: media.Mime, Valid: true}
	if err := s.queries.UpsertMirroredMedia(ctx, record); err != nil {
		// The bytes are stored but the index entry is not. Serve this request
		// -- the content is good -- and let the next one re-fetch.
		s.log.Warn("media mirror: could not record success", zap.Error(err))
	}

	metrics.MirrorRequests.WithLabelValues("miss").Inc()
	metrics.MirrorBytes.Add(float64(media.Size))
	return media, nil
}

// doFetch is the outbound request and every content check, in the order that
// lets each one fail as cheaply as possible.
func (s *mediaMirrorService) doFetch(ctx context.Context, canonical string) (*core.MirroredMedia, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, canonical, http.NoBody)
	if err != nil {
		return nil, core.NewMirrorError(core.MirrorStatusRefused, core.MirrorReasonInvalidURL, err.Error())
	}
	// Identify ourselves honestly. A remote host that wants to block the
	// mirror should be able to, and a host debugging its own traffic should
	// be able to tell what we are.
	req.Header.Set("User-Agent", "cloistr-blossom-media-mirror/1 (+https://blossom.cloistr.xyz)")
	req.Header.Set("Accept", strings.Join(s.config.AllowedMimeTypes, ", "))
	// No Referer, no cookies, no forwarded client headers. The remote host
	// learns that this server wanted the image and nothing whatsoever about
	// the person who will see it -- which is the entire point.

	res, err := s.client.Do(req)
	if err != nil {
		switch {
		case safefetch.IsBlocked(err):
			// Not "unreachable": we reached a decision and refused. Recording
			// it as unreachable would retry an SSRF attempt every 15 minutes.
			return nil, core.NewMirrorError(core.MirrorStatusRefused, core.MirrorReasonBlockedAddress, err.Error())
		case safefetch.IsTooManyRedirects(err):
			return nil, core.NewMirrorError(core.MirrorStatusRefused, core.MirrorReasonTooManyRedirects, err.Error())
		default:
			return nil, core.NewMirrorError(core.MirrorStatusUnreachable, core.MirrorReasonTransport, err.Error())
		}
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		return nil, core.NewMirrorError(
			core.MirrorStatusUnreachable,
			core.MirrorReasonHTTPStatus,
			fmt.Sprintf("remote returned %d", res.StatusCode),
		)
	}

	// A declared Content-Length over the cap lets us hang up without reading
	// the body. It is a courtesy, not a control: the header can lie, which is
	// what the LimitReader below is for.
	if res.ContentLength > s.config.MaxObjectBytes {
		return nil, core.NewMirrorError(
			core.MirrorStatusRefused,
			core.MirrorReasonTooLarge,
			fmt.Sprintf("declared %d bytes, cap is %d", res.ContentLength, s.config.MaxObjectBytes),
		)
	}

	// Read at most cap+1 bytes. The extra byte is how we distinguish "exactly
	// at the cap" from "over it" without buffering the overage.
	data, err := io.ReadAll(io.LimitReader(res.Body, s.config.MaxObjectBytes+1))
	if err != nil {
		return nil, core.NewMirrorError(core.MirrorStatusUnreachable, core.MirrorReasonTransport, err.Error())
	}
	if int64(len(data)) > s.config.MaxObjectBytes {
		return nil, core.NewMirrorError(
			core.MirrorStatusRefused,
			core.MirrorReasonTooLarge,
			fmt.Sprintf("body exceeds cap of %d bytes", s.config.MaxObjectBytes),
		)
	}
	if len(data) == 0 {
		return nil, core.NewMirrorError(core.MirrorStatusUnreachable, core.MirrorReasonTransport, "empty body")
	}

	// Type is decided from the BYTES, never from the remote Content-Type
	// header. A host that serves text/html and labels it image/png would
	// otherwise get HTML cached under our domain and served back with an image
	// content type -- which is how a media proxy becomes an XSS vector.
	detected := mimetype.Detect(data)
	mime := detected.String()
	if i := strings.IndexByte(mime, ';'); i >= 0 {
		mime = strings.TrimSpace(mime[:i])
	}
	if !s.mimeAllowed(mime) {
		return nil, core.NewMirrorError(
			core.MirrorStatusRefused,
			core.MirrorReasonTypeNotAllowed,
			fmt.Sprintf("detected %s", mime),
		)
	}

	if err := s.checkDimensions(data); err != nil {
		return nil, err
	}

	hash, err := hashBytes(data)
	if err != nil {
		return nil, core.NewMirrorError(core.MirrorStatusRefused, core.MirrorReasonStorage, err.Error())
	}

	// Stored under the mirror's own pubkey with deduplication: if a user has
	// already uploaded these exact bytes, this adds a reference rather than a
	// second copy, and the mirror's reference keeps it alive independently of
	// that user's.
	_, _, err = s.blobs.SaveWithDedup(
		ctx,
		s.config.OwnerPubkey,
		hash,
		"",
		int64(len(data)),
		mime,
		data,
		time.Now().Unix(),
		core.EncryptionModeNone,
	)
	if err != nil {
		return nil, core.NewMirrorError(core.MirrorStatusRefused, core.MirrorReasonStorage, err.Error())
	}

	return &core.MirroredMedia{
		Sha256: hash,
		Size:   int64(len(data)),
		Mime:   mime,
		Data:   data,
	}, nil
}

func (s *mediaMirrorService) mimeAllowed(mime string) bool {
	for _, allowed := range s.config.AllowedMimeTypes {
		if strings.EqualFold(mime, allowed) {
			return true
		}
	}
	return false
}

// checkDimensions is the decompression-bomb guard.
//
// A few hundred bytes of PNG can declare 60000x60000, which decodes to roughly
// 14GB of pixel buffer. Size caps do not catch it, because the FILE is tiny --
// the cost is in the decode. image.DecodeConfig reads only the header, so the
// dimensions are known before anything is allocated, and an oversized image is
// refused having cost us nothing.
func (s *mediaMirrorService) checkDimensions(data []byte) error {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		// The magic bytes said this is an image we accept and the header does
		// not parse. Refused, not unreachable: retrying will not fix it.
		return core.NewMirrorError(core.MirrorStatusRefused, core.MirrorReasonNotAnImage, err.Error())
	}
	if cfg.Width <= 0 || cfg.Height <= 0 {
		return core.NewMirrorError(core.MirrorStatusRefused, core.MirrorReasonNotAnImage, "non-positive dimensions")
	}
	if s.config.MaxDimension > 0 && (cfg.Width > s.config.MaxDimension || cfg.Height > s.config.MaxDimension) {
		return core.NewMirrorError(
			core.MirrorStatusRefused,
			core.MirrorReasonTooManyPixels,
			fmt.Sprintf("%dx%d exceeds max dimension %d", cfg.Width, cfg.Height, s.config.MaxDimension),
		)
	}
	// int64 before multiplying: 60000*60000 overflows a 32-bit int, and an
	// overflowed product can come out negative and pass a naive check.
	if s.config.MaxPixels > 0 && int64(cfg.Width)*int64(cfg.Height) > s.config.MaxPixels {
		return core.NewMirrorError(
			core.MirrorStatusRefused,
			core.MirrorReasonTooManyPixels,
			fmt.Sprintf("%dx%d exceeds max pixels %d", cfg.Width, cfg.Height, s.config.MaxPixels),
		)
	}
	return nil
}

func hashBytes(data []byte) (string, error) {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

// Evict drops least-recently-accessed entries until the cache is under its low
// water mark, and expires stale negative-cache rows.
func (s *mediaMirrorService) Evict(ctx context.Context) (*core.MirrorEvictResult, error) {
	if !s.IsEnabled() {
		return nil, core.ErrMirrorDisabled
	}

	s.evictMu.Lock()
	defer s.evictMu.Unlock()

	s.expireFailures(ctx)

	before, err := s.queries.SumMirroredMediaSize(ctx)
	if err != nil {
		return nil, fmt.Errorf("sum mirror cache: %w", err)
	}
	result := &core.MirrorEvictResult{BytesBefore: before, BytesAfter: before}

	if s.config.CacheMaxBytes <= 0 || before <= s.config.CacheMaxBytes {
		return result, nil
	}

	target := s.lowWaterMark()
	candidates, err := s.queries.ListMirroredMediaLRU(ctx, evictScanBatch)
	if err != nil {
		return nil, fmt.Errorf("list mirror LRU: %w", err)
	}

	current := before
	for _, c := range candidates {
		if current <= target {
			break
		}
		if err := s.evictOne(ctx, c.UrlHash, c.Sha256); err != nil {
			s.log.Warn("media mirror: eviction failed for one entry", zap.Error(err))
			continue
		}
		current -= c.Size
		result.Evicted++
		metrics.MirrorEvictions.Inc()
	}

	result.BytesAfter = current
	if result.Evicted > 0 {
		s.log.Info("media mirror: evicted",
			zap.Int64("entries", result.Evicted),
			zap.Int64("bytes_before", result.BytesBefore),
			zap.Int64("bytes_after", result.BytesAfter))
	}
	return result, nil
}

// lowWaterMark is how far an eviction pass drains below the cap. Draining to
// exactly the cap would put the cache one byte under it, so the very next
// insert would trigger another full pass -- an eviction per request forever.
func (s *mediaMirrorService) lowWaterMark() int64 {
	pct := int64(s.config.CacheLowWaterPercent)
	if pct <= 0 || pct > 100 {
		pct = 90
	}
	return s.config.CacheMaxBytes * pct / 100
}

// evictOne removes the index entry, then releases the mirror's reference to
// the blob.
//
// Order matters. The index row goes first so that a crash in between leaves a
// blob with a live reference and no index entry -- reclaimable later by the GC
// worker, and harmless in the meantime. The reverse order would leave an index
// row pointing at a deleted blob, which reads as "mirrored" and serves a 404.
func (s *mediaMirrorService) evictOne(ctx context.Context, key string, sha sql.NullString) error {
	if err := s.queries.DeleteMirroredMedia(ctx, key); err != nil {
		return fmt.Errorf("delete mirror row: %w", err)
	}
	if !sha.Valid {
		return nil
	}

	// Byte-identical content can be reached through several URLs. Releasing
	// the blob while another mirror entry still points at it would break that
	// entry, so the reference is only dropped when the last one goes.
	remaining, err := s.queries.CountMirroredMediaForBlob(ctx, sha)
	if err != nil {
		return fmt.Errorf("count mirror refs: %w", err)
	}
	if remaining > 0 {
		return nil
	}

	// DeleteReference removes only the MIRROR's claim. If a user uploaded the
	// same bytes themselves, their reference survives and the blob stays.
	if _, err := s.blobs.DeleteReference(ctx, s.config.OwnerPubkey, sha.String); err != nil {
		return fmt.Errorf("release blob reference: %w", err)
	}
	return nil
}

// expireFailures clears negative-cache rows old enough to retry.
func (s *mediaMirrorService) expireFailures(ctx context.Context) {
	now := time.Now()
	if s.config.NegativeTTL > 0 {
		if err := s.queries.DeleteStaleMirrorFailures(ctx, db.DeleteStaleMirrorFailuresParams{
			Status:    string(core.MirrorStatusUnreachable),
			FetchedAt: now.Add(-s.config.NegativeTTL).Unix(),
		}); err != nil {
			s.log.Warn("media mirror: expiring unreachable rows failed", zap.Error(err))
		}
	}
	if s.config.RefusedTTL > 0 {
		if err := s.queries.DeleteStaleMirrorFailures(ctx, db.DeleteStaleMirrorFailuresParams{
			Status:    string(core.MirrorStatusRefused),
			FetchedAt: now.Add(-s.config.RefusedTTL).Unix(),
		}); err != nil {
			s.log.Warn("media mirror: expiring refused rows failed", zap.Error(err))
		}
	}
}

// StartWorker runs eviction on an interval.
func (s *mediaMirrorService) StartWorker(ctx context.Context) {
	if !s.IsEnabled() || s.config.EvictInterval <= 0 {
		return
	}
	go func() {
		ticker := time.NewTicker(s.config.EvictInterval)
		defer ticker.Stop()
		s.log.Info("media mirror: eviction worker started",
			zap.Duration("interval", s.config.EvictInterval),
			zap.Int64("cache_max_bytes", s.config.CacheMaxBytes))
		for {
			select {
			case <-ctx.Done():
				return
			case <-s.stopCh:
				return
			case <-ticker.C:
				if _, err := s.Evict(ctx); err != nil {
					s.log.Warn("media mirror: eviction pass failed", zap.Error(err))
				}
			}
		}
	}()
}

// StopWorker stops the eviction worker. Safe to call more than once.
func (s *mediaMirrorService) StopWorker() {
	s.stopOnce.Do(func() { close(s.stopCh) })
}
