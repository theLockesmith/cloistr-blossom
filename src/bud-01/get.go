package bud01

import (
	"context"

	"git.aegis-hq.xyz/coldforge/cloistr-blossom/src/core"
)

// GetBlob returns a blob with its content loaded.
func GetBlob(
	ctx context.Context,
	services core.Services,
	hash string,
) (*core.Blob, error) {
	blob, err := services.Blob().GetFromHash(ctx, hash)
	if err != nil {
		return nil, core.ErrBlobNotFound
	}

	return blob, nil
}
