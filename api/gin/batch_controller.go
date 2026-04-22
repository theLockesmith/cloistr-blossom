package gin

import (
	"fmt"
	"io"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"git.aegis-hq.xyz/coldforge/cloistr-blossom/src/core"
)

type BatchController struct {
	batch core.BatchService
}

func NewBatchController(batch core.BatchService) *BatchController {
	return &BatchController{batch: batch}
}

// BatchUpload handles multi-file uploads via multipart form.
// POST /batch/upload
func (c *BatchController) BatchUpload(ctx *gin.Context) {
	pubkey, exists := ctx.Get("pubkey")
	if !exists {
		ctx.JSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
		return
	}

	// Parse multipart form
	form, err := ctx.MultipartForm()
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("failed to parse form: %v", err)})
		return
	}

	files := form.File["files"]
	if len(files) == 0 {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "no files provided"})
		return
	}

	// Build request
	req := &core.BatchUploadRequest{
		Pubkey:         pubkey.(string),
		Files:          make([]core.BatchUploadFile, 0, len(files)),
		EncryptionMode: ctx.PostForm("encryption_mode"),
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
			ctx.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("failed to open file %s: %v", fileHeader.Filename, err)})
			return
		}
		defer file.Close()

		// Read file data
		data, err := io.ReadAll(file)
		if err != nil {
			ctx.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("failed to read file %s: %v", fileHeader.Filename, err)})
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
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	ctx.JSON(http.StatusOK, response)
}

// BatchDownload creates an archive of multiple blobs.
// POST /batch/download
func (c *BatchController) BatchDownload(ctx *gin.Context) {
	var req core.BatchDownloadRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("invalid request: %v", err)})
		return
	}

	if len(req.Hashes) == 0 {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "no hashes provided"})
		return
	}

	reader, response, err := c.batch.Download(ctx.Request.Context(), &req)
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
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
	pubkey, exists := ctx.Get("pubkey")
	if !exists {
		ctx.JSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
		return
	}

	var req core.BatchDeleteRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("invalid request: %v", err)})
		return
	}

	if len(req.Hashes) == 0 {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "no hashes provided"})
		return
	}

	response, err := c.batch.Delete(ctx.Request.Context(), pubkey.(string), &req)
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	ctx.JSON(http.StatusOK, response)
}

// BatchStatus checks status of multiple blobs.
// POST /batch/status
func (c *BatchController) BatchStatus(ctx *gin.Context) {
	var req core.BatchStatusRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("invalid request: %v", err)})
		return
	}

	if len(req.Hashes) == 0 {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "no hashes provided"})
		return
	}

	response, err := c.batch.Status(ctx.Request.Context(), &req)
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	ctx.JSON(http.StatusOK, response)
}

// GetBatchJob retrieves a batch job status.
// GET /batch/jobs/:job_id
func (c *BatchController) GetBatchJob(ctx *gin.Context) {
	jobID := ctx.Param("job_id")
	if jobID == "" {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "job_id required"})
		return
	}

	job, err := c.batch.GetJob(ctx.Request.Context(), jobID)
	if err != nil {
		ctx.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}

	ctx.JSON(http.StatusOK, job)
}

// CancelBatchJob cancels a batch job.
// DELETE /batch/jobs/:job_id
func (c *BatchController) CancelBatchJob(ctx *gin.Context) {
	jobID := ctx.Param("job_id")
	if jobID == "" {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "job_id required"})
		return
	}

	if err := c.batch.CancelJob(ctx.Request.Context(), jobID); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
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
