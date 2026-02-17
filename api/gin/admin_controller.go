package gin

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"git.coldforge.xyz/coldforge/cloistr-blossom/src/core"
	"go.uber.org/zap"
)

// AdminStats represents server statistics for the admin dashboard.
type AdminStats struct {
	BytesStored   int64  `json:"bytes_stored"`
	BlobCount     int64  `json:"blob_count"`
	UserCount     int64  `json:"user_count"`
	StorageUsed   string `json:"storage_used"`    // Human readable
	QuotasEnabled bool   `json:"quotas_enabled"`
}

// AdminUser represents a user in admin views.
type AdminUser struct {
	Pubkey       string  `json:"pubkey"`
	QuotaBytes   int64   `json:"quota_bytes"`
	UsedBytes    int64   `json:"used_bytes"`
	UsagePercent float64 `json:"usage_percent"`
	IsBanned     bool    `json:"is_banned"`
	CreatedAt    int64   `json:"created_at"`
	UpdatedAt    int64   `json:"updated_at"`
}

// SetQuotaRequest is the request body for setting a user's quota.
type SetQuotaRequest struct {
	QuotaBytes int64 `json:"quota_bytes"`
}

// AdminReport represents a report in admin views.
type AdminReport struct {
	ID             int32  `json:"id"`
	ReporterPubkey string `json:"reporter_pubkey,omitempty"`
	BlobHash       string `json:"blob_hash"`
	BlobURL        string `json:"blob_url"`
	Reason         string `json:"reason"`
	Details        string `json:"details,omitempty"`
	Status         string `json:"status"`
	ActionTaken    string `json:"action_taken,omitempty"`
	ReviewedBy     string `json:"reviewed_by,omitempty"`
	CreatedAt      int64  `json:"created_at"`
	ReviewedAt     int64  `json:"reviewed_at,omitempty"`
}

// ReportActionRequest is the request body for taking action on a report.
type ReportActionRequest struct {
	Action string `json:"action" binding:"required"` // "removed", "user_banned", "dismissed"
}

// BlocklistAddRequest is the request body for adding to the blocklist.
type BlocklistAddRequest struct {
	Pubkey string `json:"pubkey" binding:"required"`
	Reason string `json:"reason" binding:"required"`
}

// AdminBlocklistEntry represents a blocklist entry in admin views.
type AdminBlocklistEntry struct {
	Pubkey    string `json:"pubkey"`
	Reason    string `json:"reason"`
	BlockedBy string `json:"blocked_by"`
	CreatedAt int64  `json:"created_at"`
}

// adminAuthMiddleware ensures the request is from an admin.
func adminAuthMiddleware(adminPubkey string) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		pubkey, exists := ctx.Get("pk")
		if !exists {
			ctx.AbortWithStatusJSON(http.StatusUnauthorized, apiError{Message: "unauthorized"})
			return
		}

		if pubkey.(string) != adminPubkey {
			ctx.AbortWithStatusJSON(http.StatusForbidden, apiError{Message: "admin access required"})
			return
		}

		ctx.Next()
	}
}

// getAdminStats returns server statistics.
func getAdminStats(services core.Services) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		stats := services.Stats()
		quota := services.Quota()

		serverStats, err := stats.Get(ctx.Request.Context())
		if err != nil {
			ctx.AbortWithStatusJSON(http.StatusInternalServerError, apiError{Message: err.Error()})
			return
		}

		userCount, err := quota.GetUserCount(ctx.Request.Context())
		if err != nil {
			userCount = 0
		}

		ctx.JSON(http.StatusOK, AdminStats{
			BytesStored:   int64(serverStats.BytesStored),
			BlobCount:     int64(serverStats.BlobCount),
			UserCount:     userCount,
			StorageUsed:   formatBytes(int64(serverStats.BytesStored)),
			QuotasEnabled: quota.IsEnabled(),
		})
	}
}

// listAdminUsers returns a paginated list of users.
func listAdminUsers(services core.Services) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		limit, _ := strconv.ParseInt(ctx.DefaultQuery("limit", "50"), 10, 64)
		offset, _ := strconv.ParseInt(ctx.DefaultQuery("offset", "0"), 10, 64)

		if limit > 100 {
			limit = 100
		}

		users, err := services.Quota().ListUsers(ctx.Request.Context(), limit, offset)
		if err != nil {
			ctx.AbortWithStatusJSON(http.StatusInternalServerError, apiError{Message: err.Error()})
			return
		}

		adminUsers := make([]AdminUser, len(users))
		for i, u := range users {
			var usagePercent float64
			if u.QuotaBytes > 0 {
				usagePercent = float64(u.UsedBytes) / float64(u.QuotaBytes) * 100
			}
			adminUsers[i] = AdminUser{
				Pubkey:       u.Pubkey,
				QuotaBytes:   u.QuotaBytes,
				UsedBytes:    u.UsedBytes,
				UsagePercent: usagePercent,
				IsBanned:     u.IsBanned,
				CreatedAt:    u.CreatedAt,
				UpdatedAt:    u.UpdatedAt,
			}
		}

		ctx.JSON(http.StatusOK, adminUsers)
	}
}

// getAdminUser returns details for a specific user.
func getAdminUser(services core.Services) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		pubkey := ctx.Param("pubkey")

		user, err := services.Quota().GetUser(ctx.Request.Context(), pubkey)
		if err != nil {
			if err == core.ErrUserNotFound {
				ctx.AbortWithStatusJSON(http.StatusNotFound, apiError{Message: "user not found"})
				return
			}
			ctx.AbortWithStatusJSON(http.StatusInternalServerError, apiError{Message: err.Error()})
			return
		}

		var usagePercent float64
		if user.QuotaBytes > 0 {
			usagePercent = float64(user.UsedBytes) / float64(user.QuotaBytes) * 100
		}

		ctx.JSON(http.StatusOK, AdminUser{
			Pubkey:       user.Pubkey,
			QuotaBytes:   user.QuotaBytes,
			UsedBytes:    user.UsedBytes,
			UsagePercent: usagePercent,
			IsBanned:     user.IsBanned,
			CreatedAt:    user.CreatedAt,
			UpdatedAt:    user.UpdatedAt,
		})
	}
}

// setUserQuota sets a user's storage quota.
func setUserQuota(services core.Services) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		pubkey := ctx.Param("pubkey")

		var req SetQuotaRequest
		if err := ctx.BindJSON(&req); err != nil {
			ctx.AbortWithStatusJSON(http.StatusBadRequest, apiError{Message: "invalid request body"})
			return
		}

		if err := services.Quota().SetQuota(ctx.Request.Context(), pubkey, req.QuotaBytes); err != nil {
			ctx.AbortWithStatusJSON(http.StatusInternalServerError, apiError{Message: err.Error()})
			return
		}

		ctx.JSON(http.StatusOK, gin.H{"message": "quota updated"})
	}
}

// banUser bans a user from uploading.
func banUser(services core.Services) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		pubkey := ctx.Param("pubkey")

		if err := services.Quota().BanUser(ctx.Request.Context(), pubkey); err != nil {
			ctx.AbortWithStatusJSON(http.StatusInternalServerError, apiError{Message: err.Error()})
			return
		}

		ctx.JSON(http.StatusOK, gin.H{"message": "user banned"})
	}
}

// unbanUser removes a ban from a user.
func unbanUser(services core.Services) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		pubkey := ctx.Param("pubkey")

		if err := services.Quota().UnbanUser(ctx.Request.Context(), pubkey); err != nil {
			ctx.AbortWithStatusJSON(http.StatusInternalServerError, apiError{Message: err.Error()})
			return
		}

		ctx.JSON(http.StatusOK, gin.H{"message": "user unbanned"})
	}
}

// recalculateUserUsage recalculates a user's storage usage.
func recalculateUserUsage(services core.Services) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		pubkey := ctx.Param("pubkey")

		if err := services.Quota().RecalculateUsage(ctx.Request.Context(), pubkey); err != nil {
			ctx.AbortWithStatusJSON(http.StatusInternalServerError, apiError{Message: err.Error()})
			return
		}

		ctx.JSON(http.StatusOK, gin.H{"message": "usage recalculated"})
	}
}

// adminDashboard serves the admin dashboard HTML page.
func adminDashboard(services core.Services) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		stats := services.Stats()
		quota := services.Quota()

		serverStats, err := stats.Get(ctx.Request.Context())
		userCount, _ := quota.GetUserCount(ctx.Request.Context())

		// Handle case where stats retrieval fails
		var adminStats AdminStats
		if err != nil || serverStats == nil {
			adminStats = AdminStats{
				BytesStored:   0,
				BlobCount:     0,
				UserCount:     userCount,
				StorageUsed:   "0 B",
				QuotasEnabled: quota.IsEnabled(),
			}
		} else {
			adminStats = AdminStats{
				BytesStored:   int64(serverStats.BytesStored),
				BlobCount:     int64(serverStats.BlobCount),
				UserCount:     userCount,
				StorageUsed:   formatBytes(int64(serverStats.BytesStored)),
				QuotasEnabled: quota.IsEnabled(),
			}
		}

		ctx.Header("Content-Type", "text/html; charset=utf-8")
		ctx.String(http.StatusOK, adminDashboardHTML(adminStats))
	}
}

// formatBytes formats bytes as a human-readable string.
func formatBytes(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return strconv.FormatInt(bytes, 10) + " B"
	}
	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return strconv.FormatFloat(float64(bytes)/float64(div), 'f', 1, 64) + " " + []string{"KB", "MB", "GB", "TB"}[exp]
}

// adminDashboardHTML returns the admin dashboard HTML.
func adminDashboardHTML(stats AdminStats) string {
	return `<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>Blossom Admin Dashboard</title>
    <style>
        * { box-sizing: border-box; margin: 0; padding: 0; }
        body { font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, Oxygen, Ubuntu, sans-serif; background: #f5f5f5; color: #333; }
        .container { max-width: 1200px; margin: 0 auto; padding: 20px; }
        header { background: #6366f1; color: white; padding: 20px; margin-bottom: 20px; border-radius: 8px; }
        header h1 { font-size: 24px; }
        .stats-grid { display: grid; grid-template-columns: repeat(auto-fit, minmax(200px, 1fr)); gap: 20px; margin-bottom: 30px; }
        .stat-card { background: white; padding: 20px; border-radius: 8px; box-shadow: 0 1px 3px rgba(0,0,0,0.1); }
        .stat-card h3 { font-size: 14px; color: #666; margin-bottom: 8px; }
        .stat-card .value { font-size: 28px; font-weight: 600; color: #6366f1; }
        .section { background: white; padding: 20px; border-radius: 8px; box-shadow: 0 1px 3px rgba(0,0,0,0.1); margin-bottom: 20px; }
        .section h2 { font-size: 18px; margin-bottom: 15px; color: #333; }
        table { width: 100%; border-collapse: collapse; }
        th, td { padding: 12px; text-align: left; border-bottom: 1px solid #eee; }
        th { font-weight: 600; color: #666; }
        .badge { padding: 4px 8px; border-radius: 4px; font-size: 12px; }
        .badge-success { background: #10b981; color: white; }
        .badge-danger { background: #ef4444; color: white; }
        .btn { padding: 8px 16px; border: none; border-radius: 4px; cursor: pointer; font-size: 14px; }
        .btn-primary { background: #6366f1; color: white; }
        .btn-danger { background: #ef4444; color: white; }
        .btn-secondary { background: #6b7280; color: white; }
        .btn:hover { opacity: 0.9; }
        #users-list { min-height: 200px; }
        .loading { text-align: center; padding: 40px; color: #666; }
        .pubkey { font-family: monospace; font-size: 12px; max-width: 200px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
        .progress-bar { height: 8px; background: #e5e7eb; border-radius: 4px; overflow: hidden; }
        .progress-bar-fill { height: 100%; background: #6366f1; transition: width 0.3s; }
        .header-content { display: flex; justify-content: space-between; align-items: center; }
        .header-right { display: flex; align-items: center; gap: 15px; }
        .admin-info { font-size: 12px; opacity: 0.9; }
    </style>
</head>
<body>
    <div class="container">
        <header>
            <div class="header-content">
                <h1>🌸 Blossom Admin</h1>
                <div class="header-right">
                    <span class="admin-info" id="admin-info"></span>
                    <button class="btn btn-secondary" onclick="logout()">Logout</button>
                </div>
            </div>
        </header>

        <div class="stats-grid">
            <div class="stat-card">
                <h3>Storage Used</h3>
                <div class="value">` + stats.StorageUsed + `</div>
            </div>
            <div class="stat-card">
                <h3>Total Blobs</h3>
                <div class="value">` + strconv.FormatInt(stats.BlobCount, 10) + `</div>
            </div>
            <div class="stat-card">
                <h3>Total Users</h3>
                <div class="value">` + strconv.FormatInt(stats.UserCount, 10) + `</div>
            </div>
            <div class="stat-card">
                <h3>Quotas</h3>
                <div class="value">` + func() string { if stats.QuotasEnabled { return "Enabled" } else { return "Disabled" } }() + `</div>
            </div>
        </div>

        <div class="section">
            <h2>Users</h2>
            <div id="users-list">
                <div class="loading">Loading users...</div>
            </div>
        </div>
    </div>

    <script>
        // Check auth status and show admin info
        async function checkAuth() {
            try {
                const resp = await fetch('/admin/api/session');
                const data = await resp.json();
                if (data.authenticated && data.pubkey) {
                    const shortKey = data.pubkey.slice(0, 8) + '...' + data.pubkey.slice(-4);
                    document.getElementById('admin-info').textContent = 'Admin: ' + shortKey;
                }
            } catch (e) {}
        }

        async function logout() {
            try {
                await fetch('/admin/api/logout', { method: 'POST' });
            } catch (e) {}
            window.location.href = '/admin/login';
        }

        async function loadUsers() {
            try {
                const resp = await fetch('/admin/api/users');
                if (resp.status === 401) {
                    window.location.href = '/admin/login';
                    return;
                }
                const users = await resp.json();
                renderUsers(users);
            } catch (err) {
                document.getElementById('users-list').innerHTML = '<p>Failed to load users</p>';
            }
        }

        function renderUsers(users) {
            if (!users || users.length === 0) {
                document.getElementById('users-list').innerHTML = '<p>No users found</p>';
                return;
            }

            const html = '<table><thead><tr><th>Pubkey</th><th>Usage</th><th>Quota</th><th>Status</th><th>Actions</th></tr></thead><tbody>' +
                users.map(u => '<tr>' +
                    '<td class="pubkey">' + u.pubkey + '</td>' +
                    '<td>' +
                        '<div class="progress-bar"><div class="progress-bar-fill" style="width: ' + Math.min(u.usage_percent, 100) + '%"></div></div>' +
                        '<small>' + formatBytes(u.used_bytes) + ' / ' + formatBytes(u.quota_bytes) + ' (' + u.usage_percent.toFixed(1) + '%)</small>' +
                    '</td>' +
                    '<td>' + formatBytes(u.quota_bytes) + '</td>' +
                    '<td>' + (u.is_banned ? '<span class="badge badge-danger">Banned</span>' : '<span class="badge badge-success">Active</span>') + '</td>' +
                    '<td>' +
                        (u.is_banned ?
                            '<button class="btn btn-primary" onclick="unbanUser(\'' + u.pubkey + '\')">Unban</button>' :
                            '<button class="btn btn-danger" onclick="banUser(\'' + u.pubkey + '\')">Ban</button>') +
                    '</td>' +
                '</tr>').join('') +
                '</tbody></table>';
            document.getElementById('users-list').innerHTML = html;
        }

        function formatBytes(bytes) {
            if (bytes === 0) return '0 B';
            const k = 1024;
            const sizes = ['B', 'KB', 'MB', 'GB', 'TB'];
            const i = Math.floor(Math.log(bytes) / Math.log(k));
            return parseFloat((bytes / Math.pow(k, i)).toFixed(1)) + ' ' + sizes[i];
        }

        async function banUser(pubkey) {
            if (!confirm('Ban user ' + pubkey + '?')) return;
            await fetch('/admin/api/users/' + pubkey + '/ban', { method: 'POST' });
            loadUsers();
        }

        async function unbanUser(pubkey) {
            await fetch('/admin/api/users/' + pubkey + '/unban', { method: 'POST' });
            loadUsers();
        }

        // Initialize
        checkAuth();
        loadUsers();
    </script>
</body>
</html>`
}

// listReports returns a paginated list of reports.
func listReports(services core.Services) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		limit, _ := strconv.ParseInt(ctx.DefaultQuery("limit", "50"), 10, 64)
		offset, _ := strconv.ParseInt(ctx.DefaultQuery("offset", "0"), 10, 64)
		status := ctx.Query("status")

		if limit > 100 {
			limit = 100
		}

		var reports []*core.Report
		var err error

		if status != "" {
			reports, err = services.Moderation().ListReportsByStatus(
				ctx.Request.Context(),
				core.ReportStatus(status),
				int(limit),
				int(offset),
			)
		} else {
			reports, err = services.Moderation().ListAllReports(ctx.Request.Context(), int(limit), int(offset))
		}

		if err != nil {
			ctx.AbortWithStatusJSON(http.StatusInternalServerError, apiError{Message: err.Error()})
			return
		}

		adminReports := make([]AdminReport, len(reports))
		for i, r := range reports {
			adminReports[i] = AdminReport{
				ID:             r.ID,
				ReporterPubkey: r.ReporterPubkey,
				BlobHash:       r.BlobHash,
				BlobURL:        r.BlobURL,
				Reason:         string(r.Reason),
				Details:        r.Details,
				Status:         string(r.Status),
				ActionTaken:    string(r.ActionTaken),
				ReviewedBy:     r.ReviewedBy,
				CreatedAt:      r.CreatedAt,
				ReviewedAt:     r.ReviewedAt,
			}
		}

		ctx.JSON(http.StatusOK, adminReports)
	}
}

// getReport returns details for a specific report.
func getReport(services core.Services) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		idStr := ctx.Param("id")
		id, err := strconv.ParseInt(idStr, 10, 32)
		if err != nil {
			ctx.AbortWithStatusJSON(http.StatusBadRequest, apiError{Message: "invalid report ID"})
			return
		}

		report, err := services.Moderation().GetReport(ctx.Request.Context(), int32(id))
		if err != nil {
			if err == core.ErrReportNotFound {
				ctx.AbortWithStatusJSON(http.StatusNotFound, apiError{Message: "report not found"})
				return
			}
			ctx.AbortWithStatusJSON(http.StatusInternalServerError, apiError{Message: err.Error()})
			return
		}

		ctx.JSON(http.StatusOK, AdminReport{
			ID:             report.ID,
			ReporterPubkey: report.ReporterPubkey,
			BlobHash:       report.BlobHash,
			BlobURL:        report.BlobURL,
			Reason:         string(report.Reason),
			Details:        report.Details,
			Status:         string(report.Status),
			ActionTaken:    string(report.ActionTaken),
			ReviewedBy:     report.ReviewedBy,
			CreatedAt:      report.CreatedAt,
			ReviewedAt:     report.ReviewedAt,
		})
	}
}

// actionReport takes action on a report.
func actionReport(services core.Services) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		idStr := ctx.Param("id")
		id, err := strconv.ParseInt(idStr, 10, 32)
		if err != nil {
			ctx.AbortWithStatusJSON(http.StatusBadRequest, apiError{Message: "invalid report ID"})
			return
		}

		var req ReportActionRequest
		if err := ctx.BindJSON(&req); err != nil {
			ctx.AbortWithStatusJSON(http.StatusBadRequest, apiError{Message: "invalid request body"})
			return
		}

		// Get admin pubkey from session
		adminPubkey := ""
		if session, exists := ctx.Get("admin_session"); exists {
			if s, ok := session.(*AdminSession); ok {
				adminPubkey = s.Pubkey
			}
		}

		var action core.ReportAction
		switch req.Action {
		case "removed":
			action = core.ReportActionRemoved
		case "user_banned":
			action = core.ReportActionUserBanned
		case "dismissed":
			// Just update status, no action
			if err := services.Moderation().ReviewReport(
				ctx.Request.Context(),
				int32(id),
				core.ReportStatusDismissed,
				core.ReportActionNone,
				adminPubkey,
			); err != nil {
				ctx.AbortWithStatusJSON(http.StatusInternalServerError, apiError{Message: err.Error()})
				return
			}
			ctx.JSON(http.StatusOK, gin.H{"message": "report dismissed"})
			return
		default:
			ctx.AbortWithStatusJSON(http.StatusBadRequest, apiError{Message: "invalid action"})
			return
		}

		if err := services.Moderation().ActionReport(ctx.Request.Context(), int32(id), action, adminPubkey); err != nil {
			ctx.AbortWithStatusJSON(http.StatusInternalServerError, apiError{Message: err.Error()})
			return
		}

		ctx.JSON(http.StatusOK, gin.H{"message": "action taken on report"})
	}
}

// getPendingReportCount returns the number of pending reports.
func getPendingReportCount(services core.Services) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		count, err := services.Moderation().GetPendingReportCount(ctx.Request.Context())
		if err != nil {
			ctx.AbortWithStatusJSON(http.StatusInternalServerError, apiError{Message: err.Error()})
			return
		}

		ctx.JSON(http.StatusOK, gin.H{"count": count})
	}
}

// listBlocklist returns all blocked pubkeys.
func listBlocklist(services core.Services) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		limit, _ := strconv.ParseInt(ctx.DefaultQuery("limit", "50"), 10, 64)
		offset, _ := strconv.ParseInt(ctx.DefaultQuery("offset", "0"), 10, 64)

		if limit > 100 {
			limit = 100
		}

		entries, err := services.Moderation().ListBlocklist(ctx.Request.Context(), int(limit), int(offset))
		if err != nil {
			ctx.AbortWithStatusJSON(http.StatusInternalServerError, apiError{Message: err.Error()})
			return
		}

		adminEntries := make([]AdminBlocklistEntry, len(entries))
		for i, e := range entries {
			adminEntries[i] = AdminBlocklistEntry{
				Pubkey:    e.Pubkey,
				Reason:    e.Reason,
				BlockedBy: e.BlockedBy,
				CreatedAt: e.CreatedAt,
			}
		}

		ctx.JSON(http.StatusOK, adminEntries)
	}
}

// addToBlocklist adds a pubkey to the blocklist.
func addToBlocklist(services core.Services) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		var req BlocklistAddRequest
		if err := ctx.BindJSON(&req); err != nil {
			ctx.AbortWithStatusJSON(http.StatusBadRequest, apiError{Message: "invalid request body"})
			return
		}

		// Get admin pubkey from session
		adminPubkey := ""
		if session, exists := ctx.Get("admin_session"); exists {
			if s, ok := session.(*AdminSession); ok {
				adminPubkey = s.Pubkey
			}
		}

		entry, err := services.Moderation().AddToBlocklist(ctx.Request.Context(), req.Pubkey, req.Reason, adminPubkey)
		if err != nil {
			ctx.AbortWithStatusJSON(http.StatusInternalServerError, apiError{Message: err.Error()})
			return
		}

		ctx.JSON(http.StatusCreated, AdminBlocklistEntry{
			Pubkey:    entry.Pubkey,
			Reason:    entry.Reason,
			BlockedBy: entry.BlockedBy,
			CreatedAt: entry.CreatedAt,
		})
	}
}

// removeFromBlocklist removes a pubkey from the blocklist.
func removeFromBlocklist(services core.Services) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		pubkey := ctx.Param("pubkey")

		if err := services.Moderation().RemoveFromBlocklist(ctx.Request.Context(), pubkey); err != nil {
			ctx.AbortWithStatusJSON(http.StatusInternalServerError, apiError{Message: err.Error()})
			return
		}

		ctx.JSON(http.StatusOK, gin.H{"message": "removed from blocklist"})
	}
}

// RegisterAdminRoutes registers admin routes on the router.
func RegisterAdminRoutes(r *gin.Engine, services core.Services, adminPubkey string, log *zap.Logger) {
	// Create auth manager for session handling
	authManager := NewAdminAuthManager(adminPubkey, log)

	admin := r.Group("/admin")

	// Public routes (no auth required)
	admin.GET("/login", func(c *gin.Context) {
		c.Header("Content-Type", "text/html; charset=utf-8")
		c.String(http.StatusOK, adminLoginPageHTML(adminPubkey))
	})

	// API routes that don't require auth
	api := admin.Group("/api")
	api.POST("/login", adminLogin(authManager))
	api.GET("/session", adminCheckSession(authManager))
	api.POST("/logout", adminLogout(authManager))

	// Protected routes (require session auth)
	protected := admin.Group("")
	protected.Use(AdminSessionMiddleware(authManager))

	// Dashboard page
	protected.GET("/", adminDashboard(services))

	// Protected API routes
	protectedAPI := api.Group("")
	protectedAPI.Use(AdminSessionMiddleware(authManager))
	protectedAPI.GET("/stats", getAdminStats(services))
	protectedAPI.GET("/users", listAdminUsers(services))
	protectedAPI.GET("/users/:pubkey", getAdminUser(services))
	protectedAPI.PUT("/users/:pubkey/quota", setUserQuota(services))
	protectedAPI.POST("/users/:pubkey/ban", banUser(services))
	protectedAPI.POST("/users/:pubkey/unban", unbanUser(services))
	protectedAPI.POST("/users/:pubkey/recalculate", recalculateUserUsage(services))

	// Report management
	protectedAPI.GET("/reports", listReports(services))
	protectedAPI.GET("/reports/pending/count", getPendingReportCount(services))
	protectedAPI.GET("/reports/:id", getReport(services))
	protectedAPI.POST("/reports/:id/action", actionReport(services))

	// Blocklist management
	protectedAPI.GET("/blocklist", listBlocklist(services))
	protectedAPI.POST("/blocklist", addToBlocklist(services))
	protectedAPI.DELETE("/blocklist/:pubkey", removeFromBlocklist(services))
}
