package service

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"git.coldforge.xyz/coldforge/cloistr-blossom/internal/storage"
	"git.coldforge.xyz/coldforge/cloistr-blossom/src/core"
	"git.coldforge.xyz/coldforge/cloistr-blossom/src/pkg/hashing"
)

type batchService struct {
	config      core.BatchConfig
	blobStorage core.BlobStorage
	storage     storage.StorageBackend
	quota       core.QuotaService
	expiration  core.ExpirationService
	cdnBaseURL  string
	jobs        sync.Map // jobID -> *core.BatchJob
	log         *zap.Logger
}

// NewBatchService creates a new batch service.
func NewBatchService(
	blobStorage core.BlobStorage,
	storageBackend storage.StorageBackend,
	quota core.QuotaService,
	expiration core.ExpirationService,
	config core.BatchConfig,
	cdnBaseURL string,
	log *zap.Logger,
) (core.BatchService, error) {
	return &batchService{
		config:      config,
		blobStorage: blobStorage,
		storage:     storageBackend,
		quota:       quota,
		expiration:  expiration,
		cdnBaseURL:  cdnBaseURL,
		log:         log,
	}, nil
}

// Upload handles batch upload of multiple files.
func (s *batchService) Upload(ctx context.Context, req *core.BatchUploadRequest) (*core.BatchUploadResponse, error) {
	if len(req.Files) == 0 {
		return nil, fmt.Errorf("no files provided")
	}

	if len(req.Files) > s.config.MaxUploadFiles {
		return nil, fmt.Errorf("too many files: max %d allowed", s.config.MaxUploadFiles)
	}

	// Calculate total size
	var totalSize int64
	for _, f := range req.Files {
		totalSize += f.Size
	}

	if totalSize > s.config.MaxUploadSizeBytes {
		return nil, fmt.Errorf("total size %d exceeds max %d bytes", totalSize, s.config.MaxUploadSizeBytes)
	}

	// Check quota if quota service is available
	if s.quota != nil && s.quota.IsEnabled() {
		if err := s.quota.CheckQuota(ctx, req.Pubkey, totalSize); err != nil {
			return nil, fmt.Errorf("quota check failed: %w", err)
		}
	}

	response := &core.BatchUploadResponse{
		JobID:      uuid.New().String(),
		TotalFiles: len(req.Files),
		Results:    make([]core.BatchItemResult, len(req.Files)),
	}

	// Process files (could be parallelized for large batches)
	encMode := core.EncryptionMode(req.EncryptionMode)
	if encMode == "" {
		encMode = core.EncryptionModeNone
	}

	now := time.Now().Unix()

	for i, file := range req.Files {
		result := core.BatchItemResult{
			Index:    i,
			Filename: file.Filename,
		}

		// Read file data
		data, err := io.ReadAll(file.Reader)
		if err != nil {
			result.Success = false
			result.Error = fmt.Sprintf("failed to read file: %v", err)
			response.FailureCount++
			response.Results[i] = result
			continue
		}

		// Calculate hash
		hash, err := hashing.Hash(data)
		if err != nil {
			result.Success = false
			result.Error = fmt.Sprintf("failed to hash file: %v", err)
			response.FailureCount++
			response.Results[i] = result
			continue
		}
		result.Hash = hash

		// Save blob with deduplication
		blob, _, err := s.blobStorage.SaveWithDedup(
			ctx,
			req.Pubkey,
			hash,
			fmt.Sprintf("%s/%s", s.cdnBaseURL, hash),
			int64(len(data)),
			file.MimeType,
			data,
			now,
			encMode,
		)
		if err != nil {
			result.Success = false
			result.Error = fmt.Sprintf("failed to save: %v", err)
			response.FailureCount++
			response.Results[i] = result
			continue
		}

		result.Success = true
		result.Size = blob.Size
		result.MimeType = blob.Type
		result.URL = blob.Url
		response.SuccessCount++

		// Apply expiration if requested
		if req.ExpiresIn > 0 && s.expiration != nil {
			ttl := time.Duration(req.ExpiresIn) * time.Second
			if err := s.expiration.SetExpirationTTL(ctx, hash, ttl); err != nil {
				s.log.Warn("failed to set expiration", zap.String("hash", hash), zap.Error(err))
			}
		}

		response.Results[i] = result
	}

	// Update quota if successful
	if s.quota != nil && s.quota.IsEnabled() && response.SuccessCount > 0 {
		var successSize int64
		for _, r := range response.Results {
			if r.Success {
				successSize += r.Size
			}
		}
		if err := s.quota.IncrementUsage(ctx, req.Pubkey, successSize); err != nil {
			s.log.Warn("failed to update quota", zap.Error(err))
		}
	}

	return response, nil
}

// Download creates an archive of multiple blobs for download.
func (s *batchService) Download(ctx context.Context, req *core.BatchDownloadRequest) (io.ReadCloser, *core.BatchDownloadResponse, error) {
	if len(req.Hashes) == 0 {
		return nil, nil, fmt.Errorf("no hashes provided")
	}

	if len(req.Hashes) > s.config.MaxDownloadFiles {
		return nil, nil, fmt.Errorf("too many files: max %d allowed", s.config.MaxDownloadFiles)
	}

	format := req.Format
	if format == "" {
		format = "zip"
	}

	// Create buffer for archive
	var buf bytes.Buffer
	var results []core.BatchItemResult
	var totalSize int64
	var fileCount int

	switch format {
	case "zip":
		zw := zip.NewWriter(&buf)

		for i, hash := range req.Hashes {
			result := core.BatchItemResult{
				Index: i,
				Hash:  hash,
			}

			// Get blob metadata
			blob, err := s.blobStorage.GetFromHash(ctx, hash)
			if err != nil {
				result.Success = false
				result.Error = fmt.Sprintf("blob not found: %v", err)
				results = append(results, result)
				continue
			}

			// Read blob data from storage
			reader, err := s.storage.Get(ctx, hash)
			if err != nil {
				result.Success = false
				result.Error = fmt.Sprintf("failed to read: %v", err)
				results = append(results, result)
				continue
			}

			// Create file in zip
			filename := hash
			if blob.Type != "" {
				filename = s.addExtension(hash, blob.Type)
			}

			fw, err := zw.Create(filename)
			if err != nil {
				reader.Close()
				result.Success = false
				result.Error = fmt.Sprintf("failed to create zip entry: %v", err)
				results = append(results, result)
				continue
			}

			n, err := io.Copy(fw, reader)
			reader.Close()
			if err != nil {
				result.Success = false
				result.Error = fmt.Sprintf("failed to write to zip: %v", err)
				results = append(results, result)
				continue
			}

			result.Success = true
			result.Size = n
			result.MimeType = blob.Type
			result.Filename = filename
			results = append(results, result)
			totalSize += n
			fileCount++
		}

		if err := zw.Close(); err != nil {
			return nil, nil, fmt.Errorf("failed to finalize zip: %w", err)
		}

	case "tar":
		tw := tar.NewWriter(&buf)

		for i, hash := range req.Hashes {
			result := core.BatchItemResult{
				Index: i,
				Hash:  hash,
			}

			blob, err := s.blobStorage.GetFromHash(ctx, hash)
			if err != nil {
				result.Success = false
				result.Error = fmt.Sprintf("blob not found: %v", err)
				results = append(results, result)
				continue
			}

			reader, err := s.storage.Get(ctx, hash)
			if err != nil {
				result.Success = false
				result.Error = fmt.Sprintf("failed to read: %v", err)
				results = append(results, result)
				continue
			}

			data, err := io.ReadAll(reader)
			reader.Close()
			if err != nil {
				result.Success = false
				result.Error = fmt.Sprintf("failed to read data: %v", err)
				results = append(results, result)
				continue
			}

			filename := hash
			if blob.Type != "" {
				filename = s.addExtension(hash, blob.Type)
			}

			hdr := &tar.Header{
				Name: filename,
				Mode: 0644,
				Size: int64(len(data)),
			}

			if err := tw.WriteHeader(hdr); err != nil {
				result.Success = false
				result.Error = fmt.Sprintf("failed to write tar header: %v", err)
				results = append(results, result)
				continue
			}

			n, err := tw.Write(data)
			if err != nil {
				result.Success = false
				result.Error = fmt.Sprintf("failed to write to tar: %v", err)
				results = append(results, result)
				continue
			}

			result.Success = true
			result.Size = int64(n)
			result.MimeType = blob.Type
			result.Filename = filename
			results = append(results, result)
			totalSize += int64(n)
			fileCount++
		}

		if err := tw.Close(); err != nil {
			return nil, nil, fmt.Errorf("failed to finalize tar: %w", err)
		}

	case "tar.gz":
		gw := gzip.NewWriter(&buf)
		tw := tar.NewWriter(gw)

		for i, hash := range req.Hashes {
			result := core.BatchItemResult{
				Index: i,
				Hash:  hash,
			}

			blob, err := s.blobStorage.GetFromHash(ctx, hash)
			if err != nil {
				result.Success = false
				result.Error = fmt.Sprintf("blob not found: %v", err)
				results = append(results, result)
				continue
			}

			reader, err := s.storage.Get(ctx, hash)
			if err != nil {
				result.Success = false
				result.Error = fmt.Sprintf("failed to read: %v", err)
				results = append(results, result)
				continue
			}

			data, err := io.ReadAll(reader)
			reader.Close()
			if err != nil {
				result.Success = false
				result.Error = fmt.Sprintf("failed to read data: %v", err)
				results = append(results, result)
				continue
			}

			filename := hash
			if blob.Type != "" {
				filename = s.addExtension(hash, blob.Type)
			}

			hdr := &tar.Header{
				Name: filename,
				Mode: 0644,
				Size: int64(len(data)),
			}

			if err := tw.WriteHeader(hdr); err != nil {
				result.Success = false
				result.Error = fmt.Sprintf("failed to write tar header: %v", err)
				results = append(results, result)
				continue
			}

			n, err := tw.Write(data)
			if err != nil {
				result.Success = false
				result.Error = fmt.Sprintf("failed to write to tar: %v", err)
				results = append(results, result)
				continue
			}

			result.Success = true
			result.Size = int64(n)
			result.MimeType = blob.Type
			result.Filename = filename
			results = append(results, result)
			totalSize += int64(n)
			fileCount++
		}

		if err := tw.Close(); err != nil {
			return nil, nil, fmt.Errorf("failed to finalize tar: %w", err)
		}
		if err := gw.Close(); err != nil {
			return nil, nil, fmt.Errorf("failed to finalize gzip: %w", err)
		}

	default:
		return nil, nil, fmt.Errorf("unsupported format: %s", format)
	}

	contentType := "application/zip"
	extension := ".zip"
	switch format {
	case "tar":
		contentType = "application/x-tar"
		extension = ".tar"
	case "tar.gz":
		contentType = "application/gzip"
		extension = ".tar.gz"
	}

	response := &core.BatchDownloadResponse{
		Filename:    fmt.Sprintf("batch-download-%d%s", time.Now().Unix(), extension),
		Size:        int64(buf.Len()),
		ContentType: contentType,
		FileCount:   fileCount,
		Results:     results,
	}

	return io.NopCloser(&buf), response, nil
}

// Delete removes multiple blobs.
func (s *batchService) Delete(ctx context.Context, pubkey string, req *core.BatchDeleteRequest) (*core.BatchDeleteResponse, error) {
	if len(req.Hashes) == 0 {
		return nil, fmt.Errorf("no hashes provided")
	}

	if len(req.Hashes) > s.config.MaxDeleteFiles {
		return nil, fmt.Errorf("too many files: max %d allowed", s.config.MaxDeleteFiles)
	}

	response := &core.BatchDeleteResponse{
		TotalRequested: len(req.Hashes),
		Results:        make([]core.BatchItemResult, len(req.Hashes)),
	}

	var totalFreedSize int64

	for i, hash := range req.Hashes {
		result := core.BatchItemResult{
			Index: i,
			Hash:  hash,
		}

		// Check if user has reference to this blob
		hasRef, err := s.blobStorage.HasReference(ctx, pubkey, hash)
		if err != nil {
			result.Success = false
			result.Error = fmt.Sprintf("failed to check reference: %v", err)
			response.FailureCount++
			response.Results[i] = result
			continue
		}

		if !hasRef {
			result.Success = false
			result.Error = "not found or not authorized"
			response.FailureCount++
			response.Results[i] = result
			continue
		}

		// Get size before deletion for quota update
		blob, err := s.blobStorage.GetFromHash(ctx, hash)
		var blobSize int64
		if err == nil {
			blobSize = blob.Size
		}

		// Delete reference (and blob if last reference)
		_, err = s.blobStorage.DeleteReference(ctx, pubkey, hash)
		if err != nil {
			result.Success = false
			result.Error = fmt.Sprintf("failed to delete: %v", err)
			response.FailureCount++
			response.Results[i] = result
			continue
		}

		result.Success = true
		result.Size = blobSize
		response.SuccessCount++
		totalFreedSize += blobSize
		response.Results[i] = result
	}

	// Update quota
	if s.quota != nil && s.quota.IsEnabled() && totalFreedSize > 0 {
		if err := s.quota.DecrementUsage(ctx, pubkey, totalFreedSize); err != nil {
			s.log.Warn("failed to update quota after deletion", zap.Error(err))
		}
	}

	return response, nil
}

// Status checks the status of multiple blobs.
func (s *batchService) Status(ctx context.Context, req *core.BatchStatusRequest) (*core.BatchStatusResponse, error) {
	if len(req.Hashes) == 0 {
		return nil, fmt.Errorf("no hashes provided")
	}

	response := &core.BatchStatusResponse{
		Items: make([]core.BatchStatusItem, len(req.Hashes)),
	}

	for i, hash := range req.Hashes {
		item := core.BatchStatusItem{
			Hash: hash,
		}

		exists, err := s.blobStorage.Exists(ctx, hash)
		if err != nil {
			item.Exists = false
			response.Items[i] = item
			continue
		}

		item.Exists = exists

		if exists {
			blob, err := s.blobStorage.GetFromHash(ctx, hash)
			if err == nil {
				item.Size = blob.Size
				item.MimeType = blob.Type
				item.Created = blob.Uploaded
				item.URL = blob.Url
			}
		}

		response.Items[i] = item
	}

	return response, nil
}

// GetJob retrieves the status of an async batch job.
func (s *batchService) GetJob(ctx context.Context, jobID string) (*core.BatchJob, error) {
	if job, ok := s.jobs.Load(jobID); ok {
		return job.(*core.BatchJob), nil
	}
	return nil, fmt.Errorf("job not found: %s", jobID)
}

// CancelJob cancels a pending or in-progress batch job.
func (s *batchService) CancelJob(ctx context.Context, jobID string) error {
	if job, ok := s.jobs.Load(jobID); ok {
		j := job.(*core.BatchJob)
		if j.Status == core.BatchStatusPending || j.Status == core.BatchStatusProcessing {
			j.Status = core.BatchStatusFailed
			j.Error = "cancelled"
			j.CompletedAt = time.Now().Unix()
			return nil
		}
		return fmt.Errorf("job cannot be cancelled in status: %s", j.Status)
	}
	return fmt.Errorf("job not found: %s", jobID)
}

// CleanupExpiredJobs removes old job records.
func (s *batchService) CleanupExpiredJobs(ctx context.Context) (int, error) {
	cutoff := time.Now().Add(-s.config.JobTTL).Unix()
	var count int

	s.jobs.Range(func(key, value interface{}) bool {
		job := value.(*core.BatchJob)
		if job.CompletedAt > 0 && job.CompletedAt < cutoff {
			s.jobs.Delete(key)
			count++
		}
		return true
	})

	return count, nil
}

// addExtension adds a file extension based on MIME type.
func (s *batchService) addExtension(filename, mimeType string) string {
	extensions := map[string]string{
		"image/jpeg":      ".jpg",
		"image/png":       ".png",
		"image/gif":       ".gif",
		"image/webp":      ".webp",
		"image/svg+xml":   ".svg",
		"video/mp4":       ".mp4",
		"video/webm":      ".webm",
		"video/quicktime": ".mov",
		"audio/mpeg":      ".mp3",
		"audio/wav":       ".wav",
		"audio/ogg":       ".ogg",
		"application/pdf": ".pdf",
		"text/plain":      ".txt",
		"text/html":       ".html",
		"text/css":        ".css",
		"application/json": ".json",
		"application/xml": ".xml",
	}

	if ext, ok := extensions[mimeType]; ok {
		return filename + ext
	}
	return filename
}

// Ensure interface compliance
var _ core.BatchService = (*batchService)(nil)
