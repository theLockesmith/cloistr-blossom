package core

import (
	"context"
	"time"
)

// NotificationType represents the type of notification.
type NotificationType string

const (
	NotificationTypeUploadProgress    NotificationType = "upload_progress"
	NotificationTypeUploadComplete    NotificationType = "upload_complete"
	NotificationTypeUploadFailed      NotificationType = "upload_failed"
	NotificationTypeTranscodeProgress NotificationType = "transcode_progress"
	NotificationTypeTranscodeComplete NotificationType = "transcode_complete"
	NotificationTypeTranscodeFailed   NotificationType = "transcode_failed"
	NotificationTypePinProgress       NotificationType = "pin_progress"
	NotificationTypePinComplete       NotificationType = "pin_complete"
	NotificationTypePinFailed         NotificationType = "pin_failed"
	NotificationTypeQuotaWarning      NotificationType = "quota_warning"
	NotificationTypeError             NotificationType = "error"
)

// Notification represents a real-time notification message.
type Notification struct {
	Type      NotificationType `json:"type"`
	Timestamp int64            `json:"timestamp"`
	Pubkey    string           `json:"pubkey,omitempty"`

	// Upload progress
	UploadID       string  `json:"upload_id,omitempty"`
	BytesReceived  int64   `json:"bytes_received,omitempty"`
	TotalBytes     int64   `json:"total_bytes,omitempty"`
	ProgressPct    float64 `json:"progress_pct,omitempty"`
	ChunksReceived int     `json:"chunks_received,omitempty"`
	TotalChunks    int     `json:"total_chunks,omitempty"`

	// Blob info (for complete notifications)
	BlobHash string `json:"blob_hash,omitempty"`
	BlobURL  string `json:"blob_url,omitempty"`
	BlobSize int64  `json:"blob_size,omitempty"`
	MimeType string `json:"mime_type,omitempty"`

	// Transcode progress
	TranscodeJobID string `json:"transcode_job_id,omitempty"`
	Quality        string `json:"quality,omitempty"`
	Codec          string `json:"codec,omitempty"`

	// Error info
	Error   string `json:"error,omitempty"`
	Details string `json:"details,omitempty"`

	// Quota warning
	QuotaUsed  int64 `json:"quota_used,omitempty"`
	QuotaLimit int64 `json:"quota_limit,omitempty"`
	QuotaPct   int   `json:"quota_pct,omitempty"`
}

// NotificationService handles real-time notifications.
type NotificationService interface {
	// Subscribe registers a client for notifications.
	// Returns a channel for receiving notifications and a function to unsubscribe.
	Subscribe(ctx context.Context, pubkey string) (<-chan *Notification, func())

	// Publish sends a notification to all subscribers for a pubkey.
	Publish(ctx context.Context, notification *Notification)

	// PublishToAll sends a notification to all connected clients.
	PublishToAll(ctx context.Context, notification *Notification)

	// GetConnectedClients returns the number of connected clients.
	GetConnectedClients() int

	// GetClientCount returns the number of connected clients for a pubkey.
	GetClientCount(pubkey string) int
}

// NewUploadProgressNotification creates an upload progress notification.
func NewUploadProgressNotification(pubkey, uploadID string, bytesReceived, totalBytes int64, chunksReceived, totalChunks int) *Notification {
	var progressPct float64
	if totalBytes > 0 {
		progressPct = float64(bytesReceived) / float64(totalBytes) * 100
	}

	return &Notification{
		Type:           NotificationTypeUploadProgress,
		Timestamp:      time.Now().Unix(),
		Pubkey:         pubkey,
		UploadID:       uploadID,
		BytesReceived:  bytesReceived,
		TotalBytes:     totalBytes,
		ProgressPct:    progressPct,
		ChunksReceived: chunksReceived,
		TotalChunks:    totalChunks,
	}
}

// NewUploadCompleteNotification creates an upload complete notification.
func NewUploadCompleteNotification(pubkey, uploadID, blobHash, blobURL, mimeType string, blobSize int64) *Notification {
	return &Notification{
		Type:      NotificationTypeUploadComplete,
		Timestamp: time.Now().Unix(),
		Pubkey:    pubkey,
		UploadID:  uploadID,
		BlobHash:  blobHash,
		BlobURL:   blobURL,
		BlobSize:  blobSize,
		MimeType:  mimeType,
	}
}

// NewTranscodeProgressNotification creates a transcode progress notification.
func NewTranscodeProgressNotification(pubkey, blobHash, jobID, quality, codec string, progressPct float64) *Notification {
	return &Notification{
		Type:           NotificationTypeTranscodeProgress,
		Timestamp:      time.Now().Unix(),
		Pubkey:         pubkey,
		BlobHash:       blobHash,
		TranscodeJobID: jobID,
		Quality:        quality,
		Codec:          codec,
		ProgressPct:    progressPct,
	}
}

// NewTranscodeCompleteNotification creates a transcode complete notification.
func NewTranscodeCompleteNotification(pubkey, blobHash, jobID string) *Notification {
	return &Notification{
		Type:           NotificationTypeTranscodeComplete,
		Timestamp:      time.Now().Unix(),
		Pubkey:         pubkey,
		BlobHash:       blobHash,
		TranscodeJobID: jobID,
	}
}

// NewErrorNotification creates an error notification.
func NewErrorNotification(pubkey, errorMsg, details string) *Notification {
	return &Notification{
		Type:      NotificationTypeError,
		Timestamp: time.Now().Unix(),
		Pubkey:    pubkey,
		Error:     errorMsg,
		Details:   details,
	}
}

// NewQuotaWarningNotification creates a quota warning notification.
func NewQuotaWarningNotification(pubkey string, used, limit int64, pct int) *Notification {
	return &Notification{
		Type:       NotificationTypeQuotaWarning,
		Timestamp:  time.Now().Unix(),
		Pubkey:     pubkey,
		QuotaUsed:  used,
		QuotaLimit: limit,
		QuotaPct:   pct,
	}
}
