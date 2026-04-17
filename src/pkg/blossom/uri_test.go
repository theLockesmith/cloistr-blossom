package blossom

import (
	"testing"
)

func TestParse(t *testing.T) {
	tests := []struct {
		name    string
		uri     string
		want    *URI
		wantErr error
	}{
		{
			name: "valid URI with all params",
			uri:  "blossom:b1674191a88ec5cdd733e4240a81803105dc412d6c6708d53ab94fc248f4f553.jpg?xs=blossom.example.com&as=abc123&sz=12345",
			want: &URI{
				Hash:      "b1674191a88ec5cdd733e4240a81803105dc412d6c6708d53ab94fc248f4f553",
				Extension: "jpg",
				Servers:   []string{"blossom.example.com"},
				Author:    "abc123",
				Size:      12345,
			},
		},
		{
			name: "valid URI minimal",
			uri:  "blossom:b1674191a88ec5cdd733e4240a81803105dc412d6c6708d53ab94fc248f4f553.bin",
			want: &URI{
				Hash:      "b1674191a88ec5cdd733e4240a81803105dc412d6c6708d53ab94fc248f4f553",
				Extension: "bin",
			},
		},
		{
			name: "multiple server hints",
			uri:  "blossom:b1674191a88ec5cdd733e4240a81803105dc412d6c6708d53ab94fc248f4f553.mp4?xs=server1.com&xs=server2.com",
			want: &URI{
				Hash:      "b1674191a88ec5cdd733e4240a81803105dc412d6c6708d53ab94fc248f4f553",
				Extension: "mp4",
				Servers:   []string{"server1.com", "server2.com"},
			},
		},
		{
			name: "uppercase hash normalized",
			uri:  "blossom:B1674191A88EC5CDD733E4240A81803105DC412D6C6708D53AB94FC248F4F553.png",
			want: &URI{
				Hash:      "b1674191a88ec5cdd733e4240a81803105dc412d6c6708d53ab94fc248f4f553",
				Extension: "png",
			},
		},
		{
			name:    "invalid scheme",
			uri:     "https://example.com/file.jpg",
			wantErr: ErrInvalidScheme,
		},
		{
			name:    "invalid hash too short",
			uri:     "blossom:abc123.jpg",
			wantErr: ErrInvalidHash,
		},
		{
			name:    "missing extension",
			uri:     "blossom:b1674191a88ec5cdd733e4240a81803105dc412d6c6708d53ab94fc248f4f553",
			wantErr: ErrMissingExt,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Parse(tt.uri)
			if tt.wantErr != nil {
				if err != tt.wantErr {
					t.Errorf("Parse() error = %v, wantErr %v", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Errorf("Parse() unexpected error = %v", err)
				return
			}
			if got.Hash != tt.want.Hash {
				t.Errorf("Parse() Hash = %v, want %v", got.Hash, tt.want.Hash)
			}
			if got.Extension != tt.want.Extension {
				t.Errorf("Parse() Extension = %v, want %v", got.Extension, tt.want.Extension)
			}
			if got.Author != tt.want.Author {
				t.Errorf("Parse() Author = %v, want %v", got.Author, tt.want.Author)
			}
			if got.Size != tt.want.Size {
				t.Errorf("Parse() Size = %v, want %v", got.Size, tt.want.Size)
			}
			if len(got.Servers) != len(tt.want.Servers) {
				t.Errorf("Parse() Servers len = %v, want %v", len(got.Servers), len(tt.want.Servers))
			}
		})
	}
}

func TestIsBlossom(t *testing.T) {
	tests := []struct {
		s    string
		want bool
	}{
		{"blossom:abc.jpg", true},
		{"blossom:hash.ext?xs=server", true},
		{"https://example.com", false},
		{"http://blossom:something", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.s, func(t *testing.T) {
			if got := IsBlossom(tt.s); got != tt.want {
				t.Errorf("IsBlossom(%q) = %v, want %v", tt.s, got, tt.want)
			}
		})
	}
}

func TestBuild(t *testing.T) {
	tests := []struct {
		name   string
		hash   string
		ext    string
		server string
		size   int64
		want   string
	}{
		{
			name:   "basic URI",
			hash:   "b1674191a88ec5cdd733e4240a81803105dc412d6c6708d53ab94fc248f4f553",
			ext:    "jpg",
			server: "",
			size:   0,
			want:   "blossom:b1674191a88ec5cdd733e4240a81803105dc412d6c6708d53ab94fc248f4f553.jpg",
		},
		{
			name:   "with server and size",
			hash:   "b1674191a88ec5cdd733e4240a81803105dc412d6c6708d53ab94fc248f4f553",
			ext:    "mp4",
			server: "https://files.example.com",
			size:   12345,
			want:   "blossom:b1674191a88ec5cdd733e4240a81803105dc412d6c6708d53ab94fc248f4f553.mp4?xs=files.example.com&sz=12345",
		},
		{
			name:   "empty ext becomes bin",
			hash:   "b1674191a88ec5cdd733e4240a81803105dc412d6c6708d53ab94fc248f4f553",
			ext:    "",
			server: "",
			size:   0,
			want:   "blossom:b1674191a88ec5cdd733e4240a81803105dc412d6c6708d53ab94fc248f4f553.bin",
		},
		{
			name:   "strips leading dot from ext",
			hash:   "b1674191a88ec5cdd733e4240a81803105dc412d6c6708d53ab94fc248f4f553",
			ext:    ".png",
			server: "",
			size:   0,
			want:   "blossom:b1674191a88ec5cdd733e4240a81803105dc412d6c6708d53ab94fc248f4f553.png",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Build(tt.hash, tt.ext, tt.server, tt.size); got != tt.want {
				t.Errorf("Build() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestURI_ToHTTPURLs(t *testing.T) {
	uri := &URI{
		Hash:    "b1674191a88ec5cdd733e4240a81803105dc412d6c6708d53ab94fc248f4f553",
		Servers: []string{"blossom.example.com", "https://backup.example.com"},
	}

	urls := uri.ToHTTPURLs()
	if len(urls) != 2 {
		t.Errorf("ToHTTPURLs() len = %v, want 2", len(urls))
	}
	if urls[0] != "https://blossom.example.com/b1674191a88ec5cdd733e4240a81803105dc412d6c6708d53ab94fc248f4f553" {
		t.Errorf("ToHTTPURLs()[0] = %v, want https://blossom.example.com/hash", urls[0])
	}
	if urls[1] != "https://backup.example.com/b1674191a88ec5cdd733e4240a81803105dc412d6c6708d53ab94fc248f4f553" {
		t.Errorf("ToHTTPURLs()[1] = %v, want https://backup.example.com/hash", urls[1])
	}
}
