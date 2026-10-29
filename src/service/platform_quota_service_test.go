package service

import (
	"context"
	"errors"
	"testing"

	"go.uber.org/zap"

	"git.aegis-hq.xyz/coldforge/cloistr-blossom/src/core"
)

// SetQuota cannot take effect in platform mode, so it must say so rather
// than report success.
func TestPlatformQuotaService_SetQuotaRefuses(t *testing.T) {
	svc, err := NewPlatformQuotaService(nil, 0, 0, true, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	err = svc.SetQuota(context.Background(), "abc", 1<<30)
	if !errors.Is(err, core.ErrQuotaManagedCentrally) {
		t.Fatalf("SetQuota error = %v, want ErrQuotaManagedCentrally", err)
	}
}
