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
