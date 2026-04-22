package bud02

import (
	"context"
	"errors"

	"git.aegis-hq.xyz/coldforge/cloistr-blossom/src/core"
)

func DeleteBlob(
	ctx context.Context,
	services core.Services,
	pubkey string,
	hash string,
	authHash string,
) error {
	var (
		blobs = services.Blob()
		quota = services.Quota()
	)

	// Get blob info first (for size calculation)
	blobDescriptor, err := blobs.GetFromHash(ctx, hash)
	if err != nil {
		return core.ErrBlobNotFound
	}

	// verify both hashes are the same
	if hash != authHash {
		return errors.New("unauthorized")
	}

	// Check if user has a reference to this blob (dedup-aware ownership)
	hasRef, err := blobs.HasReference(ctx, pubkey, hash)
	if err != nil {
		return err
	}
	if !hasRef {
		return errors.New("unauthorized")
	}

	blobSize := blobDescriptor.Size

	// Delete the user's reference to this blob
	// If this was the last reference, the actual blob is deleted
	_, err = blobs.DeleteReference(ctx, pubkey, hash)
	if err != nil {
		return err
	}

	// Decrement quota usage after successful delete
	if err := quota.DecrementUsage(ctx, pubkey, blobSize); err != nil {
		// Log but don't fail - the reference was deleted successfully
		// Usage will be corrected on next recalculation
	}

	return nil
}
