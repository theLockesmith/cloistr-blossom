package config

import "testing"

// The upload bandwidth check runs on Content-Length BEFORE the body is read,
// so a per-minute budget smaller than the largest allowed upload refuses every
// upload above it, forever, no matter how idle the client is.
func TestUploadBandwidthCoversMaxUpload(t *testing.T) {
	c := &Config{MaxUploadSizeBytes: 100 << 20}
	c.RateLimiting.Enabled = true
	c.RateLimiting.Bandwidth.UploadMBPerMinute = 50
	c.ApplyDefaults()
	if got := int64(c.RateLimiting.Bandwidth.UploadMBPerMinute) << 20; got < int64(c.MaxUploadSizeBytes) {
		t.Fatalf("upload budget %d bytes/min is below max upload %d: large uploads can never pass", got, c.MaxUploadSizeBytes)
	}
}

func TestUploadBandwidthKeptWhenAlreadyLarger(t *testing.T) {
	c := &Config{MaxUploadSizeBytes: 100 << 20}
	c.RateLimiting.Enabled = true
	c.RateLimiting.Bandwidth.UploadMBPerMinute = 1000
	c.ApplyDefaults()
	if c.RateLimiting.Bandwidth.UploadMBPerMinute != 1000 {
		t.Fatalf("an explicit larger budget must be kept, got %d", c.RateLimiting.Bandwidth.UploadMBPerMinute)
	}
}
