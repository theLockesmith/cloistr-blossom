package service

import (
	"context"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"go.uber.org/zap"

	"git.aegis-hq.xyz/coldforge/cloistr-blossom/db"
	"git.aegis-hq.xyz/coldforge/cloistr-blossom/internal/storage"
	"git.aegis-hq.xyz/coldforge/cloistr-blossom/src/core"
)

// An expired blob must give its size back to every key that was charged for it.
//
// The worker used to ask the quota service to recalculate each owner's usage.
// In platform mode (production) nothing recomputes usage, so that call did
// nothing and expired blobs counted against their owners forever. The mock
// quota service refuses RecalculateUsage the same way the platform one does.
func TestCleanupExpiredReleasesUsageForEveryOwner(t *testing.T) {
	database, err := db.NewDBWithConfig(
		db.DBConfig{Driver: "sqlite", DSN: t.TempDir() + "/expiry.sqlite3"},
		"../../db/migrations",
	)
	if err != nil {
		t.Fatalf("open sqlite test database: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	queries := db.New(database)
	store, err := storage.NewLocalStorage(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocalStorage: %v", err)
	}
	blobs, err := NewBlobService(database, queries, store, nil, "http://test.invalid", zap.NewNop())
	if err != nil {
		t.Fatalf("NewBlobService: %v", err)
	}
	quota := newMockQuotaService(true)

	ctx := context.Background()
	const (
		alice   = "00000000000000000000000000000000000000000000000000000000000000a1"
		bob     = "00000000000000000000000000000000000000000000000000000000000000b2"
		expired = "00000000000000000000000000000000000000000000000000000000000000e1"
		kept    = "00000000000000000000000000000000000000000000000000000000000000f1"
	)
	expiredData := []byte("bytes that expire")
	keptData := []byte("bytes that stay")

	// Both keys own the expiring blob (dedup) and are each charged its full
	// size, as the upload path does. Alice also owns a blob that stays.
	for _, pk := range []string{alice, bob} {
		if _, _, err := blobs.SaveWithDedup(ctx, pk, expired, "", int64(len(expiredData)), "text/plain", expiredData, 1, core.EncryptionModeNone); err != nil {
			t.Fatalf("save expiring blob for %s: %v", pk, err)
		}
		quota.quotaUsage[pk] += int64(len(expiredData))
	}
	if _, _, err := blobs.SaveWithDedup(ctx, alice, kept, "", int64(len(keptData)), "text/plain", keptData, 1, core.EncryptionModeNone); err != nil {
		t.Fatalf("save kept blob: %v", err)
	}
	quota.quotaUsage[alice] += int64(len(keptData))

	svc := NewExpirationService(database, queries, store, quota, core.ExpirationConfig{}, zap.NewNop()).(*expirationService)
	if err := svc.SetExpiration(ctx, expired, time.Now().Add(-time.Hour)); err != nil {
		t.Fatalf("SetExpiration: %v", err)
	}

	deleted, err := svc.cleanupExpiredLocked(ctx)
	if err != nil {
		t.Fatalf("cleanupExpiredLocked: %v", err)
	}
	if deleted != 1 {
		t.Fatalf("deleted %d blobs, want 1", deleted)
	}

	if got, want := quota.quotaUsage[alice], int64(len(keptData)); got != want {
		t.Errorf("alice usage after expiry = %d, want %d (only the blob that stays)", got, want)
	}
	if got := quota.quotaUsage[bob]; got != 0 {
		t.Errorf("bob usage after expiry = %d, want 0", got)
	}
}
