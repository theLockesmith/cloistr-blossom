package core

import (
	"context"
	"errors"
)

var (
	// ErrQuotaExceeded is returned when a user would exceed their storage quota.
	ErrQuotaExceeded = errors.New("storage quota exceeded")
	// ErrUserBanned is returned when a banned user attempts an operation.
	ErrUserBanned = errors.New("user is banned")
	// ErrUserNotFound is returned when a user does not exist.
	ErrUserNotFound = errors.New("user not found")
)

// User represents a user with quota information.
type User struct {
	Pubkey     string
	QuotaBytes int64
	UsedBytes  int64
	IsBanned   bool
	CreatedAt  int64
	UpdatedAt  int64
}

// QuotaInfo contains quota usage information for a user.
type QuotaInfo struct {
	QuotaBytes     int64   // Total quota in bytes
	UsedBytes      int64   // Currently used bytes
	AvailableBytes int64   // Remaining bytes
	UsagePercent   float64 // Usage as percentage (0-100)
}

// QuotaService manages user storage quotas.
type QuotaService interface {
	// GetUser returns user information including quota.
	// Returns ErrUserNotFound if the user doesn't exist.
	GetUser(ctx context.Context, pubkey string) (*User, error)

	// GetOrCreateUser gets an existing user or creates one with default quota.
	GetOrCreateUser(ctx context.Context, pubkey string) (*User, error)

	// GetQuotaInfo returns detailed quota information for a user.
	GetQuotaInfo(ctx context.Context, pubkey string) (*QuotaInfo, error)

	// CheckQuota verifies if a user can store additional bytes.
	// Returns ErrQuotaExceeded if the upload would exceed quota.
	// Returns ErrUserBanned if the user is banned.
	CheckQuota(ctx context.Context, pubkey string, additionalBytes int64) error

	// IncrementUsage increases a user's used storage.
	// Called after a successful upload.
	IncrementUsage(ctx context.Context, pubkey string, bytes int64) error

	// DecrementUsage decreases a user's used storage.
	// Called after a successful delete.
	DecrementUsage(ctx context.Context, pubkey string, bytes int64) error

	// SetQuota sets the quota limit for a user.
	SetQuota(ctx context.Context, pubkey string, quotaBytes int64) error

	// BanUser bans a user from uploads.
	BanUser(ctx context.Context, pubkey string) error

	// UnbanUser removes the ban from a user.
	UnbanUser(ctx context.Context, pubkey string) error

	// ListUsers returns a paginated list of users.
	ListUsers(ctx context.Context, limit, offset int64) ([]*User, error)

	// GetUserCount returns the total number of users.
	GetUserCount(ctx context.Context) (int64, error)

	// RecalculateUsage recalculates a user's storage usage from their blobs.
	RecalculateUsage(ctx context.Context, pubkey string) error

	// IsEnabled returns whether quota enforcement is enabled.
	IsEnabled() bool
}
