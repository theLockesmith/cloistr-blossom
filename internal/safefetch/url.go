package safefetch

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
)

// ErrInvalidURL is returned for input that is not an absolute http(s) URL.
var ErrInvalidURL = errors.New("invalid URL")

// MaxURLLength bounds the URL accepted for signing and mirroring. Long URLs
// are not a security problem by themselves, but they inflate every signed link
// and every stored row, and no legitimate emoji URL approaches this.
const MaxURLLength = 2048

// parseAbsoluteHTTPURL parses raw and requires it to be an absolute http or
// https URL.
func parseAbsoluteHTTPURL(raw string) (*url.URL, error) {
	if raw == "" {
		return nil, fmt.Errorf("%w: empty", ErrInvalidURL)
	}
	if len(raw) > MaxURLLength {
		return nil, fmt.Errorf("%w: longer than %d bytes", ErrInvalidURL, MaxURLLength)
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("%w: %s", ErrInvalidURL, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("%w: scheme %q is not http or https", ErrInvalidURL, u.Scheme)
	}
	if u.Host == "" {
		return nil, fmt.Errorf("%w: no host", ErrInvalidURL)
	}
	if u.User != nil {
		// Credentials in the URL would be signed, stored, and replayed by us.
		return nil, fmt.Errorf("%w: userinfo is not permitted", ErrInvalidURL)
	}
	return u, nil
}

// CanonicalURL normalizes a URL into the exact byte string that gets signed,
// hashed for cache lookup, and fetched.
//
// Signature verification is a byte comparison, so "the same image" must produce
// the same string every time or the cache misses and the signature fails. It
// also has to be STABLE under the round trip through a query parameter: what
// the signer signs and what the fetcher verifies must agree after base64
// decoding, which is why the canonical form is produced once here and used by
// both sides rather than each re-deriving it.
//
// Normalization is deliberately conservative — scheme and host are lowercased
// and the default port is dropped, both of which are case- and
// representation-insensitive per RFC 3986. The path and query are left ALONE.
// Re-encoding a path can change which byte sequence the origin server sees, and
// two URLs that differ only in percent-encoding may genuinely address different
// resources; treating them as one would let a signature for one URL fetch
// another.
//
// The fragment is dropped: it is never sent to the server, so retaining it
// would split the cache on a distinction the origin cannot observe.
func CanonicalURL(raw string) (string, error) {
	u, err := parseAbsoluteHTTPURL(raw)
	if err != nil {
		return "", err
	}

	u.Scheme = strings.ToLower(u.Scheme)
	u.Fragment = ""
	u.RawFragment = ""

	host := u.Hostname()
	port := u.Port()
	host = strings.ToLower(host)

	// Drop the port when it is the scheme default, so https://h:443/x and
	// https://h/x are one cache entry rather than two.
	if (u.Scheme == "http" && port == "80") || (u.Scheme == "https" && port == "443") {
		port = ""
	}
	if port == "" {
		u.Host = host
	} else {
		u.Host = host + ":" + port
	}

	// An empty path is equivalent to "/" for an origin request.
	if u.Path == "" {
		u.Path = "/"
	}

	return u.String(), nil
}
