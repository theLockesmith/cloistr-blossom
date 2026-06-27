package gin

import (
	"fmt"
	clerrors "git.aegis-hq.xyz/coldforge/cloistr-common/errors"
	"net/http"
	"strconv"

	"git.aegis-hq.xyz/coldforge/cloistr-blossom/src/core"
	"github.com/gin-gonic/gin"
)

// pinBlob handles POST /:hash/pin to pin a blob to IPFS.
func pinBlob(
	services core.Services,
) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		hash := ctx.Param("hash")
		if hash == "" {
			clerrors.BadRequest(clerrors.CodeInvalidInput, "hash is required").Abort(ctx)
			return
		}

		// Check if IPFS is configured
		if !services.IPFS().IsConfigured() {
			clerrors.ServiceUnavailable(clerrors.CodeServiceUnavailable, "IPFS pinning is not configured", 0).Abort(ctx)
			return
		}

		// Check if blob exists
		exists, err := services.Blob().Exists(ctx.Request.Context(), hash)
		if err != nil || !exists {
			clerrors.NotFound(clerrors.CodeResourceNotFound, "blob not found").Abort(ctx)
			return
		}

		// Get optional name from query
		name := ctx.Query("name")

		// Pin the blob
		pin, err := services.IPFS().PinBlob(ctx.Request.Context(), hash, name)
		if err != nil {
			if err == core.ErrIPFSNotConfigured {
				clerrors.ServiceUnavailable(clerrors.CodeServiceUnavailable, "IPFS pinning is not configured", 0).Abort(ctx)
				return
			}
			clerrors.InternalError(clerrors.CodeInternalError, fmt.Sprintf("failed to pin blob: %s", err.Error())).Abort(ctx)
			return
		}

		ctx.JSON(http.StatusAccepted, ipfsPinResponse{
			BlobHash:   pin.BlobHash,
			CID:        pin.CID,
			Status:     string(pin.Status),
			GatewayURL: services.IPFS().GetIPFSGatewayURL(pin.CID),
			Message:    "pin request submitted",
		})
	}
}

// unpinBlob handles DELETE /:hash/pin to unpin a blob from IPFS.
func unpinBlob(
	services core.Services,
) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		hash := ctx.Param("hash")
		if hash == "" {
			clerrors.BadRequest(clerrors.CodeInvalidInput, "hash is required").Abort(ctx)
			return
		}

		// Check if IPFS is configured
		if !services.IPFS().IsConfigured() {
			clerrors.ServiceUnavailable(clerrors.CodeServiceUnavailable, "IPFS pinning is not configured", 0).Abort(ctx)
			return
		}

		err := services.IPFS().UnpinBlob(ctx.Request.Context(), hash)
		if err != nil {
			if err == core.ErrIPFSPinNotFound {
				clerrors.NotFound(clerrors.CodeResourceNotFound, "pin not found").Abort(ctx)
				return
			}
			clerrors.InternalError(clerrors.CodeInternalError, fmt.Sprintf("failed to unpin blob: %s", err.Error())).Abort(ctx)
			return
		}

		ctx.JSON(http.StatusOK, gin.H{
			"message": "blob unpinned successfully",
		})
	}
}

// getPinStatus handles GET /:hash/pin to get the pin status of a blob.
func getPinStatus(
	services core.Services,
) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		hash := ctx.Param("hash")
		if hash == "" {
			clerrors.BadRequest(clerrors.CodeInvalidInput, "hash is required").Abort(ctx)
			return
		}

		// Check if IPFS is configured
		if !services.IPFS().IsConfigured() {
			clerrors.ServiceUnavailable(clerrors.CodeServiceUnavailable, "IPFS pinning is not configured", 0).Abort(ctx)
			return
		}

		pin, err := services.IPFS().GetPinStatus(ctx.Request.Context(), hash)
		if err != nil {
			if err == core.ErrIPFSPinNotFound {
				clerrors.NotFound(clerrors.CodeResourceNotFound, "pin not found").Abort(ctx)
				return
			}
			clerrors.InternalError(clerrors.CodeInternalError, fmt.Sprintf("failed to get pin status: %s", err.Error())).Abort(ctx)
			return
		}

		ctx.JSON(http.StatusOK, ipfsPinResponse{
			BlobHash:   pin.BlobHash,
			CID:        pin.CID,
			Name:       pin.Name,
			Status:     string(pin.Status),
			RequestID:  pin.RequestID,
			GatewayURL: services.IPFS().GetIPFSGatewayURL(pin.CID),
			CreatedAt:  pin.CreatedAt,
			PinnedAt:   pin.PinnedAt,
		})
	}
}

// listPins handles GET /pins to list all IPFS pins.
func listPins(
	services core.Services,
) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		// Check if IPFS is configured
		if !services.IPFS().IsConfigured() {
			clerrors.ServiceUnavailable(clerrors.CodeServiceUnavailable, "IPFS pinning is not configured", 0).Abort(ctx)
			return
		}

		// Parse query parameters
		statusFilter := core.IPFSPinStatus(ctx.Query("status"))
		limit := 100 // default
		if limitStr := ctx.Query("limit"); limitStr != "" {
			if l, err := strconv.Atoi(limitStr); err == nil && l > 0 && l <= 1000 {
				limit = l
			}
		}

		pins, err := services.IPFS().ListPins(ctx.Request.Context(), statusFilter, limit)
		if err != nil {
			clerrors.InternalError(clerrors.CodeInternalError, fmt.Sprintf("failed to list pins: %s", err.Error())).Abort(ctx)
			return
		}

		// Convert to response format
		var response []ipfsPinResponse
		for _, pin := range pins {
			response = append(response, ipfsPinResponse{
				BlobHash:   pin.BlobHash,
				CID:        pin.CID,
				Name:       pin.Name,
				Status:     string(pin.Status),
				RequestID:  pin.RequestID,
				GatewayURL: services.IPFS().GetIPFSGatewayURL(pin.CID),
				CreatedAt:  pin.CreatedAt,
				PinnedAt:   pin.PinnedAt,
			})
		}

		ctx.JSON(http.StatusOK, ipfsPinsListResponse{
			Pins:  response,
			Count: len(response),
		})
	}
}

// ipfsPinResponse is the response for IPFS pin operations.
type ipfsPinResponse struct {
	BlobHash   string `json:"blob_hash"`
	CID        string `json:"cid"`
	Name       string `json:"name,omitempty"`
	Status     string `json:"status"`
	RequestID  string `json:"request_id,omitempty"`
	GatewayURL string `json:"gateway_url,omitempty"`
	Message    string `json:"message,omitempty"`
	CreatedAt  int64  `json:"created_at,omitempty"`
	PinnedAt   int64  `json:"pinned_at,omitempty"`
}

// ipfsPinsListResponse is the response for listing IPFS pins.
type ipfsPinsListResponse struct {
	Pins  []ipfsPinResponse `json:"pins"`
	Count int               `json:"count"`
}
