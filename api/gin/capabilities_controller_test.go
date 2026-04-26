package gin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"git.aegis-hq.xyz/coldforge/cloistr-blossom/src/pkg/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetServerCapabilities(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name           string
		conf           *config.Config
		adminPubkey    string
		expectedBUDs   []string
		paymentEnabled bool
	}{
		{
			name: "basic config",
			conf: &config.Config{
				MaxUploadSizeBytes: 100 * 1024 * 1024,
				Quota: config.QuotaConfig{
					Enabled:      true,
					DefaultBytes: 1024 * 1024 * 1024,
					MaxBytes:     10 * 1024 * 1024 * 1024,
				},
			},
			adminPubkey: "abcd1234",
			expectedBUDs: []string{
				"BUD-01", "BUD-02", "BUD-03", "BUD-04", "BUD-05",
				"BUD-06", "BUD-08", "BUD-09", "BUD-10", "BUD-11",
			},
			paymentEnabled: false,
		},
		{
			name: "with payments enabled",
			conf: &config.Config{
				MaxUploadSizeBytes: 100 * 1024 * 1024,
				Payment: config.PaymentConfig{
					Enabled:         true,
					SatoshisPerByte: 0.001,
					MinPaymentSats:  10,
					FreeBytesLimit:  10 * 1024 * 1024,
					Lightning: config.LightningConfig{
						Enabled: true,
					},
					Cashu: config.CashuConfig{
						Enabled: true,
					},
				},
			},
			adminPubkey: "abcd1234",
			expectedBUDs: []string{
				"BUD-01", "BUD-02", "BUD-03", "BUD-04", "BUD-05",
				"BUD-06", "BUD-08", "BUD-09", "BUD-10", "BUD-11", "BUD-07",
			},
			paymentEnabled: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := gin.New()
			r.GET("/.well-known/blossom", getServerCapabilities(nil, tt.conf, tt.adminPubkey))

			req := httptest.NewRequest(http.MethodGet, "/.well-known/blossom", nil)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			assert.Equal(t, http.StatusOK, w.Code)

			var caps ServerCapabilities
			err := json.Unmarshal(w.Body.Bytes(), &caps)
			require.NoError(t, err)

			// Check basic fields
			assert.Equal(t, "Cloistr Blossom", caps.Name)
			assert.Equal(t, "1.2.0", caps.Version)
			assert.Equal(t, tt.adminPubkey, caps.Pubkey)

			// Check BUDs
			assert.ElementsMatch(t, tt.expectedBUDs, caps.BUDs)

			// Check limits
			assert.Equal(t, int64(tt.conf.MaxUploadSizeBytes), caps.Limits.MaxUploadSize)

			// Check payment
			if tt.paymentEnabled {
				require.NotNil(t, caps.Payment)
				assert.Contains(t, caps.Payment.Methods, "lightning")
				assert.Contains(t, caps.Payment.Methods, "cashu")
				assert.Equal(t, tt.conf.Payment.SatoshisPerByte, caps.Payment.SatoshisPerByte)
				assert.Equal(t, tt.conf.Payment.FreeBytesLimit, caps.Payment.FreeTierBytes)
			} else {
				assert.Nil(t, caps.Payment)
			}
		})
	}
}

func TestCapabilitiesFeatureFlags(t *testing.T) {
	gin.SetMode(gin.TestMode)

	conf := &config.Config{
		Encryption: config.EncryptionConfig{Enabled: true},
		CDN:        config.CDNConfig{Enabled: true},
		IPFS:       config.IPFSConfig{Enabled: true},
		Transcoding: config.TranscodingConfig{
			WorkDir: "/tmp/transcode",
		},
		ChunkedUpload: config.ChunkedUploadConfig{
			Enabled: true,
		},
		RateLimiting: config.RateLimitingConfig{
			Enabled: true,
		},
	}

	r := gin.New()
	r.GET("/.well-known/blossom", getServerCapabilities(nil, conf, ""))

	req := httptest.NewRequest(http.MethodGet, "/.well-known/blossom", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	var caps ServerCapabilities
	err := json.Unmarshal(w.Body.Bytes(), &caps)
	require.NoError(t, err)

	assert.True(t, caps.Features.Encryption)
	assert.True(t, caps.Features.CDN)
	assert.True(t, caps.Features.IPFS)
	assert.True(t, caps.Features.Transcoding)
	assert.True(t, caps.Features.ChunkedUpload)
	assert.True(t, caps.Features.TusUpload)
	assert.True(t, caps.Limits.RateLimitEnabled)
}
