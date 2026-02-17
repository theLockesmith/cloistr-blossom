package bud01

import (
	"context"

	"git.coldforge.xyz/coldforge/cloistr-blossom/src/core"
)

func HasBlob(
	ctx context.Context,
	services core.Services,
	hash string,
) (bool, error) {
	return services.Blob().Exists(ctx, hash)
}
