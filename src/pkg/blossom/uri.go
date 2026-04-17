// Package blossom provides BUD-10 URI parsing and generation.
package blossom

import (
	"errors"
	"fmt"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"
)

// URI represents a parsed blossom: URI per BUD-10.
type URI struct {
	Hash      string   // 64-char hex SHA256
	Extension string   // File extension (e.g., "jpg")
	Servers   []string // xs params - server hints
	Author    string   // as param - author pubkey
	Size      int64    // sz param - file size in bytes
}

var (
	ErrInvalidScheme = errors.New("invalid scheme: expected 'blossom:'")
	ErrInvalidHash   = errors.New("invalid hash: expected 64 hex characters")
	ErrMissingExt    = errors.New("missing file extension")

	hashRegex = regexp.MustCompile(`^[a-f0-9]{64}$`)
)

// Parse parses a blossom: URI string into a URI struct.
// Format: blossom:hash.ext?as=pubkey&xs=server1&xs=server2&sz=1234
func Parse(uri string) (*URI, error) {
	// Check scheme
	if !strings.HasPrefix(uri, "blossom:") {
		return nil, ErrInvalidScheme
	}

	// Remove scheme
	rest := strings.TrimPrefix(uri, "blossom:")

	// Split path and query
	var pathPart, queryPart string
	if idx := strings.Index(rest, "?"); idx >= 0 {
		pathPart = rest[:idx]
		queryPart = rest[idx+1:]
	} else {
		pathPart = rest
	}

	// Parse hash.ext
	ext := path.Ext(pathPart)
	if ext == "" {
		return nil, ErrMissingExt
	}
	hash := strings.TrimSuffix(pathPart, ext)
	ext = strings.TrimPrefix(ext, ".")

	// Validate hash
	hash = strings.ToLower(hash)
	if !hashRegex.MatchString(hash) {
		return nil, ErrInvalidHash
	}

	result := &URI{
		Hash:      hash,
		Extension: ext,
	}

	// Parse query params
	if queryPart != "" {
		values, err := url.ParseQuery(queryPart)
		if err != nil {
			return nil, fmt.Errorf("invalid query params: %w", err)
		}

		// xs - server hints (can be multiple)
		result.Servers = values["xs"]

		// as - author pubkey
		if as := values.Get("as"); as != "" {
			result.Author = as
		}

		// sz - size
		if sz := values.Get("sz"); sz != "" {
			size, err := strconv.ParseInt(sz, 10, 64)
			if err == nil && size > 0 {
				result.Size = size
			}
		}
	}

	return result, nil
}

// IsBlossom returns true if the string looks like a blossom: URI.
func IsBlossom(s string) bool {
	return strings.HasPrefix(s, "blossom:")
}

// Build creates a blossom: URI string from components.
func Build(hash, ext, server string, size int64) string {
	if ext == "" {
		ext = "bin"
	}
	ext = strings.TrimPrefix(ext, ".")

	uri := fmt.Sprintf("blossom:%s.%s", strings.ToLower(hash), ext)

	var params []string
	if server != "" {
		// Strip protocol for cleaner URI
		server = strings.TrimPrefix(server, "https://")
		server = strings.TrimPrefix(server, "http://")
		params = append(params, "xs="+url.QueryEscape(server))
	}
	if size > 0 {
		params = append(params, fmt.Sprintf("sz=%d", size))
	}

	if len(params) > 0 {
		uri += "?" + strings.Join(params, "&")
	}

	return uri
}

// BuildFull creates a blossom: URI with all available parameters.
func BuildFull(hash, ext string, servers []string, author string, size int64) string {
	if ext == "" {
		ext = "bin"
	}
	ext = strings.TrimPrefix(ext, ".")

	uri := fmt.Sprintf("blossom:%s.%s", strings.ToLower(hash), ext)

	var params []string
	for _, server := range servers {
		server = strings.TrimPrefix(server, "https://")
		server = strings.TrimPrefix(server, "http://")
		params = append(params, "xs="+url.QueryEscape(server))
	}
	if author != "" {
		params = append(params, "as="+url.QueryEscape(author))
	}
	if size > 0 {
		params = append(params, fmt.Sprintf("sz=%d", size))
	}

	if len(params) > 0 {
		uri += "?" + strings.Join(params, "&")
	}

	return uri
}

// ToHTTPURLs converts a blossom URI to potential HTTP URLs to try.
func (u *URI) ToHTTPURLs() []string {
	var urls []string
	for _, server := range u.Servers {
		// Ensure server has protocol
		if !strings.HasPrefix(server, "http://") && !strings.HasPrefix(server, "https://") {
			server = "https://" + server
		}
		urls = append(urls, fmt.Sprintf("%s/%s", strings.TrimSuffix(server, "/"), u.Hash))
	}
	return urls
}
