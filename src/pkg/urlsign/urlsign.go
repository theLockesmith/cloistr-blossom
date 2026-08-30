// Package urlsign mints and verifies HMAC signatures over remote media URLs.
//
// WHY. A media mirror that fetches whatever URL it is handed is an open proxy:
// anyone on the internet can use our bandwidth, our IP reputation, and our
// cache to launder their own traffic. The signature is the answer to "did one
// of our apps ask for this?" — the mirror serves a URL only if it carries a
// signature made with the server's key.
//
// WHAT THIS IS NOT. It is not authentication and it does not identify anybody.
// The signature covers the URL and an expiry, nothing else — no pubkey, no
// session, no nonce. That is deliberate: a signature that varied per user
// would make every mirrored image URL a per-user identifier, which is the
// surveillance this whole feature exists to remove. Because the signature is
// deterministic, everyone who mirrors :blobcatpeek: gets the SAME link, it
// caches once for all of them, and the link discloses nothing about who
// requested it.
//
// The consequence, stated plainly so nobody is surprised by it: signed links
// are bearer tokens for one URL and are shareable. Someone who obtains one can
// re-request that URL. That is an accepted trade — the cost is bounded to a
// single already-mirrored image, and the alternative costs the user's privacy.
// Revocation is by key rotation (or by expiry, if configured).
//
// KEY HANDLING. The key must never reach a browser. A signature key shipped in
// client JavaScript is a public key in the worst sense — anyone can read it out
// of the bundle and mint links. Clients that cannot hold a secret must obtain
// signed links from a signing endpoint instead.
package urlsign

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"time"
)

var (
	// ErrBadSignature means the signature did not verify against the key.
	ErrBadSignature = errors.New("signature is not valid")
	// ErrExpired means the signature was well-formed but past its expiry.
	ErrExpired = errors.New("signature has expired")
	// ErrMalformed means the signed parameters could not be parsed at all.
	ErrMalformed = errors.New("signed parameters are malformed")
	// ErrNoKey means the signer was constructed without a key.
	ErrNoKey = errors.New("no signing key configured")
)

// MinKeyLength is the shortest accepted key. HMAC-SHA256 has no hard minimum,
// but a short shared secret is guessable offline, and the whole point of the
// signature is that it cannot be forged.
const MinKeyLength = 32

// Signer mints and verifies signatures with a single key.
type Signer struct {
	key []byte
	// ttl is how long a fresh signature stays valid. Zero means it never
	// expires, which is the sensible default for immutable content: an emoji
	// at a URL does not change, so a link that stops working after a week
	// breaks already-rendered pages for no security gain. Rotate the key to
	// revoke.
	ttl time.Duration
}

// NewSigner returns a Signer. It rejects a short or absent key rather than
// running unsigned — an unsigned mirror is an open proxy, so failing loudly at
// startup is the only safe behaviour.
func NewSigner(key string, ttl time.Duration) (*Signer, error) {
	if key == "" {
		return nil, ErrNoKey
	}
	if len(key) < MinKeyLength {
		return nil, fmt.Errorf("signing key must be at least %d bytes, got %d", MinKeyLength, len(key))
	}
	if ttl < 0 {
		return nil, errors.New("signature ttl must not be negative")
	}
	return &Signer{key: []byte(key), ttl: ttl}, nil
}

// TTL reports the configured signature lifetime. Zero means signatures do not
// expire.
func (s *Signer) TTL() time.Duration { return s.ttl }

// mac computes the raw HMAC over the canonical URL and expiry.
//
// The two fields are joined with a newline, and the URL cannot contain one
// (it was parsed as a URL), so the encoding is unambiguous: no pair of
// distinct (url, expiry) inputs produces the same signed bytes. Concatenating
// without a separator would not have that property.
func (s *Signer) mac(canonicalURL string, expiresAt int64) []byte {
	m := hmac.New(sha256.New, s.key)
	m.Write([]byte(canonicalURL))
	m.Write([]byte("\n"))
	m.Write([]byte(strconv.FormatInt(expiresAt, 10)))
	return m.Sum(nil)
}

// Sign returns the expiry and signature for a canonical URL. The caller is
// responsible for having canonicalized the URL (see safefetch.CanonicalURL);
// signing a raw URL and verifying a canonical one would never match.
//
// expiresAt is 0 when no TTL is configured, meaning "does not expire".
func (s *Signer) Sign(canonicalURL string) (expiresAt int64, signature string) {
	if s.ttl > 0 {
		expiresAt = time.Now().Add(s.ttl).Unix()
	}
	return expiresAt, base64.RawURLEncoding.EncodeToString(s.mac(canonicalURL, expiresAt))
}

// Verify checks a signature against a canonical URL and expiry.
//
// Expiry is checked only AFTER the signature verifies. Checking it first would
// leak, through response timing and through which error comes back, whether an
// attacker-supplied expiry was attached to an otherwise valid signature.
func (s *Signer) Verify(canonicalURL string, expiresAt int64, signature string) error {
	want := s.mac(canonicalURL, expiresAt)
	got, err := base64.RawURLEncoding.DecodeString(signature)
	if err != nil {
		return fmt.Errorf("%w: signature is not base64url", ErrMalformed)
	}
	// Constant-time: a byte-by-byte comparison that returns early leaks how
	// many leading bytes were right, which is enough to forge a signature one
	// byte at a time.
	if !hmac.Equal(want, got) {
		return ErrBadSignature
	}
	if expiresAt != 0 && time.Now().After(time.Unix(expiresAt, 0)) {
		return ErrExpired
	}
	return nil
}

// EncodeURL encodes a URL for transport in a query parameter.
//
// Base64url rather than percent-encoding, because a URL nested inside a query
// string is a notorious source of double-decoding bugs: intermediaries,
// proxies, and frameworks disagree about how many times to unescape, and any
// disagreement changes the bytes we verify the signature over. Base64url has
// exactly one decoding and no characters that need escaping.
func EncodeURL(canonicalURL string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(canonicalURL))
}

// DecodeURL reverses EncodeURL.
func DecodeURL(encoded string) (string, error) {
	b, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return "", fmt.Errorf("%w: url parameter is not base64url", ErrMalformed)
	}
	return string(b), nil
}
