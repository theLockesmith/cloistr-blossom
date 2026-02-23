package bud02

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/gabriel-vasile/mimetype"
	"git.coldforge.xyz/coldforge/cloistr-blossom/src/core"
	"git.coldforge.xyz/coldforge/cloistr-blossom/src/pkg/hashing"
)

func UploadBlob(
	ctx context.Context,
	services core.Services,
	cdnBaseUrl string,
	authHash string,
	pubkey string,
	blobBytes []byte,
	encryptionMode core.EncryptionMode,
) (*core.Blob, error) {
	var (
		blobs    = services.Blob()
		mimes    = services.Mime()
		settings = services.Settings()
		quota    = services.Quota()
	)

	if err := services.ACR().Validate(
		ctx,
		pubkey,
		core.ResourceUpload,
	); err != nil {
		return nil, err
	}

	mimeType := mimetype.Detect(blobBytes)
	if err := mimes.IsAllowed(ctx, mimeType.String()); err != nil {
		return nil, fmt.Errorf("mime type %s not allowed", mimeType.String())
	}

	if err := settings.ValidateFileSizeMaxBytes(ctx, len(blobBytes)); err != nil {
		return nil, fmt.Errorf("file size: %w", err)
	}

	// Check quota before upload
	if err := quota.CheckQuota(ctx, pubkey, int64(len(blobBytes))); err != nil {
		return nil, fmt.Errorf("quota check: %w", err)
	}

	hash, err := hashing.Hash(blobBytes)
	if err != nil {
		return nil, fmt.Errorf("hash blob: %w", err)
	}

	// calculated hash MUST match hash set in auth event
	if hash != authHash {
		return nil, errors.New("blob hash doesn't match auth event 'x' tag")
	}

	// for now the URL of the file is the URL where the CDN is being hosted
	// plus the file hash
	url := cdnBaseUrl + "/" + hash

	// SaveWithDedup handles content-addressable deduplication:
	// - If blob exists and user already has it: returns existing blob
	// - If blob exists but user doesn't have it: creates reference (no re-store)
	// - If blob doesn't exist: saves blob and creates reference
	blobDescriptor, isNewBlob, err := blobs.SaveWithDedup(
		ctx,
		pubkey,
		hash,
		url,
		int64(len(blobBytes)),
		mimeType.String(),
		blobBytes,
		time.Now().Unix(),
		encryptionMode,
	)
	if err != nil {
		return nil, fmt.Errorf("save blob: %w", err)
	}

	// Update quota usage - count the blob size for this user regardless of dedup
	// Each user's quota reflects their "ownership" of blob references
	if isNewBlob {
		// New blob was stored - increment quota
		if err := quota.IncrementUsage(ctx, pubkey, int64(len(blobBytes))); err != nil {
			// Log but don't fail - the blob was saved successfully
		}
	} else {
		// Blob was deduplicated - still count against user's quota
		// This is fair: user gets the space benefit of dedup in storage,
		// but their quota reflects what they've uploaded
		if err := quota.IncrementUsage(ctx, pubkey, blobDescriptor.Size); err != nil {
			// Log but don't fail
		}
	}

	return blobDescriptor, nil
}
