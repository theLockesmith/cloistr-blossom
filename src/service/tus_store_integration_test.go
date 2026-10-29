//go:build integration

package service

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"

	"git.aegis-hq.xyz/coldforge/cloistr-blossom/db"
	"git.aegis-hq.xyz/coldforge/cloistr-blossom/internal/storage"
	"git.aegis-hq.xyz/coldforge/cloistr-blossom/src/core"
)

// applyTusIntegrationSchema creates the tus tables from the real Postgres
// migration.
func applyTusIntegrationSchema(t *testing.T) {
	t.Helper()
	raw, err := os.ReadFile("../../db/migrations/postgres/18_add_tus_uploads.sql")
	if err != nil {
		t.Fatalf("read migration: %v", err)
	}
	up := strings.SplitN(string(raw), "-- +migrate Down", 2)[0]
	if _, err := integrationDB.Exec(up); err != nil {
		t.Fatalf("apply migration: %v", err)
	}
}

// Two replicas appending at the same offset at the same moment: exactly one
// chunk is recorded, the others are refused, and only the winner's bytes are
// left in blob storage.
func TestTusStore_ConcurrentAppendOneWinner_Postgres(t *testing.T) {
	if integrationDB == nil {
		t.Skip("no integration database")
	}
	applyTusIntegrationSchema(t)
	store, dir := newTusStoreOn(t, integrationDB)
	ctx := context.Background()
	id := fmt.Sprintf("race-%d", time.Now().UnixNano())
	createTusUpload(t, store, id, 5, time.Now().Add(time.Hour).Unix())

	const racers = 8
	var wg sync.WaitGroup
	errs := make([]error, racers)
	start := make(chan struct{})
	for i := 0; i < racers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			_, errs[i] = store.AppendChunk(ctx, id, 0, []byte(fmt.Sprintf("r%04d", i)))
		}(i)
	}
	close(start)
	wg.Wait()

	winner := -1
	for i, err := range errs {
		switch err {
		case nil:
			if winner >= 0 {
				t.Fatalf("racers %d and %d both appended at offset 0", winner, i)
			}
			winner = i
		case core.ErrTusOffsetConflict:
		default:
			t.Fatalf("racer %d: unexpected error %v", i, err)
		}
	}
	if winner < 0 {
		t.Fatal("no racer appended")
	}
	got, err := store.ReadAll(ctx, id)
	if err != nil || string(got) != fmt.Sprintf("r%04d", winner) {
		t.Fatalf("ReadAll = %q, %v; want the winner's chunk", got, err)
	}
	if n := countObjects(t, dir); n != 1 {
		t.Fatalf("%d objects in blob storage, want only the winner's", n)
	}
}

// Both replicas run the expiry sweep on their own timer, so two sweeps can
// hit the same expired uploads at once. Each upload goes exactly once, with
// no error, and a live upload is left alone.
func TestTusStore_ConcurrentSweepsBothReplicas_Postgres(t *testing.T) {
	if integrationDB == nil {
		t.Skip("no integration database")
	}
	applyTusIntegrationSchema(t)
	dir := t.TempDir()
	objects, err := storage.NewLocalStorage(dir)
	if err != nil {
		t.Fatalf("NewLocalStorage: %v", err)
	}
	a := NewTusStore(integrationDB, db.New(integrationDB), objects, zap.NewNop())
	b := NewTusStore(integrationDB, db.New(integrationDB), objects, zap.NewNop())
	ctx := context.Background()
	now := time.Now().Unix()
	prefix := fmt.Sprintf("sweep-%d", time.Now().UnixNano())

	const expired = 20
	for i := 0; i < expired; i++ {
		id := fmt.Sprintf("%s-old-%d", prefix, i)
		createTusUpload(t, a, id, 5, now+3600)
		if _, err := a.AppendChunk(ctx, id, 0, []byte("ab")); err != nil {
			t.Fatalf("AppendChunk %s: %v", id, err)
		}
		expireTusUpload(t, a, id, now-1)
	}
	live := prefix + "-live"
	createTusUpload(t, a, live, 5, now+3600)
	if _, err := a.AppendChunk(ctx, live, 0, []byte("cd")); err != nil {
		t.Fatalf("AppendChunk live: %v", err)
	}

	var wg sync.WaitGroup
	start := make(chan struct{})
	errs := make([]error, 2)
	for i, s := range []core.TusUploadStore{a, b} {
		wg.Add(1)
		go func(i int, s core.TusUploadStore) {
			defer wg.Done()
			<-start
			_, errs[i] = s.DeleteExpired(ctx, now)
		}(i, s)
	}
	close(start)
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("sweep %d: %v", i, err)
		}
	}
	for i := 0; i < expired; i++ {
		id := fmt.Sprintf("%s-old-%d", prefix, i)
		if _, err := a.Get(ctx, id); err != core.ErrTusUploadNotFound {
			t.Fatalf("expired upload %s still there: %v", id, err)
		}
	}
	if got, err := b.ReadAll(ctx, live); err != nil || string(got) != "cd" {
		t.Fatalf("live upload = %q, %v", got, err)
	}
	if n := countObjects(t, dir); n != 1 {
		t.Fatalf("%d objects left, want only the live upload's chunk", n)
	}
}
