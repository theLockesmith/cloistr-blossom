package gin

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"git.aegis-hq.xyz/coldforge/cloistr-blossom/internal/metrics"
	"git.aegis-hq.xyz/coldforge/cloistr-blossom/src/core"
	"git.aegis-hq.xyz/coldforge/cloistr-blossom/src/pkg/mimetypes"
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
	blobService  core.BlobStorage
	quotaService core.QuotaService
	moderation   core.ModerationService   // nil means no blocklist check
	acr          core.ACRStorage          // nil means no platform access check
	limits       core.UploadLimitsService // nil means no tier limits
	store        core.TusUploadStore
	cdnBaseURL   string
	maxFileBytes int64
	log          *zap.Logger

	sweepMu   sync.Mutex
	lastSweep time.Time
}

// TusConfig contains configuration for the tus handler.
type TusConfig struct {
	// Store holds uploads where every replica can see them. Required: the
	// replicas do not pin a client, so any request may land on any of them.
	Store      core.TusUploadStore
	CDNBaseURL string
	// MaxFileBytes caps a single upload, as max_upload_size_bytes does on the
	// other upload paths. 0 falls back to tusMaxSize.
	MaxFileBytes int64
}

// NewTusHandler creates a new tus protocol handler.
func NewTusHandler(
	blobSvc core.BlobStorage,
	quotaSvc core.QuotaService,
	moderation core.ModerationService,
	acr core.ACRStorage,
	limits core.UploadLimitsService,
	config TusConfig,
	log *zap.Logger,
) (*TusHandler, error) {
	if config.Store == nil {
		return nil, errors.New("tus needs an upload store")
	}

	return &TusHandler{
		blobService:  blobSvc,
		quotaService: quotaSvc,
		moderation:   moderation,
		acr:          acr,
		limits:       limits,
		store:        config.Store,
		cdnBaseURL:   config.CDNBaseURL,
		maxFileBytes: config.MaxFileBytes,
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
		pubkey, ok := ctx.Get(ctxPubkey)
		if !ok {
			ctx.Header("Content-Type", "text/plain")
			ctx.String(http.StatusUnauthorized, "authentication required")
			return
		}

		// Refuse a blocklisted key before any upload exists, as the other
		// upload paths do.
		if blocked, err := h.blocklistCheck(ctx, pubkey.(string)); err != nil {
			ctx.Header("Content-Type", "text/plain")
			ctx.String(http.StatusServiceUnavailable, blocklistUnavailableMessage)
			return
		} else if blocked {
			metrics.BlockedUploadsTotal.Inc()
			ctx.Header("Content-Type", "text/plain")
			ctx.String(http.StatusForbidden, tusBlockedMessage)
			return
		}
		if !h.accessAllowed(ctx, pubkey.(string)) {
			ctx.Header("Content-Type", "text/plain")
			ctx.String(http.StatusForbidden, tusAccessDeniedMessage)
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

		if uploadLength > h.maxSize() {
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

		// Refuse a declared length over the tier cap before any data is sent;
		// the full tier check runs again on the finished upload.
		if h.limits != nil {
			if err := h.limits.ValidateFileSize(ctx, pubkey.(string), int(uploadLength)); err != nil {
				ctx.Header("Content-Type", "text/plain")
				ctx.String(http.StatusRequestEntityTooLarge, err.Error())
				return
			}
		}

		// Parse Upload-Metadata header (optional)
		metadata := h.parseMetadata(ctx.GetHeader("Upload-Metadata"))

		h.maybeSweepExpired()

		// Generate upload ID
		uploadID := uuid.New().String()

		info := core.TusUpload{
			ID:        uploadID,
			Pubkey:    pubkey.(string),
			Size:      uploadLength,
			Offset:    0,
			Metadata:  metadata,
			CreatedAt: time.Now().Unix(),
			ExpiresAt: time.Now().Add(24 * time.Hour).Unix(),
		}

		if err := h.store.Create(ctx.Request.Context(), &info); err != nil {
			h.log.Error("failed to save upload info", zap.Error(err))
			ctx.Header("Content-Type", "text/plain")
			ctx.String(http.StatusInternalServerError, "failed to create upload")
			return
		}

		// Handle creation-with-upload extension
		if ctx.Request.ContentLength > 0 {
			// Client is sending data with the creation request
			chunk, err := h.appendBody(ctx, &info, 0)
			if err != nil {
				h.log.Error("creation-with-upload failed", zap.Error(err))
				_ = h.deleteUpload(uploadID)
				ctx.Header("Content-Type", "text/plain")
				ctx.String(chunkErrorStatus(err), err.Error())
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

		uploadID, ok := uploadIDParam(ctx)
		if !ok {
			ctx.Status(http.StatusNotFound)
			return
		}

		info, err := h.store.Get(ctx.Request.Context(), uploadID)
		if err != nil {
			ctx.Status(lookupErrorStatus(err))
			return
		}
		if !ownsUpload(ctx, info) {
			ctx.Status(http.StatusForbidden)
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

		uploadID, ok := uploadIDParam(ctx)
		if !ok {
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
		info, err := h.store.Get(ctx.Request.Context(), uploadID)
		if err != nil {
			ctx.Header("Content-Type", "text/plain")
			ctx.String(lookupErrorStatus(err), "upload not found")
			return
		}
		if !ownsUpload(ctx, info) {
			ctx.Header("Content-Type", "text/plain")
			ctx.String(http.StatusForbidden, tusNotOwnerMessage)
			return
		}

		// Check expiration
		if time.Now().Unix() > info.ExpiresAt {
			_ = h.deleteUpload(uploadID)
			ctx.Header("Content-Type", "text/plain")
			ctx.String(http.StatusGone, "upload expired")
			return
		}

		// Verify offset matches current position. Once every byte is stored
		// but finalize failed (e.g. 503), a client retrying its last chunk
		// sends an earlier offset; that retry is checked against the stored
		// bytes instead of written.
		finalizeRetry := offset < info.Offset && info.Offset >= info.Size
		if offset != info.Offset && !finalizeRetry {
			ctx.Header("Content-Type", "text/plain")
			ctx.String(http.StatusConflict, fmt.Sprintf("offset mismatch: expected %d, got %d", info.Offset, offset))
			return
		}

		// The key may have been blocklisted since the last chunk; stop it here
		// rather than accept the rest of the upload.
		if blocked, err := h.blocklistCheck(ctx, info.Pubkey); err != nil {
			ctx.Header("Content-Type", "text/plain")
			ctx.String(http.StatusServiceUnavailable, blocklistUnavailableMessage)
			return
		} else if blocked {
			_ = h.deleteUpload(uploadID)
			metrics.BlockedUploadsTotal.Inc()
			ctx.Header("Content-Type", "text/plain")
			ctx.String(http.StatusForbidden, tusBlockedMessage)
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

		// Write chunk. The store refuses it if another request moved the
		// offset first, so two replicas cannot both write at one offset.
		var newOffset int64
		if finalizeRetry {
			newOffset, err = h.matchStoredTail(ctx, info, offset)
		} else {
			newOffset, err = h.appendBody(ctx, info, offset)
		}
		if err != nil {
			if !errors.Is(err, core.ErrTusOffsetConflict) && !errors.Is(err, errTusChunkTooLarge) {
				h.log.Error("chunk upload failed", zap.Error(err))
			}
			ctx.Header("Content-Type", "text/plain")
			ctx.String(chunkErrorStatus(err), err.Error())
			return
		}
		info.Offset = newOffset

		ctx.Header("Upload-Offset", strconv.FormatInt(newOffset, 10))

		// Check if upload is complete
		if newOffset >= info.Size {
			// Finalize upload
			blob, err := h.finalizeUpload(ctx, uploadID, info, expectedChecksum)
			if errors.Is(err, core.ErrPubkeyBlocked) {
				// Blocked after the upload was created; it can never finish.
				_ = h.deleteUpload(uploadID)
				metrics.BlockedUploadsTotal.Inc()
				ctx.Header("Content-Type", "text/plain")
				ctx.String(http.StatusForbidden, tusBlockedMessage)
				return
			}
			if errors.Is(err, errTusBlocklistUnavailable) {
				// Keep the upload: the client can retry the last chunk.
				ctx.Header("Content-Type", "text/plain")
				ctx.String(http.StatusServiceUnavailable, blocklistUnavailableMessage)
				return
			}
			if errors.Is(err, errTusAccessDenied) {
				// Lost access after the upload was created; it can never finish.
				_ = h.deleteUpload(uploadID)
				ctx.Header("Content-Type", "text/plain")
				ctx.String(http.StatusForbidden, tusAccessDeniedMessage)
				return
			}
			if errors.Is(err, core.ErrFileSizeLimit) {
				// Over the current cap; it can never finish.
				_ = h.deleteUpload(uploadID)
				ctx.Header("Content-Type", "text/plain")
				ctx.String(http.StatusRequestEntityTooLarge, "upload too large")
				return
			}
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

		uploadID, ok := uploadIDParam(ctx)
		if !ok {
			ctx.Status(http.StatusNotFound)
			return
		}

		info, err := h.store.Get(ctx.Request.Context(), uploadID)
		if err != nil {
			ctx.Status(lookupErrorStatus(err))
			return
		}
		if !ownsUpload(ctx, info) {
			ctx.Header("Content-Type", "text/plain")
			ctx.String(http.StatusForbidden, tusNotOwnerMessage)
			return
		}

		if err := h.deleteUpload(uploadID); err != nil {
			if errors.Is(err, core.ErrTusUploadNotFound) {
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

// tusNotOwnerMessage is sent when the caller did not create the upload.
const tusNotOwnerMessage = "not your upload"

// ownsUpload reports whether the authenticated caller created the upload.
// Only the creator may append to, inspect or terminate it.
func ownsUpload(ctx *gin.Context, info *core.TusUpload) bool {
	caller := ctx.GetString(ctxPubkey)
	return caller != "" && caller == info.Pubkey
}

// uploadIDParam returns the :id parameter if it is an id this server could
// have issued (a canonical UUID). The id once named a directory, and gin
// passes ".." and "." through as ids, so anything else is refused before it
// reaches the store.
func uploadIDParam(ctx *gin.Context) (string, bool) {
	id := ctx.Param("id")
	u, err := uuid.Parse(id)
	if err != nil || u.String() != id {
		return "", false
	}
	return id, true
}

// maxSize is the largest single upload accepted.
func (h *TusHandler) maxSize() int64 {
	if h.maxFileBytes > 0 {
		return h.maxFileBytes
	}
	return tusMaxSize
}

// setTusHeaders sets standard tus protocol headers.
func (h *TusHandler) setTusHeaders(ctx *gin.Context) {
	ctx.Header("Tus-Resumable", tusVersion)
	ctx.Header("Tus-Version", tusVersion)
	ctx.Header("Tus-Extension", tusExtensions)
	ctx.Header("Tus-Max-Size", strconv.FormatInt(h.maxSize(), 10))
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

// errTusChunkTooLarge means a chunk ran past the declared Upload-Length.
var errTusChunkTooLarge = errors.New("chunk exceeds Upload-Length")

// appendBody stores the request body as the chunk at offset and returns the
// new offset. A body longer than the bytes left in the upload is refused
// whole rather than truncated.
func (h *TusHandler) appendBody(ctx *gin.Context, info *core.TusUpload, offset int64) (int64, error) {
	remaining := info.Size - offset
	data, err := io.ReadAll(io.LimitReader(ctx.Request.Body, remaining+1))
	if err != nil {
		return offset, fmt.Errorf("read chunk: %w", err)
	}
	if int64(len(data)) > remaining {
		return offset, errTusChunkTooLarge
	}
	return h.store.AppendChunk(ctx.Request.Context(), info.ID, offset, data)
}

// matchStoredTail checks a retried last chunk against the bytes already
// stored: it must run from offset to the end and match them exactly. It
// returns the upload's end offset, or ErrTusOffsetConflict if it does not
// match.
func (h *TusHandler) matchStoredTail(ctx *gin.Context, info *core.TusUpload, offset int64) (int64, error) {
	remaining := info.Size - offset
	data, err := io.ReadAll(io.LimitReader(ctx.Request.Body, remaining+1))
	if err != nil {
		return offset, fmt.Errorf("read chunk: %w", err)
	}
	stored, err := h.store.ReadFrom(ctx.Request.Context(), info.ID, offset)
	if err != nil {
		return offset, fmt.Errorf("read upload: %w", err)
	}
	if int64(len(stored)) != remaining || !bytes.Equal(data, stored) {
		return offset, fmt.Errorf("%w: retried chunk does not match the stored upload", core.ErrTusOffsetConflict)
	}
	return info.Size, nil
}

// chunkErrorStatus maps a failed chunk write to its response status.
func chunkErrorStatus(err error) int {
	switch {
	case errors.Is(err, core.ErrTusOffsetConflict):
		return http.StatusConflict
	case errors.Is(err, errTusChunkTooLarge):
		return http.StatusRequestEntityTooLarge
	default:
		return http.StatusInternalServerError
	}
}

// lookupErrorStatus maps a failed upload lookup to its response status: 404
// for no such upload, 500 when the store could not be read.
func lookupErrorStatus(err error) int {
	if errors.Is(err, core.ErrTusUploadNotFound) {
		return http.StatusNotFound
	}
	return http.StatusInternalServerError
}

// tusSweepInterval is how often an upload creation also removes expired
// uploads, so abandoned ones do not pile up in the store.
const tusSweepInterval = time.Hour

func (h *TusHandler) maybeSweepExpired() {
	h.sweepMu.Lock()
	if time.Since(h.lastSweep) < tusSweepInterval {
		h.sweepMu.Unlock()
		return
	}
	h.lastSweep = time.Now()
	h.sweepMu.Unlock()

	go func() {
		n, err := h.store.DeleteExpired(context.Background(), time.Now().Unix())
		if err != nil {
			h.log.Warn("failed to remove expired tus uploads", zap.Error(err))
			return
		}
		if n > 0 {
			h.log.Info("removed expired tus uploads", zap.Int("count", n))
		}
	}()
}

// tusBlockedMessage matches what the other upload paths tell a blocked key.
const tusBlockedMessage = blockedUploaderMessage

// errTusBlocklistUnavailable means the blocklist could not be checked.
var errTusBlocklistUnavailable = errors.New(blocklistUnavailableMessage)

// blocklistCheck reports whether pubkey is on the blocklist. A lookup error is
// returned, not swallowed: callers refuse (503) rather than let the upload
// through, as on every other upload path.
func (h *TusHandler) blocklistCheck(ctx *gin.Context, pubkey string) (bool, error) {
	if h.moderation == nil {
		return false, nil
	}
	return h.moderation.IsBlocked(ctx.Request.Context(), pubkey)
}

// tusAccessDeniedMessage is sent when the platform access rule refuses a key.
const tusAccessDeniedMessage = "upload not allowed for this key"

var errTusAccessDenied = errors.New(tusAccessDeniedMessage)

// accessAllowed applies the platform access rule PUT /upload applies (it
// refuses e.g. a disabled platform account). The rule itself refuses on a
// lookup error.
func (h *TusHandler) accessAllowed(ctx *gin.Context, pubkey string) bool {
	if h.acr == nil {
		return true
	}
	return h.acr.Validate(ctx.Request.Context(), pubkey, core.ResourceUpload) == nil
}

// finalizeUpload completes the upload and creates the blob.
func (h *TusHandler) finalizeUpload(ctx *gin.Context, uploadID string, info *core.TusUpload, expectedChecksum string) (*core.Blob, error) {
	// The key may have been blocklisted or lost access since the upload was
	// created.
	if blocked, err := h.blocklistCheck(ctx, info.Pubkey); err != nil {
		return nil, errTusBlocklistUnavailable
	} else if blocked {
		return nil, core.ErrPubkeyBlocked
	}
	if !h.accessAllowed(ctx, info.Pubkey) {
		return nil, errTusAccessDenied
	}

	data, err := h.store.ReadAll(ctx.Request.Context(), uploadID)
	if err != nil {
		return nil, fmt.Errorf("read upload: %w", err)
	}

	// The cap may have been lowered since the upload was created.
	if int64(len(data)) > h.maxSize() {
		return nil, core.ErrFileSizeLimit
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

	storedType := mimetypes.Resolve(data, mimeType)
	if h.limits != nil {
		if err := h.limits.CheckUpload(ctx, info.Pubkey, storedType, len(data)); err != nil {
			return nil, err
		}
	}

	// Save blob
	url := fmt.Sprintf("%s/%s", h.cdnBaseURL, hashStr)
	blob, newReference, err := h.blobService.SaveWithDedup(
		ctx,
		info.Pubkey,
		hashStr,
		url,
		int64(len(data)),
		storedType,
		data,
		time.Now().Unix(),
		core.EncryptionModeNone,
	)
	if err != nil {
		return nil, fmt.Errorf("save blob: %w", err)
	}
	if h.limits != nil {
		h.limits.RecordUpload(ctx, info.Pubkey)
	}

	// Charge once per blob the key owns (not again for a re-upload).
	if h.quotaService != nil && newReference {
		_ = h.quotaService.IncrementUsage(ctx, info.Pubkey, int64(len(data)))
	}

	// Clean up temp files
	go func() {
		_ = h.deleteUpload(uploadID)
	}()

	return blob, nil
}

// deleteUpload removes an upload and its bytes. It does not use the request
// context, so a client hanging up cannot leave half an upload behind.
func (h *TusHandler) deleteUpload(uploadID string) error {
	return h.store.Delete(context.Background(), uploadID)
}

// RegisterTusRoutes registers tus protocol routes.
func RegisterTusRoutes(r *gin.Engine, handler *TusHandler, authMiddleware gin.HandlerFunc, log *zap.Logger) {
	// OPTIONS for protocol discovery
	r.OPTIONS("/files", handler.tusOptions())
	r.OPTIONS("/files/:id", handler.tusOptions())

	// POST to create new upload (requires auth)
	r.POST("/files", authMiddleware, handler.tusCreate())

	// HEAD to get upload status (creator only)
	r.HEAD("/files/:id", authMiddleware, handler.tusHead())

	// PATCH to resume upload (creator only)
	r.PATCH("/files/:id", authMiddleware, handler.tusPatch())

	// DELETE to terminate upload (requires auth for security)
	r.DELETE("/files/:id", authMiddleware, handler.tusDelete())

	log.Info("tus protocol routes registered")
}
