//go:build integration

package service

// Integration tests for the platform ACR service. They share the ephemeral
// Postgres started by TestMain in gc_service_integration_test.go and add a
// minimal stand-in for the platform users table, has_service_access() and
// ensure_user().
//
//	GOWORK=off go test -v -tags=integration ./src/service/... -run TestPlatformACR

import (
	"context"
	"errors"
	"testing"

	"go.uber.org/zap"

	"git.aegis-hq.xyz/coldforge/cloistr-blossom/src/core"
	"git.aegis-hq.xyz/coldforge/cloistr-common/platform"
)

// applyPlatformACRSchema mirrors the parts of the platform schema the access
// check depends on: has_service_access() denies a pubkey with no users row and
// a pubkey whose row is disabled; ensure_user() inserts a row if missing. The stub ignores the service argument; these
// tests cover row provisioning, not per-service grants.
func applyPlatformACRSchema(t *testing.T) {
	t.Helper()
	_, err := integrationDB.Exec(`
		CREATE TABLE IF NOT EXISTS users (
			pubkey  CHAR(64) PRIMARY KEY,
			enabled BOOLEAN NOT NULL DEFAULT TRUE
		);
		CREATE OR REPLACE FUNCTION has_service_access(check_pubkey CHAR(64), check_service VARCHAR)
		RETURNS BOOLEAN AS $$
			SELECT COALESCE((SELECT enabled FROM users WHERE pubkey = check_pubkey), FALSE);
		$$ LANGUAGE sql STABLE;
		CREATE OR REPLACE FUNCTION ensure_user(check_pubkey CHAR(64))
		RETURNS void AS $$
			INSERT INTO users (pubkey) VALUES (check_pubkey) ON CONFLICT (pubkey) DO NOTHING;
		$$ LANGUAGE sql;
	`)
	if err != nil {
		t.Fatalf("apply platform ACR schema: %v", err)
	}
}

func newTestPlatformACRService(t *testing.T) core.ACRStorage {
	t.Helper()
	client, err := platform.NewClient(platform.Config{
		Mode:        platform.ModePlatform,
		DatabaseURL: integrationDSN,
		ServiceID:   "blossom",
	})
	if err != nil {
		t.Fatalf("platform.NewClient: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })

	acr, err := NewPlatformACRService(client, zap.NewNop())
	if err != nil {
		t.Fatalf("NewPlatformACRService: %v", err)
	}
	return acr
}

func userEnabled(t *testing.T, pubkey string) (exists, enabled bool) {
	t.Helper()
	err := integrationDB.QueryRow(`SELECT enabled FROM users WHERE pubkey = $1`, pubkey).Scan(&enabled)
	if err != nil {
		return false, false
	}
	return true, enabled
}

func TestPlatformACR_EnsureUser(t *testing.T) {
	if integrationDB == nil {
		t.Skip("no ephemeral postgres available; start docker to run platform ACR integration tests")
	}
	applyPlatformACRSchema(t)
	acr := newTestPlatformACRService(t)
	ctx := context.Background()

	t.Run("fresh key with no users row is provisioned and allowed", func(t *testing.T) {
		pubkey := randomHash()

		if err := acr.Validate(ctx, pubkey, core.ResourceUpload); err != nil {
			t.Fatalf("Validate() for fresh key = %v, want nil", err)
		}
		if exists, enabled := userEnabled(t, pubkey); !exists || !enabled {
			t.Fatalf("users row after Validate: exists=%v enabled=%v, want true/true", exists, enabled)
		}
	})

	t.Run("disabled user stays disabled and is denied", func(t *testing.T) {
		pubkey := randomHash()
		if _, err := integrationDB.Exec(`INSERT INTO users (pubkey, enabled) VALUES ($1, FALSE)`, pubkey); err != nil {
			t.Fatalf("insert disabled user: %v", err)
		}

		err := acr.Validate(ctx, pubkey, core.ResourceUpload)
		if !errors.Is(err, ErrUnauthorized) {
			t.Fatalf("Validate() for disabled user = %v, want ErrUnauthorized", err)
		}
		if exists, enabled := userEnabled(t, pubkey); !exists || enabled {
			t.Fatalf("disabled users row after Validate: exists=%v enabled=%v, want true/false", exists, enabled)
		}
	})

	t.Run("provisioning is skipped once a pubkey has been ensured", func(t *testing.T) {
		pubkey := randomHash()
		if err := acr.Validate(ctx, pubkey, core.ResourceUpload); err != nil {
			t.Fatalf("first Validate() = %v, want nil", err)
		}
		// Remove the row behind the service's back. A second Validate must not
		// write again, so the row stays gone and access is denied.
		if _, err := integrationDB.Exec(`DELETE FROM users WHERE pubkey = $1`, pubkey); err != nil {
			t.Fatalf("delete user: %v", err)
		}
		if err := acr.Validate(ctx, pubkey, core.ResourceUpload); !errors.Is(err, ErrUnauthorized) {
			t.Fatalf("second Validate() = %v, want ErrUnauthorized (no re-insert)", err)
		}
		if exists, _ := userEnabled(t, pubkey); exists {
			t.Fatal("users row was re-inserted; EnsureUser should run once per pubkey per process")
		}
	})

	t.Run("existing enabled user is unaffected and allowed", func(t *testing.T) {
		pubkey := randomHash()
		if _, err := integrationDB.Exec(`INSERT INTO users (pubkey) VALUES ($1)`, pubkey); err != nil {
			t.Fatalf("insert user: %v", err)
		}

		if err := acr.Validate(ctx, pubkey, core.ResourceUpload); err != nil {
			t.Fatalf("Validate() for existing user = %v, want nil", err)
		}
	})
}
