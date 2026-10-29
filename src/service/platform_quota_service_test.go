package service

import (
	"context"
	"errors"
	"testing"

	"go.uber.org/zap"

	"git.aegis-hq.xyz/coldforge/cloistr-blossom/src/core"
)

// In platform mode these admin operations cannot take effect here, so each
// must say so rather than report success.
func TestPlatformQuotaService_AdminWritesRefuse(t *testing.T) {
	svc, err := NewPlatformQuotaService(nil, 0, 0, true, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	cases := []struct {
		name string
		call func() error
		want error
	}{
		{"SetQuota", func() error { return svc.SetQuota(ctx, "abc", 1<<30) }, core.ErrQuotaManagedCentrally},
		{"BanUser", func() error { return svc.BanUser(ctx, "abc") }, core.ErrBanNotInPlatformMode},
		{"UnbanUser", func() error { return svc.UnbanUser(ctx, "abc") }, core.ErrBanNotInPlatformMode},
		{"RecalculateUsage", func() error { return svc.RecalculateUsage(ctx, "abc") }, core.ErrRecalculateNotInPlatformMode},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.call(); !errors.Is(err, tc.want) {
				t.Fatalf("error = %v, want %v", err, tc.want)
			}
		})
	}
}
