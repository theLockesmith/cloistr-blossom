package core

import (
	"context"
	"errors"
)

var (
	// ErrReportNotFound is returned when a report does not exist.
	ErrReportNotFound = errors.New("report not found")
	// ErrPubkeyBlocked is returned when a blocked pubkey attempts an operation.
	ErrPubkeyBlocked = errors.New("pubkey is blocked")
	// ErrAlreadyBlocked is returned when trying to block an already blocked pubkey.
	ErrAlreadyBlocked = errors.New("pubkey is already blocked")
	// ErrHashRemoved is returned when trying to upload a removed blob hash.
	ErrHashRemoved = errors.New("blob hash has been removed and cannot be re-uploaded")
)

// ReportReason represents the type of content being reported.
type ReportReason string

const (
	ReportReasonCSAM      ReportReason = "csam"
	ReportReasonIllegal   ReportReason = "illegal"
	ReportReasonCopyright ReportReason = "copyright"
	ReportReasonAbuse     ReportReason = "abuse"
	ReportReasonOther     ReportReason = "other"
)

// ReportStatus represents the current state of a report.
type ReportStatus string

const (
	ReportStatusPending   ReportStatus = "pending"
	ReportStatusReviewed  ReportStatus = "reviewed"
	ReportStatusActioned  ReportStatus = "actioned"
	ReportStatusDismissed ReportStatus = "dismissed"
)

// ReportAction represents the action taken on a report.
type ReportAction string

const (
	ReportActionNone       ReportAction = "none"
	ReportActionRemoved    ReportAction = "removed"
	ReportActionUserBanned ReportAction = "user_banned"
)

// Report represents a content report.
type Report struct {
	ID             int32
	ReporterPubkey string // Empty for anonymous reports
	BlobHash       string
	BlobURL        string
	Reason         ReportReason
	Details        string
	Status         ReportStatus
	ActionTaken    ReportAction
	ReviewedBy     string
	CreatedAt      int64
	ReviewedAt     int64
}

// BlocklistEntry represents a blocked pubkey.
type BlocklistEntry struct {
	Pubkey    string
	Reason    string
	BlockedBy string
	CreatedAt int64
}

// TransparencyStats contains public statistics about moderation actions.
type TransparencyStats struct {
	TotalReports     int64
	ReportsActioned  int64
	ReportsDismissed int64
	BlobsRemoved     int64
	UsersBanned      int64
	LastUpdated      int64
}

// RemovedBlob represents a blob hash that has been removed and cannot be re-uploaded.
type RemovedBlob struct {
	Hash      string
	Reason    string
	RemovedBy string
	ReportID  int32
	CreatedAt int64
}

// ReportService handles content reports.
type ReportService interface {
	// CreateReport creates a new report.
	CreateReport(ctx context.Context, reporterPubkey, blobHash, blobURL string, reason ReportReason, details string) (*Report, error)

	// GetReport returns a report by ID.
	GetReport(ctx context.Context, id int32) (*Report, error)

	// ListPendingReports returns reports awaiting review.
	ListPendingReports(ctx context.Context, limit, offset int) ([]*Report, error)

	// ListAllReports returns all reports.
	ListAllReports(ctx context.Context, limit, offset int) ([]*Report, error)

	// ListReportsByStatus returns reports with a specific status.
	ListReportsByStatus(ctx context.Context, status ReportStatus, limit, offset int) ([]*Report, error)

	// GetReportCountForBlob returns how many times a blob has been reported.
	GetReportCountForBlob(ctx context.Context, blobHash string) (int64, error)

	// GetPendingReportCount returns the number of pending reports.
	GetPendingReportCount(ctx context.Context) (int64, error)

	// ReviewReport marks a report as reviewed with an action.
	ReviewReport(ctx context.Context, id int32, status ReportStatus, action ReportAction, reviewerPubkey string) error

	// GetTransparencyStats returns public moderation statistics.
	GetTransparencyStats(ctx context.Context) (*TransparencyStats, error)
}

// BlocklistService manages the pubkey blocklist.
type BlocklistService interface {
	// IsBlocked checks if a pubkey is blocked.
	IsBlocked(ctx context.Context, pubkey string) (bool, error)

	// AddToBlocklist blocks a pubkey.
	AddToBlocklist(ctx context.Context, pubkey, reason, blockedBy string) (*BlocklistEntry, error)

	// RemoveFromBlocklist unblocks a pubkey.
	RemoveFromBlocklist(ctx context.Context, pubkey string) error

	// GetBlocklistEntry returns details about a blocked pubkey.
	GetBlocklistEntry(ctx context.Context, pubkey string) (*BlocklistEntry, error)

	// ListBlocklist returns all blocked pubkeys.
	ListBlocklist(ctx context.Context, limit, offset int) ([]*BlocklistEntry, error)

	// GetBlocklistCount returns the total number of blocked pubkeys.
	GetBlocklistCount(ctx context.Context) (int64, error)
}

// ModerationService combines report and blocklist functionality.
type ModerationService interface {
	ReportService
	BlocklistService

	// ActionReport takes action on a report (remove blob, ban user, etc.)
	// This is a convenience method that handles the full workflow.
	ActionReport(ctx context.Context, reportID int32, action ReportAction, reviewerPubkey string) error

	// Removed blob tracking (BUD-09)

	// IsHashRemoved checks if a blob hash has been removed and cannot be re-uploaded.
	IsHashRemoved(ctx context.Context, hash string) (bool, error)

	// AddRemovedBlob marks a blob hash as removed to prevent re-upload.
	AddRemovedBlob(ctx context.Context, hash, reason, removedBy string, reportID int32) error

	// GetRemovedBlob returns details about a removed blob hash.
	GetRemovedBlob(ctx context.Context, hash string) (*RemovedBlob, error)
}
