package safefetch

import (
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestGuardBlocksNonPublicRanges(t *testing.T) {
	g, err := NewGuard(Policy{})
	if err != nil {
		t.Fatalf("NewGuard: %v", err)
	}

	// Each of these has been used in a real SSRF report against some proxy or
	// other; they are here so a future "simplification" of the denylist fails
	// loudly rather than quietly reopening one.
	blocked := map[string]string{
		"169.254.169.254":       "AWS/GCP/Azure instance metadata — the classic SSRF target",
		"169.254.170.2":         "ECS task metadata",
		"127.0.0.1":             "loopback",
		"127.9.9.9":             "all of 127/8 is loopback, not just .0.1",
		"0.0.0.0":               "this-network, routes to localhost on Linux",
		"10.1.2.3":              "RFC1918",
		"172.16.5.5":            "RFC1918",
		"172.31.255.255":        "RFC1918 upper bound",
		"192.168.1.1":           "RFC1918",
		"100.64.1.1":            "CGNAT — routable inside many hosting providers",
		"255.255.255.255":       "broadcast",
		"224.0.0.1":             "multicast",
		"::1":                   "IPv6 loopback",
		"fe80::1":               "IPv6 link-local",
		"fd00::1":               "IPv6 unique-local",
		"::ffff:127.0.0.1":      "IPv4-mapped loopback — must normalize to IPv4 before checking",
		"::ffff:169.254.169.254": "IPv4-mapped metadata address",
		"64:ff9b::7f00:1":       "NAT64-embedded 127.0.0.1",
	}
	for addr, why := range blocked {
		ip := net.ParseIP(addr)
		if ip == nil {
			t.Fatalf("test bug: %q is not an IP", addr)
		}
		if g.Allowed(ip) {
			t.Errorf("Allowed(%s) = true, want false (%s)", addr, why)
		}
	}

	allowed := []string{"1.1.1.1", "8.8.8.8", "93.184.216.34", "2606:4700:4700::1111"}
	for _, addr := range allowed {
		if !g.Allowed(net.ParseIP(addr)) {
			t.Errorf("Allowed(%s) = false, want true", addr)
		}
	}
}

func TestGuardAllowPrivateAddressesOptsOut(t *testing.T) {
	g, err := NewGuard(Policy{AllowPrivateAddresses: true})
	if err != nil {
		t.Fatalf("NewGuard: %v", err)
	}
	if !g.Allowed(net.ParseIP("127.0.0.1")) {
		t.Error("AllowPrivateAddresses did not permit loopback")
	}
}

func TestGuardExtraDeniedCIDRs(t *testing.T) {
	g, err := NewGuard(Policy{ExtraDeniedCIDRs: []string{"93.184.216.0/24"}})
	if err != nil {
		t.Fatalf("NewGuard: %v", err)
	}
	if g.Allowed(net.ParseIP("93.184.216.34")) {
		t.Error("extra denied CIDR was not applied")
	}
	if !g.Allowed(net.ParseIP("8.8.8.8")) {
		t.Error("extra denied CIDR leaked onto an unrelated address")
	}
}

// A malformed deny rule must fail construction. Silently dropping it would
// leave an operator believing they had a fence where they have a gap.
func TestGuardRejectsMalformedCIDR(t *testing.T) {
	if _, err := NewGuard(Policy{ExtraDeniedCIDRs: []string{"not-a-cidr"}}); err == nil {
		t.Fatal("NewGuard accepted a malformed CIDR")
	}
}

// The dialer, not a pre-flight lookup, is the boundary. This drives a real
// client at a real loopback listener and requires the connection to be refused.
func TestClientRefusesLoopbackConnection(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("handler ran: the connection to loopback should never have been made")
	}))
	defer srv.Close()

	c, err := NewClient(Policy{Timeout: 3 * time.Second})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	_, err = c.Get(srv.URL)
	if err == nil {
		t.Fatal("fetching a loopback URL succeeded")
	}
	if !IsBlocked(err) {
		t.Fatalf("error was not classified as blocked: %v", err)
	}
}

// A public URL that redirects to a blocked one is the reason validation lives
// in the dialer rather than in a pre-flight check of the caller's URL.
//
// Constructing this needs care. If both hops are loopback, the FIRST hop is
// refused and the test passes without ever exercising redirect handling --
// green for the wrong reason. So private addresses are allowed generally (the
// first hop connects) and only the victim's own address is denied, which is
// possible because Linux routes all of 127/8 to loopback and the victim can be
// bound to 127.0.0.2 while the redirector stays on 127.0.0.1.
func TestClientRefusesRedirectToBlockedAddress(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.2:0")
	if err != nil {
		t.Skipf("cannot bind 127.0.0.2 on this platform: %v", err)
	}

	var victimReached bool
	victim := &httptest.Server{
		Listener: ln,
		Config: &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			victimReached = true
			w.WriteHeader(http.StatusOK)
		})},
	}
	victim.Start()
	defer victim.Close()

	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, victim.URL, http.StatusFound)
	}))
	defer redirector.Close()

	c, err := NewClient(Policy{
		AllowPrivateAddresses: false,
		ExtraDeniedCIDRs:      []string{"127.0.0.2/32"},
		MaxRedirects:          3,
		Timeout:               3 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	// Sanity check that the guard used here is the narrow one, not a blanket
	// refusal: if the first hop were also denied this test would be vacuous.
	g, err := NewGuard(Policy{ExtraDeniedCIDRs: []string{"127.0.0.2/32"}})
	if err != nil {
		t.Fatalf("NewGuard: %v", err)
	}
	if g.Allowed(net.ParseIP("127.0.0.2")) {
		t.Fatal("test bug: 127.0.0.2 was not denied")
	}

	if _, err := c.Get(redirector.URL); err == nil {
		t.Error("redirect to a blocked address succeeded")
	}
	if victimReached {
		t.Error("redirect target was fetched; the dialer did not re-check the second hop")
	}
}

func TestClientBoundsRedirects(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, srv.URL+"/next", http.StatusFound)
	}))
	defer srv.Close()

	// Private addresses allowed so the loop is exercised rather than refused
	// on the first dial.
	c, err := NewClient(Policy{AllowPrivateAddresses: true, MaxRedirects: 2, Timeout: 3 * time.Second})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if _, err := c.Get(srv.URL); err == nil {
		t.Fatal("infinite redirect loop was followed")
	} else if !IsTooManyRedirects(err) {
		t.Fatalf("error was not a redirect-budget overrun: %v", err)
	}
}

// The transport must not honour HTTP_PROXY: a proxy would terminate every
// connection at the proxy's address, so the dialer would only ever vet the
// proxy and the real destination would go unchecked.
func TestClientIgnoresEnvironmentProxy(t *testing.T) {
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:9")
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:9")

	c, err := NewClient(Policy{})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	tr, ok := c.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("transport is %T, want *http.Transport", c.Transport)
	}
	if tr.Proxy != nil {
		t.Error("transport has a proxy configured; destination checks would be bypassed")
	}
}

func TestCanonicalURL(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{"lowercases scheme and host", "HTTPS://Example.COM/Path.PNG", "https://example.com/Path.PNG"},
		{"drops default https port", "https://example.com:443/a.png", "https://example.com/a.png"},
		{"drops default http port", "http://example.com:80/a.png", "http://example.com/a.png"},
		{"keeps non-default port", "https://example.com:8443/a.png", "https://example.com:8443/a.png"},
		{"drops fragment", "https://example.com/a.png#frag", "https://example.com/a.png"},
		{"adds root path", "https://example.com", "https://example.com/"},
		{"preserves query", "https://example.com/a?v=2", "https://example.com/a?v=2"},
		// Path case and percent-encoding are load-bearing on many origins, so
		// they must survive untouched: normalizing them could make a signature
		// for one URL fetch a different resource.
		{"preserves path case", "https://example.com/CaSe", "https://example.com/CaSe"},
		{"preserves percent-encoding", "https://example.com/a%2Fb.png", "https://example.com/a%2Fb.png"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := CanonicalURL(tc.in)
			if err != nil {
				t.Fatalf("CanonicalURL(%q): %v", tc.in, err)
			}
			if got != tc.want {
				t.Errorf("CanonicalURL(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// Canonicalization must be idempotent, or a URL signed after one pass would
// fail verification after two.
func TestCanonicalURLIsIdempotent(t *testing.T) {
	for _, in := range []string{
		"HTTPS://Example.COM:443/a.png#x",
		"http://host/a?b=c",
		"https://h.example/%E2%98%83.png",
	} {
		once, err := CanonicalURL(in)
		if err != nil {
			t.Fatalf("CanonicalURL(%q): %v", in, err)
		}
		twice, err := CanonicalURL(once)
		if err != nil {
			t.Fatalf("CanonicalURL(%q): %v", once, err)
		}
		if once != twice {
			t.Errorf("not idempotent: %q -> %q -> %q", in, once, twice)
		}
	}
}

func TestCanonicalURLRejects(t *testing.T) {
	cases := []struct{ name, in string }{
		{"empty", ""},
		{"relative", "/just/a/path.png"},
		{"file scheme", "file:///etc/passwd"},
		{"gopher scheme", "gopher://example.com/1"},
		{"data scheme", "data:image/png;base64,AAAA"},
		{"javascript scheme", "javascript:alert(1)"},
		{"no host", "https:///path"},
		// Credentials would be signed, stored in our database, and replayed by
		// us on every re-fetch.
		{"userinfo", "https://user:pass@example.com/a.png"},
		{"over length", "https://example.com/" + strings.Repeat("a", MaxURLLength)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := CanonicalURL(tc.in); err == nil {
				t.Errorf("CanonicalURL(%q) succeeded, want rejection", tc.in)
			} else if !errors.Is(err, ErrInvalidURL) {
				t.Errorf("CanonicalURL(%q) error = %v, want ErrInvalidURL", tc.in, err)
			}
		})
	}
}
