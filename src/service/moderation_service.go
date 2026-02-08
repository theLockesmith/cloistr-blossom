package service

import (
	"context"
	"database/sql"
	"time"

	"git.coldforge.xyz/coldforge/coldforge-blossom/db"
	"git.coldforge.xyz/coldforge/coldforge-blossom/src/core"
	"go.uber.org/zap"
)

type moderationService struct {
	queries     *db.Queries
	blobService core.BlobStorage
	quotaService core.QuotaService
	log         *zap.Logger
}

// NewModerationService creates a new moderation service.
func NewModerationService(
	queries *db.Queries,
	blobService core.BlobStorage,
	quotaService core.QuotaService,
	log *zap.Logger,
) (core.ModerationService, error) {
	return &moderationService{
		queries:      queries,
		blobService:  blobService,
		quotaService: quotaService,
		log:          log,
	}, nil
}

// CreateReport creates a new report.
func (s *moderationService) CreateReport(ctx context.Context, reporterPubkey, blobHash, blobURL string, reason core.ReportReason, details string) (*core.Report, error) {
	var reporterPubkeyNull sql.NullString
	if reporterPubkey != "" {
		reporterPubkeyNull = sql.NullString{String: reporterPubkey, Valid: true}
	}

	var detailsNull sql.NullString
	if details != "" {
		detailsNull = sql.NullString{String: details, Valid: true}
	}

	dbReport, err := s.queries.CreateReport(ctx, db.CreateReportParams{
		ReporterPubkey: reporterPubkeyNull,
		BlobHash:       blobHash,
		BlobUrl:        blobURL,
		Reason:         string(reason),
		Details:        detailsNull,
		CreatedAt:      time.Now().Unix(),
	})
	if err != nil {
		return nil, err
	}

	return s.dbReportToCore(dbReport), nil
}

// GetReport returns a report by ID.
func (s *moderationService) GetReport(ctx context.Context, id int32) (*core.Report, error) {
	dbReport, err := s.queries.GetReport(ctx, id)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, core.ErrReportNotFound
		}
		return nil, err
	}

	return s.dbReportToCore(dbReport), nil
}

// ListPendingReports returns reports awaiting review.
func (s *moderationService) ListPendingReports(ctx context.Context, limit, offset int) ([]*core.Report, error) {
	dbReports, err := s.queries.ListPendingReports(ctx, db.ListPendingReportsParams{
		Limit:  int32(limit),
		Offset: int32(offset),
	})
	if err != nil {
		return nil, err
	}

	reports := make([]*core.Report, len(dbReports))
	for i, r := range dbReports {
		reports[i] = s.dbReportToCore(r)
	}
	return reports, nil
}

// ListAllReports returns all reports.
func (s *moderationService) ListAllReports(ctx context.Context, limit, offset int) ([]*core.Report, error) {
	dbReports, err := s.queries.ListAllReports(ctx, db.ListAllReportsParams{
		Limit:  int32(limit),
		Offset: int32(offset),
	})
	if err != nil {
		return nil, err
	}

	reports := make([]*core.Report, len(dbReports))
	for i, r := range dbReports {
		reports[i] = s.dbReportToCore(r)
	}
	return reports, nil
}

// ListReportsByStatus returns reports with a specific status.
func (s *moderationService) ListReportsByStatus(ctx context.Context, status core.ReportStatus, limit, offset int) ([]*core.Report, error) {
	dbReports, err := s.queries.ListReportsByStatus(ctx, db.ListReportsByStatusParams{
		Status: string(status),
		Limit:  int32(limit),
		Offset: int32(offset),
	})
	if err != nil {
		return nil, err
	}

	reports := make([]*core.Report, len(dbReports))
	for i, r := range dbReports {
		reports[i] = s.dbReportToCore(r)
	}
	return reports, nil
}

// GetReportCountForBlob returns how many times a blob has been reported.
func (s *moderationService) GetReportCountForBlob(ctx context.Context, blobHash string) (int64, error) {
	return s.queries.GetReportCountByBlobHash(ctx, blobHash)
}

// GetPendingReportCount returns the number of pending reports.
func (s *moderationService) GetPendingReportCount(ctx context.Context) (int64, error) {
	return s.queries.GetPendingReportCount(ctx)
}

// ReviewReport marks a report as reviewed with an action.
func (s *moderationService) ReviewReport(ctx context.Context, id int32, status core.ReportStatus, action core.ReportAction, reviewerPubkey string) error {
	return s.queries.UpdateReportStatus(ctx, db.UpdateReportStatusParams{
		Status:     string(status),
		ActionTaken: sql.NullString{String: string(action), Valid: true},
		ReviewedBy: sql.NullString{String: reviewerPubkey, Valid: true},
		ReviewedAt: sql.NullInt64{Int64: time.Now().Unix(), Valid: true},
		ID:         id,
	})
}

// GetTransparencyStats returns public moderation statistics.
func (s *moderationService) GetTransparencyStats(ctx context.Context) (*core.TransparencyStats, error) {
	stats, err := s.queries.GetReportStats(ctx)
	if err != nil {
		return nil, err
	}

	blockedCount, err := s.queries.GetBlocklistCount(ctx)
	if err != nil {
		return nil, err
	}

	return &core.TransparencyStats{
		TotalReports:     stats.TotalReports,
		ReportsActioned:  stats.ReportsActioned,
		ReportsDismissed: stats.ReportsDismissed,
		BlobsRemoved:     stats.BlobsRemoved,
		UsersBanned:      blockedCount,
		LastUpdated:      time.Now().Unix(),
	}, nil
}

// IsBlocked checks if a pubkey is blocked.
func (s *moderationService) IsBlocked(ctx context.Context, pubkey string) (bool, error) {
	return s.queries.IsBlocked(ctx, pubkey)
}

// AddToBlocklist blocks a pubkey.
func (s *moderationService) AddToBlocklist(ctx context.Context, pubkey, reason, blockedBy string) (*core.BlocklistEntry, error) {
	entry, err := s.queries.AddToBlocklist(ctx, db.AddToBlocklistParams{
		Pubkey:    pubkey,
		Reason:    reason,
		BlockedBy: blockedBy,
		CreatedAt: time.Now().Unix(),
	})
	if err != nil {
		return nil, err
	}

	return &core.BlocklistEntry{
		Pubkey:    entry.Pubkey,
		Reason:    entry.Reason,
		BlockedBy: entry.BlockedBy,
		CreatedAt: entry.CreatedAt,
	}, nil
}

// RemoveFromBlocklist unblocks a pubkey.
func (s *moderationService) RemoveFromBlocklist(ctx context.Context, pubkey string) error {
	return s.queries.RemoveFromBlocklist(ctx, pubkey)
}

// GetBlocklistEntry returns details about a blocked pubkey.
func (s *moderationService) GetBlocklistEntry(ctx context.Context, pubkey string) (*core.BlocklistEntry, error) {
	entry, err := s.queries.GetBlocklistEntry(ctx, pubkey)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, core.ErrPubkeyBlocked
		}
		return nil, err
	}

	return &core.BlocklistEntry{
		Pubkey:    entry.Pubkey,
		Reason:    entry.Reason,
		BlockedBy: entry.BlockedBy,
		CreatedAt: entry.CreatedAt,
	}, nil
}

// ListBlocklist returns all blocked pubkeys.
func (s *moderationService) ListBlocklist(ctx context.Context, limit, offset int) ([]*core.BlocklistEntry, error) {
	entries, err := s.queries.ListBlocklist(ctx, db.ListBlocklistParams{
		Limit:  int32(limit),
		Offset: int32(offset),
	})
	if err != nil {
		return nil, err
	}

	result := make([]*core.BlocklistEntry, len(entries))
	for i, e := range entries {
		result[i] = &core.BlocklistEntry{
			Pubkey:    e.Pubkey,
			Reason:    e.Reason,
			BlockedBy: e.BlockedBy,
			CreatedAt: e.CreatedAt,
		}
	}
	return result, nil
}

// GetBlocklistCount returns the total number of blocked pubkeys.
func (s *moderationService) GetBlocklistCount(ctx context.Context) (int64, error) {
	return s.queries.GetBlocklistCount(ctx)
}

// ActionReport takes action on a report (remove blob, ban user, etc.)
func (s *moderationService) ActionReport(ctx context.Context, reportID int32, action core.ReportAction, reviewerPubkey string) error {
	report, err := s.GetReport(ctx, reportID)
	if err != nil {
		return err
	}

	switch action {
	case core.ReportActionRemoved:
		// Get the blob to find the owner
		blob, err := s.blobService.GetFromHash(ctx, report.BlobHash)
		if err == nil && blob != nil {
			// Delete the blob
			if err := s.blobService.DeleteFromHash(ctx, report.BlobHash); err != nil {
				s.log.Error("failed to delete blob", zap.Error(err), zap.String("hash", report.BlobHash))
			}

			// Decrement user's usage
			if s.quotaService != nil && blob.Pubkey != "" {
				if err := s.quotaService.DecrementUsage(ctx, blob.Pubkey, blob.Size); err != nil {
					s.log.Error("failed to decrement user usage", zap.Error(err))
				}
			}
		}

	case core.ReportActionUserBanned:
		// Get the blob to find the owner
		blob, err := s.blobService.GetFromHash(ctx, report.BlobHash)
		if err == nil && blob != nil && blob.Pubkey != "" {
			// Ban the user
			if s.quotaService != nil {
				if err := s.quotaService.BanUser(ctx, blob.Pubkey); err != nil {
					s.log.Error("failed to ban user", zap.Error(err))
				}
			}

			// Add to blocklist
			if _, err := s.AddToBlocklist(ctx, blob.Pubkey, "Report action: "+string(report.Reason), reviewerPubkey); err != nil {
				s.log.Error("failed to add to blocklist", zap.Error(err))
			}

			// Delete the blob
			if err := s.blobService.DeleteFromHash(ctx, report.BlobHash); err != nil {
				s.log.Error("failed to delete blob", zap.Error(err), zap.String("hash", report.BlobHash))
			}
		}
	}

	// Update report status
	return s.ReviewReport(ctx, reportID, core.ReportStatusActioned, action, reviewerPubkey)
}

// dbReportToCore converts a database report to a core report.
func (s *moderationService) dbReportToCore(r db.Report) *core.Report {
	report := &core.Report{
		ID:        r.ID,
		BlobHash:  r.BlobHash,
		BlobURL:   r.BlobUrl,
		Reason:    core.ReportReason(r.Reason),
		Status:    core.ReportStatus(r.Status),
		CreatedAt: r.CreatedAt,
	}

	if r.ReporterPubkey.Valid {
		report.ReporterPubkey = r.ReporterPubkey.String
	}
	if r.Details.Valid {
		report.Details = r.Details.String
	}
	if r.ActionTaken.Valid {
		report.ActionTaken = core.ReportAction(r.ActionTaken.String)
	}
	if r.ReviewedBy.Valid {
		report.ReviewedBy = r.ReviewedBy.String
	}
	if r.ReviewedAt.Valid {
		report.ReviewedAt = r.ReviewedAt.Int64
	}

	return report
}
