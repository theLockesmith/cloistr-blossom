package bud02

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"git.aegis-hq.xyz/coldforge/cloistr-blossom/src/core"
)

// AdminDeleteResult reports what an admin delete did, or in a dry run, would do.
type AdminDeleteResult struct {
	Hash string `json:"hash"`
	Size int64  `json:"size"`
	// Owners is every owner found when the call started.
	Owners []string `json:"owners"`
	// Removed is the owners whose reference this call deleted and whose usage
	// it refunded. An owner who deleted their own copy mid-run is not in it.
	Removed []string `json:"removed"`
	DryRun  bool     `json:"dry_run"`
	// Deleted is true once the blob row is gone. Removing the stored object is
	// best-effort, as on every other delete path.
	Deleted bool `json:"deleted"`
}

// AdminDeleteBlob removes a blob on an operator's behalf: each owner's
// reference is deleted and the blob's size taken off that owner's usage, the
// same accounting as an owner deleting it themselves. Unlike a moderation
// "removed" action it does not add the hash to the removed-blobs list, so the
// same bytes may be uploaded again. With dryRun it only reports.
//
// It is safe to retry: owners whose reference is already gone are skipped.
func AdminDeleteBlob(ctx context.Context, services core.Services, hash string, dryRun bool) (*AdminDeleteResult, error) {
	blobs := services.Blob()

	desc, err := blobs.GetDescriptor(ctx, hash)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && desc == nil) {
		return nil, core.ErrBlobNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get blob: %w", err)
	}

	owners, err := blobs.ListReferenceOwners(ctx, hash)
	if err != nil {
		return nil, fmt.Errorf("list owners: %w", err)
	}

	res := &AdminDeleteResult{Hash: hash, Size: desc.Size, Owners: owners, Removed: []string{}, DryRun: dryRun}
	if dryRun {
		return res, nil
	}

	quota := services.Quota()
	for _, pk := range owners {
		// The owner may have deleted their own copy since the listing.
		has, err := blobs.HasReference(ctx, pk, hash)
		if err != nil {
			return res, fmt.Errorf("check reference for %s: %w", pk, err)
		}
		if !has {
			continue
		}
		// DeleteReference also deletes the blob when this is the last reference.
		if _, err := blobs.DeleteReference(ctx, pk, hash); err != nil {
			return res, fmt.Errorf("delete reference for %s: %w", pk, err)
		}
		res.Removed = append(res.Removed, pk)
		// Usage drift is corrected by /users/:pubkey/recalculate, so a failed
		// decrement does not abort the removal.
		_ = quota.DecrementUsage(ctx, pk, desc.Size)
	}

	// Normally DeleteReference has already removed the row. This covers a blob
	// that had no references to begin with. If a concurrent upload re-referenced
	// it, that owner's copy is left alone and Deleted stays false.
	remaining, err := blobs.ListReferenceOwners(ctx, hash)
	if err != nil {
		return res, fmt.Errorf("recheck owners: %w", err)
	}
	if len(remaining) > 0 {
		return res, nil
	}
	_, err = blobs.GetDescriptor(ctx, hash)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		res.Deleted = true
	case err != nil:
		return res, fmt.Errorf("recheck blob: %w", err)
	default:
		if err := blobs.DeleteFromHash(ctx, hash); err != nil {
			return res, fmt.Errorf("delete blob: %w", err)
		}
		res.Deleted = true
	}
	return res, nil
}
