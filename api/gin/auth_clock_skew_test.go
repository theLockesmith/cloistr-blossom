package gin

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/nbd-wtf/go-nostr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Uploads failed from one device and worked from another with the same account.
//
// created_at and expiration are stamped by the BROWSER (Date.now()), so they
// measure the user's phone, not this server. The created_at check was a strict
// `created_at > now`, so a device running one second fast had every upload
// refused with a bare 401, while a correctly-set desktop was fine.
//
// Observed: the client signed at 08:30:18 by its own clock; the request reached
// this service at 08:30:17.8. A request cannot arrive before it was sent — the
// phone was ahead, and its "now" looked like our future.

// authEventAt builds a signed upload auth event stamped at an arbitrary time,
// standing in for a signer whose clock disagrees with ours.
func authEventAt(createdAt time.Time, expiresAt time.Time) *nostr.Event {
	sk := nostr.GeneratePrivateKey()
	pk, _ := nostr.GetPublicKey(sk)

	ev := &nostr.Event{
		PubKey:    pk,
		CreatedAt: nostr.Timestamp(createdAt.Unix()),
		Kind:      24242,
		Tags: nostr.Tags{
			nostr.Tag{"expiration", strconv.FormatInt(expiresAt.Unix(), 10)},
			nostr.Tag{"t", "upload"},
			nostr.Tag{"x", "sha256hash"},
		},
		Content: "",
	}
	ev.Sign(sk)
	return ev
}

func serveAuth(t *testing.T, ev *nostr.Event) int {
	t.Helper()
	r, _ := setupTestRouter("upload")
	encoded, err := encodeAuthEvent(ev)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("Authorization", "Nostr "+encoded)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w.Code
}

// The actual bug. One second of phone drift must not break uploads.
func TestAuth_AcceptsSignerClockSlightlyAhead(t *testing.T) {
	now := time.Now()
	code := serveAuth(t, authEventAt(now.Add(1*time.Second), now.Add(5*time.Minute)))
	assert.Equal(t, http.StatusOK, code,
		"a signer 1s ahead of us must still authenticate — this is the mobile upload failure")
}

// The tolerance has to cover realistic consumer-device drift, not just a tick.
func TestAuth_AcceptsSignerClockModeratelyAhead(t *testing.T) {
	now := time.Now()
	code := serveAuth(t, authEventAt(now.Add(30*time.Second), now.Add(5*time.Minute)))
	assert.Equal(t, http.StatusOK, code, "30s of clock drift is well within normal for a phone")
}

// ...but it is a tolerance for clock error, not an open door. An event stamped
// far in the future is still refused.
func TestAuth_RejectsCreatedAtFarInFuture(t *testing.T) {
	now := time.Now()
	code := serveAuth(t, authEventAt(now.Add(10*time.Minute), now.Add(20*time.Minute)))
	assert.Equal(t, http.StatusUnauthorized, code,
		"10 minutes ahead is not clock skew and must still be rejected")
}

// A signer whose clock is BEHIND ours would otherwise present events that look
// already-expired the moment they arrive.
func TestAuth_AcceptsExpirationWithinSkewWindow(t *testing.T) {
	now := time.Now()
	// Expired 5s ago by our clock — consistent with a signer running slow.
	code := serveAuth(t, authEventAt(now.Add(-1*time.Minute), now.Add(-5*time.Second)))
	assert.Equal(t, http.StatusOK, code, "a few seconds past expiry is clock skew, not an expired event")
}

// A genuinely old event is still refused — the skew window must not become a
// blanket extension of every event's lifetime.
func TestAuth_RejectsGenuinelyExpired(t *testing.T) {
	now := time.Now()
	code := serveAuth(t, authEventAt(now.Add(-2*time.Hour), now.Add(-1*time.Hour)))
	assert.Equal(t, http.StatusUnauthorized, code, "an hour past expiry is expired, not skew")
}

// The Atoi error used to be discarded, so garbage became n=0 and was rejected
// as "1970" — correct by accident, and indistinguishable in the logs from a
// real expiry.
func TestAuth_RejectsNonNumericExpiration(t *testing.T) {
	sk := nostr.GeneratePrivateKey()
	pk, _ := nostr.GetPublicKey(sk)
	ev := &nostr.Event{
		PubKey:    pk,
		CreatedAt: nostr.Now(),
		Kind:      24242,
		Tags: nostr.Tags{
			nostr.Tag{"expiration", "not-a-timestamp"},
			nostr.Tag{"t", "upload"},
			nostr.Tag{"x", "sha256hash"},
		},
	}
	ev.Sign(sk)
	assert.Equal(t, http.StatusUnauthorized, serveAuth(t, ev))
}

// Skew tolerance must not weaken any other check.
func TestAuth_SkewToleranceDoesNotBypassOtherChecks(t *testing.T) {
	now := time.Now()

	t.Run("wrong action still rejected", func(t *testing.T) {
		ev := authEventAt(now.Add(1*time.Second), now.Add(5*time.Minute))
		sk := nostr.GeneratePrivateKey()
		ev.Tags = nostr.Tags{
			nostr.Tag{"expiration", strconv.FormatInt(now.Add(5*time.Minute).Unix(), 10)},
			nostr.Tag{"t", "delete"}, // endpoint expects upload
			nostr.Tag{"x", "sha256hash"},
		}
		ev.Sign(sk)
		assert.Equal(t, http.StatusUnauthorized, serveAuth(t, ev))
	})

	t.Run("missing x tag still rejected", func(t *testing.T) {
		sk := nostr.GeneratePrivateKey()
		pk, _ := nostr.GetPublicKey(sk)
		ev := &nostr.Event{
			PubKey:    pk,
			CreatedAt: nostr.Timestamp(now.Add(1 * time.Second).Unix()),
			Kind:      24242,
			Tags: nostr.Tags{
				nostr.Tag{"expiration", strconv.FormatInt(now.Add(5*time.Minute).Unix(), 10)},
				nostr.Tag{"t", "upload"},
			},
		}
		ev.Sign(sk)
		assert.Equal(t, http.StatusUnauthorized, serveAuth(t, ev))
	})

	t.Run("tampered signature still rejected", func(t *testing.T) {
		ev := authEventAt(now.Add(1*time.Second), now.Add(5*time.Minute))
		ev.PubKey = "0000000000000000000000000000000000000000000000000000000000000000"
		assert.Equal(t, http.StatusUnauthorized, serveAuth(t, ev))
	})
}
