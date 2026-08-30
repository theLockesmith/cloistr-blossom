// Package safefetch provides an HTTP client for fetching attacker-influenced
// URLs without turning the server into an SSRF gadget.
//
// THE THREAT. Any endpoint that fetches a URL supplied by someone else can be
// pointed at the server's own network: 169.254.169.254 for cloud instance
// credentials, 10.x/172.16.x/192.168.x for internal services, 127.0.0.1 for
// admin ports bound to loopback. A naive http.Get is a request-forgery
// primitive on behalf of anyone who can reach the endpoint.
//
// WHY THE CHECK LIVES IN THE DIALER. The obvious implementation — parse the
// URL, resolve the host, reject private IPs, then fetch — is broken in two
// ways that matter:
//
//	DNS REBINDING. Between the check and the fetch, the name is resolved a
//	SECOND time by the transport. A DNS server the attacker controls can
//	answer 1.2.3.4 (public, passes) for the check and 127.0.0.1 for the
//	fetch. The check and the connection must observe the SAME resolution.
//
//	REDIRECTS. A public URL can 302 to http://169.254.169.254/. Validating
//	only the URL the caller handed us checks the one hop that was never the
//	problem.
//
// Both close if validation happens in net.Dialer.Control, which runs after
// resolution with the ACTUAL address about to be connected to, once per
// connection — so every redirect hop and every rebound answer is checked, with
// no window in between. Control returning an error aborts the connection.
//
// The redirect policy below is therefore not the security boundary; it only
// bounds hop count and keeps the scheme to http/https. The dialer is the
// boundary.
package safefetch

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"syscall"
	"time"
)

// ErrBlockedAddress is returned when a connection was refused because the
// destination address is in a denied range. It is deliberately distinct from a
// transport error: "we refused to go there" and "we went there and it failed"
// are different facts, and callers surface them to users differently.
var ErrBlockedAddress = errors.New("destination address is not permitted")

// ErrTooManyRedirects is returned when a fetch exceeded the redirect budget.
var ErrTooManyRedirects = errors.New("too many redirects")

// blockedNetworks lists the CIDR ranges a fetch may never connect to.
//
// This is a DENYLIST of things that are, by definition, not the public
// internet. It is enumerated rather than derived because Go's IsPrivate() and
// friends do not cover all of it: notably 100.64.0.0/10 (CGNAT, routable
// inside many hosting providers) and 64:ff9b::/96 (NAT64, which smuggles an
// IPv4 destination inside an IPv6 address and would otherwise sail past every
// IPv4 range check here).
var blockedCIDRs = []string{
	// IPv4
	"0.0.0.0/8",          // "this network"
	"10.0.0.0/8",         // RFC1918 private
	"100.64.0.0/10",      // RFC6598 CGNAT — routable inside many providers
	"127.0.0.0/8",        // loopback
	"169.254.0.0/16",     // link-local — cloud metadata lives at 169.254.169.254
	"172.16.0.0/12",      // RFC1918 private
	"192.0.0.0/24",       // IETF protocol assignments
	"192.0.2.0/24",       // TEST-NET-1
	"192.88.99.0/24",     // 6to4 relay anycast (deprecated)
	"192.168.0.0/16",     // RFC1918 private
	"198.18.0.0/15",      // benchmarking
	"198.51.100.0/24",    // TEST-NET-2
	"203.0.113.0/24",     // TEST-NET-3
	"224.0.0.0/4",        // multicast
	"240.0.0.0/4",        // reserved, includes 255.255.255.255 broadcast
	// IPv6
	"::/128",             // unspecified
	"::1/128",            // loopback
	"64:ff9b::/96",       // NAT64 — embeds an IPv4 destination
	"64:ff9b:1::/48",     // local-use NAT64
	"100::/64",           // discard-only
	"2001:db8::/32",      // documentation
	"fc00::/7",           // unique-local
	"fe80::/10",          // link-local
	"ff00::/8",           // multicast
}

// Policy configures which destinations a Client may reach.
type Policy struct {
	// AllowPrivateAddresses disables the denylist entirely. It exists for
	// self-hosters running a mirror against a LAN-local source and for tests
	// that dial 127.0.0.1. It is a foot-gun on a public deployment, which is
	// why it is opt-in and named for what it does.
	AllowPrivateAddresses bool

	// ExtraDeniedCIDRs are additional ranges to refuse, on top of the
	// built-ins. Use for provider-specific metadata endpoints that live on
	// public-looking addresses.
	ExtraDeniedCIDRs []string

	// MaxRedirects bounds the redirect chain. Zero means no redirects are
	// followed at all.
	MaxRedirects int

	// Timeout bounds the whole request, including body read. Zero means
	// DefaultTimeout.
	Timeout time.Duration
}

// DefaultTimeout is used when Policy.Timeout is zero.
const DefaultTimeout = 10 * time.Second

// Guard decides whether a resolved address may be connected to.
type Guard struct {
	allowPrivate bool
	denied       []*net.IPNet
}

// NewGuard compiles a Policy's ranges into a Guard. It fails if a configured
// CIDR does not parse, rather than silently ignoring it: a typo'd deny rule
// that is quietly dropped is a hole that looks like a fence.
func NewGuard(p Policy) (*Guard, error) {
	g := &Guard{allowPrivate: p.AllowPrivateAddresses}
	if p.AllowPrivateAddresses {
		return g, nil
	}

	for _, cidr := range blockedCIDRs {
		_, n, err := net.ParseCIDR(cidr)
		if err != nil {
			// A malformed built-in is a programming error, not user input.
			return nil, fmt.Errorf("safefetch: built-in CIDR %q: %w", cidr, err)
		}
		g.denied = append(g.denied, n)
	}
	for _, cidr := range p.ExtraDeniedCIDRs {
		_, n, err := net.ParseCIDR(cidr)
		if err != nil {
			return nil, fmt.Errorf("safefetch: extra_denied_cidrs entry %q: %w", cidr, err)
		}
		g.denied = append(g.denied, n)
	}
	return g, nil
}

// Allowed reports whether ip may be connected to.
func (g *Guard) Allowed(ip net.IP) bool {
	if g.allowPrivate {
		return true
	}
	if ip == nil {
		return false
	}

	// Normalize an IPv4-mapped IPv6 address (::ffff:127.0.0.1) down to its
	// IPv4 form before range checks. Without this, ::ffff:127.0.0.1 misses
	// every IPv4 CIDR above and is treated as an ordinary IPv6 address.
	if v4 := ip.To4(); v4 != nil {
		ip = v4
	}

	for _, n := range g.denied {
		if n.Contains(ip) {
			return false
		}
	}
	return true
}

// control is the net.Dialer.Control hook. It runs after name resolution with
// the concrete address the kernel is about to connect to, which is the only
// point where the check and the connection cannot disagree.
func (g *Guard) control(_ string, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		// Control is documented to receive host:port. If it does not, refuse:
		// an address we cannot parse is an address we cannot vet.
		return fmt.Errorf("%w: unparseable address %q", ErrBlockedAddress, address)
	}
	ip := net.ParseIP(host)
	if ip == nil {
		// Control receives a literal address, never a name. A non-literal here
		// means resolution did not happen where we assumed it did.
		return fmt.Errorf("%w: non-literal address %q", ErrBlockedAddress, host)
	}
	if !g.Allowed(ip) {
		return fmt.Errorf("%w: %s", ErrBlockedAddress, ip)
	}
	return nil
}

// NewClient builds an http.Client that refuses to connect to denied ranges.
//
// The returned client does not share Go's DefaultTransport: a shared transport
// would pool connections established under a different policy, and a pooled
// connection is never re-dialed, so it would never be re-checked.
func NewClient(p Policy) (*http.Client, error) {
	guard, err := NewGuard(p)
	if err != nil {
		return nil, err
	}

	timeout := p.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}

	dialer := &net.Dialer{
		Timeout:   timeout,
		KeepAlive: 30 * time.Second,
		Control:   guard.control,
	}

	transport := &http.Transport{
		DialContext:           dialer.DialContext,
		TLSHandshakeTimeout:   timeout,
		ResponseHeaderTimeout: timeout,
		ExpectContinueTimeout: 1 * time.Second,
		MaxIdleConns:          32,
		IdleConnTimeout:       30 * time.Second,
		// Proxying is disabled deliberately. An HTTP proxy read from the
		// environment would terminate every connection at the proxy, so the
		// dialer would only ever vet the PROXY's address and the real
		// destination would go unchecked — silently disabling this package.
		Proxy: nil,
	}

	maxRedirects := p.MaxRedirects
	return &http.Client{
		Transport: transport,
		Timeout:   timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) > maxRedirects {
				return fmt.Errorf("%w: after %d hops", ErrTooManyRedirects, len(via))
			}
			// Address safety is enforced by the dialer on the new connection.
			// Scheme is not: a redirect to file:// or gopher:// never reaches
			// the dialer, so it is refused here.
			if req.URL.Scheme != "http" && req.URL.Scheme != "https" {
				return fmt.Errorf("%w: redirect to scheme %q", ErrBlockedAddress, req.URL.Scheme)
			}
			return nil
		},
	}, nil
}

// IsBlocked reports whether err is (or wraps) a refusal to connect to a denied
// address. http.Client wraps transport errors in *url.Error, so callers cannot
// rely on a bare errors.Is against the returned error without this.
func IsBlocked(err error) bool {
	return errors.Is(err, ErrBlockedAddress)
}

// IsTooManyRedirects reports whether err is (or wraps) a redirect-budget
// overrun.
func IsTooManyRedirects(err error) bool {
	return errors.Is(err, ErrTooManyRedirects)
}

// PreCheckURL rejects URLs that cannot possibly be fetchable before any network
// traffic happens. It is an optimization and an input-validation courtesy, NOT
// a security boundary — a host that resolves to a private address passes here
// and is refused later by the dialer, which is the check that counts.
func PreCheckURL(raw string) error {
	_, err := parseAbsoluteHTTPURL(raw)
	return err
}
