package gin

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"git.aegis-hq.xyz/coldforge/cloistr-blossom/internal/metrics"
	"git.aegis-hq.xyz/coldforge/cloistr-blossom/src/core"
)

type apiStat struct {
	BytesStored int `json:"bytes_stored"`
	BlobCount   int `json:"blob_count"`
	PubkeyCount int `json:"pubkey_count"`
}

func fromCoreStat(s *core.Stats) apiStat {
	return apiStat{
		BytesStored: s.BytesStored,
		BlobCount:   s.BlobCount,
		PubkeyCount: s.PubkeyCount,
	}
}

func getStats(
	services core.Services,
) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		stats, err := services.Stats().Get(ctx.Request.Context())
		if err != nil {
			ctx.AbortWithStatus(http.StatusBadRequest)
			return
		}

		// Update Prometheus gauges
		metrics.StorageBytes.Set(float64(stats.BytesStored))
		metrics.StoredBlobs.Set(float64(stats.BlobCount))
		metrics.ActiveUsers.Set(float64(stats.PubkeyCount))

		ctx.JSON(
			http.StatusOK,
			fromCoreStat(stats),
		)
	}
}
