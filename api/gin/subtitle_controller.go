package gin

import (
	"fmt"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"
	"git.coldforge.xyz/coldforge/cloistr-blossom/src/core"
)

// addSubtitle handles PUT /:hash/subtitles/:lang to add a subtitle track.
func addSubtitle(
	services core.Services,
) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		hash := ctx.Param("hash")
		lang := ctx.Param("lang")

		if hash == "" || lang == "" {
			ctx.AbortWithStatusJSON(http.StatusBadRequest, apiError{
				Message: "hash and language are required",
			})
			return
		}

		// Read subtitle content
		content, err := io.ReadAll(ctx.Request.Body)
		if err != nil {
			ctx.AbortWithStatusJSON(http.StatusBadRequest, apiError{
				Message: fmt.Sprintf("failed to read body: %s", err.Error()),
			})
			return
		}

		if len(content) == 0 {
			ctx.AbortWithStatusJSON(http.StatusBadRequest, apiError{
				Message: "subtitle content is required",
			})
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
				ctx.AbortWithStatusJSON(http.StatusBadRequest, apiError{
					Message: "invalid subtitle format (must be WebVTT)",
				})
				return
			}
			ctx.AbortWithStatusJSON(http.StatusInternalServerError, apiError{
				Message: fmt.Sprintf("failed to add subtitle: %s", err.Error()),
			})
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
			ctx.AbortWithStatusJSON(http.StatusBadRequest, apiError{
				Message: "hash and language are required",
			})
			return
		}

		content, err := services.Video().GetSubtitle(ctx.Request.Context(), hash, lang)
		if err != nil {
			if err == core.ErrSubtitleNotFound {
				ctx.AbortWithStatusJSON(http.StatusNotFound, apiError{
					Message: "subtitle not found",
				})
				return
			}
			ctx.AbortWithStatusJSON(http.StatusInternalServerError, apiError{
				Message: fmt.Sprintf("failed to get subtitle: %s", err.Error()),
			})
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
			ctx.AbortWithStatusJSON(http.StatusBadRequest, apiError{
				Message: "hash is required",
			})
			return
		}

		tracks, err := services.Video().ListSubtitles(ctx.Request.Context(), hash)
		if err != nil {
			ctx.AbortWithStatusJSON(http.StatusInternalServerError, apiError{
				Message: fmt.Sprintf("failed to list subtitles: %s", err.Error()),
			})
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
			ctx.AbortWithStatusJSON(http.StatusBadRequest, apiError{
				Message: "hash and language are required",
			})
			return
		}

		if err := services.Video().DeleteSubtitle(ctx.Request.Context(), hash, lang); err != nil {
			ctx.AbortWithStatusJSON(http.StatusInternalServerError, apiError{
				Message: fmt.Sprintf("failed to delete subtitle: %s", err.Error()),
			})
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
