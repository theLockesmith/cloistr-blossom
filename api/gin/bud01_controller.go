package gin

import (
	"bytes"
	"net/http"
	"strconv"
	"strings"

	"github.com/gabriel-vasile/mimetype"
	"github.com/gin-gonic/gin"
	bud01 "git.coldforge.xyz/coldforge/coldforge-blossom/src/bud-01"
	"git.coldforge.xyz/coldforge/coldforge-blossom/src/core"
	"git.coldforge.xyz/coldforge/coldforge-blossom/internal/media"
)

func getBlob(
	services core.Services,
) gin.HandlerFunc {
	processor := media.NewImageProcessor()

	return func(ctx *gin.Context) {
		pathParts := strings.Split(ctx.Param("path"), ".")
		hash := pathParts[0]

		fileBytes, err := bud01.GetBlob(
			ctx.Request.Context(),
			services,
			hash,
		)
		if err != nil {
			ctx.AbortWithStatusJSON(
				http.StatusBadRequest,
				apiError{
					Message: err.Error(),
				},
			)
			return
		}

		// Check for image processing parameters
		width, _ := strconv.Atoi(ctx.Query("width"))
		height, _ := strconv.Atoi(ctx.Query("height"))
		format := ctx.Query("format")
		thumbnail := ctx.Query("thumbnail")

		// If no processing requested, return original
		if width == 0 && height == 0 && format == "" && thumbnail == "" {
			mType := mimetype.Detect(fileBytes)
			ctx.Header("Content-Type", mType.String())
			_, _ = ctx.Writer.Write(fileBytes)
			ctx.Status(http.StatusOK)
			return
		}

		// Check if this is an image
		mType := mimetype.Detect(fileBytes)
		if !media.IsImage(mType.String()) {
			ctx.Header("Content-Type", mType.String())
			_, _ = ctx.Writer.Write(fileBytes)
			ctx.Status(http.StatusOK)
			return
		}

		// Process the image
		opts := media.ProcessOptions{
			Width:  width,
			Height: height,
		}

		// Handle thumbnail presets
		if thumbnail != "" {
			switch thumbnail {
			case "small":
				opts.Width = 150
				opts.Height = 150
				opts.Crop = true
			case "medium":
				opts.Width = 300
				opts.Height = 300
				opts.Crop = true
			case "large":
				opts.Width = 600
				opts.Height = 600
				opts.Crop = true
			}
		}

		// Handle format conversion
		switch format {
		case "jpeg", "jpg":
			opts.Format = media.FormatJPEG
		case "png":
			opts.Format = media.FormatPNG
		case "webp":
			opts.Format = media.FormatWebP
		case "gif":
			opts.Format = media.FormatGIF
		}

		// Process image
		result, err := processor.Process(bytes.NewReader(fileBytes), opts)
		if err != nil {
			// On processing error, return original
			ctx.Header("Content-Type", mType.String())
			_, _ = ctx.Writer.Write(fileBytes)
			ctx.Status(http.StatusOK)
			return
		}

		ctx.Header("Content-Type", result.ContentType)
		ctx.Header("X-Image-Width", strconv.Itoa(result.Width))
		ctx.Header("X-Image-Height", strconv.Itoa(result.Height))
		_, _ = ctx.Writer.Write(result.Data)
		ctx.Status(http.StatusOK)
	}
}

func hasBlob(
	services core.Services,
) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		pathParts := strings.Split(ctx.Param("path"), ".")
		_, err := bud01.HasBlob(
			ctx.Request.Context(),
			services,
			pathParts[0],
		)
		if err != nil {
			ctx.AbortWithStatus(http.StatusNotFound)
			return
		}

		ctx.Status(http.StatusOK)
	}
}
