package urlsign

import (
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"
)

const testKey = "0123456789abcdef0123456789abcdef" // exactly MinKeyLength

func mustSigner(t *testing.T, ttl time.Duration) *Signer {
	t.Helper()
	s, err := NewSigner(testKey, ttl)
	if err != nil {
		t.Fatalf("NewSigner: %v", err)
	}
	return s
}

func TestSignVerifyRoundTrip(t *testing.T) {
	s := mustSigner(t, 0)
	const u = "https://example.com/blobcatpeek.png"

	exp, sig := s.Sign(u)
	if exp != 0 {
		t.Errorf("expiry = %d with no TTL configured, want 0 (never expires)", exp)
	}
	if err := s.Verify(u, exp, sig); err != nil {
		t.Fatalf("Verify on a freshly signed URL: %v", err)
	}
}

// The signature must not vary between calls. A per-call nonce would give every
// user a different link for the same emoji, which would both defeat caching and
// turn each mirrored image URL into a per-user identifier.
func TestSignatureIsDeterministic(t *testing.T) {
	s := mustSigner(t, 0)
	const u = "https://example.com/a.png"

	_, first := s.Sign(u)
	_, second := s.Sign(u)
	if first != second {
		t.Errorf("signature varies between calls: %q vs %q", first, second)
	}
}

func TestVerifyRejectsTampering(t *testing.T) {
	s := mustSigner(t, 0)
	const u = "https://example.com/a.png"
	exp, sig := s.Sign(u)

	t.Run("different url", func(t *testing.T) {
		// The whole point: a signature for one URL must not fetch another.
		if err := s.Verify("https://evil.example/b.png", exp, sig); !errors.Is(err, ErrBadSignature) {
			t.Errorf("error = %v, want ErrBadSignature", err)
		}
	})

	t.Run("altered expiry", func(t *testing.T) {
		// Expiry is inside the MAC, so extending it invalidates the signature
		// rather than extending the link's life.
		if err := s.Verify(u, exp+86400, sig); !errors.Is(err, ErrBadSignature) {
			t.Errorf("error = %v, want ErrBadSignature", err)
		}
	})

	t.Run("altered signature", func(t *testing.T) {
		flipped := []byte(sig)
		if flipped[0] == 'A' {
			flipped[0] = 'B'
		} else {
			flipped[0] = 'A'
		}
		if err := s.Verify(u, exp, string(flipped)); !errors.Is(err, ErrBadSignature) {
			t.Errorf("error = %v, want ErrBadSignature", err)
		}
	})

	t.Run("empty signature", func(t *testing.T) {
		if err := s.Verify(u, exp, ""); err == nil {
			t.Error("empty signature verified")
		}
	})

	t.Run("non-base64 signature", func(t *testing.T) {
		if err := s.Verify(u, exp, "!!!not base64!!!"); !errors.Is(err, ErrMalformed) {
			t.Errorf("error = %v, want ErrMalformed", err)
		}
	})

	t.Run("different key", func(t *testing.T) {
		other, err := NewSigner("ffffffffffffffffffffffffffffffff", 0)
		if err != nil {
			t.Fatalf("NewSigner: %v", err)
		}
		if err := other.Verify(u, exp, sig); !errors.Is(err, ErrBadSignature) {
			t.Errorf("error = %v, want ErrBadSignature — key rotation must invalidate old links", err)
		}
	})
}

// The two signed fields are joined by a delimiter so no pair of distinct
// inputs can produce the same signed bytes. Without one, ("ab", 1) and
// ("a", 1) style confusions become possible on inputs that can contain the
// separator.
func TestSignedFieldsAreUnambiguous(t *testing.T) {
	s := mustSigner(t, 0)
	_, a := s.Sign("https://example.com/a")
	_, b := s.Sign("https://example.com/a\n0")
	if a == b {
		t.Error("two distinct inputs produced the same signature")
	}
}

func TestExpiry(t *testing.T) {
	t.Run("valid before expiry", func(t *testing.T) {
		s := mustSigner(t, time.Hour)
		const u = "https://example.com/a.png"
		exp, sig := s.Sign(u)
		if exp == 0 {
			t.Fatal("expiry not set despite a configured TTL")
		}
		if err := s.Verify(u, exp, sig); err != nil {
			t.Errorf("Verify before expiry: %v", err)
		}
	})

	t.Run("rejected after expiry", func(t *testing.T) {
		s := mustSigner(t, time.Hour)
		const u = "https://example.com/a.png"
		// Sign an already-past expiry directly rather than sleeping.
		past := time.Now().Add(-time.Minute).Unix()
		sig := signAt(s, u, past)
		if err := s.Verify(u, past, sig); !errors.Is(err, ErrExpired) {
			t.Errorf("error = %v, want ErrExpired", err)
		}
	})

	t.Run("zero expiry never expires", func(t *testing.T) {
		s := mustSigner(t, 0)
		const u = "https://example.com/a.png"
		exp, sig := s.Sign(u)
		if err := s.Verify(u, exp, sig); err != nil {
			t.Errorf("Verify with no expiry: %v", err)
		}
	})
}

// A bad signature must be reported as such even when the expiry is also past,
// so that an attacker cannot use the error to learn that their forged
// signature was otherwise well-formed.
func TestBadSignatureTakesPrecedenceOverExpiry(t *testing.T) {
	s := mustSigner(t, time.Hour)
	past := time.Now().Add(-time.Hour).Unix()
	if err := s.Verify("https://example.com/a.png", past, "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"); !errors.Is(err, ErrBadSignature) {
		t.Errorf("error = %v, want ErrBadSignature", err)
	}
}

func signAt(s *Signer, u string, expiresAt int64) string {
	// Mirrors Sign but with a caller-chosen expiry, for testing expiry
	// handling without sleeping.
	return encodeForTest(s.mac(u, expiresAt))
}

func TestNewSignerRejectsWeakKeys(t *testing.T) {
	cases := []struct{ name, key string }{
		{"empty", ""},
		{"one char", "x"},
		{"one below minimum", strings.Repeat("a", MinKeyLength-1)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := NewSigner(tc.key, 0); err == nil {
				t.Error("NewSigner accepted a weak key; an unsigned or guessable mirror is an open proxy")
			}
		})
	}
	if _, err := NewSigner(strings.Repeat("a", MinKeyLength), 0); err != nil {
		t.Errorf("NewSigner rejected a key of exactly MinKeyLength: %v", err)
	}
}

func TestNewSignerRejectsNegativeTTL(t *testing.T) {
	if _, err := NewSigner(testKey, -time.Second); err == nil {
		t.Error("NewSigner accepted a negative TTL")
	}
}

func TestEncodeDecodeURL(t *testing.T) {
	// Base64url is used precisely so URLs containing query strings, slashes,
	// and percent-encoding survive a round trip through a query parameter
	// unchanged.
	for _, u := range []string{
		"https://example.com/a.png",
		"https://example.com/a.png?v=2&x=y",
		"https://example.com/%E2%98%83.png",
		"https://example.com/a+b/c=d",
	} {
		enc := EncodeURL(u)
		if strings.ContainsAny(enc, "+/=&?#") {
			t.Errorf("EncodeURL(%q) = %q contains characters that need escaping in a query", u, enc)
		}
		got, err := DecodeURL(enc)
		if err != nil {
			t.Fatalf("DecodeURL(%q): %v", enc, err)
		}
		if got != u {
			t.Errorf("round trip: %q -> %q", u, got)
		}
	}
}

func TestDecodeURLRejectsGarbage(t *testing.T) {
	if _, err := DecodeURL("!!!!"); !errors.Is(err, ErrMalformed) {
		t.Errorf("error = %v, want ErrMalformed", err)
	}
}

// encodeForTest mirrors the encoding Sign uses, so tests can build a signature
// for an arbitrary expiry.
func encodeForTest(mac []byte) string {
	return base64.RawURLEncoding.EncodeToString(mac)
}
