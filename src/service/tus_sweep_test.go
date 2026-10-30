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

// panickySweepStore panics on its first sweep and reports each later one.
type panickySweepStore struct {
	core.TusUploadStore
	calls int
	swept chan struct{}
}

func (s *panickySweepStore) DeleteExpired(ctx context.Context, now int64) (int, error) {
	s.calls++
	if s.calls == 1 {
		panic("sweep exploded")
	}
	select {
	case s.swept <- struct{}{}:
	default:
	}
	return 0, nil
}

// A panic in one sweep is logged and the next sweep still runs; it must not
// take the API down with it.
func TestTusExpirySweep_SurvivesPanic(t *testing.T) {
	store := &panickySweepStore{swept: make(chan struct{}, 1)}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		RunTusExpirySweep(ctx, store, 10*time.Millisecond, zap.NewNop())
	}()

	select {
	case <-store.swept:
	case <-time.After(5 * time.Second):
		t.Fatal("no sweep ran after the one that panicked")
	}
	cancel()
	<-done
}

// Expired uploads are listed and removed a batch at a time, until a short
// batch shows none are left.
func TestTusStore_DeleteExpiredInBatches(t *testing.T) {
	store, dir := newSQLiteTusStore(t)
	store.(*tusStore).sweepBatch = 2
	ctx := context.Background()
	now := time.Now().Unix()
	for _, id := range []string{"a", "b", "c", "d", "e"} {
		createTusUpload(t, store, id, 5, now+3600)
		_, _ = store.AppendChunk(ctx, id, 0, []byte("x"))
		expireTusUpload(t, store, id, now-1)
	}
	createTusUpload(t, store, "live", 5, now+3600)
	_, _ = store.AppendChunk(ctx, "live", 0, []byte("y"))

	n, err := store.DeleteExpired(ctx, now)
	if err != nil {
		t.Fatalf("DeleteExpired: %v", err)
	}
	if n != 5 {
		t.Fatalf("removed %d uploads, want 5", n)
	}
	for _, id := range []string{"a", "b", "c", "d", "e"} {
		if _, err := store.Get(ctx, id); err != core.ErrTusUploadNotFound {
			t.Fatalf("expired upload %s still there: %v", id, err)
		}
	}
	if _, err := store.Get(ctx, "live"); err != nil {
		t.Fatalf("live upload: %v", err)
	}
	if c := countObjects(t, dir); c != 1 {
		t.Fatalf("%d objects left, want only the live upload's chunk", c)
	}
}
