package core

import (
	"context"
	"errors"
)

var (
	// ErrTusUploadNotFound is returned for an upload id with no upload.
	ErrTusUploadNotFound = errors.New("tus upload not found")

	// ErrTusOffsetConflict is returned when the upload's offset is no longer
	// the one a chunk was written for: another request appended first, or the
	// upload was terminated meanwhile.
	ErrTusOffsetConflict = errors.New("tus upload offset changed")
)

// TusUpload is a resumable (tus) upload in progress.
type TusUpload struct {
	ID        string
	Pubkey    string
	Size      int64
	Offset    int64
	Metadata  map[string]string
	CreatedAt int64
	ExpiresAt int64
}

// TusUploadStore keeps resumable uploads where every replica can see them, so
// a client's requests may land on any of them. An upload's state is in the
// database; each chunk's bytes are an object in blob storage.
type TusUploadStore interface {
	Create(ctx context.Context, upload *TusUpload) error

	// Get returns ErrTusUploadNotFound if there is no such upload.
	Get(ctx context.Context, id string) (*TusUpload, error)

	// AppendChunk stores data at offset and advances the upload to
	// offset+len(data), returning the new offset. It returns
	// ErrTusOffsetConflict, and stores nothing, if the upload's offset is not
	// offset by the time the chunk is recorded.
	AppendChunk(ctx context.Context, id string, offset int64, data []byte) (int64, error)

	// ReadAll returns the upload's bytes, in order.
	ReadAll(ctx context.Context, id string) ([]byte, error)

	// Delete removes the upload and its bytes. It returns
	// ErrTusUploadNotFound if there is no such upload.
	Delete(ctx context.Context, id string) error

	// DeleteExpired removes every upload that expired before now (Unix
	// seconds) and returns how many it removed.
	DeleteExpired(ctx context.Context, now int64) (int, error)
}
