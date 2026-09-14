package client

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestList_ReturnsDecodeError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`not valid json`))
	}))
	defer srv.Close()

	c := &Client{serverUrl: srv.URL, client: srv.Client()}
	_, err := c.List("deadbeef")
	require.Error(t, err, "List should surface the JSON decode error")
}

func TestList_ReturnsBlobs(t *testing.T) {
	blobs := []BlobDescriptor{
		{Url: "https://example.com/abc123", Sha256: "abc123", Size: 1024, Type: "image/png"},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(blobs)
	}))
	defer srv.Close()

	c := &Client{serverUrl: srv.URL, client: srv.Client()}
	result, err := c.List("deadbeef")
	require.NoError(t, err)
	require.Len(t, result, 1)
	assert.Equal(t, "abc123", result[0].Sha256)
}

func TestGet_ReturnsBody(t *testing.T) {
	body := []byte("hello blob")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	c := &Client{serverUrl: srv.URL, client: srv.Client()}
	result, err := c.Get("somehash")
	require.NoError(t, err)
	assert.Equal(t, body, result)
}

func TestHas_ReturnsTrueOn200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := &Client{serverUrl: srv.URL, client: srv.Client()}
	ok, err := c.Has("somehash")
	require.NoError(t, err)
	assert.True(t, ok)
}

func TestHas_ReturnsFalseOn404(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	c := &Client{serverUrl: srv.URL, client: srv.Client()}
	ok, err := c.Has("somehash")
	require.NoError(t, err)
	assert.False(t, ok)
}
