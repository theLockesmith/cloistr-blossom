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

// ErrSourceFetch is returned for every source that could not be fetched:
// refused by the address policy, unresolvable, unreachable, or answering an
// error. One message for all of them, so a caller cannot probe which
// internal names exist by comparing errors.
var ErrSourceFetch = errors.New("could not fetch blob from mirror source")

// fetchPolicy is the safefetch policy for mirror sources. The URL is chosen
// by the caller, so the fetch must not reach the server's own network.
var fetchPolicy = safefetch.Policy{
	MaxRedirects: 3,
	Timeout:      10 * time.Second,
}

// MirrorBlob fetches a blob from blobUrl and stores it for pubkey. Once the
// bytes are in hand it goes through the same path as PUT /upload, so access
// rules, tier limits, quota, the removed-hash list and usage accounting all
// apply to a mirror exactly as to an upload. Access and quota are also
// checked before the source is contacted, so a refused key cannot make the
// server fetch anything.
func MirrorBlob(
	ctx context.Context,
	services core.Services,
	cdnBaseUrl string,
	pubkey string,
	authHash string,
	blobUrl url.URL,
	encryptionMode core.EncryptionMode,
) (*core.Blob, error) {
	if err := services.ACR().Validate(ctx, pubkey, core.ResourceUpload); err != nil {
		return nil, err
	}
	// One byte: a key that is banned, or whose quota has no room for any
	// non-empty blob, is refused before the fetch. The real size is checked
	// by UploadBlob once the bytes are in hand.
	if err := services.Quota().CheckQuota(ctx, pubkey, 1); err != nil {
		return nil, fmt.Errorf("quota check: %w", err)
	}

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
// buffered whole. A max of 0 admits no non-empty upload, so it reads one
// byte, never an unbounded body.
func fetchSource(ctx context.Context, blobUrl url.URL, maxBytes int) ([]byte, error) {
	if blobUrl.Scheme != "http" && blobUrl.Scheme != "https" {
		return nil, ErrSourceFetch
	}
	client, err := safefetch.NewClient(fetchPolicy)
	if err != nil {
		return nil, fmt.Errorf("mirror fetch client: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, blobUrl.String(), http.NoBody)
	if err != nil {
		return nil, ErrSourceFetch
	}
	res, err := client.Do(req)
	if err != nil {
		return nil, ErrSourceFetch
	}
	defer res.Body.Close()
	if res.StatusCode < http.StatusOK || res.StatusCode >= http.StatusMultipleChoices {
		return nil, ErrSourceFetch
	}

	body, err := io.ReadAll(io.LimitReader(res.Body, int64(max(maxBytes, 0))+1))
	if err != nil {
		return nil, ErrSourceFetch
	}
	return body, nil
}
