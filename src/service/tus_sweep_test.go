package service

import (
	"bytes"
	"context"
	"testing"
	"time"

	"go.uber.org/zap"

	"git.aegis-hq.xyz/coldforge/cloistr-blossom/src/core"
)

// The sweep used to run only when a new upload was created, so with no new
// uploads an abandoned one kept its rows and chunk objects forever.
func TestTusExpirySweep_RunsWithoutUploadTraffic(t *testing.T) {
	store, dir := newSQLiteTusStore(t)
	ctx := context.Background()
	now := time.Now().Unix()
	createTusUpload(t, store, "old", 5, now+3600)
	createTusUpload(t, store, "live", 5, now+3600)
	_, _ = store.AppendChunk(ctx, "old", 0, []byte("ab"))
	_, _ = store.AppendChunk(ctx, "live", 0, []byte("cd"))
	expireTusUpload(t, store, "old", now-1)

	sweepCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		RunTusExpirySweep(sweepCtx, store, time.Hour, zap.NewNop())
		close(done)
	}()

	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := store.Get(ctx, "old"); err == core.ErrTusUploadNotFound {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the expired upload was never swept")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	<-done

	if got, err := store.ReadAll(ctx, "live"); err != nil || !bytes.Equal(got, []byte("cd")) {
		t.Fatalf("live upload = %q, %v", got, err)
	}
	if n := countObjects(t, dir); n != 1 {
		t.Fatalf("%d objects left, want only the live upload's chunk", n)
	}
}

// Expiry counts from the last chunk, not from creation: an upload still
// receiving chunks is not abandoned.
func TestTusStore_ChunkExtendsExpiry(t *testing.T) {
	store, _ := newSQLiteTusStore(t)
	ctx := context.Background()
	soon := time.Now().Unix() + 60
	createTusUpload(t, store, "slow", 5, soon)

	if _, err := store.AppendChunk(ctx, "slow", 0, []byte("ab")); err != nil {
		t.Fatalf("AppendChunk: %v", err)
	}
	got, err := store.Get(ctx, "slow")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if want := time.Now().Add(core.TusUploadTTL).Unix() - 5; got.ExpiresAt < want {
		t.Fatalf("ExpiresAt = %d, want at least %d (a full window from the chunk)", got.ExpiresAt, want)
	}
}

// The sweep lists expired uploads, then removes them one by one. A chunk
// landing in between moves the expiry forward, and the upload must survive.
func TestTusStore_SweepSparesUploadRefreshedAfterListing(t *testing.T) {
	store, dir := newSQLiteTusStore(t)
	ctx := context.Background()
	now := time.Now().Unix()
	createTusUpload(t, store, "busy", 5, now+3600)
	_, _ = store.AppendChunk(ctx, "busy", 0, []byte("ab"))

	// As if "busy" had been listed as expired at now.
	removed, err := store.(*tusStore).deleteIfExpired(ctx, "busy", now)
	if err != nil {
		t.Fatalf("deleteIfExpired: %v", err)
	}
	if removed {
		t.Fatal("removed an upload that is no longer expired")
	}
	if got, err := store.ReadAll(ctx, "busy"); err != nil || !bytes.Equal(got, []byte("ab")) {
		t.Fatalf("busy upload = %q, %v", got, err)
	}
	if n := countObjects(t, dir); n != 1 {
		t.Fatalf("%d objects left, want the busy upload's chunk", n)
	}
}
