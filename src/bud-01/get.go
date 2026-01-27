package bud01

import (
	"context"

	"git.coldforge.xyz/coldforge/coldforge-blossom/src/core"
)

func GetBlob(
	ctx context.Context,
	services core.Services,
	hash string,
) ([]byte, error) {
	blob, err := services.Blob().GetFromHash(ctx, hash)
	if err != nil {
		return nil, core.ErrBlobNotFound
	}

	return blob.Blob, nil
}
