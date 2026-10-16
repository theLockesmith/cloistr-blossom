package bud01

import (
	"context"

	"git.aegis-hq.xyz/coldforge/cloistr-blossom/src/core"
)

// GetBlobDescriptor returns a blob's metadata without loading its content.
func GetBlobDescriptor(
	ctx context.Context,
	services core.Services,
	hash string,
) (*core.Blob, error) {
	blob, err := services.Blob().GetDescriptor(ctx, hash)
	if err != nil {
		return nil, core.ErrBlobNotFound
	}

	return blob, nil
}
