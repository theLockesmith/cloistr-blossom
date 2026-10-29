package bud02

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/gabriel-vasile/mimetype"
	"git.aegis-hq.xyz/coldforge/cloistr-blossom/src/core"
	"git.aegis-hq.xyz/coldforge/cloistr-blossom/src/pkg/hashing"
	"git.aegis-hq.xyz/coldforge/cloistr-blossom/src/pkg/mimetypes"
)

func UploadBlob(
	ctx context.Context,
	services core.Services,
	cdnBaseUrl string,
	authHash string,
	pubkey string,
	blobBytes []byte,
	encryptionMode core.EncryptionMode,
	declaredType string,
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

	// Detect MIME type from magic bytes. Policy checks use the sniffed type;
	// the stored type may be upgraded from declaredType for allowlisted text
	// formats the sniffer cannot recognise (see mimetypes.Resolve).
	mimeType := mimetype.Detect(blobBytes)
	storedType := mimetypes.Resolve(blobBytes, declaredType)

	// Tier-aware enforcement when upload_limits.enabled is true.
	// When disabled, fall back to the existing global checks so behaviour is
	// identical to the pre-enforcement baseline for self-hosters.
	// The tier check sees the type that will be stored, which Resolve may
	// have upgraded from the sniffed one.
	ul := services.UploadLimits()
	if ul.IsEnabled() {
		if err := ul.CheckUpload(ctx, pubkey, storedType, len(blobBytes)); err != nil {
			if errors.Is(err, core.ErrUploadMimeTypeNotAllowed) {
				return nil, fmt.Errorf("mime type %s not allowed for your tier", storedType)
			}
			return nil, fmt.Errorf("upload limits: %w", err)
		}
	} else {
		if err := mimes.IsAllowed(ctx, mimeType.String()); err != nil {
			return nil, fmt.Errorf("mime type %s not allowed", mimeType.String())
		}
		if err := settings.ValidateFileSizeMaxBytes(ctx, len(blobBytes)); err != nil {
			return nil, fmt.Errorf("file size: %w", err)
		}
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

	// Check if blob hash has been removed (BUD-09 compliance)
	// Removed blobs cannot be re-uploaded
	if moderation := services.Moderation(); moderation != nil {
		if removed, err := moderation.IsHashRemoved(ctx, hash); err == nil && removed {
			return nil, core.ErrHashRemoved
		}
	}

	// for now the URL of the file is the URL where the CDN is being hosted
	// plus the file hash
	url := cdnBaseUrl + "/" + hash

	// SaveWithDedup handles content-addressable deduplication:
	// - If blob exists and user already has it: returns existing blob
	// - If blob exists but user doesn't have it: creates reference (no re-store)
	// - If blob doesn't exist: saves blob and creates reference
	blobDescriptor, newReference, err := blobs.SaveWithDedup(
		ctx,
		pubkey,
		hash,
		url,
		int64(len(blobBytes)),
		storedType,
		blobBytes,
		time.Now().Unix(),
		encryptionMode,
	)
	if err != nil {
		return nil, fmt.Errorf("save blob: %w", err)
	}
	ul.RecordUpload(ctx, pubkey)

	// Charge the key once per blob it owns, whether or not the bytes were
	// already stored for someone else (dedup). Re-uploading a blob the key
	// already owns is not charged again: deleting it credits it only once.
	if newReference {
		if err := quota.IncrementUsage(ctx, pubkey, blobDescriptor.Size); err != nil {
			// Log but don't fail - the blob was saved successfully
		}
	}

	return blobDescriptor, nil
}
