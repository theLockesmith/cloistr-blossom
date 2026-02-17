package bud02

import (
	"context"

	"git.coldforge.xyz/coldforge/cloistr-blossom/src/core"
)

func ListBlobs(
	ctx context.Context,
	services core.Services,
	pubkey string,
) ([]*core.Blob, error) {
	return services.Blob().GetFromPubkey(ctx, pubkey)
}
