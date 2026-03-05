package gin

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"git.coldforge.xyz/coldforge/cloistr-blossom/src/core"
)

const (
	// TUS protocol version
	tusVersion = "1.0.0"

	// TUS extension support
	tusExtensions = "creation,creation-with-upload,termination,concatenation"

	// Maximum upload size (100GB)
	tusMaxSize = 100 * 1024 * 1024 * 1024

	// Checksum algorithms supported
	tusChecksumAlgorithms = "sha256"
)

// TusHandler implements the tus resumable upload protocol.
type TusHandler struct {
	blobService   core.BlobStorage
	quotaService  core.QuotaService
	tempDir       string
	cdnBaseURL    string
	log           *zap.Logger
}

// TusConfig contains configuration for the tus handler.
type TusConfig struct {
	TempDir    string
	CDNBaseURL string
}

// NewTusHandler creates a new tus protocol handler.
func NewTusHandler(
	blobSvc core.BlobStorage,
	quotaSvc core.QuotaService,
	config TusConfig,
	log *zap.Logger,
) (*TusHandler, error) {
	tempDir := config.TempDir
	if tempDir == "" {
		tempDir = "/tmp/blossom-tus"
	}

	// Ensure temp directory exists
	if err := os.MkdirAll(tempDir, 0755); err != nil {
		return nil, fmt.Errorf("create temp dir: %w", err)
	}

	return &TusHandler{
		blobService:  blobSvc,
		quotaService: quotaSvc,
		tempDir:      tempDir,
		cdnBaseURL:   config.CDNBaseURL,
		log:          log,
	}, nil
}

// tusOptions handles OPTIONS /files
// Returns tus protocol capabilities.
func (h *TusHandler) tusOptions() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		h.setTusHeaders(ctx)
		ctx.Status(http.StatusNoContent)
	}
}

// tusCreate handles POST /files
// Creates a new upload resource.
func (h *TusHandler) tusCreate() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		h.setTusHeaders(ctx)

		// Get pubkey from auth
		pubkey, ok := ctx.Get("pubkey")
		if !ok {
			ctx.Header("Content-Type", "text/plain")
			ctx.String(http.StatusUnauthorized, "authentication required")
			return
		}

		// Parse Upload-Length header (required for creation)
		uploadLengthStr := ctx.GetHeader("Upload-Length")
		if uploadLengthStr == "" {
			ctx.Header("Content-Type", "text/plain")
			ctx.String(http.StatusBadRequest, "Upload-Length header required")
			return
		}

		uploadLength, err := strconv.ParseInt(uploadLengthStr, 10, 64)
		if err != nil || uploadLength < 0 {
			ctx.Header("Content-Type", "text/plain")
			ctx.String(http.StatusBadRequest, "invalid Upload-Length")
			return
		}

		if uploadLength > tusMaxSize {
			ctx.Header("Content-Type", "text/plain")
			ctx.String(http.StatusRequestEntityTooLarge, "upload too large")
			return
		}

		// Check quota
		if h.quotaService != nil {
			if err := h.quotaService.CheckQuota(ctx, pubkey.(string), uploadLength); err != nil {
				ctx.Header("Content-Type", "text/plain")
				ctx.String(http.StatusForbidden, "quota exceeded")
				return
			}
		}

		// Parse Upload-Metadata header (optional)
		metadata := h.parseMetadata(ctx.GetHeader("Upload-Metadata"))

		// Generate upload ID
		uploadID := uuid.New().String()

		// Create upload directory
		uploadDir := filepath.Join(h.tempDir, uploadID)
		if err := os.MkdirAll(uploadDir, 0755); err != nil {
			h.log.Error("failed to create upload directory", zap.Error(err))
			ctx.Header("Content-Type", "text/plain")
			ctx.String(http.StatusInternalServerError, "failed to create upload")
			return
		}

		// Create info file
		info := tusUploadInfo{
			ID:           uploadID,
			Pubkey:       pubkey.(string),
			Size:         uploadLength,
			Offset:       0,
			Metadata:     metadata,
			CreatedAt:    time.Now().Unix(),
			ExpiresAt:    time.Now().Add(24 * time.Hour).Unix(),
		}

		if err := h.saveUploadInfo(uploadID, &info); err != nil {
			h.log.Error("failed to save upload info", zap.Error(err))
			_ = os.RemoveAll(uploadDir)
			ctx.Header("Content-Type", "text/plain")
			ctx.String(http.StatusInternalServerError, "failed to create upload")
			return
		}

		// Create empty data file
		dataPath := filepath.Join(uploadDir, "data")
		f, err := os.Create(dataPath)
		if err != nil {
			h.log.Error("failed to create data file", zap.Error(err))
			_ = os.RemoveAll(uploadDir)
			ctx.Header("Content-Type", "text/plain")
			ctx.String(http.StatusInternalServerError, "failed to create upload")
			return
		}
		f.Close()

		// Handle creation-with-upload extension
		if ctx.Request.ContentLength > 0 {
			// Client is sending data with the creation request
			chunk, err := h.handleChunkUpload(ctx, uploadID, 0, uploadLength)
			if err != nil {
				h.log.Error("creation-with-upload failed", zap.Error(err))
				_ = os.RemoveAll(uploadDir)
				ctx.Header("Content-Type", "text/plain")
				ctx.String(http.StatusInternalServerError, err.Error())
				return
			}
			ctx.Header("Upload-Offset", strconv.FormatInt(chunk, 10))
		}

		// Return location of new resource
		location := fmt.Sprintf("%s/files/%s", h.cdnBaseURL, uploadID)
		ctx.Header("Location", location)

		h.log.Info("tus upload created",
			zap.String("upload_id", uploadID),
			zap.String("pubkey", pubkey.(string)),
			zap.Int64("size", uploadLength))

		ctx.Status(http.StatusCreated)
	}
}

// tusHead handles HEAD /files/:id
// Returns current upload status.
func (h *TusHandler) tusHead() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		h.setTusHeaders(ctx)

		uploadID := ctx.Param("id")
		if uploadID == "" {
			ctx.Status(http.StatusNotFound)
			return
		}

		info, err := h.loadUploadInfo(uploadID)
		if err != nil {
			ctx.Status(http.StatusNotFound)
			return
		}

		// Check expiration
		if time.Now().Unix() > info.ExpiresAt {
			_ = h.deleteUpload(uploadID)
			ctx.Status(http.StatusGone)
			return
		}

		ctx.Header("Upload-Offset", strconv.FormatInt(info.Offset, 10))
		ctx.Header("Upload-Length", strconv.FormatInt(info.Size, 10))
		ctx.Header("Cache-Control", "no-store")

		ctx.Status(http.StatusOK)
	}
}

// tusPatch handles PATCH /files/:id
// Resumes upload from current offset.
func (h *TusHandler) tusPatch() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		h.setTusHeaders(ctx)

		uploadID := ctx.Param("id")
		if uploadID == "" {
			ctx.Header("Content-Type", "text/plain")
			ctx.String(http.StatusNotFound, "upload not found")
			return
		}

		// Verify Content-Type
		contentType := ctx.GetHeader("Content-Type")
		if contentType != "application/offset+octet-stream" {
			ctx.Header("Content-Type", "text/plain")
			ctx.String(http.StatusUnsupportedMediaType, "Content-Type must be application/offset+octet-stream")
			return
		}

		// Parse Upload-Offset header
		offsetStr := ctx.GetHeader("Upload-Offset")
		if offsetStr == "" {
			ctx.Header("Content-Type", "text/plain")
			ctx.String(http.StatusBadRequest, "Upload-Offset header required")
			return
		}

		offset, err := strconv.ParseInt(offsetStr, 10, 64)
		if err != nil || offset < 0 {
			ctx.Header("Content-Type", "text/plain")
			ctx.String(http.StatusBadRequest, "invalid Upload-Offset")
			return
		}

		// Load upload info
		info, err := h.loadUploadInfo(uploadID)
		if err != nil {
			ctx.Header("Content-Type", "text/plain")
			ctx.String(http.StatusNotFound, "upload not found")
			return
		}

		// Check expiration
		if time.Now().Unix() > info.ExpiresAt {
			_ = h.deleteUpload(uploadID)
			ctx.Header("Content-Type", "text/plain")
			ctx.String(http.StatusGone, "upload expired")
			return
		}

		// Verify offset matches current position
		if offset != info.Offset {
			ctx.Header("Content-Type", "text/plain")
			ctx.String(http.StatusConflict, fmt.Sprintf("offset mismatch: expected %d, got %d", info.Offset, offset))
			return
		}

		// Handle checksum if provided
		checksumHeader := ctx.GetHeader("Upload-Checksum")
		var expectedChecksum string
		if checksumHeader != "" {
			parts := strings.SplitN(checksumHeader, " ", 2)
			if len(parts) == 2 && parts[0] == "sha256" {
				expectedChecksum = parts[1]
			}
		}

		// Write chunk
		newOffset, err := h.handleChunkUpload(ctx, uploadID, offset, info.Size)
		if err != nil {
			h.log.Error("chunk upload failed", zap.Error(err))
			ctx.Header("Content-Type", "text/plain")
			ctx.String(http.StatusInternalServerError, err.Error())
			return
		}

		// Update info
		info.Offset = newOffset
		if err := h.saveUploadInfo(uploadID, info); err != nil {
			h.log.Error("failed to update upload info", zap.Error(err))
		}

		ctx.Header("Upload-Offset", strconv.FormatInt(newOffset, 10))

		// Check if upload is complete
		if newOffset >= info.Size {
			// Finalize upload
			blob, err := h.finalizeUpload(ctx, uploadID, info, expectedChecksum)
			if err != nil {
				h.log.Error("finalize upload failed", zap.Error(err))
				ctx.Header("Content-Type", "text/plain")
				ctx.String(http.StatusInternalServerError, err.Error())
				return
			}

			h.log.Info("tus upload completed",
				zap.String("upload_id", uploadID),
				zap.String("hash", blob.Sha256),
				zap.Int64("size", blob.Size))
		}

		ctx.Status(http.StatusNoContent)
	}
}

// tusDelete handles DELETE /files/:id
// Terminates an upload.
func (h *TusHandler) tusDelete() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		h.setTusHeaders(ctx)

		uploadID := ctx.Param("id")
		if uploadID == "" {
			ctx.Status(http.StatusNotFound)
			return
		}

		if err := h.deleteUpload(uploadID); err != nil {
			if os.IsNotExist(err) {
				ctx.Status(http.StatusNotFound)
				return
			}
			h.log.Error("delete upload failed", zap.Error(err))
			ctx.Header("Content-Type", "text/plain")
			ctx.String(http.StatusInternalServerError, "delete failed")
			return
		}

		h.log.Info("tus upload terminated", zap.String("upload_id", uploadID))
		ctx.Status(http.StatusNoContent)
	}
}

// setTusHeaders sets standard tus protocol headers.
func (h *TusHandler) setTusHeaders(ctx *gin.Context) {
	ctx.Header("Tus-Resumable", tusVersion)
	ctx.Header("Tus-Version", tusVersion)
	ctx.Header("Tus-Extension", tusExtensions)
	ctx.Header("Tus-Max-Size", strconv.FormatInt(tusMaxSize, 10))
	ctx.Header("Tus-Checksum-Algorithm", tusChecksumAlgorithms)
}

// parseMetadata parses the Upload-Metadata header.
func (h *TusHandler) parseMetadata(header string) map[string]string {
	metadata := make(map[string]string)
	if header == "" {
		return metadata
	}

	pairs := strings.Split(header, ",")
	for _, pair := range pairs {
		pair = strings.TrimSpace(pair)
		parts := strings.SplitN(pair, " ", 2)
		if len(parts) == 2 {
			key := parts[0]
			value, err := base64.StdEncoding.DecodeString(parts[1])
			if err == nil {
				metadata[key] = string(value)
			}
		} else if len(parts) == 1 {
			metadata[parts[0]] = ""
		}
	}

	return metadata
}

// tusUploadInfo stores metadata about an upload.
type tusUploadInfo struct {
	ID        string            `json:"id"`
	Pubkey    string            `json:"pubkey"`
	Size      int64             `json:"size"`
	Offset    int64             `json:"offset"`
	Metadata  map[string]string `json:"metadata"`
	CreatedAt int64             `json:"created_at"`
	ExpiresAt int64             `json:"expires_at"`
}

// saveUploadInfo saves upload info to disk.
func (h *TusHandler) saveUploadInfo(uploadID string, info *tusUploadInfo) error {
	infoPath := filepath.Join(h.tempDir, uploadID, "info.json")
	data := fmt.Sprintf(`{"id":"%s","pubkey":"%s","size":%d,"offset":%d,"created_at":%d,"expires_at":%d}`,
		info.ID, info.Pubkey, info.Size, info.Offset, info.CreatedAt, info.ExpiresAt)
	return os.WriteFile(infoPath, []byte(data), 0644)
}

// loadUploadInfo loads upload info from disk.
func (h *TusHandler) loadUploadInfo(uploadID string) (*tusUploadInfo, error) {
	infoPath := filepath.Join(h.tempDir, uploadID, "info.json")
	data, err := os.ReadFile(infoPath)
	if err != nil {
		return nil, err
	}

	// Simple parsing (in production, use proper JSON)
	info := &tusUploadInfo{ID: uploadID}

	// Parse fields manually to avoid import overhead
	str := string(data)
	if idx := strings.Index(str, `"pubkey":"`); idx >= 0 {
		end := strings.Index(str[idx+10:], `"`)
		if end >= 0 {
			info.Pubkey = str[idx+10 : idx+10+end]
		}
	}
	if idx := strings.Index(str, `"size":`); idx >= 0 {
		end := strings.IndexAny(str[idx+7:], ",}")
		if end >= 0 {
			info.Size, _ = strconv.ParseInt(str[idx+7:idx+7+end], 10, 64)
		}
	}
	if idx := strings.Index(str, `"offset":`); idx >= 0 {
		end := strings.IndexAny(str[idx+9:], ",}")
		if end >= 0 {
			info.Offset, _ = strconv.ParseInt(str[idx+9:idx+9+end], 10, 64)
		}
	}
	if idx := strings.Index(str, `"created_at":`); idx >= 0 {
		end := strings.IndexAny(str[idx+13:], ",}")
		if end >= 0 {
			info.CreatedAt, _ = strconv.ParseInt(str[idx+13:idx+13+end], 10, 64)
		}
	}
	if idx := strings.Index(str, `"expires_at":`); idx >= 0 {
		end := strings.IndexAny(str[idx+13:], ",}")
		if end >= 0 {
			info.ExpiresAt, _ = strconv.ParseInt(str[idx+13:idx+13+end], 10, 64)
		}
	}

	return info, nil
}

// handleChunkUpload handles writing a chunk to the data file.
func (h *TusHandler) handleChunkUpload(ctx *gin.Context, uploadID string, offset, totalSize int64) (int64, error) {
	dataPath := filepath.Join(h.tempDir, uploadID, "data")

	// Open file for appending
	f, err := os.OpenFile(dataPath, os.O_WRONLY|os.O_CREATE, 0644)
	if err != nil {
		return offset, fmt.Errorf("open data file: %w", err)
	}
	defer f.Close()

	// Seek to offset
	if _, err := f.Seek(offset, 0); err != nil {
		return offset, fmt.Errorf("seek to offset: %w", err)
	}

	// Copy data from request body
	written, err := io.Copy(f, ctx.Request.Body)
	if err != nil {
		return offset, fmt.Errorf("write data: %w", err)
	}

	newOffset := offset + written

	// Don't exceed total size
	if newOffset > totalSize {
		return totalSize, nil
	}

	return newOffset, nil
}

// finalizeUpload completes the upload and creates the blob.
func (h *TusHandler) finalizeUpload(ctx *gin.Context, uploadID string, info *tusUploadInfo, expectedChecksum string) (*core.Blob, error) {
	dataPath := filepath.Join(h.tempDir, uploadID, "data")

	// Read the complete file
	data, err := os.ReadFile(dataPath)
	if err != nil {
		return nil, fmt.Errorf("read data file: %w", err)
	}

	// Calculate hash
	hash := sha256.Sum256(data)
	hashStr := hex.EncodeToString(hash[:])

	// Verify checksum if provided
	if expectedChecksum != "" {
		expectedBytes, err := base64.StdEncoding.DecodeString(expectedChecksum)
		if err == nil && hex.EncodeToString(expectedBytes) != hashStr {
			return nil, fmt.Errorf("checksum mismatch")
		}
	}

	// Determine MIME type from metadata
	mimeType := info.Metadata["filetype"]
	if mimeType == "" {
		mimeType = info.Metadata["content-type"]
	}
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}

	// Save blob
	url := fmt.Sprintf("%s/%s", h.cdnBaseURL, hashStr)
	blob, _, err := h.blobService.SaveWithDedup(
		ctx,
		info.Pubkey,
		hashStr,
		url,
		int64(len(data)),
		mimeType,
		data,
		time.Now().Unix(),
		core.EncryptionModeNone,
	)
	if err != nil {
		return nil, fmt.Errorf("save blob: %w", err)
	}

	// Update quota
	if h.quotaService != nil {
		_ = h.quotaService.IncrementUsage(ctx, info.Pubkey, int64(len(data)))
	}

	// Clean up temp files
	go func() {
		_ = h.deleteUpload(uploadID)
	}()

	return blob, nil
}

// deleteUpload removes an upload and its files.
func (h *TusHandler) deleteUpload(uploadID string) error {
	uploadDir := filepath.Join(h.tempDir, uploadID)
	return os.RemoveAll(uploadDir)
}

// RegisterTusRoutes registers tus protocol routes.
func RegisterTusRoutes(r *gin.Engine, handler *TusHandler, authMiddleware gin.HandlerFunc, log *zap.Logger) {
	// OPTIONS for protocol discovery
	r.OPTIONS("/files", handler.tusOptions())
	r.OPTIONS("/files/:id", handler.tusOptions())

	// POST to create new upload (requires auth)
	r.POST("/files", authMiddleware, handler.tusCreate())

	// HEAD to get upload status
	r.HEAD("/files/:id", handler.tusHead())

	// PATCH to resume upload
	r.PATCH("/files/:id", handler.tusPatch())

	// DELETE to terminate upload (requires auth for security)
	r.DELETE("/files/:id", authMiddleware, handler.tusDelete())

	log.Info("tus protocol routes registered")
}
