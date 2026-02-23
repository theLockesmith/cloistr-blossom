package gin

import (
	"fmt"
	"io"
	"net/http"
	"regexp"

	"github.com/gin-gonic/gin"
	goNostr "github.com/nbd-wtf/go-nostr"
	"go.uber.org/zap"

	"git.coldforge.xyz/coldforge/cloistr-blossom/src/core"
)

var validReasons = map[string]bool{
	"csam":      true,
	"illegal":   true,
	"copyright": true,
	"abuse":     true,
	"other":     true,
}

var sha256Regex = regexp.MustCompile(`^[a-fA-F0-9]{64}$`)

// ReportRequest represents a content report submission.
type ReportRequest struct {
	BlobHash       string `json:"blob_hash" binding:"required"`
	BlobURL        string `json:"blob_url"`
	Reason         string `json:"reason" binding:"required"`
	Details        string `json:"details"`
	ReporterPubkey string `json:"reporter_pubkey"`
}

// ReportResponse represents the response for a submitted report.
type ReportResponse struct {
	ID        int32  `json:"id"`
	Status    string `json:"status"`
	Message   string `json:"message"`
	CreatedAt int64  `json:"created_at"`
}

// submitReport handles POST /report
func submitReport(services core.Services) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req ReportRequest
		if err := c.BindJSON(&req); err != nil {
			c.AbortWithStatusJSON(http.StatusBadRequest, apiError{Message: "invalid request body"})
			return
		}

		// Validate reason
		if !validReasons[req.Reason] {
			c.AbortWithStatusJSON(http.StatusBadRequest, apiError{
				Message: "invalid reason: must be one of csam, illegal, copyright, abuse, other",
			})
			return
		}

		// Validate blob hash format
		if !sha256Regex.MatchString(req.BlobHash) {
			c.AbortWithStatusJSON(http.StatusBadRequest, apiError{
				Message: "invalid blob_hash: must be a valid SHA-256 hash",
			})
			return
		}

		// If no URL provided, construct one
		if req.BlobURL == "" {
			req.BlobURL = c.Request.Host + "/" + req.BlobHash
		}

		// Create the report
		report, err := services.Moderation().CreateReport(
			c.Request.Context(),
			req.ReporterPubkey,
			req.BlobHash,
			req.BlobURL,
			core.ReportReason(req.Reason),
			req.Details,
		)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusInternalServerError, apiError{
				Message: "failed to create report",
			})
			return
		}

		c.JSON(http.StatusCreated, ReportResponse{
			ID:        report.ID,
			Status:    string(report.Status),
			Message:   "Report submitted successfully. It will be reviewed by our team.",
			CreatedAt: report.CreatedAt,
		})
	}
}

// BUD09ReportResponse represents the response for a BUD-09 report submission.
type BUD09ReportResponse struct {
	Success   bool    `json:"success"`
	Message   string  `json:"message"`
	ReportIDs []int32 `json:"report_ids,omitempty"`
}

// NIP-56 report types mapped to our reasons
var nip56ToReason = map[string]string{
	"nudity":      "abuse",
	"malware":     "illegal",
	"profanity":   "abuse",
	"illegal":     "illegal",
	"spam":        "abuse",
	"impersonation": "abuse",
	"other":       "other",
	// Additional Blossom-specific mappings
	"csam":        "csam",
	"copyright":   "copyright",
	"abuse":       "abuse",
}

// submitReportBUD09 handles PUT /report (BUD-09 compliant)
// Accepts a signed NIP-56 report event (kind 1984) with blob hashes in x tags
func submitReportBUD09(services core.Services, log *zap.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		// Read request body
		body, err := io.ReadAll(c.Request.Body)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusBadRequest, apiError{Message: "failed to read request body"})
			return
		}

		// Parse as Nostr event
		ev := &goNostr.Event{}
		if err := ev.UnmarshalJSON(body); err != nil {
			c.AbortWithStatusJSON(http.StatusBadRequest, apiError{
				Message: "invalid request body: must be a valid Nostr event JSON",
			})
			return
		}

		// Verify signature
		ok, err := ev.CheckSignature()
		if !ok || err != nil {
			c.AbortWithStatusJSON(http.StatusBadRequest, apiError{
				Message: "invalid event signature",
			})
			return
		}

		// Must be kind 1984 (NIP-56 report)
		if ev.Kind != 1984 {
			c.AbortWithStatusJSON(http.StatusBadRequest, apiError{
				Message: "invalid event kind: must be 1984 (NIP-56 report)",
			})
			return
		}

		// Extract blob hashes and report types from x tags
		// Format: ["x", "<sha256>"] or ["x", "<sha256>", "<report-type>"]
		var blobReports []struct {
			hash       string
			reportType string
		}

		for _, tag := range ev.Tags {
			if len(tag) >= 2 && tag[0] == "x" {
				hash := tag[1]
				if !sha256Regex.MatchString(hash) {
					continue // Skip invalid hashes
				}

				reportType := "other"
				if len(tag) >= 3 {
					if reason, ok := nip56ToReason[tag[2]]; ok {
						reportType = reason
					} else {
						reportType = "other"
					}
				}

				blobReports = append(blobReports, struct {
					hash       string
					reportType string
				}{hash: hash, reportType: reportType})
			}
		}

		if len(blobReports) == 0 {
			c.AbortWithStatusJSON(http.StatusBadRequest, apiError{
				Message: "no valid blob hashes found in x tags",
			})
			return
		}

		// Create reports for each blob
		var reportIDs []int32
		for _, br := range blobReports {
			blobURL := c.Request.Host + "/" + br.hash

			report, err := services.Moderation().CreateReport(
				c.Request.Context(),
				ev.PubKey, // Reporter pubkey from signed event
				br.hash,
				blobURL,
				core.ReportReason(br.reportType),
				ev.Content, // Details from event content
			)
			if err != nil {
				log.Warn("failed to create report for blob",
					zap.String("hash", br.hash),
					zap.Error(err))
				continue
			}
			reportIDs = append(reportIDs, report.ID)
		}

		if len(reportIDs) == 0 {
			c.AbortWithStatusJSON(http.StatusInternalServerError, apiError{
				Message: "failed to create any reports",
			})
			return
		}

		log.Info("BUD-09 report submitted",
			zap.String("reporter", ev.PubKey),
			zap.Int("blob_count", len(blobReports)),
			zap.Int("reports_created", len(reportIDs)))

		c.JSON(http.StatusOK, BUD09ReportResponse{
			Success:   true,
			Message:   fmt.Sprintf("Report submitted successfully for %d blob(s)", len(reportIDs)),
			ReportIDs: reportIDs,
		})
	}
}

// TransparencyStats represents public moderation statistics.
type TransparencyStatsResponse struct {
	TotalReports     int64  `json:"total_reports"`
	ReportsActioned  int64  `json:"reports_actioned"`
	ReportsDismissed int64  `json:"reports_dismissed"`
	BlobsRemoved     int64  `json:"blobs_removed"`
	UsersBanned      int64  `json:"users_banned"`
	LastUpdated      int64  `json:"last_updated"`
	PrivacyStatement string `json:"privacy_statement"`
}

// getTransparency handles GET /transparency
func getTransparency(services core.Services) gin.HandlerFunc {
	return func(c *gin.Context) {
		stats, err := services.Moderation().GetTransparencyStats(c.Request.Context())
		if err != nil {
			c.AbortWithStatusJSON(http.StatusInternalServerError, apiError{
				Message: "failed to get transparency stats",
			})
			return
		}

		c.JSON(http.StatusOK, TransparencyStatsResponse{
			TotalReports:     stats.TotalReports,
			ReportsActioned:  stats.ReportsActioned,
			ReportsDismissed: stats.ReportsDismissed,
			BlobsRemoved:     stats.BlobsRemoved,
			UsersBanned:      stats.UsersBanned,
			LastUpdated:      stats.LastUpdated,
			PrivacyStatement: getPrivacyStatement(),
		})
	}
}

// getTransparencyPage handles GET /transparency (HTML)
func getTransparencyPage(services core.Services) gin.HandlerFunc {
	return func(c *gin.Context) {
		// Check if client wants JSON
		if c.GetHeader("Accept") == "application/json" {
			getTransparency(services)(c)
			return
		}

		stats, err := services.Moderation().GetTransparencyStats(c.Request.Context())
		if err != nil {
			c.AbortWithStatusJSON(http.StatusInternalServerError, apiError{
				Message: "failed to get transparency stats",
			})
			return
		}

		c.Header("Content-Type", "text/html")
		c.String(http.StatusOK, transparencyPageHTML(stats))
	}
}

func getPrivacyStatement() string {
	return `Blossom stores encrypted blobs. We cannot decrypt user content and will not build backdoors. For encrypted uploads, we rely on user reports and legal process. For unencrypted uploads, we check against known illegal content databases where available. We comply with valid legal orders and ban accounts that violate our Terms of Service. We publish transparency reports showing enforcement actions.

Privacy is our primary concern. We believe that protecting the privacy of billions of users cannot be compromised for any purpose. At the same time, we do not tolerate illegal content and will take action on valid reports.

To report content, send a POST request to /report with the blob hash and reason.`
}

func transparencyPageHTML(stats *core.TransparencyStats) string {
	return `<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>Transparency Report - Blossom</title>
    <style>
        * { box-sizing: border-box; margin: 0; padding: 0; }
        body {
            font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif;
            background: #f8fafc;
            color: #1e293b;
            line-height: 1.6;
        }
        .container {
            max-width: 800px;
            margin: 0 auto;
            padding: 40px 20px;
        }
        h1 {
            font-size: 32px;
            margin-bottom: 8px;
        }
        .subtitle {
            color: #64748b;
            margin-bottom: 40px;
        }
        .stats-grid {
            display: grid;
            grid-template-columns: repeat(auto-fit, minmax(200px, 1fr));
            gap: 20px;
            margin-bottom: 40px;
        }
        .stat-card {
            background: white;
            padding: 24px;
            border-radius: 12px;
            box-shadow: 0 1px 3px rgba(0,0,0,0.1);
        }
        .stat-value {
            font-size: 36px;
            font-weight: 700;
            color: #6366f1;
        }
        .stat-label {
            color: #64748b;
            font-size: 14px;
            margin-top: 4px;
        }
        .section {
            background: white;
            padding: 32px;
            border-radius: 12px;
            box-shadow: 0 1px 3px rgba(0,0,0,0.1);
            margin-bottom: 24px;
        }
        .section h2 {
            font-size: 20px;
            margin-bottom: 16px;
            color: #334155;
        }
        .section p {
            color: #475569;
            margin-bottom: 16px;
        }
        .section p:last-child {
            margin-bottom: 0;
        }
        code {
            background: #f1f5f9;
            padding: 2px 6px;
            border-radius: 4px;
            font-family: monospace;
            font-size: 14px;
        }
        .api-example {
            background: #1e293b;
            color: #e2e8f0;
            padding: 16px;
            border-radius: 8px;
            overflow-x: auto;
            font-family: monospace;
            font-size: 13px;
            margin-top: 16px;
        }
    </style>
</head>
<body>
    <div class="container">
        <h1>Transparency Report</h1>
        <p class="subtitle">Content moderation statistics and privacy statement</p>

        <div class="stats-grid">
            <div class="stat-card">
                <div class="stat-value">` + formatInt64(stats.TotalReports) + `</div>
                <div class="stat-label">Total Reports Received</div>
            </div>
            <div class="stat-card">
                <div class="stat-value">` + formatInt64(stats.ReportsActioned) + `</div>
                <div class="stat-label">Reports Actioned</div>
            </div>
            <div class="stat-card">
                <div class="stat-value">` + formatInt64(stats.BlobsRemoved) + `</div>
                <div class="stat-label">Content Removed</div>
            </div>
            <div class="stat-card">
                <div class="stat-value">` + formatInt64(stats.UsersBanned) + `</div>
                <div class="stat-label">Users Blocked</div>
            </div>
        </div>

        <div class="section">
            <h2>Privacy Statement</h2>
            <p>Blossom stores encrypted blobs. <strong>We cannot decrypt user content and will not build backdoors.</strong></p>
            <p>For encrypted uploads, we rely on user reports and legal process. For unencrypted uploads, we check against known illegal content databases where available.</p>
            <p>We comply with valid legal orders and ban accounts that violate our Terms of Service. We publish this transparency report to show our enforcement actions.</p>
        </div>

        <div class="section">
            <h2>Our Position</h2>
            <p>Privacy is our primary concern. We believe that protecting the privacy of billions of users cannot be compromised for any purpose, including content moderation.</p>
            <p>This is the same position taken by Signal, ProtonMail, and other privacy-focused services. If content is encrypted, we genuinely cannot see it.</p>
            <p>At the same time, <strong>we do not tolerate illegal content</strong> and will take action on valid reports. Accounts that violate our terms are permanently blocked.</p>
        </div>

        <div class="section">
            <h2>Report Content</h2>
            <p>To report content that violates our terms, submit a report with the blob hash and reason:</p>
            <div class="api-example">
POST /report
Content-Type: application/json

{
  "blob_hash": "abc123...",
  "reason": "csam | illegal | copyright | abuse | other",
  "details": "Additional context (optional)",
  "reporter_pubkey": "your_nostr_pubkey (optional)"
}
            </div>
            <p style="margin-top: 16px;">Valid reasons: <code>csam</code>, <code>illegal</code>, <code>copyright</code>, <code>abuse</code>, <code>other</code></p>
        </div>
    </div>
</body>
</html>`
}

func formatInt64(n int64) string {
	return fmt.Sprintf("%d", n)
}
