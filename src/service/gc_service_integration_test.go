//go:build integration

package service

// Integration tests for the GC service. These tests require a live Postgres
// instance and are excluded from the normal `go test ./...` / ci-verify run.
//
// Run manually:
//
//	GOWORK=off go test -v -tags=integration ./src/service/... -run TestDeleteOwnerlessBlob_TOCTOU
//	GOWORK=off go test -v -tags=integration ./src/service/... -run TestReconcile_AdvisoryLock
//	GOWORK=off go test -v -tags=integration ./src/service/...   # all tests in package
//
// TestMain starts an ephemeral postgres:16 container on a non-default port,
// applies the minimal schema, runs every test in the package (including the
// pre-existing unit tests which do not need a DB), then stops the container.
// If docker is not available, integration-specific tests skip themselves and
// the unit tests still run.

import (
	"context"
	crand "crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"os/exec"
	"testing"
	"time"

	_ "github.com/lib/pq"
	"go.uber.org/zap"

	"git.aegis-hq.xyz/coldforge/cloistr-blossom/db"
	"git.aegis-hq.xyz/coldforge/cloistr-blossom/src/core"
)

// integrationDB is set by TestMain when an ephemeral container is running.
// Integration tests check for nil and call t.Skip when it is unset.
var integrationDB *sql.DB

// gcIntegrationContainerName is the Docker container name chosen by TestMain.
var gcIntegrationContainerName string

// TestMain is the entry point for the service package test binary when the
// integration build tag is active. It owns the lifecycle of the ephemeral
// Postgres container and calls m.Run() so that existing unit tests continue to
// run as part of the same binary.
func TestMain(m *testing.M) {
	os.Exit(runIntegrationMain(m))
}

// runIntegrationMain separates the logic from TestMain so that deferred cleanup
// runs before os.Exit is called (deferred calls are skipped by os.Exit).
func runIntegrationMain(m *testing.M) int {
	// If docker is not on PATH, fall back gracefully: unit tests still run and
	// integration tests skip themselves via the nil integrationDB guard.
	if _, err := exec.LookPath("docker"); err != nil {
		fmt.Fprintln(os.Stderr, "gc integration: docker not found; DB integration tests will be skipped")
		return m.Run()
	}

	// Pick a random high port to avoid clashing with anything else on the host.
	port := 17100 + rand.IntN(900)
	gcIntegrationContainerName = fmt.Sprintf("blossom-gc-inttest-%d", port)
	dsn := fmt.Sprintf(
		"host=localhost port=%d user=postgres password=testpass dbname=testdb sslmode=disable",
		port,
	)

	// Start an ephemeral postgres:16 container.
	startOut, startErr := exec.Command(
		"docker", "run", "-d", "--rm",
		"--name", gcIntegrationContainerName,
		"-p", fmt.Sprintf("%d:5432", port),
		"-e", "POSTGRES_PASSWORD=testpass",
		"-e", "POSTGRES_DB=testdb",
		"-e", "POSTGRES_USER=postgres",
		"postgres:16",
	).CombinedOutput()
	if startErr != nil {
		fmt.Fprintf(os.Stderr, "gc integration: failed to start postgres container: %v\n%s\n", startErr, startOut)
		// No container to clean up; just run tests (integration tests will skip).
		return m.Run()
	}
	defer stopGCIntegrationContainer()

	// Poll until Postgres accepts connections (up to 30 s).
	sqlDB, err := waitForPostgres(dsn, 30*time.Second)
	if err != nil {
		fmt.Fprintf(os.Stderr, "gc integration: postgres did not become ready: %v\n", err)
		// Run anyway so unit tests still execute.
		return m.Run()
	}
	defer sqlDB.Close()

	// Apply the minimal schema required by GC queries.
	if err := applyGCIntegrationSchema(sqlDB); err != nil {
		fmt.Fprintf(os.Stderr, "gc integration: schema setup failed: %v\n", err)
		return m.Run()
	}

	integrationDB = sqlDB
	return m.Run()
}

func stopGCIntegrationContainer() {
	if gcIntegrationContainerName == "" {
		return
	}
	if out, err := exec.Command("docker", "stop", gcIntegrationContainerName).CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "gc integration: failed to stop container %s: %v\n%s\n",
			gcIntegrationContainerName, err, out)
	}
}

// waitForPostgres polls dsn until a Ping succeeds or the deadline elapses.
func waitForPostgres(dsn string, timeout time.Duration) (*sql.DB, error) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		time.Sleep(300 * time.Millisecond)
		candidate, err := sql.Open("postgres", dsn)
		if err != nil {
			continue
		}
		if pingErr := candidate.Ping(); pingErr == nil {
			return candidate, nil
		}
		candidate.Close()
	}
	return nil, fmt.Errorf("timed out waiting for postgres at %s", dsn)
}

// applyGCIntegrationSchema creates the two tables that the GC queries touch.
// This intentionally avoids running the full migration suite so the test
// harness starts quickly and does not depend on seed data.
func applyGCIntegrationSchema(sqlDB *sql.DB) error {
	_, err := sqlDB.Exec(`
		CREATE TABLE IF NOT EXISTS blobs (
			pubkey           TEXT    NOT NULL,
			hash             TEXT    PRIMARY KEY,
			type             TEXT    NOT NULL,
			size             BIGINT  NOT NULL,
			blob             BYTEA   NOT NULL,
			created          BIGINT  NOT NULL,
			encryption_mode  TEXT    NOT NULL DEFAULT 'none',
			encrypted_dek    TEXT,
			encryption_nonce TEXT,
			original_size    BIGINT,
			ref_count        INTEGER NOT NULL DEFAULT 1,
			expires_at       BIGINT
		);

		CREATE TABLE IF NOT EXISTS blob_references (
			pubkey  TEXT   NOT NULL,
			hash    TEXT   NOT NULL REFERENCES blobs(hash) ON DELETE CASCADE,
			created BIGINT NOT NULL,
			PRIMARY KEY (pubkey, hash)
		);

		CREATE INDEX IF NOT EXISTS idx_blob_references_hash   ON blob_references(hash);
		CREATE INDEX IF NOT EXISTS idx_blob_references_pubkey ON blob_references(pubkey);
	`)
	return err
}

// ─── helpers ────────────────────────────────────────────────────────────────

// randomHash returns a random 64-hex-character string (SHA-256 size).
func randomHash() string {
	b := make([]byte, 32)
	if _, err := crand.Read(b); err != nil {
		panic(fmt.Sprintf("crypto/rand.Read: %v", err))
	}
	return hex.EncodeToString(b)
}

// insertOwnerlessBlob inserts a blobs row with no matching blob_references entry
// and registers a t.Cleanup that removes both tables' rows on test completion.
func insertOwnerlessBlob(t *testing.T, sqlDB *sql.DB, hash string) {
	t.Helper()
	_, err := sqlDB.ExecContext(
		context.Background(),
		`INSERT INTO blobs (pubkey, hash, type, size, blob, created, ref_count)
		 VALUES ($1, $2, 'application/octet-stream', 1, $3, $4, 1)`,
		"testowner-"+hash,
		hash,
		[]byte{0x00},
		time.Now().Unix(),
	)
	if err != nil {
		t.Fatalf("insertOwnerlessBlob %q: %v", hash, err)
	}
	t.Cleanup(func() {
		// Delete blob_references first (FK constraint); ignore errors since the
		// row may already be gone if Reconcile deleted it.
		sqlDB.ExecContext(context.Background(), //nolint:errcheck
			"DELETE FROM blob_references WHERE hash = $1", hash)
		sqlDB.ExecContext(context.Background(), //nolint:errcheck
			"DELETE FROM blobs WHERE hash = $1", hash)
	})
}

// insertBlobReference adds a blob_references row. The blob row must exist first
// (FK constraint).
func insertBlobReference(t *testing.T, sqlDB *sql.DB, pubkey, hash string) {
	t.Helper()
	_, err := sqlDB.ExecContext(
		context.Background(),
		`INSERT INTO blob_references (pubkey, hash, created) VALUES ($1, $2, $3)`,
		pubkey, hash, time.Now().Unix(),
	)
	if err != nil {
		t.Fatalf("insertBlobReference (pubkey=%q hash=%q): %v", pubkey, hash, err)
	}
}

// blobRowExists reports whether a blobs row with hash still exists.
func blobRowExists(t *testing.T, sqlDB *sql.DB, hash string) bool {
	t.Helper()
	var count int
	if err := sqlDB.QueryRowContext(
		context.Background(),
		"SELECT COUNT(*) FROM blobs WHERE hash = $1", hash,
	).Scan(&count); err != nil {
		t.Fatalf("blobRowExists query: %v", err)
	}
	return count > 0
}

// newTestGCService constructs a gcService backed by sqlDB with a no-op logger
// and nil storage backend. nil storage is safe: deleteOwnerless checks
// `if s.storage != nil` before calling Delete.
func newTestGCService(sqlDB *sql.DB) *gcService {
	return &gcService{
		rawDB:   sqlDB,
		queries: db.New(sqlDB),
		storage: nil,
		config:  core.DefaultGCConfig(),
		log:     zap.NewNop(),
		stopCh:  make(chan struct{}),
	}
}

// ─── Test 1: guarded-delete TOCTOU safety ───────────────────────────────────

// TestDeleteOwnerlessBlob_TOCTOU verifies the atomic NOT-EXISTS guard in the
// DeleteOwnerlessBlob query prevents destroying a blob that gains an owner
// between the ownerless scan and the delete statement.
//
// Two sub-cases:
//
//  1. Happy path – a genuinely ownerless blob is deleted and its hash returned.
//  2. TOCTOU guard – if a blob_references row is inserted after the ownerless
//     scan but before the delete, DeleteOwnerlessBlob returns sql.ErrNoRows
//     and leaves the blobs row intact.
func TestDeleteOwnerlessBlob_TOCTOU(t *testing.T) {
	if integrationDB == nil {
		t.Skip("no ephemeral postgres available; start docker to run GC integration tests")
	}

	ctx := context.Background()
	q := db.New(integrationDB)

	t.Run("happy path: genuinely ownerless blob is deleted", func(t *testing.T) {
		hash := randomHash()
		insertOwnerlessBlob(t, integrationDB, hash)

		// Precondition: the row exists and has no owner.
		if !blobRowExists(t, integrationDB, hash) {
			t.Fatal("precondition: blob row not found before delete")
		}

		returnedHash, err := q.DeleteOwnerlessBlob(ctx, hash)
		if err != nil {
			t.Fatalf("DeleteOwnerlessBlob returned unexpected error: %v", err)
		}
		if returnedHash != hash {
			t.Fatalf("DeleteOwnerlessBlob returned hash %q, want %q", returnedHash, hash)
		}
		if blobRowExists(t, integrationDB, hash) {
			t.Fatal("blobs row still exists after a successful ownerless delete")
		}
	})

	t.Run("TOCTOU guard: blob that gained an owner is NOT deleted", func(t *testing.T) {
		hash := randomHash()
		insertOwnerlessBlob(t, integrationDB, hash)

		// Simulate the TOCTOU window: between ListOwnerlessBlobs (which produced
		// this hash as a candidate) and DeleteOwnerlessBlob, a concurrent upload
		// of the same content inserts a blob_references row for a new owner.
		insertBlobReference(t, integrationDB, "new-owner-"+hash, hash)

		// The DELETE … WHERE NOT EXISTS guard re-evaluates the reference set
		// atomically. It must find the new owner and delete zero rows.
		_, err := q.DeleteOwnerlessBlob(ctx, hash)
		if !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("expected sql.ErrNoRows when blob has an owner, got: %v", err)
		}
		// The blob must be intact: the TOCTOU guard prevented destruction of a
		// blob that a new upload now depends on.
		if !blobRowExists(t, integrationDB, hash) {
			t.Fatal("blobs row was deleted despite having an owner; TOCTOU guard failed")
		}
	})
}

// ─── Test 2: advisory-lock serialization ────────────────────────────────────

// TestReconcile_AdvisoryLock verifies that reconcileLocked serializes correctly
// on the cross-replica Postgres advisory lock (key: gcLockKey).
//
// Two sub-cases:
//
//  1. While a separate connection holds the advisory lock (simulating a
//     concurrent replica or a concurrent operator call), Reconcile must return
//     LockSkipped=true and Deleted=0 without touching any rows.
//  2. After the external lock is released, a subsequent Reconcile must acquire
//     the lock, run the sweep, and return LockSkipped=false.
func TestReconcile_AdvisoryLock(t *testing.T) {
	if integrationDB == nil {
		t.Skip("no ephemeral postgres available; start docker to run GC integration tests")
	}

	ctx := context.Background()
	svc := newTestGCService(integrationDB)

	t.Run("LockSkipped when another connection holds the advisory lock", func(t *testing.T) {
		// Acquire the advisory lock on a dedicated, pinned connection, simulating
		// another replica (or another worker goroutine) that already holds it.
		lockConn, err := integrationDB.Conn(ctx)
		if err != nil {
			t.Fatalf("acquire lock connection: %v", err)
		}
		defer lockConn.Close()

		var acquired bool
		if err := lockConn.QueryRowContext(
			ctx, "SELECT pg_try_advisory_lock($1)", gcLockKey,
		).Scan(&acquired); err != nil {
			t.Fatalf("pg_try_advisory_lock on lock connection: %v", err)
		}
		if !acquired {
			t.Fatal("test setup failure: could not acquire advisory lock on the dedicated lock connection")
		}
		t.Cleanup(func() {
			// Release explicitly on the same connection so the lock is freed even
			// if the connection is reused (though it is closed by defer above).
			lockConn.ExecContext( //nolint:errcheck
				context.Background(),
				"SELECT pg_advisory_unlock($1)", gcLockKey,
			)
		})

		// reconcileLocked will call pg_try_advisory_lock on a DIFFERENT connection.
		// Because lockConn already holds the session-scoped advisory lock, the
		// service's attempt must return false → LockSkipped.
		result, err := svc.Reconcile(ctx, false /*dryRun*/, 10)
		if err != nil {
			t.Fatalf("Reconcile returned unexpected error while lock is held externally: %v", err)
		}
		if !result.LockSkipped {
			t.Fatal("expected LockSkipped=true while advisory lock is held by another connection")
		}
		if result.Deleted != 0 {
			t.Fatalf("expected Deleted=0 when run is lock-skipped, got %d", result.Deleted)
		}
	})

	t.Run("Reconcile proceeds and acquires lock after external lock is released", func(t *testing.T) {
		// Sanity-check: the advisory lock is free (not held by any connection in
		// this test). Insert an ownerless blob so the sweep has a real work item.
		hash := randomHash()
		insertOwnerlessBlob(t, integrationDB, hash)

		// No external lock is held. Reconcile must acquire the lock, run the
		// sweep, delete the ownerless blob, and return LockSkipped=false.
		result, err := svc.Reconcile(ctx, false /*dryRun*/, 10)
		if err != nil {
			t.Fatalf("Reconcile returned unexpected error with no external lock: %v", err)
		}
		if result.LockSkipped {
			t.Fatal("expected LockSkipped=false when the advisory lock is free")
		}
		// The blob we inserted must appear in the deleted set (Deleted >= 1).
		// In the unlikely event another test's cleanup raced ahead, we accept
		// Deleted >= 0 but assert the blob itself is gone.
		deleted := false
		for _, h := range result.Hashes {
			if h == hash {
				deleted = true
				break
			}
		}
		if !deleted && blobRowExists(t, integrationDB, hash) {
			// The blob still exists AND Reconcile did not delete it. This is a
			// genuine failure: either the lock was not acquired or the sweep did
			// not run correctly.
			t.Fatalf(
				"blob %q was not deleted by Reconcile even though it is ownerless "+
					"and the advisory lock was free (Deleted=%d)",
				hash, result.Deleted,
			)
		}
	})
}
