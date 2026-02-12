package storage

import (
	"bytes"
	"context"
	"io"
	"os"
	"testing"
)

// TestS3StorageInterface verifies S3Storage implements StorageBackend.
func TestS3StorageInterface(t *testing.T) {
	// Compile-time check that S3Storage implements StorageBackend
	var _ StorageBackend = (*S3Storage)(nil)
}

// TestS3StorageIntegration tests against a real S3-compatible endpoint.
// Set S3_TEST_ENDPOINT, S3_TEST_BUCKET, S3_TEST_ACCESS_KEY, S3_TEST_SECRET_KEY to run.
func TestS3StorageIntegration(t *testing.T) {
	endpoint := os.Getenv("S3_TEST_ENDPOINT")
	bucket := os.Getenv("S3_TEST_BUCKET")
	accessKey := os.Getenv("S3_TEST_ACCESS_KEY")
	secretKey := os.Getenv("S3_TEST_SECRET_KEY")

	if endpoint == "" || bucket == "" || accessKey == "" || secretKey == "" {
		t.Skip("S3 integration test requires S3_TEST_ENDPOINT, S3_TEST_BUCKET, S3_TEST_ACCESS_KEY, S3_TEST_SECRET_KEY")
	}

	ctx := context.Background()
	storage, err := NewS3Storage(ctx, S3Config{
		Endpoint:  endpoint,
		Bucket:    bucket,
		Region:    "us-east-1",
		AccessKey: accessKey,
		SecretKey: secretKey,
		PathStyle: true,
	})
	if err != nil {
		t.Fatalf("NewS3Storage: %v", err)
	}

	testHash := "test-blob-abc123def456"
	testData := []byte("hello world from S3 test")

	// Test Put
	err = storage.Put(ctx, testHash, bytes.NewReader(testData), int64(len(testData)))
	if err != nil {
		t.Fatalf("Put: %v", err)
	}

	// Test Exists
	exists, err := storage.Exists(ctx, testHash)
	if err != nil {
		t.Fatalf("Exists: %v", err)
	}
	if !exists {
		t.Fatal("Exists returned false after Put")
	}

	// Test Size
	size, err := storage.Size(ctx, testHash)
	if err != nil {
		t.Fatalf("Size: %v", err)
	}
	if size != int64(len(testData)) {
		t.Fatalf("Size: got %d, want %d", size, len(testData))
	}

	// Test Get
	reader, err := storage.Get(ctx, testHash)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	gotData, err := io.ReadAll(reader)
	reader.Close()
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if !bytes.Equal(gotData, testData) {
		t.Fatalf("Get data mismatch: got %q, want %q", gotData, testData)
	}

	// Test Delete
	err = storage.Delete(ctx, testHash)
	if err != nil {
		t.Fatalf("Delete: %v", err)
	}

	// Verify deleted
	exists, err = storage.Exists(ctx, testHash)
	if err != nil {
		t.Fatalf("Exists after delete: %v", err)
	}
	if exists {
		t.Fatal("Exists returned true after Delete")
	}

	// Test Get on non-existent blob
	_, err = storage.Get(ctx, testHash)
	if err != ErrBlobNotFound {
		t.Fatalf("Get non-existent: got %v, want ErrBlobNotFound", err)
	}
}
