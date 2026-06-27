package gin

import (
	"fmt"
	"net/http"
	"strings"

	"git.aegis-hq.xyz/coldforge/cloistr-blossom/src/core"
	clerrors "git.aegis-hq.xyz/coldforge/cloistr-common/errors"
	"github.com/gin-gonic/gin"
)

// startTranscode handles POST /:hash/transcode to start video transcoding.
func startTranscode(
	services core.Services,
) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		hash := ctx.Param("hash")
		if hash == "" {
			clerrors.BadRequest(clerrors.CodeInvalidInput, "hash is required").Abort(ctx)
			return
		}

		// Check if blob exists
		exists, err := services.Blob().Exists(ctx.Request.Context(), hash)
		if err != nil || !exists {
			clerrors.NotFound(clerrors.CodeResourceNotFound, "blob not found").Abort(ctx)
			return
		}

		// Get blob to check mime type
		blob, err := services.Blob().GetFromHash(ctx.Request.Context(), hash)
		if err != nil {
			clerrors.NotFound(clerrors.CodeResourceNotFound, "blob not found").Abort(ctx)
			return
		}

		// Check if it's a video
		if !services.Video().IsSupported(blob.Type) {
			clerrors.BadRequest(clerrors.CodeInvalidInput, fmt.Sprintf("unsupported video format: %s", blob.Type)).Abort(ctx)
			return
		}

		// Check if FFmpeg is available
		if !services.Video().IsFFmpegAvailable() {
			clerrors.ServiceUnavailable(clerrors.CodeServiceUnavailable, "video transcoding is not available (FFmpeg not installed)", 0).Abort(ctx)
			return
		}

		// Start transcoding
		job, err := services.Video().StartTranscode(ctx.Request.Context(), hash, nil)
		if err != nil {
			if err == core.ErrTranscodeInProgress {
				// Return existing job status
				job, _ = services.Video().GetTranscodeStatus(ctx.Request.Context(), hash)
				ctx.JSON(http.StatusAccepted, transcodeStatusResponse{
					Status:   string(job.Status),
					Progress: job.Progress,
					Message:  "transcoding in progress",
				})
				return
			}
			clerrors.InternalError(clerrors.CodeInternalError, fmt.Sprintf("failed to start transcoding: %s", err.Error())).Abort(ctx)
			return
		}

		ctx.JSON(http.StatusAccepted, transcodeStatusResponse{
			Status:   string(job.Status),
			Progress: job.Progress,
			Message:  "transcoding started",
		})
	}
}

// getTranscodeStatus handles GET /:hash/transcode to get transcoding status.
func getTranscodeStatus(
	services core.Services,
) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		hash := ctx.Param("hash")
		if hash == "" {
			clerrors.BadRequest(clerrors.CodeInvalidInput, "hash is required").Abort(ctx)
			return
		}

		job, err := services.Video().GetTranscodeStatus(ctx.Request.Context(), hash)
		if err != nil {
			if err == core.ErrTranscodeNotFound {
				clerrors.NotFound(clerrors.CodeResourceNotFound, "no transcoding job found").Abort(ctx)
				return
			}
			clerrors.InternalError(clerrors.CodeInternalError, fmt.Sprintf("failed to get status: %s", err.Error())).Abort(ctx)
			return
		}

		ctx.JSON(http.StatusOK, transcodeStatusResponse{
			Status:      string(job.Status),
			Progress:    job.Progress,
			Error:       job.Error,
			CreatedAt:   job.CreatedAt,
			CompletedAt: job.CompletedAt,
		})
	}
}

// getHLSMasterPlaylist handles GET /:hash/hls/master.m3u8 to get the master playlist.
func getHLSMasterPlaylist(
	services core.Services,
	cdnBaseUrl string,
) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		hash := ctx.Param("hash")
		if hash == "" {
			clerrors.BadRequest(clerrors.CodeInvalidInput, "hash is required").Abort(ctx)
			return
		}

		manifest, err := services.Video().GetHLSManifest(ctx.Request.Context(), hash)
		if err != nil {
			if err == core.ErrTranscodeNotFound {
				// Check if transcoding is in progress
				job, jobErr := services.Video().GetTranscodeStatus(ctx.Request.Context(), hash)
				if jobErr == nil && job.Status == core.TranscodeStatusProcessing {
					ctx.JSON(http.StatusAccepted, gin.H{"message": fmt.Sprintf("transcoding in progress: %d%%", job.Progress)})
					return
				}
				clerrors.NotFound(clerrors.CodeResourceNotFound, "video not transcoded. POST to /:hash/transcode to start transcoding").Abort(ctx)
				return
			}
			clerrors.InternalError(clerrors.CodeInternalError, fmt.Sprintf("failed to get manifest: %s", err.Error())).Abort(ctx)
			return
		}

		// Rewrite playlist URLs to use the API endpoint
		playlist := rewritePlaylistURLs(manifest.MasterPlaylist, cdnBaseUrl, hash)

		ctx.Header("Content-Type", "application/vnd.apple.mpegurl")
		ctx.Header("Cache-Control", "public, max-age=3600")
		ctx.String(http.StatusOK, playlist)
	}
}

// getHLSVariantPlaylist handles GET /:hash/hls/:quality/stream.m3u8 to get a variant playlist.
func getHLSVariantPlaylist(
	services core.Services,
	cdnBaseUrl string,
) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		hash := ctx.Param("hash")
		quality := ctx.Param("quality")

		if hash == "" || quality == "" {
			clerrors.BadRequest(clerrors.CodeInvalidInput, "hash and quality are required").Abort(ctx)
			return
		}

		manifest, err := services.Video().GetHLSManifest(ctx.Request.Context(), hash)
		if err != nil {
			clerrors.NotFound(clerrors.CodeResourceNotFound, "video not transcoded").Abort(ctx)
			return
		}

		variantPlaylist, exists := manifest.Variants[quality]
		if !exists {
			clerrors.NotFound(clerrors.CodeResourceNotFound, fmt.Sprintf("quality %s not found", quality)).Abort(ctx)
			return
		}

		// Rewrite segment URLs
		playlist := rewriteSegmentURLs(variantPlaylist, cdnBaseUrl, hash, quality)

		ctx.Header("Content-Type", "application/vnd.apple.mpegurl")
		ctx.Header("Cache-Control", "public, max-age=3600")
		ctx.String(http.StatusOK, playlist)
	}
}

// getHLSSegment handles GET /:hash/hls/:quality/:segment to get a video segment.
func getHLSSegment(
	services core.Services,
) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		hash := ctx.Param("hash")
		quality := ctx.Param("quality")
		segment := ctx.Param("segment")

		if hash == "" || quality == "" || segment == "" {
			clerrors.BadRequest(clerrors.CodeInvalidInput, "hash, quality, and segment are required").Abort(ctx)
			return
		}

		data, err := services.Video().GetSegment(ctx.Request.Context(), hash, quality, segment)
		if err != nil {
			clerrors.NotFound(clerrors.CodeResourceNotFound, "segment not found").Abort(ctx)
			return
		}

		// Determine content type
		contentType := "video/mp2t"
		if strings.HasSuffix(segment, ".m4s") {
			contentType = "video/mp4"
		}

		ctx.Header("Content-Type", contentType)
		ctx.Header("Cache-Control", "public, max-age=31536000") // Cache for 1 year
		ctx.Data(http.StatusOK, contentType, data)
	}
}

// transcodeStatusResponse is the response for transcode status endpoints.
type transcodeStatusResponse struct {
	Status      string `json:"status"`
	Progress    int    `json:"progress"`
	Message     string `json:"message,omitempty"`
	Error       string `json:"error,omitempty"`
	CreatedAt   int64  `json:"created_at,omitempty"`
	CompletedAt int64  `json:"completed_at,omitempty"`
}

// rewritePlaylistURLs rewrites variant playlist URLs in the master playlist.
func rewritePlaylistURLs(playlist, cdnBaseUrl, hash string) string {
	lines := strings.Split(playlist, "\n")
	var result []string

	for _, line := range lines {
		if strings.HasSuffix(line, "/stream.m3u8") {
			// Extract quality from path like "720p/stream.m3u8"
			parts := strings.Split(line, "/")
			if len(parts) >= 1 {
				quality := parts[0]
				line = fmt.Sprintf("%s/%s/hls/%s/stream.m3u8", cdnBaseUrl, hash, quality)
			}
		}
		// Rewrite subtitle URIs
		if strings.Contains(line, "URI=\"subtitles/") {
			line = strings.Replace(line, "URI=\"subtitles/", fmt.Sprintf("URI=\"%s/%s/subtitles/", cdnBaseUrl, hash), 1)
			// Fix the .vtt extension path
			line = strings.Replace(line, ".vtt\"", "\"", 1)
		}
		result = append(result, line)
	}

	return strings.Join(result, "\n")
}

// rewriteSegmentURLs rewrites segment URLs in a variant playlist.
func rewriteSegmentURLs(playlist, cdnBaseUrl, hash, quality string) string {
	lines := strings.Split(playlist, "\n")
	var result []string

	for _, line := range lines {
		if strings.HasSuffix(line, ".ts") || strings.HasSuffix(line, ".m4s") {
			line = fmt.Sprintf("%s/%s/hls/%s/%s", cdnBaseUrl, hash, quality, line)
		}
		result = append(result, line)
	}

	return strings.Join(result, "\n")
}

// getDASHManifest handles GET /:hash/dash/manifest.mpd to get the DASH manifest.
func getDASHManifest(
	services core.Services,
	cdnBaseUrl string,
) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		hash := ctx.Param("hash")
		if hash == "" {
			clerrors.BadRequest(clerrors.CodeInvalidInput, "hash is required").Abort(ctx)
			return
		}

		manifest, err := services.Video().GetDASHManifest(ctx.Request.Context(), hash)
		if err != nil {
			if err == core.ErrTranscodeNotFound {
				// Check if transcoding is in progress
				job, jobErr := services.Video().GetTranscodeStatus(ctx.Request.Context(), hash)
				if jobErr == nil && job.Status == core.TranscodeStatusProcessing {
					ctx.JSON(http.StatusAccepted, gin.H{"message": fmt.Sprintf("transcoding in progress: %d%%", job.Progress)})
					return
				}
				clerrors.NotFound(clerrors.CodeResourceNotFound, "video not transcoded. POST to /:hash/transcode to start transcoding").Abort(ctx)
				return
			}
			clerrors.InternalError(clerrors.CodeInternalError, fmt.Sprintf("failed to get DASH manifest: %s", err.Error())).Abort(ctx)
			return
		}

		// Rewrite segment URLs in the MPD to use API endpoints
		mpd := rewriteDASHURLs(manifest.MPD, cdnBaseUrl, hash)

		ctx.Header("Content-Type", "application/dash+xml")
		ctx.Header("Cache-Control", "public, max-age=3600")
		ctx.String(http.StatusOK, mpd)
	}
}

// getDASHSegment handles GET /:hash/dash/:segment to get a DASH segment.
func getDASHSegment(
	services core.Services,
) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		hash := ctx.Param("hash")
		segment := ctx.Param("segment")

		if hash == "" || segment == "" {
			clerrors.BadRequest(clerrors.CodeInvalidInput, "hash and segment are required").Abort(ctx)
			return
		}

		data, err := services.Video().GetDASHSegment(ctx.Request.Context(), hash, segment)
		if err != nil {
			clerrors.NotFound(clerrors.CodeResourceNotFound, "segment not found").Abort(ctx)
			return
		}

		// Determine content type
		contentType := "video/mp4"
		if strings.HasSuffix(segment, ".m4s") {
			contentType = "video/iso.segment"
		}

		ctx.Header("Content-Type", contentType)
		ctx.Header("Cache-Control", "public, max-age=31536000") // Cache for 1 year
		ctx.Data(http.StatusOK, contentType, data)
	}
}

// rewriteDASHURLs rewrites segment URLs in the DASH MPD to use API endpoints.
func rewriteDASHURLs(mpd, cdnBaseUrl, hash string) string {
	// Replace relative segment URLs with absolute API URLs
	// The MPD references files like "init-stream0.m4s" and "chunk-stream0-00001.m4s"
	// We need to rewrite them to use the API endpoint

	// Replace initialization segment URLs
	mpd = strings.ReplaceAll(mpd, `initialization="init-`, fmt.Sprintf(`initialization="%s/%s/dash/init-`, cdnBaseUrl, hash))

	// Replace media segment URLs
	mpd = strings.ReplaceAll(mpd, `media="chunk-`, fmt.Sprintf(`media="%s/%s/dash/chunk-`, cdnBaseUrl, hash))

	// Replace subtitle URLs
	mpd = strings.ReplaceAll(mpd, `<BaseURL>subtitles/`, fmt.Sprintf(`<BaseURL>%s/%s/subtitles/`, cdnBaseUrl, hash))

	return mpd
}
