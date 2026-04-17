package gin

import (
	"mime"
	"strings"

	"git.coldforge.xyz/coldforge/cloistr-blossom/src/core"
	"git.coldforge.xyz/coldforge/cloistr-blossom/src/pkg/blossom"
)

// generic api error
type apiError struct {
	Message string `json:"message"`
}

// blobs
type blobDescriptor struct {
	Url               string             `json:"url"`
	Sha256            string             `json:"sha256"`
	Size              int64              `json:"size"`
	Type              string             `json:"type"`
	Uploaded          int64              `json:"uploaded"`
	BlossomURI        string             `json:"blossom_uri,omitempty"` // BUD-10 URI
	EncryptionMode    string             `json:"encryption_mode,omitempty"`
	NIP94FileMetadata *nip94FileMetadata `json:"nip94,omitempty"`
}

// NIP94 https://github.com/nostr-protocol/nips/blob/master/94.md
type nip94FileMetadata struct {
	Url            string  `json:"url"`
	MimeType       string  `json:"m"`
	Sha256         string  `json:"x"`
	OriginalSha256 string  `json:"ox"`
	Size           *int64  `json:"size,omitempty"`
	Dimension      *string `json:"dim,omitempty"`
	Magnet         *string `json:"magnet,omitempty"`
	Infohash       *string `json:"i,omitempty"`
	Blurhash       *string `json:"blurhash,omitempty"`
	ThumbnailUrl   *string `json:"thumb,omitempty"`
	ImageUrl       *string `json:"image,omitempty"`
	Summary        *string `json:"summary,omitempty"`
	Alt            *string `json:"alt,omitempty"`
	Fallback       *string `json:"fallback,omitempty"`
	Service        *string `json:"service,omitempty"`
}

func fromDomainBlobDescriptor(blob *core.Blob) *blobDescriptor {
	apiBlob := &blobDescriptor{
		Url:            blob.Url,
		Sha256:         blob.Sha256,
		Size:           blob.Size,
		Type:           blob.Type,
		Uploaded:       blob.Uploaded,
		EncryptionMode: string(blob.EncryptionMode),
	}

	// BUD-10: Generate blossom URI
	ext := extensionFromMimeType(blob.Type)
	apiBlob.BlossomURI = blossom.Build(blob.Sha256, ext, blob.Url, blob.Size)

	if blob.NIP94 != nil {
		apiBlob.NIP94FileMetadata = &nip94FileMetadata{
			Url:            blob.NIP94.Url,
			MimeType:       blob.NIP94.MimeType,
			Sha256:         blob.NIP94.Sha256,
			OriginalSha256: blob.NIP94.OriginalSha256,
		}
	}

	return apiBlob
}

// extensionFromMimeType extracts file extension from MIME type
func extensionFromMimeType(mimeType string) string {
	if mimeType == "" {
		return "bin"
	}

	// Try to get extension from mime package
	exts, err := mime.ExtensionsByType(mimeType)
	if err == nil && len(exts) > 0 {
		// Prefer common extensions
		for _, ext := range exts {
			ext = strings.TrimPrefix(ext, ".")
			switch ext {
			case "jpg", "jpeg", "png", "gif", "webp", "mp4", "webm", "mp3", "ogg", "pdf", "txt":
				return ext
			}
		}
		return strings.TrimPrefix(exts[0], ".")
	}

	// Fallback: extract from mime type directly
	parts := strings.Split(mimeType, "/")
	if len(parts) == 2 {
		subtype := parts[1]
		// Handle common subtypes
		switch subtype {
		case "jpeg":
			return "jpg"
		case "mpeg":
			if parts[0] == "audio" {
				return "mp3"
			}
			return "mpg"
		default:
			// Remove any parameters (e.g., "plain; charset=utf-8")
			subtype = strings.Split(subtype, ";")[0]
			return strings.TrimSpace(subtype)
		}
	}

	return "bin"
}

func fromSliceDomainBlobDescriptor(blobs []*core.Blob) []*blobDescriptor {
	apiBlobs := make([]*blobDescriptor, len(blobs))
	for i := range blobs {
		apiBlobs[i] = fromDomainBlobDescriptor(blobs[i])
	}

	return apiBlobs
}

// bud-04 mirror a blob
type mirrorInput struct {
	Url string `json:"url"`
}
