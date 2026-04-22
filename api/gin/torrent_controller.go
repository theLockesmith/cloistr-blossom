package gin

import (
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/gin-gonic/gin"
	"git.aegis-hq.xyz/coldforge/cloistr-blossom/src/core"
)

const (
	// maxTorrentTrackers limits the number of tracker URLs to prevent abuse
	maxTorrentTrackers = 10
	// maxTorrentWebSeeds limits the number of web seed URLs
	maxTorrentWebSeeds = 10
)

// hashPattern validates that the hash is a valid SHA-256 hex string
var hashPattern = regexp.MustCompile(`^[a-fA-F0-9]{64}$`)

// generateTorrent handles POST /:hash/torrent to generate a torrent file for a blob.
func generateTorrent(
	services core.Services,
	webSeedBaseURL string,
) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		hash := ctx.Param("hash")
		if hash == "" {
			ctx.AbortWithStatusJSON(http.StatusBadRequest, apiError{Message: "hash is required"})
			return
		}

		// Validate hash format (SHA-256 hex string)
		if !hashPattern.MatchString(hash) {
			ctx.AbortWithStatusJSON(http.StatusBadRequest, apiError{Message: "invalid hash format"})
			return
		}

		// Check if blob exists
		exists, err := services.Blob().Exists(ctx.Request.Context(), hash)
		if err != nil || !exists {
			ctx.AbortWithStatusJSON(http.StatusNotFound, apiError{Message: "blob not found"})
			return
		}

		// Build torrent config from query parameters
		config := &core.TorrentConfig{
			EnableDHT: ctx.Query("dht") != "false", // enabled by default
			Comment:   ctx.Query("comment"),
			CreatedBy: ctx.Query("created_by"),
		}

		// Add trackers from query params (can be multiple, with limit)
		if trackers := ctx.QueryArray("tracker"); len(trackers) > 0 {
			if len(trackers) > maxTorrentTrackers {
				ctx.AbortWithStatusJSON(http.StatusBadRequest, apiError{
					Message: fmt.Sprintf("too many trackers (max %d)", maxTorrentTrackers),
				})
				return
			}
			// Validate tracker URLs
			for _, t := range trackers {
				if _, err := url.Parse(t); err != nil {
					ctx.AbortWithStatusJSON(http.StatusBadRequest, apiError{
						Message: fmt.Sprintf("invalid tracker URL: %s", t),
					})
					return
				}
			}
			config.TrackerURLs = trackers
		}

		// Add web seeds - default to the blossom server URL
		if webSeeds := ctx.QueryArray("webseed"); len(webSeeds) > 0 {
			if len(webSeeds) > maxTorrentWebSeeds {
				ctx.AbortWithStatusJSON(http.StatusBadRequest, apiError{
					Message: fmt.Sprintf("too many web seeds (max %d)", maxTorrentWebSeeds),
				})
				return
			}
			// Validate web seed URLs
			for _, ws := range webSeeds {
				if _, err := url.Parse(ws); err != nil {
					ctx.AbortWithStatusJSON(http.StatusBadRequest, apiError{
						Message: fmt.Sprintf("invalid web seed URL: %s", ws),
					})
					return
				}
			}
			config.WebSeedURLs = webSeeds
		} else if webSeedBaseURL != "" {
			// Default web seed pointing to this server
			config.WebSeedURLs = []string{webSeedBaseURL + "/"}
		}

		info, torrentBytes, err := services.Torrent().GenerateTorrent(ctx.Request.Context(), hash, config)
		if err != nil {
			if err == core.ErrTorrentNotFound {
				ctx.AbortWithStatusJSON(http.StatusNotFound, apiError{Message: "blob not found"})
				return
			}
			ctx.AbortWithStatusJSON(http.StatusInternalServerError, apiError{
				Message: fmt.Sprintf("failed to generate torrent: %s", err.Error()),
			})
			return
		}

		// Check Accept header - return JSON metadata or torrent file
		accept := ctx.GetHeader("Accept")
		if strings.Contains(accept, "application/json") {
			ctx.JSON(http.StatusOK, torrentInfoResponse{
				BlobHash:    info.BlobHash,
				InfoHash:    info.InfoHash,
				MagnetURI:   info.MagnetURI,
				PieceLength: info.PieceLength,
				PieceCount:  info.PieceCount,
				TotalSize:   info.TotalSize,
				Name:        info.Name,
				CreatedAt:   info.CreatedAt,
			})
			return
		}

		// Return torrent file
		ctx.Header("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s.torrent\"", hash))
		ctx.Data(http.StatusOK, "application/x-bittorrent", torrentBytes)
	}
}

// getTorrent handles GET /:hash/torrent to retrieve a previously generated torrent file.
func getTorrent(
	services core.Services,
) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		hash := ctx.Param("hash")
		if hash == "" {
			ctx.AbortWithStatusJSON(http.StatusBadRequest, apiError{Message: "hash is required"})
			return
		}

		// Check Accept header - return JSON metadata or torrent file
		accept := ctx.GetHeader("Accept")
		if strings.Contains(accept, "application/json") {
			info, err := services.Torrent().GetTorrentInfo(ctx.Request.Context(), hash)
			if err != nil {
				if err == core.ErrTorrentNotFound {
					ctx.AbortWithStatusJSON(http.StatusNotFound, apiError{Message: "torrent not found"})
					return
				}
				ctx.AbortWithStatusJSON(http.StatusInternalServerError, apiError{
					Message: fmt.Sprintf("failed to get torrent info: %s", err.Error()),
				})
				return
			}

			ctx.JSON(http.StatusOK, torrentInfoResponse{
				BlobHash:    info.BlobHash,
				InfoHash:    info.InfoHash,
				MagnetURI:   info.MagnetURI,
				PieceLength: info.PieceLength,
				PieceCount:  info.PieceCount,
				TotalSize:   info.TotalSize,
				Name:        info.Name,
				CreatedAt:   info.CreatedAt,
			})
			return
		}

		// Return torrent file
		torrentBytes, err := services.Torrent().GetTorrent(ctx.Request.Context(), hash)
		if err != nil {
			if err == core.ErrTorrentNotFound {
				ctx.AbortWithStatusJSON(http.StatusNotFound, apiError{Message: "torrent not found"})
				return
			}
			ctx.AbortWithStatusJSON(http.StatusInternalServerError, apiError{
				Message: fmt.Sprintf("failed to get torrent: %s", err.Error()),
			})
			return
		}

		ctx.Header("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s.torrent\"", hash))
		ctx.Data(http.StatusOK, "application/x-bittorrent", torrentBytes)
	}
}

// deleteTorrent handles DELETE /:hash/torrent to remove a cached torrent.
func deleteTorrent(
	services core.Services,
) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		hash := ctx.Param("hash")
		if hash == "" {
			ctx.AbortWithStatusJSON(http.StatusBadRequest, apiError{Message: "hash is required"})
			return
		}

		err := services.Torrent().DeleteTorrent(ctx.Request.Context(), hash)
		if err != nil {
			ctx.AbortWithStatusJSON(http.StatusInternalServerError, apiError{
				Message: fmt.Sprintf("failed to delete torrent: %s", err.Error()),
			})
			return
		}

		ctx.JSON(http.StatusOK, gin.H{
			"message": "torrent deleted successfully",
		})
	}
}

// torrentInfoResponse is the JSON response for torrent metadata.
type torrentInfoResponse struct {
	BlobHash    string `json:"blob_hash"`
	InfoHash    string `json:"info_hash"`
	MagnetURI   string `json:"magnet_uri"`
	PieceLength int64  `json:"piece_length"`
	PieceCount  int    `json:"piece_count"`
	TotalSize   int64  `json:"total_size"`
	Name        string `json:"name"`
	CreatedAt   int64  `json:"created_at"`
}
