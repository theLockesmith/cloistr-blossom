package service

import (
	"bytes"
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"git.aegis-hq.xyz/coldforge/cloistr-blossom/src/core"
	"git.aegis-hq.xyz/coldforge/cloistr-blossom/src/pkg/config"
)

// The tier check must see the type that is stored. Plain text declared as
// JavaScript is stored as text/javascript, so a tier that allows only
// text/plain must refuse it.
func TestBatchUpload_ChecksStoredType(t *testing.T) {
	svc, blobs, _, _, _ := setupBatchTest(t)
	limits, err := NewUploadLimitsService(&config.UploadLimitsConfig{
		Enabled: true,
		Named:   config.UploadTierLimitsConfig{AllowedTypePrefixes: []string{"text/plain"}},
	}, nil, nil, zap.NewNop())
	require.NoError(t, err)
	svc.limits = limits
	script := []byte("console.log('hello')")

	resp, err := svc.Upload(context.Background(), &core.BatchUploadRequest{
		Pubkey: "testpubkey",
		Files: []core.BatchUploadFile{
			{Filename: "a.js", MimeType: "text/javascript", Size: int64(len(script)), Reader: bytes.NewReader(script)},
		},
	})
	require.NoError(t, err)
	assert.False(t, resp.Results[0].Success, "stored as text/javascript, which the tier does not allow")
	assert.Empty(t, blobs.blobs)
}
