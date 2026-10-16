package db

import (
	"context"
	"path/filepath"
	"testing"
)

func TestNumberPlaceholders(t *testing.T) {
	tests := []struct {
		name, in, want string
	}{
		{"in order", "SELECT * FROM t WHERE a = $1 AND b = $2", "SELECT * FROM t WHERE a = ?1 AND b = ?2"},
		{"out of order", "UPDATE t SET a = $2, b = $3 WHERE id = $1", "UPDATE t SET a = ?2, b = ?3 WHERE id = ?1"},
		{"reused", "SELECT $1 WHERE x = $1", "SELECT ?1 WHERE x = ?1"},
		{"multi digit", "VALUES ($9, $10, $11)", "VALUES (?9, ?10, ?11)"},
		{"string literal untouched", "SELECT '$1 costs $2' WHERE a = $1", "SELECT '$1 costs $2' WHERE a = ?1"},
		{"escaped quote in literal", "SELECT 'it''s $1' WHERE a = $1", "SELECT 'it''s $1' WHERE a = ?1"},
		{"quoted identifier untouched", `SELECT "$1" FROM t WHERE a = $1`, `SELECT "$1" FROM t WHERE a = ?1`},
		{"line comment untouched", "SELECT 1 -- uses $2\nWHERE a = $1", "SELECT 1 -- uses $2\nWHERE a = ?1"},
		{"block comment untouched", "SELECT /* $2 */ 1 WHERE a = $1", "SELECT /* $2 */ 1 WHERE a = ?1"},
		{"bare dollar untouched", "SELECT '$' || $1, $x", "SELECT '$' || ?1, $x"},
		{"no placeholders", "SELECT 1", "SELECT 1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := numberPlaceholders(tt.in); got != tt.want {
				t.Errorf("numberPlaceholders(%q)\n got  %q\n want %q", tt.in, got, tt.want)
			}
		})
	}
}

// TestSQLite_OutOfOrderPlaceholders runs a generated query whose $N
// placeholders appear out of order (WHERE id = $1 after $2, $3) against SQLite.
func TestSQLite_OutOfOrderPlaceholders(t *testing.T) {
	database, err := NewDB(filepath.Join(t.TempDir(), "t.sqlite3"), "migrations")
	if err != nil {
		t.Fatalf("NewDB: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	q := New(database)
	ctx := context.Background()

	if _, err := q.CreateUploadSession(ctx, CreateUploadSessionParams{
		ID: "s1", Pubkey: "pk", TotalSize: 10, ChunkSize: 5, EncryptionMode: "none",
		Status: "active", Created: 1, Updated: 1, ExpiresAt: 100,
	}); err != nil {
		t.Fatalf("CreateUploadSession: %v", err)
	}
	if err := q.UpdateUploadSessionStatus(ctx, UpdateUploadSessionStatusParams{
		ID: "s1", Status: "complete", Updated: 2,
	}); err != nil {
		t.Fatalf("UpdateUploadSessionStatus: %v", err)
	}
	got, err := q.GetUploadSession(ctx, "s1")
	if err != nil {
		t.Fatalf("GetUploadSession: %v", err)
	}
	if got.Status != "complete" || got.Updated != 2 {
		t.Fatalf("session after update: status=%q updated=%d, want complete/2", got.Status, got.Updated)
	}
}
