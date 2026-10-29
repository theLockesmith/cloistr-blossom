package gin

import (
	"fmt"
	"io"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"git.aegis-hq.xyz/coldforge/cloistr-blossom/src/core"
	clerrors "git.aegis-hq.xyz/coldforge/cloistr-common/errors"
)

type BatchController struct {
	batch      core.BatchService
	moderation core.ModerationService // nil means no blocklist check
	acr        core.ACRStorage        // nil means no platform access check
}

func NewBatchController(batch core.BatchService, moderation core.ModerationService, acr core.ACRStorage) *BatchController {
	return &BatchController{batch: batch, moderation: moderation, acr: acr}
}

// BatchUpload handles multi-file uploads via multipart form.
// POST /batch/upload
func (c *BatchController) BatchUpload(ctx *gin.Context) {
	pubkey, exists := ctx.Get(ctxPubkey)
	if !exists {
		clerrors.Unauthorized(clerrors.CodeAuthRequired, "authentication required").Abort(ctx)
		return
	}

	// Same checks as PUT /upload, before the body is read.
	if !c.mayUpload(ctx, pubkey.(string)) {
		return
	}

	// Parse multipart form
	form, err := ctx.MultipartForm()
	if err != nil {
		clerrors.BadRequest(clerrors.CodeInvalidInput, fmt.Sprintf("failed to parse form: %v", err)).Abort(ctx)
		return
	}

	files := form.File["files"]
	if len(files) == 0 {
		clerrors.BadRequest(clerrors.CodeInvalidInput, "no files provided").Abort(ctx)
		return
	}

	// Build request
	req := &core.BatchUploadRequest{
		Pubkey:         pubkey.(string),
		Files:          make([]core.BatchUploadFile, 0, len(files)),
		EncryptionMode: ctx.PostForm("encryption_mode"),
		AllowedHashes:  tokenHashes(ctx),
	}

	// Parse expires_in if provided
	if expiresStr := ctx.PostForm("expires_in"); expiresStr != "" {
		expires, err := strconv.ParseInt(expiresStr, 10, 64)
		if err == nil {
			req.ExpiresIn = expires
		}
	}

	// Process each file
	for _, fileHeader := range files {
		file, err := fileHeader.Open()
		if err != nil {
			clerrors.BadRequest(clerrors.CodeInvalidInput, fmt.Sprintf("failed to open file %s: %v", fileHeader.Filename, err)).Abort(ctx)
			return
		}
		defer file.Close()

		// Read file data
		data, err := io.ReadAll(file)
		if err != nil {
			clerrors.BadRequest(clerrors.CodeInvalidInput, fmt.Sprintf("failed to read file %s: %v", fileHeader.Filename, err)).Abort(ctx)
			return
		}

		mimeType := fileHeader.Header.Get("Content-Type")
		if mimeType == "" {
			mimeType = "application/octet-stream"
		}

		req.Files = append(req.Files, core.BatchUploadFile{
			Filename: fileHeader.Filename,
			MimeType: mimeType,
			Size:     fileHeader.Size,
			Reader:   nil, // Data already read
		})

		// Store the data for processing
		req.Files[len(req.Files)-1].Reader = newBytesReader(data)
	}

	// Process upload
	response, err := c.batch.Upload(ctx.Request.Context(), req)
	if err != nil {
		clerrors.InternalError(clerrors.CodeInternalError, err.Error()).Abort(ctx)
		return
	}

	ctx.JSON(http.StatusOK, response)
}

// tokenHashes returns the hashes the auth token names (its x tags), never nil,
// so a request without them is limited to nothing rather than unrestricted.
func tokenHashes(ctx *gin.Context) []string {
	hashes := ctx.GetStringSlice("xs")
	if hashes == nil {
		hashes = []string{}
	}
	return hashes
}

// mayUpload refuses a blocklisted key or one the platform access rule denies
// (e.g. a disabled account). A blocklist lookup error refuses too: a refused
// upload can be retried, a banned key's stored content cannot be taken back.
func (c *BatchController) mayUpload(ctx *gin.Context, pubkey string) bool {
	if refuseBlockedUploader(ctx, c.moderation, pubkey) {
		return false
	}
	if c.acr != nil {
		if err := c.acr.Validate(ctx.Request.Context(), pubkey, core.ResourceUpload); err != nil {
			clerrors.Forbidden(clerrors.CodeAccessDenied, "upload not allowed for this key").Abort(ctx)
			return false
		}
	}
	return true
}

// BatchDownload creates an archive of multiple blobs.
// POST /batch/download
func (c *BatchController) BatchDownload(ctx *gin.Context) {
	var req core.BatchDownloadRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		clerrors.BadRequest(clerrors.CodeInvalidInput, fmt.Sprintf("invalid request: %v", err)).Abort(ctx)
		return
	}

	if len(req.Hashes) == 0 {
		clerrors.BadRequest(clerrors.CodeInvalidInput, "no hashes provided").Abort(ctx)
		return
	}

	reader, response, err := c.batch.Download(ctx.Request.Context(), &req)
	if err != nil {
		clerrors.InternalError(clerrors.CodeInternalError, err.Error()).Abort(ctx)
		return
	}
	defer reader.Close()

	// Set headers
	ctx.Header("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"", response.Filename))
	ctx.Header("Content-Type", response.ContentType)
	ctx.Header("Content-Length", strconv.FormatInt(response.Size, 10))
	ctx.Header("X-File-Count", strconv.Itoa(response.FileCount))

	// Stream the archive
	ctx.Status(http.StatusOK)
	if _, err := io.Copy(ctx.Writer, reader); err != nil {
		// Can't send error response here, headers already sent
		return
	}
}

// BatchDelete deletes multiple blobs.
// DELETE /batch
func (c *BatchController) BatchDelete(ctx *gin.Context) {
	pubkey, exists := ctx.Get(ctxPubkey)
	if !exists {
		clerrors.Unauthorized(clerrors.CodeAuthRequired, "authentication required").Abort(ctx)
		return
	}

	var req core.BatchDeleteRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		clerrors.BadRequest(clerrors.CodeInvalidInput, fmt.Sprintf("invalid request: %v", err)).Abort(ctx)
		return
	}

	if len(req.Hashes) == 0 {
		clerrors.BadRequest(clerrors.CodeInvalidInput, "no hashes provided").Abort(ctx)
		return
	}

	// The token must name every hash, as DELETE /:hash requires x == hash;
	// otherwise a token signed for one blob deletes them all.
	named := make(map[string]bool)
	for _, h := range tokenHashes(ctx) {
		named[h] = true
	}
	for _, h := range req.Hashes {
		if !named[h] {
			clerrors.Forbidden(clerrors.CodeAccessDenied, fmt.Sprintf("auth token does not authorize deleting %s", h)).Abort(ctx)
			return
		}
	}

	response, err := c.batch.Delete(ctx.Request.Context(), pubkey.(string), &req)
	if err != nil {
		clerrors.InternalError(clerrors.CodeInternalError, err.Error()).Abort(ctx)
		return
	}

	ctx.JSON(http.StatusOK, response)
}

// BatchStatus checks status of multiple blobs.
// POST /batch/status
func (c *BatchController) BatchStatus(ctx *gin.Context) {
	var req core.BatchStatusRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		clerrors.BadRequest(clerrors.CodeInvalidInput, fmt.Sprintf("invalid request: %v", err)).Abort(ctx)
		return
	}

	if len(req.Hashes) == 0 {
		clerrors.BadRequest(clerrors.CodeInvalidInput, "no hashes provided").Abort(ctx)
		return
	}

	response, err := c.batch.Status(ctx.Request.Context(), &req)
	if err != nil {
		clerrors.InternalError(clerrors.CodeInternalError, err.Error()).Abort(ctx)
		return
	}

	ctx.JSON(http.StatusOK, response)
}

// GetBatchJob retrieves a batch job status.
// GET /batch/jobs/:job_id
func (c *BatchController) GetBatchJob(ctx *gin.Context) {
	jobID := ctx.Param("job_id")
	if jobID == "" {
		clerrors.BadRequest(clerrors.CodeInvalidInput, "job_id required").Abort(ctx)
		return
	}

	job, ok := c.ownedJob(ctx, jobID)
	if !ok {
		return
	}

	ctx.JSON(http.StatusOK, job)
}

// ownedJob loads a job for the signed caller: 404 if unknown, 403 if another
// key created it. It answers the request itself when it returns false.
func (c *BatchController) ownedJob(ctx *gin.Context, jobID string) (*core.BatchJob, bool) {
	caller := ctx.GetString(ctxPubkey)
	if caller == "" {
		clerrors.Unauthorized(clerrors.CodeAuthRequired, "authentication required").Abort(ctx)
		return nil, false
	}
	job, err := c.batch.GetJob(ctx.Request.Context(), jobID)
	if err != nil || job == nil {
		clerrors.NotFound(clerrors.CodeResourceNotFound, "job not found").Abort(ctx)
		return nil, false
	}
	if job.Pubkey != caller {
		clerrors.Forbidden(clerrors.CodeAccessDenied, "not your job").Abort(ctx)
		return nil, false
	}
	return job, true
}

// CancelBatchJob cancels a batch job.
// DELETE /batch/jobs/:job_id
func (c *BatchController) CancelBatchJob(ctx *gin.Context) {
	jobID := ctx.Param("job_id")
	if jobID == "" {
		clerrors.BadRequest(clerrors.CodeInvalidInput, "job_id required").Abort(ctx)
		return
	}

	if _, ok := c.ownedJob(ctx, jobID); !ok {
		return
	}

	if err := c.batch.CancelJob(ctx.Request.Context(), jobID); err != nil {
		clerrors.BadRequest(clerrors.CodeInvalidInput, err.Error()).Abort(ctx)
		return
	}

	ctx.JSON(http.StatusOK, gin.H{"status": "cancelled"})
}

// bytesReader wraps a byte slice as an io.Reader
type bytesReader struct {
	data   []byte
	offset int
}

func newBytesReader(data []byte) *bytesReader {
	return &bytesReader{data: data}
}

func (r *bytesReader) Read(p []byte) (n int, err error) {
	if r.offset >= len(r.data) {
		return 0, io.EOF
	}
	n = copy(p, r.data[r.offset:])
	r.offset += n
	return n, nil
}

// RegisterBatchRoutes registers the /batch routes.
func RegisterBatchRoutes(r *gin.Engine, c *BatchController, log *zap.Logger) {
	batch := r.Group("/batch")
	{
		// Upload requires auth
		batch.POST("/upload", nostrAuthMiddleware("upload", log), c.BatchUpload)

		// Download can be done without auth (public blobs) or with auth
		batch.POST("/download", c.BatchDownload)

		// Delete requires auth
		batch.DELETE("", nostrAuthMiddleware("delete", log), c.BatchDelete)

		// Status check doesn't require auth
		batch.POST("/status", c.BatchStatus)

		// Job management
		batch.GET("/jobs/:job_id", nostrAuthMiddleware("get", log), c.GetBatchJob)
		batch.DELETE("/jobs/:job_id", nostrAuthMiddleware("delete", log), c.CancelBatchJob)
	}
}
