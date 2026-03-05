package core

import (
	"context"
	"io"
	"time"
)

// BatchOperationType represents the type of batch operation.
type BatchOperationType string

const (
	BatchOperationUpload   BatchOperationType = "upload"
	BatchOperationDownload BatchOperationType = "download"
	BatchOperationDelete   BatchOperationType = "delete"
	BatchOperationStatus   BatchOperationType = "status"
)

// BatchJobStatus represents the status of a batch job.
type BatchJobStatus string

const (
	BatchStatusPending    BatchJobStatus = "pending"
	BatchStatusProcessing BatchJobStatus = "processing"
	BatchStatusComplete   BatchJobStatus = "complete"
	BatchStatusFailed     BatchJobStatus = "failed"
	BatchStatusPartial    BatchJobStatus = "partial" // Some items succeeded, some failed
)

// BatchJob represents a batch operation job.
type BatchJob struct {
	ID           string             `json:"id"`
	Type         BatchOperationType `json:"type"`
	Status       BatchJobStatus     `json:"status"`
	Pubkey       string             `json:"pubkey"`
	TotalItems   int                `json:"total_items"`
	ProcessedItems int             `json:"processed_items"`
	SuccessCount int                `json:"success_count"`
	FailureCount int                `json:"failure_count"`
	Progress     float64            `json:"progress"` // 0-100
	CreatedAt    int64              `json:"created_at"`
	StartedAt    int64              `json:"started_at,omitempty"`
	CompletedAt  int64              `json:"completed_at,omitempty"`
	Error        string             `json:"error,omitempty"`
	Results      []BatchItemResult  `json:"results,omitempty"`
}

// BatchItemResult represents the result of a single item in a batch operation.
type BatchItemResult struct {
	Index    int    `json:"index"`
	Hash     string `json:"hash,omitempty"`
	Filename string `json:"filename,omitempty"`
	Success  bool   `json:"success"`
	Error    string `json:"error,omitempty"`
	Size     int64  `json:"size,omitempty"`
	MimeType string `json:"mime_type,omitempty"`
	URL      string `json:"url,omitempty"`
}

// BatchUploadRequest represents a request to upload multiple files.
type BatchUploadRequest struct {
	Pubkey         string            `json:"pubkey"`
	Files          []BatchUploadFile `json:"-"` // Files are handled separately in multipart
	EncryptionMode string            `json:"encryption_mode,omitempty"`
	ExpiresIn      int64             `json:"expires_in,omitempty"` // TTL in seconds
}

// BatchUploadFile represents a single file in a batch upload.
type BatchUploadFile struct {
	Filename string
	MimeType string
	Size     int64
	Reader   io.Reader
}

// BatchUploadResponse represents the response from a batch upload.
type BatchUploadResponse struct {
	JobID        string            `json:"job_id"`
	TotalFiles   int               `json:"total_files"`
	SuccessCount int               `json:"success_count"`
	FailureCount int               `json:"failure_count"`
	Results      []BatchItemResult `json:"results"`
}

// BatchDownloadRequest represents a request to download multiple files.
type BatchDownloadRequest struct {
	Hashes     []string `json:"hashes"`
	Format     string   `json:"format,omitempty"` // "zip" (default), "tar", "tar.gz"
	Flatten    bool     `json:"flatten,omitempty"` // Don't preserve directory structure
}

// BatchDownloadResponse contains metadata about the download.
type BatchDownloadResponse struct {
	JobID       string            `json:"job_id,omitempty"` // For async downloads
	Filename    string            `json:"filename"`
	Size        int64             `json:"size"`
	ContentType string            `json:"content_type"`
	FileCount   int               `json:"file_count"`
	Results     []BatchItemResult `json:"results,omitempty"` // Which files were included
}

// BatchDeleteRequest represents a request to delete multiple blobs.
type BatchDeleteRequest struct {
	Hashes []string `json:"hashes"`
}

// BatchDeleteResponse represents the response from a batch delete.
type BatchDeleteResponse struct {
	TotalRequested int               `json:"total_requested"`
	SuccessCount   int               `json:"success_count"`
	FailureCount   int               `json:"failure_count"`
	Results        []BatchItemResult `json:"results"`
}

// BatchStatusRequest represents a request to check status of multiple blobs.
type BatchStatusRequest struct {
	Hashes []string `json:"hashes"`
}

// BatchStatusItem represents the status of a single blob.
type BatchStatusItem struct {
	Hash      string `json:"hash"`
	Exists    bool   `json:"exists"`
	Size      int64  `json:"size,omitempty"`
	MimeType  string `json:"mime_type,omitempty"`
	Created   int64  `json:"created,omitempty"`
	ExpiresAt int64  `json:"expires_at,omitempty"`
	URL       string `json:"url,omitempty"`
}

// BatchStatusResponse represents the response from a batch status check.
type BatchStatusResponse struct {
	Items []BatchStatusItem `json:"items"`
}

// BatchConfig contains configuration for batch operations.
type BatchConfig struct {
	MaxUploadFiles     int           `yaml:"max_upload_files"`     // Max files per upload batch
	MaxDownloadFiles   int           `yaml:"max_download_files"`   // Max files per download batch
	MaxDeleteFiles     int           `yaml:"max_delete_files"`     // Max files per delete batch
	MaxUploadSizeBytes int64         `yaml:"max_upload_size_bytes"` // Max total size for batch upload
	AsyncThreshold     int           `yaml:"async_threshold"`      // Switch to async processing above this count
	WorkerCount        int           `yaml:"worker_count"`         // Number of parallel workers
	JobTTL             time.Duration `yaml:"job_ttl"`              // How long to keep job results
}

// DefaultBatchConfig returns sensible defaults.
func DefaultBatchConfig() BatchConfig {
	return BatchConfig{
		MaxUploadFiles:     50,
		MaxDownloadFiles:   100,
		MaxDeleteFiles:     100,
		MaxUploadSizeBytes: 500 * 1024 * 1024, // 500 MB
		AsyncThreshold:     10,
		WorkerCount:        4,
		JobTTL:             24 * time.Hour,
	}
}

// BatchService handles batch operations on blobs.
type BatchService interface {
	// Upload handles batch upload of multiple files.
	Upload(ctx context.Context, req *BatchUploadRequest) (*BatchUploadResponse, error)

	// Download creates an archive of multiple blobs for download.
	Download(ctx context.Context, req *BatchDownloadRequest) (io.ReadCloser, *BatchDownloadResponse, error)

	// Delete removes multiple blobs.
	Delete(ctx context.Context, pubkey string, req *BatchDeleteRequest) (*BatchDeleteResponse, error)

	// Status checks the status of multiple blobs.
	Status(ctx context.Context, req *BatchStatusRequest) (*BatchStatusResponse, error)

	// GetJob retrieves the status of an async batch job.
	GetJob(ctx context.Context, jobID string) (*BatchJob, error)

	// CancelJob cancels a pending or in-progress batch job.
	CancelJob(ctx context.Context, jobID string) error

	// CleanupExpiredJobs removes old job records.
	CleanupExpiredJobs(ctx context.Context) (int, error)
}
