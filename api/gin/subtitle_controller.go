package gin

import (
	"fmt"
	"io"
	"net/http"

	"git.aegis-hq.xyz/coldforge/cloistr-blossom/src/core"
	clerrors "git.aegis-hq.xyz/coldforge/cloistr-common/errors"
	"github.com/gin-gonic/gin"
)

// addSubtitle handles PUT /:hash/subtitles/:lang to add a subtitle track.
func addSubtitle(
	services core.Services,
) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		hash := ctx.Param("hash")
		lang := ctx.Param("lang")

		if hash == "" || lang == "" {
			clerrors.BadRequest(clerrors.CodeInvalidInput, "hash and language are required").Abort(ctx)
			return
		}

		// Read subtitle content
		content, err := io.ReadAll(ctx.Request.Body)
		if err != nil {
			clerrors.BadRequest(clerrors.CodeInvalidInput, fmt.Sprintf("failed to read body: %s", err.Error())).Abort(ctx)
			return
		}

		if len(content) == 0 {
			clerrors.BadRequest(clerrors.CodeInvalidInput, "subtitle content is required").Abort(ctx)
			return
		}

		// Get optional parameters from query/headers
		label := ctx.Query("label")
		if label == "" {
			label = lang // Default label to language code
		}
		isDefault := ctx.Query("default") == "true"
		isForced := ctx.Query("forced") == "true"

		subtitle := core.Subtitle{
			Language: lang,
			Label:    label,
			Default:  isDefault,
			Forced:   isForced,
		}

		if err := services.Video().AddSubtitle(ctx.Request.Context(), hash, subtitle, content); err != nil {
			if err == core.ErrInvalidSubtitleFormat {
				clerrors.BadRequest(clerrors.CodeInvalidInput, "invalid subtitle format (must be WebVTT)").Abort(ctx)
				return
			}
			clerrors.InternalError(clerrors.CodeInternalError, fmt.Sprintf("failed to add subtitle: %s", err.Error())).Abort(ctx)
			return
		}

		ctx.JSON(http.StatusOK, subtitleResponse{
			Language: lang,
			Label:    label,
			Default:  isDefault,
			Forced:   isForced,
			Message:  "subtitle added successfully",
		})
	}
}

// getSubtitle handles GET /:hash/subtitles/:lang to get a subtitle track.
func getSubtitle(
	services core.Services,
) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		hash := ctx.Param("hash")
		lang := ctx.Param("lang")

		if hash == "" || lang == "" {
			clerrors.BadRequest(clerrors.CodeInvalidInput, "hash and language are required").Abort(ctx)
			return
		}

		content, err := services.Video().GetSubtitle(ctx.Request.Context(), hash, lang)
		if err != nil {
			if err == core.ErrSubtitleNotFound {
				clerrors.NotFound(clerrors.CodeResourceNotFound, "subtitle not found").Abort(ctx)
				return
			}
			clerrors.InternalError(clerrors.CodeInternalError, fmt.Sprintf("failed to get subtitle: %s", err.Error())).Abort(ctx)
			return
		}

		ctx.Header("Content-Type", "text/vtt")
		ctx.Header("Cache-Control", "public, max-age=3600")
		ctx.Data(http.StatusOK, "text/vtt", content)
	}
}

// listSubtitles handles GET /:hash/subtitles to list all subtitle tracks.
func listSubtitles(
	services core.Services,
) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		hash := ctx.Param("hash")

		if hash == "" {
			clerrors.BadRequest(clerrors.CodeInvalidInput, "hash is required").Abort(ctx)
			return
		}

		tracks, err := services.Video().ListSubtitles(ctx.Request.Context(), hash)
		if err != nil {
			clerrors.InternalError(clerrors.CodeInternalError, fmt.Sprintf("failed to list subtitles: %s", err.Error())).Abort(ctx)
			return
		}

		ctx.JSON(http.StatusOK, subtitlesListResponse{
			Subtitles: tracks,
		})
	}
}

// deleteSubtitle handles DELETE /:hash/subtitles/:lang to remove a subtitle track.
func deleteSubtitle(
	services core.Services,
) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		hash := ctx.Param("hash")
		lang := ctx.Param("lang")

		if hash == "" || lang == "" {
			clerrors.BadRequest(clerrors.CodeInvalidInput, "hash and language are required").Abort(ctx)
			return
		}

		if err := services.Video().DeleteSubtitle(ctx.Request.Context(), hash, lang); err != nil {
			clerrors.InternalError(clerrors.CodeInternalError, fmt.Sprintf("failed to delete subtitle: %s", err.Error())).Abort(ctx)
			return
		}

		ctx.JSON(http.StatusOK, gin.H{
			"message": "subtitle deleted successfully",
		})
	}
}

// subtitleResponse is the response for subtitle add operations.
type subtitleResponse struct {
	Language string `json:"language"`
	Label    string `json:"label"`
	Default  bool   `json:"default"`
	Forced   bool   `json:"forced"`
	Message  string `json:"message"`
}

// subtitlesListResponse is the response for listing subtitles.
type subtitlesListResponse struct {
	Subtitles []core.SubtitleTrack `json:"subtitles"`
}
