package gin

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"git.coldforge.xyz/coldforge/cloistr-blossom/src/core"
	"go.uber.org/zap"
)

// AnalyticsQueryParams represents query parameters for analytics endpoints.
type AnalyticsQueryParams struct {
	StartTime int64  `form:"start_time"` // Unix timestamp
	EndTime   int64  `form:"end_time"`   // Unix timestamp
	Bucket    string `form:"bucket"`     // hourly, daily, weekly, monthly
	Limit     int    `form:"limit"`
}

// parseAnalyticsQuery parses query parameters into an AnalyticsQuery.
func parseAnalyticsQuery(ctx *gin.Context) core.AnalyticsQuery {
	var params AnalyticsQueryParams
	_ = ctx.ShouldBindQuery(&params)

	query := core.DefaultAnalyticsQuery()

	if params.StartTime > 0 {
		query.StartTime = time.Unix(params.StartTime, 0)
	}
	if params.EndTime > 0 {
		query.EndTime = time.Unix(params.EndTime, 0)
	}

	switch params.Bucket {
	case "hourly":
		query.Bucket = core.TimeBucketHourly
	case "daily":
		query.Bucket = core.TimeBucketDaily
	case "weekly":
		query.Bucket = core.TimeBucketWeekly
	case "monthly":
		query.Bucket = core.TimeBucketMonthly
	}

	if params.Limit > 0 && params.Limit <= 100 {
		query.Limit = params.Limit
	}

	return query
}

// getAnalyticsOverview returns a high-level dashboard summary.
func getAnalyticsOverview(services core.Services) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		overview, err := services.Analytics().GetOverview(ctx.Request.Context())
		if err != nil {
			ctx.AbortWithStatusJSON(http.StatusInternalServerError, apiError{Message: err.Error()})
			return
		}

		ctx.JSON(http.StatusOK, overview)
	}
}

// getStorageAnalytics returns storage metrics over time.
func getStorageAnalytics(services core.Services) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		query := parseAnalyticsQuery(ctx)

		analytics, err := services.Analytics().GetStorageAnalytics(ctx.Request.Context(), query)
		if err != nil {
			ctx.AbortWithStatusJSON(http.StatusInternalServerError, apiError{Message: err.Error()})
			return
		}

		ctx.JSON(http.StatusOK, analytics)
	}
}

// getActivityAnalytics returns upload/download activity over time.
func getActivityAnalytics(services core.Services) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		query := parseAnalyticsQuery(ctx)

		analytics, err := services.Analytics().GetActivityAnalytics(ctx.Request.Context(), query)
		if err != nil {
			ctx.AbortWithStatusJSON(http.StatusInternalServerError, apiError{Message: err.Error()})
			return
		}

		ctx.JSON(http.StatusOK, analytics)
	}
}

// getUserAnalytics returns user-related metrics.
func getUserAnalytics(services core.Services) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		query := parseAnalyticsQuery(ctx)

		analytics, err := services.Analytics().GetUserAnalytics(ctx.Request.Context(), query)
		if err != nil {
			ctx.AbortWithStatusJSON(http.StatusInternalServerError, apiError{Message: err.Error()})
			return
		}

		ctx.JSON(http.StatusOK, analytics)
	}
}

// getContentAnalytics returns content type breakdown.
func getContentAnalytics(services core.Services) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		analytics, err := services.Analytics().GetContentAnalytics(ctx.Request.Context())
		if err != nil {
			ctx.AbortWithStatusJSON(http.StatusInternalServerError, apiError{Message: err.Error()})
			return
		}

		ctx.JSON(http.StatusOK, analytics)
	}
}

// RegisterAnalyticsRoutes registers analytics routes on the admin API.
func RegisterAnalyticsRoutes(protectedAPI *gin.RouterGroup, services core.Services, log *zap.Logger) {
	analytics := protectedAPI.Group("/analytics")
	{
		analytics.GET("/overview", getAnalyticsOverview(services))
		analytics.GET("/storage", getStorageAnalytics(services))
		analytics.GET("/activity", getActivityAnalytics(services))
		analytics.GET("/users", getUserAnalytics(services))
		analytics.GET("/content", getContentAnalytics(services))
	}
	log.Info("analytics routes registered")
}

// analyticsPageHTML returns the analytics dashboard HTML page.
func analyticsPageHTML() string {
	return `<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>Analytics - Blossom Admin</title>
    <script src="https://cdn.jsdelivr.net/npm/chart.js"></script>
    <style>
        * { box-sizing: border-box; margin: 0; padding: 0; }
        body { font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif; background: #f5f5f5; color: #333; }
        .container { max-width: 1400px; margin: 0 auto; padding: 20px; }
        header { background: #6366f1; color: white; padding: 20px; margin-bottom: 20px; border-radius: 8px; display: flex; justify-content: space-between; align-items: center; }
        header h1 { font-size: 24px; }
        .nav-links { display: flex; gap: 15px; }
        .nav-links a { color: white; text-decoration: none; opacity: 0.9; }
        .nav-links a:hover { opacity: 1; text-decoration: underline; }
        .stats-grid { display: grid; grid-template-columns: repeat(auto-fit, minmax(200px, 1fr)); gap: 20px; margin-bottom: 30px; }
        .stat-card { background: white; padding: 20px; border-radius: 8px; box-shadow: 0 1px 3px rgba(0,0,0,0.1); }
        .stat-card h3 { font-size: 14px; color: #666; margin-bottom: 8px; }
        .stat-card .value { font-size: 28px; font-weight: 600; color: #6366f1; }
        .stat-card .change { font-size: 12px; margin-top: 4px; }
        .stat-card .change.positive { color: #10b981; }
        .stat-card .change.negative { color: #ef4444; }
        .chart-grid { display: grid; grid-template-columns: repeat(auto-fit, minmax(500px, 1fr)); gap: 20px; margin-bottom: 30px; }
        .chart-container { background: white; padding: 20px; border-radius: 8px; box-shadow: 0 1px 3px rgba(0,0,0,0.1); }
        .chart-container h2 { font-size: 16px; margin-bottom: 15px; color: #333; }
        .chart-container canvas { max-height: 300px; }
        .section { background: white; padding: 20px; border-radius: 8px; box-shadow: 0 1px 3px rgba(0,0,0,0.1); margin-bottom: 20px; }
        .section h2 { font-size: 18px; margin-bottom: 15px; color: #333; }
        table { width: 100%; border-collapse: collapse; }
        th, td { padding: 12px; text-align: left; border-bottom: 1px solid #eee; }
        th { font-weight: 600; color: #666; }
        .pubkey { font-family: monospace; font-size: 12px; max-width: 200px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
        .time-range { display: flex; gap: 10px; margin-bottom: 20px; }
        .time-range button { padding: 8px 16px; border: 1px solid #ddd; background: white; border-radius: 4px; cursor: pointer; }
        .time-range button.active { background: #6366f1; color: white; border-color: #6366f1; }
        .loading { text-align: center; padding: 40px; color: #666; }
    </style>
</head>
<body>
    <div class="container">
        <header>
            <h1>Analytics Dashboard</h1>
            <div class="nav-links">
                <a href="/admin/">Dashboard</a>
                <a href="/admin/analytics">Analytics</a>
            </div>
        </header>

        <div class="time-range">
            <button onclick="setTimeRange(7)" id="range-7">7 Days</button>
            <button onclick="setTimeRange(30)" class="active" id="range-30">30 Days</button>
            <button onclick="setTimeRange(90)" id="range-90">90 Days</button>
        </div>

        <div class="stats-grid" id="overview-stats">
            <div class="loading">Loading overview...</div>
        </div>

        <div class="chart-grid">
            <div class="chart-container">
                <h2>Storage Growth</h2>
                <canvas id="storageChart"></canvas>
            </div>
            <div class="chart-container">
                <h2>Upload Activity</h2>
                <canvas id="activityChart"></canvas>
            </div>
            <div class="chart-container">
                <h2>New Users</h2>
                <canvas id="usersChart"></canvas>
            </div>
            <div class="chart-container">
                <h2>Content Types</h2>
                <canvas id="contentChart"></canvas>
            </div>
        </div>

        <div class="section">
            <h2>Top Users by Storage</h2>
            <div id="top-users">
                <div class="loading">Loading top users...</div>
            </div>
        </div>
    </div>

    <script>
        let currentRange = 30;
        let charts = {};

        function formatBytes(bytes) {
            if (bytes === 0) return '0 B';
            const k = 1024;
            const sizes = ['B', 'KB', 'MB', 'GB', 'TB'];
            const i = Math.floor(Math.log(bytes) / Math.log(k));
            return parseFloat((bytes / Math.pow(k, i)).toFixed(1)) + ' ' + sizes[i];
        }

        function formatNumber(num) {
            return num.toLocaleString();
        }

        function formatPct(pct) {
            const sign = pct >= 0 ? '+' : '';
            return sign + pct.toFixed(1) + '%';
        }

        function formatDate(timestamp) {
            return new Date(timestamp * 1000).toLocaleDateString();
        }

        function setTimeRange(days) {
            currentRange = days;
            document.querySelectorAll('.time-range button').forEach(b => b.classList.remove('active'));
            document.getElementById('range-' + days).classList.add('active');
            loadAllData();
        }

        function getTimeParams() {
            const end = Math.floor(Date.now() / 1000);
            const start = end - (currentRange * 24 * 60 * 60);
            return 'start_time=' + start + '&end_time=' + end;
        }

        async function loadOverview() {
            try {
                const resp = await fetch('/admin/api/analytics/overview');
                if (resp.status === 401) {
                    window.location.href = '/admin/login';
                    return;
                }
                const data = await resp.json();
                renderOverview(data);
            } catch (err) {
                console.error('Failed to load overview:', err);
            }
        }

        function renderOverview(data) {
            const html = ` + "`" + `
                <div class="stat-card">
                    <h3>Total Storage</h3>
                    <div class="value">${formatBytes(data.total_storage)}</div>
                    <div class="change ${data.storage_growth >= 0 ? 'positive' : 'negative'}">${formatPct(data.storage_growth)} this week</div>
                </div>
                <div class="stat-card">
                    <h3>Total Blobs</h3>
                    <div class="value">${formatNumber(data.total_blobs)}</div>
                    <div class="change ${data.blob_growth >= 0 ? 'positive' : 'negative'}">${formatPct(data.blob_growth)} this week</div>
                </div>
                <div class="stat-card">
                    <h3>Total Users</h3>
                    <div class="value">${formatNumber(data.total_users)}</div>
                    <div class="change ${data.user_growth >= 0 ? 'positive' : 'negative'}">${formatPct(data.user_growth)} this week</div>
                </div>
                <div class="stat-card">
                    <h3>Uploads (24h)</h3>
                    <div class="value">${formatNumber(data.uploads_last_24h)}</div>
                    <div class="change">${formatBytes(data.bytes_in_last_24h)} uploaded</div>
                </div>
                <div class="stat-card">
                    <h3>New Users (24h)</h3>
                    <div class="value">${formatNumber(data.new_users_last_24h)}</div>
                </div>
            ` + "`" + `;
            document.getElementById('overview-stats').innerHTML = html;
        }

        async function loadStorageChart() {
            try {
                const resp = await fetch('/admin/api/analytics/storage?' + getTimeParams());
                const data = await resp.json();
                renderStorageChart(data);
            } catch (err) {
                console.error('Failed to load storage data:', err);
            }
        }

        function renderStorageChart(data) {
            const ctx = document.getElementById('storageChart').getContext('2d');
            if (charts.storage) charts.storage.destroy();

            const points = data.bytes_over_time?.points || [];
            charts.storage = new Chart(ctx, {
                type: 'line',
                data: {
                    labels: points.map(p => formatDate(p.timestamp)),
                    datasets: [{
                        label: 'Storage (cumulative)',
                        data: points.map(p => p.value),
                        borderColor: '#6366f1',
                        backgroundColor: 'rgba(99, 102, 241, 0.1)',
                        fill: true,
                        tension: 0.3
                    }]
                },
                options: {
                    responsive: true,
                    plugins: {
                        tooltip: {
                            callbacks: {
                                label: (ctx) => formatBytes(ctx.raw)
                            }
                        }
                    },
                    scales: {
                        y: {
                            ticks: {
                                callback: (val) => formatBytes(val)
                            }
                        }
                    }
                }
            });
        }

        async function loadActivityChart() {
            try {
                const resp = await fetch('/admin/api/analytics/activity?' + getTimeParams());
                const data = await resp.json();
                renderActivityChart(data);
            } catch (err) {
                console.error('Failed to load activity data:', err);
            }
        }

        function renderActivityChart(data) {
            const ctx = document.getElementById('activityChart').getContext('2d');
            if (charts.activity) charts.activity.destroy();

            const points = data.uploads_over_time?.points || [];
            charts.activity = new Chart(ctx, {
                type: 'bar',
                data: {
                    labels: points.map(p => formatDate(p.timestamp)),
                    datasets: [{
                        label: 'Uploads',
                        data: points.map(p => p.value),
                        backgroundColor: '#6366f1'
                    }]
                },
                options: {
                    responsive: true,
                    plugins: {
                        legend: { display: false }
                    }
                }
            });
        }

        async function loadUsersChart() {
            try {
                const resp = await fetch('/admin/api/analytics/users?' + getTimeParams());
                const data = await resp.json();
                renderUsersChart(data);
                renderTopUsers(data.top_users || []);
            } catch (err) {
                console.error('Failed to load user data:', err);
            }
        }

        function renderUsersChart(data) {
            const ctx = document.getElementById('usersChart').getContext('2d');
            if (charts.users) charts.users.destroy();

            const points = data.new_users_over_time?.points || [];
            charts.users = new Chart(ctx, {
                type: 'bar',
                data: {
                    labels: points.map(p => formatDate(p.timestamp)),
                    datasets: [{
                        label: 'New Users',
                        data: points.map(p => p.value),
                        backgroundColor: '#10b981'
                    }]
                },
                options: {
                    responsive: true,
                    plugins: {
                        legend: { display: false }
                    }
                }
            });
        }

        function renderTopUsers(users) {
            if (!users.length) {
                document.getElementById('top-users').innerHTML = '<p>No users with stored data</p>';
                return;
            }
            const html = '<table><thead><tr><th>Pubkey</th><th>Storage Used</th><th>Blob Count</th><th>Last Active</th></tr></thead><tbody>' +
                users.map(u => '<tr>' +
                    '<td class="pubkey" title="' + u.pubkey + '">' + u.pubkey.slice(0, 8) + '...' + u.pubkey.slice(-4) + '</td>' +
                    '<td>' + formatBytes(u.used_bytes) + '</td>' +
                    '<td>' + formatNumber(u.blob_count) + '</td>' +
                    '<td>' + formatDate(u.last_active) + '</td>' +
                '</tr>').join('') +
                '</tbody></table>';
            document.getElementById('top-users').innerHTML = html;
        }

        async function loadContentChart() {
            try {
                const resp = await fetch('/admin/api/analytics/content');
                const data = await resp.json();
                renderContentChart(data);
            } catch (err) {
                console.error('Failed to load content data:', err);
            }
        }

        function renderContentChart(data) {
            const ctx = document.getElementById('contentChart').getContext('2d');
            if (charts.content) charts.content.destroy();

            const categories = data.by_category || [];
            const colors = ['#6366f1', '#10b981', '#f59e0b', '#ef4444', '#8b5cf6', '#06b6d4', '#84cc16'];

            charts.content = new Chart(ctx, {
                type: 'doughnut',
                data: {
                    labels: categories.map(c => c.category),
                    datasets: [{
                        data: categories.map(c => c.total_size),
                        backgroundColor: colors.slice(0, categories.length)
                    }]
                },
                options: {
                    responsive: true,
                    plugins: {
                        tooltip: {
                            callbacks: {
                                label: (ctx) => ctx.label + ': ' + formatBytes(ctx.raw)
                            }
                        }
                    }
                }
            });
        }

        function loadAllData() {
            loadOverview();
            loadStorageChart();
            loadActivityChart();
            loadUsersChart();
            loadContentChart();
        }

        // Initial load
        loadAllData();
    </script>
</body>
</html>`
}

// analyticsPage serves the analytics dashboard HTML page.
func analyticsPage() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		ctx.Header("Content-Type", "text/html; charset=utf-8")
		ctx.String(http.StatusOK, analyticsPageHTML())
	}
}
