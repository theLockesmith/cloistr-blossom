package bud04

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"git.aegis-hq.xyz/coldforge/cloistr-blossom/internal/safefetch"
	bud02 "git.aegis-hq.xyz/coldforge/cloistr-blossom/src/bud-02"
	"git.aegis-hq.xyz/coldforge/cloistr-blossom/src/core"
)

var (
	// ErrSourceNotPermitted is returned when the mirror source resolves to a
	// private, loopback, link-local or otherwise non-public address.
	ErrSourceNotPermitted = errors.New("mirror source address is not permitted")
	// ErrSourceFetch is returned when the source could not be fetched. Its
	// message never carries the transport error, which can name internal
	// hosts.
	ErrSourceFetch = errors.New("could not fetch blob from mirror source")
)

// fetchPolicy is the safefetch policy for mirror sources. The URL is chosen
// by the caller, so the fetch must not reach the server's own network.
var fetchPolicy = safefetch.Policy{
	MaxRedirects: 3,
	Timeout:      10 * time.Second,
}

// MirrorBlob fetches a blob from blobUrl and stores it for pubkey. Once the
// bytes are in hand it goes through the same path as PUT /upload, so access
// rules, tier limits, quota, the removed-hash list and usage accounting all
// apply to a mirror exactly as to an upload.
func MirrorBlob(
	ctx context.Context,
	services core.Services,
	cdnBaseUrl string,
	pubkey string,
	authHash string,
	blobUrl url.URL,
	encryptionMode core.EncryptionMode,
) (*core.Blob, error) {
	var blobBytes []byte
	if blob, err := services.Blob().GetFromHash(ctx, authHash); err == nil && (len(blob.Blob) > 0 || blob.Size == 0) {
		// Already stored: reference it for this user without refetching.
		// GetFromHash leaves the bytes empty when the storage read fails, so
		// that case falls through to a fetch instead.
		blobBytes = blob.Blob
	} else {
		fetched, err := fetchSource(ctx, blobUrl, services.Settings().GetMaxUploadSizeBytes())
		if err != nil {
			return nil, err
		}
		blobBytes = fetched
	}

	return bud02.UploadBlob(ctx, services, cdnBaseUrl, authHash, pubkey, blobBytes, encryptionMode, "")
}

// fetchSource GETs blobUrl through safefetch and reads at most maxBytes+1
// bytes, so an oversized source fails the upload size check without being
// buffered whole.
func fetchSource(ctx context.Context, blobUrl url.URL, maxBytes int) ([]byte, error) {
	if blobUrl.Scheme != "http" && blobUrl.Scheme != "https" {
		return nil, fmt.Errorf("%w: URL must be http or https", ErrSourceFetch)
	}
	client, err := safefetch.NewClient(fetchPolicy)
	if err != nil {
		return nil, fmt.Errorf("mirror fetch client: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, blobUrl.String(), http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid URL", ErrSourceFetch)
	}
	res, err := client.Do(req)
	if err != nil {
		if safefetch.IsBlocked(err) {
			return nil, ErrSourceNotPermitted
		}
		if safefetch.IsTooManyRedirects(err) {
			return nil, fmt.Errorf("%w: too many redirects", ErrSourceFetch)
		}
		return nil, ErrSourceFetch
	}
	defer res.Body.Close()
	if res.StatusCode < http.StatusOK || res.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("%w: source answered HTTP %d", ErrSourceFetch, res.StatusCode)
	}

	limit := int64(maxBytes) + 1
	if maxBytes <= 0 {
		limit = 1 << 62
	}
	body, err := io.ReadAll(io.LimitReader(res.Body, limit))
	if err != nil {
		return nil, ErrSourceFetch
	}
	return body, nil
}
