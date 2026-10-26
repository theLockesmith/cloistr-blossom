package mimetypes

import "testing"

func TestResolve(t *testing.T) {
	plainText := []byte("body { color: red }\n")
	binary := []byte{0x00, 0x01, 0x02, 0x03, 0xfe, 0xff}
	png := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x06\x00\x00\x00")

	tests := []struct {
		name     string
		data     []byte
		declared string
		want     string
	}{
		// Allowlisted upgrades over a generic sniff.
		{"css over text", plainText, "text/css", "text/css; charset=utf-8"},
		{"javascript over text", []byte("alert(1)\n"), "text/javascript", "text/javascript; charset=utf-8"},
		{"json over text", []byte("not quite json\n"), "application/json", "application/json; charset=utf-8"},
		{"markdown over text", []byte("# title\n"), "text/markdown", "text/markdown; charset=utf-8"},
		{"csv over text", []byte("a\n"), "text/csv", "text/csv; charset=utf-8"},
		{"css over octet-stream", binary, "text/css", "text/css"},
		{"declared params and case ignored", plainText, "Text/CSS; charset=latin1", "text/css; charset=utf-8"},

		// Never upgraded.
		{"html over text stays text", plainText, "text/html", "text/plain; charset=utf-8"},
		{"png over text stays text", plainText, "image/png", "text/plain; charset=utf-8"},
		{"svg over text stays text", plainText, "image/svg+xml", "text/plain; charset=utf-8"},
		{"xml over text stays text", plainText, "application/xml", "text/plain; charset=utf-8"},
		{"xhtml over text stays text", plainText, "application/xhtml+xml", "text/plain; charset=utf-8"},
		{"unlisted javascript alias stays text", plainText, "application/javascript", "text/plain; charset=utf-8"},
		{"malformed declared stays text", plainText, "not a type", "text/plain; charset=utf-8"},
		{"empty declared stays text", plainText, "", "text/plain; charset=utf-8"},
		{"html over octet-stream stays octet-stream", binary, "text/html", "application/octet-stream"},

		// A declared type on bytes the sniffer recognises is ignored.
		{"recognised png ignores declared css", png, "text/css", "image/png"},
		{"recognised html ignores declared css", []byte("<!DOCTYPE html><html></html>"), "text/css", "text/html; charset=utf-8"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Resolve(tt.data, tt.declared); got != tt.want {
				t.Errorf("Resolve(%q) = %q, want %q", tt.declared, got, tt.want)
			}
		})
	}
}
