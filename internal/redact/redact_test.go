package redact

import (
	"fmt"
	"strings"
	"testing"

	"github.com/redis/go-redis/v9"
)

const secret = "s3cr3tP4ss"

func TestURL(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"", "(not set)"},
		{"redis://:" + secret + "@dragonfly.dragonfly.svc:6379", "redis://dragonfly.dragonfly.svc:6379"},
		{"redis://default:" + secret + "@dragonfly:6379/2", "redis://dragonfly:6379"},
		{"rediss://dragonfly:6380", "rediss://dragonfly:6380"},
		{"postgres://blossom:" + secret + "@postgres-rw.db:5432/cloistr?sslmode=require", "postgres://postgres-rw.db:5432"},
		{"postgres://postgres-rw.db:5432/cloistr?password=" + secret, "postgres://postgres-rw.db:5432"},
		{"postgres://u:p@ss" + secret + "@host:5432/db", "postgres://host:5432"},
		{"host=db user=u password=" + secret + " dbname=x", "(redacted)"},
		{"redis://:" + secret + "@host:notaport", "(redacted)"},
	}
	for _, tt := range tests {
		got := URL(tt.in)
		if got != tt.want {
			t.Errorf("URL(%q) = %q, want %q", tt.in, got, tt.want)
		}
		if strings.Contains(got, secret) {
			t.Errorf("URL(%q) leaked the password: %q", tt.in, got)
		}
	}
}

func TestErrStripsPasswordFromParseErrors(t *testing.T) {
	// A malformed URL makes net/url echo the whole input, password included.
	bad := "redis://:" + secret + "@dragonfly:notaport"
	_, err := redis.ParseURL(bad)
	if err == nil {
		t.Fatal("expected a parse error")
	}
	if !strings.Contains(err.Error(), secret) {
		t.Skip("go-redis no longer echoes the URL; nothing to redact")
	}
	got := Err(fmt.Errorf("connect cache: %w", err))
	if strings.Contains(got.Error(), secret) {
		t.Fatalf("redacted error still has the password: %v", got)
	}
	if !strings.Contains(got.Error(), "connect cache:") {
		t.Errorf("redaction lost the surrounding context: %v", got)
	}
}

func TestErrPassesThroughOtherErrors(t *testing.T) {
	if Err(nil) != nil {
		t.Error("Err(nil) should be nil")
	}
	plain := fmt.Errorf("dial tcp 10.0.0.1:6379: connection refused")
	if Err(plain) != plain {
		t.Error("an error with no url.Error should be returned unchanged")
	}
}
