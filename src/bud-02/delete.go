package bud02

import (
	"context"
	"errors"

	"git.coldforge.xyz/coldforge/cloistr-blossom/src/core"
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
	blobDescriptor, err := blobs.GetFromHash(ctx, hash)
	if err != nil {
		return core.ErrBlobNotFound
	}

	// only the owner can delete the file
	if blobDescriptor.Pubkey != pubkey {
		return errors.New("unauthorized")
	}

	// verify both hashes are the same
	if hash != authHash {
		return errors.New("unauthorized")
	}

	blobSize := blobDescriptor.Size

	if err := blobs.DeleteFromHash(ctx, hash); err != nil {
		return err
	}

	// Decrement quota usage after successful delete
	if err := quota.DecrementUsage(ctx, pubkey, blobSize); err != nil {
		// Log but don't fail - the blob was deleted successfully
		// Usage will be corrected on next recalculation
	}

	return nil
}
