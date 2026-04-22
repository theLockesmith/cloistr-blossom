package gin

import (
	"io"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"git.aegis-hq.xyz/coldforge/cloistr-blossom/src/core"
)

// ChunkedUploadHandler holds the chunked upload service reference.
type ChunkedUploadHandler struct {
	service    core.ChunkedUploadService
	cdnBaseURL string
}

// NewChunkedUploadHandler creates a new chunked upload handler.
func NewChunkedUploadHandler(service core.ChunkedUploadService, cdnBaseURL string) *ChunkedUploadHandler {
	return &ChunkedUploadHandler{
		service:    service,
		cdnBaseURL: cdnBaseURL,
	}
}

// createChunkedSession handles POST /upload/chunked
// Creates a new chunked upload session.
func (h *ChunkedUploadHandler) createChunkedSession() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		pubkey, ok := ctx.Get("pubkey")
		if !ok {
			ctx.JSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
			return
		}

		var req struct {
			TotalSize      int64  `json:"total_size" binding:"required,gt=0"`
			ChunkSize      int64  `json:"chunk_size,omitempty"`
			MimeType       string `json:"mime_type,omitempty"`
			Hash           string `json:"hash,omitempty"`
			EncryptionMode string `json:"encryption_mode,omitempty"`
			TTL            int64  `json:"ttl,omitempty"`
		}

		if err := ctx.ShouldBindJSON(&req); err != nil {
			ctx.JSON(http.StatusBadRequest, gin.H{"error": "invalid request: " + err.Error()})
			return
		}

		response, err := h.service.CreateSession(ctx, &core.CreateSessionRequest{
			Pubkey:         pubkey.(string),
			TotalSize:      req.TotalSize,
			ChunkSize:      req.ChunkSize,
			MimeType:       req.MimeType,
			Hash:           req.Hash,
			EncryptionMode: req.EncryptionMode,
			TTL:            req.TTL,
		})
		if err != nil {
			ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		ctx.JSON(http.StatusCreated, response)
	}
}

// uploadChunk handles PUT /upload/chunked/:session_id/:chunk_num
// Uploads a single chunk.
func (h *ChunkedUploadHandler) uploadChunk() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		sessionID := ctx.Param("session_id")
		chunkNumStr := ctx.Param("chunk_num")

		if sessionID == "" {
			ctx.JSON(http.StatusBadRequest, gin.H{"error": "session_id required"})
			return
		}

		chunkNum, err := strconv.Atoi(chunkNumStr)
		if err != nil {
			ctx.JSON(http.StatusBadRequest, gin.H{"error": "invalid chunk_num"})
			return
		}

		// Read chunk data
		data, err := io.ReadAll(ctx.Request.Body)
		if err != nil {
			ctx.JSON(http.StatusBadRequest, gin.H{"error": "failed to read request body"})
			return
		}

		chunk, err := h.service.UploadChunk(ctx, sessionID, chunkNum, data)
		if err != nil {
			switch err {
			case core.ErrSessionNotFound:
				ctx.JSON(http.StatusNotFound, gin.H{"error": "session not found"})
			case core.ErrSessionExpired:
				ctx.JSON(http.StatusGone, gin.H{"error": "session expired"})
			case core.ErrSessionAborted:
				ctx.JSON(http.StatusGone, gin.H{"error": "session aborted"})
			case core.ErrSessionComplete:
				ctx.JSON(http.StatusConflict, gin.H{"error": "session already complete"})
			case core.ErrChunkAlreadyUploaded:
				ctx.JSON(http.StatusConflict, gin.H{"error": "chunk already uploaded"})
			case core.ErrChunkOutOfRange:
				ctx.JSON(http.StatusBadRequest, gin.H{"error": "chunk number out of range"})
			default:
				ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			}
			return
		}

		ctx.JSON(http.StatusOK, chunk)
	}
}

// completeUpload handles POST /upload/chunked/:session_id/complete
// Finalizes the upload and creates the blob.
func (h *ChunkedUploadHandler) completeUpload() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		sessionID := ctx.Param("session_id")

		if sessionID == "" {
			ctx.JSON(http.StatusBadRequest, gin.H{"error": "session_id required"})
			return
		}

		var req struct {
			Hash string `json:"hash,omitempty"` // Optional final hash verification
		}
		// Ignore error - hash is optional
		_ = ctx.ShouldBindJSON(&req)

		response, err := h.service.CompleteUpload(ctx, sessionID, req.Hash)
		if err != nil {
			switch err {
			case core.ErrSessionNotFound:
				ctx.JSON(http.StatusNotFound, gin.H{"error": "session not found"})
			case core.ErrSessionExpired:
				ctx.JSON(http.StatusGone, gin.H{"error": "session expired"})
			case core.ErrSessionAborted:
				ctx.JSON(http.StatusGone, gin.H{"error": "session aborted"})
			case core.ErrSessionComplete:
				ctx.JSON(http.StatusConflict, gin.H{"error": "session already complete"})
			case core.ErrUploadIncomplete:
				ctx.JSON(http.StatusPreconditionFailed, gin.H{"error": err.Error()})
			case core.ErrHashMismatch:
				ctx.JSON(http.StatusPreconditionFailed, gin.H{"error": err.Error()})
			default:
				ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			}
			return
		}

		ctx.JSON(http.StatusOK, response)
	}
}

// abortUpload handles DELETE /upload/chunked/:session_id
// Aborts an upload session and cleans up resources.
func (h *ChunkedUploadHandler) abortUpload() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		sessionID := ctx.Param("session_id")

		if sessionID == "" {
			ctx.JSON(http.StatusBadRequest, gin.H{"error": "session_id required"})
			return
		}

		err := h.service.AbortUpload(ctx, sessionID)
		if err != nil {
			if err == core.ErrSessionNotFound {
				ctx.JSON(http.StatusNotFound, gin.H{"error": "session not found"})
				return
			}
			ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}

		ctx.JSON(http.StatusOK, gin.H{"status": "aborted"})
	}
}

// getSession handles GET /upload/chunked/:session_id
// Returns session information and progress.
func (h *ChunkedUploadHandler) getSession() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		sessionID := ctx.Param("session_id")

		if sessionID == "" {
			ctx.JSON(http.StatusBadRequest, gin.H{"error": "session_id required"})
			return
		}

		session, err := h.service.GetSession(ctx, sessionID)
		if err != nil {
			if err == core.ErrSessionNotFound {
				ctx.JSON(http.StatusNotFound, gin.H{"error": "session not found"})
				return
			}
			ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}

		// Include chunks info
		chunks, _ := h.service.GetChunks(ctx, sessionID)

		// Calculate missing chunks
		missingChunks := []int{}
		receivedMap := make(map[int]bool)
		for _, c := range chunks {
			receivedMap[c.ChunkNum] = true
		}
		for i := 0; i < session.TotalChunks; i++ {
			if !receivedMap[i] {
				missingChunks = append(missingChunks, i)
			}
		}

		ctx.JSON(http.StatusOK, gin.H{
			"session":        session,
			"chunks":         chunks,
			"missing_chunks": missingChunks,
			"progress":       float64(session.BytesReceived) / float64(session.TotalSize) * 100,
		})
	}
}

// RegisterChunkedUploadRoutes registers chunked upload routes.
func RegisterChunkedUploadRoutes(r *gin.Engine, handler *ChunkedUploadHandler, authMiddleware gin.HandlerFunc) {
	// Create session requires auth
	r.POST("/upload/chunked", authMiddleware, handler.createChunkedSession())

	// Upload chunk - session ownership is verified internally
	r.PUT("/upload/chunked/:session_id/:chunk_num", handler.uploadChunk())

	// Complete upload - session ownership is verified internally
	r.POST("/upload/chunked/:session_id/complete", handler.completeUpload())

	// Abort upload - session ownership is verified internally
	r.DELETE("/upload/chunked/:session_id", handler.abortUpload())

	// Get session status - useful for resuming
	r.GET("/upload/chunked/:session_id", handler.getSession())
}
