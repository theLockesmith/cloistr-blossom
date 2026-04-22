package bud02

import (
	"context"

	"git.aegis-hq.xyz/coldforge/cloistr-blossom/src/core"
)

func ListBlobs(
	ctx context.Context,
	services core.Services,
	pubkey string,
) ([]*core.Blob, error) {
	return services.Blob().GetFromPubkey(ctx, pubkey)
}
