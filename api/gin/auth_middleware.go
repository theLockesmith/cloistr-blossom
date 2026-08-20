package gin

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/gin-gonic/gin"
	goNostr "github.com/nbd-wtf/go-nostr"
)

// clockSkewTolerance absorbs the difference between the SIGNER's clock and ours.
//
// created_at and expiration are both stamped from Date.now() in the browser, so
// they measure the USER'S DEVICE, not this server. Phones drift. The check on
// created_at used to be a strict `created_at > now`, meaning a device running
// even ONE SECOND fast had every upload rejected with a bare 401 — while the
// same account on a correctly-set desktop worked perfectly. That is exactly how
// it presented: uploads failed only from mobile.
//
// Traced from a phone one second ahead of this server: the client signed at
// 08:30:18 by its own clock and the request arrived here at 08:30:17.8, so
// created_at was "in the future" and the upload was refused.
//
// Note the asymmetry that hid this: expiration already carried 300s of slack in
// one direction, so only the zero-tolerance side ever bit.
//
// 60s matches NIP-98's recommended window. It is a bound on clock error, not on
// how long an event stays usable — the expiration tag governs that, and this
// tolerance does not extend an expired event beyond a minute of skew.
const clockSkewTolerance = 60 * time.Second

// reject logs WHY a request was refused, then aborts with 401.
//
// Every rejection here used to log at Debug and return a bare status with no
// body. In production, running at INFO, that produced a 401 with no recorded
// reason anywhere — eleven distinct failure paths collapsed into one
// indistinguishable symptom, and diagnosing the skew bug above required
// reasoning backwards from timestamps in two different services' logs.
//
// The reason is logged SERVER-SIDE only and deliberately not returned to the
// caller: telling an unauthenticated client precisely which check it failed
// helps an attacker iterate.
func reject(c *gin.Context, log *zap.Logger, reason string) {
	log.Warn("[nostrAuthMiddleware] rejected", zap.String("reason", reason))
	c.AbortWithStatus(http.StatusUnauthorized)
}

func nostrAuthMiddleware(action string, log *zap.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		authHeader := c.GetHeader("Authorization")
		if authHeader == "" {
			reject(c, log, "missing Authorization header")
			return
		}

		if !strings.HasPrefix(authHeader, "Nostr ") {
			reject(c, log, "missing Nostr header prefix")
			return
		}

		eventBase64 := strings.TrimPrefix(authHeader, "Nostr ")

		eventBytes, err := base64.StdEncoding.DecodeString(eventBase64)
		if err != nil {
			reject(c, log, "base64 decode failed: "+err.Error())
			return
		}

		ev := &goNostr.Event{}
		if err := json.Unmarshal(eventBytes, ev); err != nil {
			reject(c, log, "event json decode failed: "+err.Error())
			return
		}

		if ok, err := ev.CheckSignature(); !ok || err != nil {
			reject(c, log, "invalid event signature")
			return
		}

		// ****************************** Blossom Auth logic from this point *******************************************

		// kind must be 24242
		if ev.Kind != 24242 {
			reject(c, log, "wrong event kind (want 24242)")
			return
		}

		// created_at must be in the past, allowing for the signer's clock being
		// ahead of ours. Without the tolerance a device one second fast has
		// every request refused — see clockSkewTolerance.
		if ev.CreatedAt.Time().After(time.Now().Add(clockSkewTolerance)) {
			reject(c, log, "created_at too far in the future")
			return
		}

		expirationTagValue := ""
		tTagValue := ""
		xTagValue := ""

		for i := range ev.Tags {
			if ev.Tags[i][0] == "expiration" && len(ev.Tags[i]) == 2 {
				expirationTagValue = ev.Tags[i][1]
			} else if ev.Tags[i][0] == "t" && len(ev.Tags[i]) == 2 {
				tTagValue = ev.Tags[i][1]
			} else if ev.Tags[i][0] == "x" && len(ev.Tags[i]) == 2 {
				xTagValue = ev.Tags[i][1]
			}
		}
		if expirationTagValue == "" || tTagValue == "" {
			reject(c, log, "missing expiration or t tag")
			return
		}

		// the expiration tag must be set to a Unix timestamp in the future.
		//
		// The Atoi error was previously discarded, so a non-numeric expiration
		// fell through as n=0 and was rejected as "1970" — the right outcome by
		// accident, and indistinguishable in the logs from a genuinely expired
		// event. It is now its own reason.
		n, convErr := strconv.Atoi(expirationTagValue)
		if convErr != nil {
			reject(c, log, "expiration tag is not a unix timestamp")
			return
		}
		// Same tolerance as created_at: a signer whose clock is BEHIND ours
		// would otherwise have events that look expired on arrival.
		if time.Unix(int64(n), 0).Before(time.Now().Add(-clockSkewTolerance)) {
			reject(c, log, "expiration is in the past")
			return
		}

		// the t tag must have a verb matching the intended action of the endpoint
		if tTagValue != action {
			reject(c, log, "t tag does not match endpoint action")
			return
		}

		// additional checks depending on action
		if action == "upload" {
			if xTagValue == "" {
				reject(c, log, "upload requires x tag")
				return
			}
		} else if action == "delete" {
			if xTagValue == "" {
				reject(c, log, "delete requires x tag")
				return
			}
		}

		c.Set("pk", ev.PubKey)
		c.Set("x", xTagValue)

		c.Next()
	}
}
