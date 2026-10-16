// Package mimetypes decides the MIME type stored for an uploaded blob.
package mimetypes

import (
	"mime"

	"github.com/gabriel-vasile/mimetype"
)

// upgradable lists the declared types allowed to replace a generic sniff
// result. Text formats like CSS and JS cannot be told apart from plain text by
// their bytes, so the uploader's declaration is the only signal. HTML, SVG and
// any XML type are deliberately absent: they keep the sniffed type.
var upgradable = map[string]bool{
	"text/css":         true,
	"text/javascript":  true,
	"application/json": true,
	"text/markdown":    true,
	"text/csv":         true,
}

// Resolve returns the MIME type to store for data. The type is sniffed from
// the bytes; the uploader's declared type is used only when the sniff is
// generic (text/plain or application/octet-stream) and the declared type is
// on the upgradable allowlist. The sniffed parameters (charset) are kept.
func Resolve(data []byte, declared string) string {
	sniffed := mimetype.Detect(data).String()
	sniffedBase, sniffedParams, err := mime.ParseMediaType(sniffed)
	if err != nil || (sniffedBase != "text/plain" && sniffedBase != "application/octet-stream") {
		return sniffed
	}

	declaredBase, _, err := mime.ParseMediaType(declared)
	if err != nil || !upgradable[declaredBase] {
		return sniffed
	}

	if resolved := mime.FormatMediaType(declaredBase, sniffedParams); resolved != "" {
		return resolved
	}
	return sniffed
}
