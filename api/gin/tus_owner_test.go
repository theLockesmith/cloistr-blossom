package gin

import (
	"context"
	"net/http"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// PATCH and HEAD did not check the caller: anyone holding an upload id could
// append to or probe another key's upload in progress. They now require the
// creator, as DELETE does.
func TestTusOwner_OtherKeyCannotPatchOrHead(t *testing.T) {
	shared := newTusShared(t)
	r := shared.replica(t)
	data := []byte("hello, world")

	id := tusCreateOn(t, r, len(data))
	require.Equal(t, http.StatusNoContent, tusPatchOn(r, id, 0, data[:5]).Code)

	for _, caller := range []string{tusOtherPK, ""} {
		w := tusPatchAs(r, caller, id, 5, []byte(", EVIL!"))
		assert.Equal(t, http.StatusForbidden, w.Code, "PATCH by %q, body %s", caller, w.Body)
		w = tusHeadAs(r, caller, id)
		assert.Equal(t, http.StatusForbidden, w.Code, "HEAD by %q", caller)
		assert.Empty(t, w.Header().Get("Upload-Offset"), "HEAD by %q leaks the offset", caller)
	}

	got, err := shared.store.ReadAll(context.Background(), id)
	require.NoError(t, err)
	assert.Equal(t, data[:5], got, "the upload is unchanged")

	w := tusHeadOn(r, id)
	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "5", w.Header().Get("Upload-Offset"))

	w = tusPatchOn(r, id, 5, data[5:])
	require.Equal(t, http.StatusNoContent, w.Code, "owner finishes, body %s", w.Body)
	assert.Equal(t, strconv.Itoa(len(data)), w.Header().Get("Upload-Offset"))
	has, err := shared.blobs.HasReference(context.Background(), tusOwnerPK, sha256Hex(data))
	require.NoError(t, err)
	assert.True(t, has, "stored and owned by the creator")
}
