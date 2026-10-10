package gin

import (
	"fmt"
	"net"

	"github.com/gin-gonic/gin"

	"git.aegis-hq.xyz/coldforge/cloistr-blossom/src/pkg/config"
)

// newEngine returns a gin engine whose ClientIP() cannot be forged.
//
// Gin's defaults trust every peer as a proxy and read X-Forwarded-For first.
// Our public edge overwrites X-Real-IP with the real client address but only
// APPENDS to X-Forwarded-For, so the leftmost XFF entry is whatever the client
// sent -- and a rotating forged XFF bought a fresh rate-limit budget per
// request. So: read only the edge-set header, and only when the TCP peer is
// one of our own proxies (the in-cluster router); from anyone else, the peer
// address is the client.
func newEngine(conf *config.Config) (*gin.Engine, error) {
	r := gin.New()
	if err := r.SetTrustedProxies(conf.TrustedProxies); err != nil {
		return nil, fmt.Errorf("trusted_proxies: %w", err)
	}
	r.RemoteIPHeaders = []string{conf.ClientIPHeader}
	return r, nil
}

// isInternalCaller reports whether the request came straight from inside the
// cluster: a private peer and no client IP header. The public edge always sets
// that header, so public traffic can never look internal.
func isInternalCaller(c *gin.Context, header string) bool {
	if header == "" || c.GetHeader(header) != "" {
		return false
	}
	ip := net.ParseIP(c.RemoteIP())
	return ip != nil && (ip.IsPrivate() || ip.IsLoopback())
}
