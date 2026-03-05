package service

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"git.coldforge.xyz/coldforge/cloistr-blossom/src/core"
)

// mockBlobStorage is a mock implementation of core.BlobStorage for testing
type mockBlobStorage struct {
	blobs      map[string]*core.Blob
	references map[string]map[string]bool // hash -> pubkey -> exists
}

func newMockBlobStorage() *mockBlobStorage {
	return &mockBlobStorage{
		blobs:      make(map[string]*core.Blob),
		references: make(map[string]map[string]bool),
	}
}

func (m *mockBlobStorage) Save(ctx context.Context, pubkey string, sha256 string, url string, size int64, mimeType string, blob []byte, created int64, encryptionMode core.EncryptionMode) (*core.Blob, error) {
	b := &core.Blob{
		Pubkey:         pubkey,
		Url:            url,
		Sha256:         sha256,
		Size:           size,
		Type:           mimeType,
		Blob:           blob,
		Uploaded:       created,
		EncryptionMode: encryptionMode,
	}
	m.blobs[sha256] = b
	if m.references[sha256] == nil {
		m.references[sha256] = make(map[string]bool)
	}
	m.references[sha256][pubkey] = true
	return b, nil
}

func (m *mockBlobStorage) SaveWithDedup(ctx context.Context, pubkey string, sha256 string, url string, size int64, mimeType string, blob []byte, created int64, encryptionMode core.EncryptionMode) (*core.Blob, bool, error) {
	isNew := false
	if _, exists := m.blobs[sha256]; !exists {
		isNew = true
		b := &core.Blob{
			Pubkey:         pubkey,
			Url:            url,
			Sha256:         sha256,
			Size:           size,
			Type:           mimeType,
			Blob:           blob,
			Uploaded:       created,
			EncryptionMode: encryptionMode,
		}
		m.blobs[sha256] = b
	}
	if m.references[sha256] == nil {
		m.references[sha256] = make(map[string]bool)
	}
	m.references[sha256][pubkey] = true
	return m.blobs[sha256], isNew, nil
}

func (m *mockBlobStorage) Exists(ctx context.Context, sha256 string) (bool, error) {
	_, exists := m.blobs[sha256]
	return exists, nil
}

func (m *mockBlobStorage) GetFromHash(ctx context.Context, sha256 string) (*core.Blob, error) {
	if blob, exists := m.blobs[sha256]; exists {
		return blob, nil
	}
	return nil, core.ErrBlobNotFound
}

func (m *mockBlobStorage) GetFromPubkey(ctx context.Context, pubkey string) ([]*core.Blob, error) {
	var blobs []*core.Blob
	for hash, refs := range m.references {
		if refs[pubkey] {
			if blob, exists := m.blobs[hash]; exists {
				blobs = append(blobs, blob)
			}
		}
	}
	return blobs, nil
}

func (m *mockBlobStorage) GetFromPubkeyWithFilter(ctx context.Context, pubkey string, filter *core.BlobFilter) (*core.BlobListResult, error) {
	blobs, _ := m.GetFromPubkey(ctx, pubkey)
	return &core.BlobListResult{
		Blobs: blobs,
		Total: int64(len(blobs)),
	}, nil
}

func (m *mockBlobStorage) DeleteFromHash(ctx context.Context, sha256 string) error {
	delete(m.blobs, sha256)
	delete(m.references, sha256)
	return nil
}

func (m *mockBlobStorage) HasReference(ctx context.Context, pubkey string, sha256 string) (bool, error) {
	if refs, exists := m.references[sha256]; exists {
		return refs[pubkey], nil
	}
	return false, nil
}

func (m *mockBlobStorage) DeleteReference(ctx context.Context, pubkey string, sha256 string) (bool, error) {
	if refs, exists := m.references[sha256]; exists {
		delete(refs, pubkey)
		if len(refs) == 0 {
			delete(m.blobs, sha256)
			delete(m.references, sha256)
			return true, nil
		}
	}
	return false, nil
}

func (m *mockBlobStorage) IsEncryptionEnabled() bool {
	return false
}

// mockStorageBackend is a mock implementation of storage.StorageBackend for testing
type mockStorageBackend struct {
	data map[string][]byte
}

func newMockStorageBackend() *mockStorageBackend {
	return &mockStorageBackend{
		data: make(map[string][]byte),
	}
}

func (m *mockStorageBackend) Put(ctx context.Context, key string, reader io.Reader, size int64) error {
	data, err := io.ReadAll(reader)
	if err != nil {
		return err
	}
	m.data[key] = data
	return nil
}

func (m *mockStorageBackend) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	if data, exists := m.data[key]; exists {
		return io.NopCloser(bytes.NewReader(data)), nil
	}
	return nil, errors.New("blob not found")
}

func (m *mockStorageBackend) Delete(ctx context.Context, key string) error {
	delete(m.data, key)
	return nil
}

func (m *mockStorageBackend) Exists(ctx context.Context, key string) (bool, error) {
	_, exists := m.data[key]
	return exists, nil
}

func (m *mockStorageBackend) Size(ctx context.Context, key string) (int64, error) {
	if data, exists := m.data[key]; exists {
		return int64(len(data)), nil
	}
	return 0, errors.New("blob not found")
}

// mockQuotaService is a mock implementation of core.QuotaService for testing
type mockQuotaService struct {
	enabled      bool
	quotaLimits  map[string]int64
	quotaUsage   map[string]int64
	checkError   error
	incrementErr error
	decrementErr error
}

func newMockQuotaService(enabled bool) *mockQuotaService {
	return &mockQuotaService{
		enabled:     enabled,
		quotaLimits: make(map[string]int64),
		quotaUsage:  make(map[string]int64),
	}
}

func (m *mockQuotaService) IsEnabled() bool {
	return m.enabled
}

func (m *mockQuotaService) CheckQuota(ctx context.Context, pubkey string, additionalBytes int64) error {
	if m.checkError != nil {
		return m.checkError
	}
	if !m.enabled {
		return nil
	}
	limit, hasLimit := m.quotaLimits[pubkey]
	if !hasLimit {
		limit = 1024 * 1024 * 1024 // 1GB default
	}
	current := m.quotaUsage[pubkey]
	if current+additionalBytes > limit {
		return core.ErrQuotaExceeded
	}
	return nil
}

func (m *mockQuotaService) IncrementUsage(ctx context.Context, pubkey string, bytes int64) error {
	if m.incrementErr != nil {
		return m.incrementErr
	}
	m.quotaUsage[pubkey] += bytes
	return nil
}

func (m *mockQuotaService) DecrementUsage(ctx context.Context, pubkey string, bytes int64) error {
	if m.decrementErr != nil {
		return m.decrementErr
	}
	m.quotaUsage[pubkey] -= bytes
	if m.quotaUsage[pubkey] < 0 {
		m.quotaUsage[pubkey] = 0
	}
	return nil
}

func (m *mockQuotaService) GetUser(ctx context.Context, pubkey string) (*core.User, error) {
	return nil, errors.New("not implemented")
}

func (m *mockQuotaService) GetOrCreateUser(ctx context.Context, pubkey string) (*core.User, error) {
	return nil, errors.New("not implemented")
}

func (m *mockQuotaService) GetQuotaInfo(ctx context.Context, pubkey string) (*core.QuotaInfo, error) {
	return nil, errors.New("not implemented")
}

func (m *mockQuotaService) SetQuota(ctx context.Context, pubkey string, quotaBytes int64) error {
	m.quotaLimits[pubkey] = quotaBytes
	return nil
}

func (m *mockQuotaService) BanUser(ctx context.Context, pubkey string) error {
	return errors.New("not implemented")
}

func (m *mockQuotaService) UnbanUser(ctx context.Context, pubkey string) error {
	return errors.New("not implemented")
}

func (m *mockQuotaService) ListUsers(ctx context.Context, limit, offset int64) ([]*core.User, error) {
	return nil, errors.New("not implemented")
}

func (m *mockQuotaService) GetUserCount(ctx context.Context) (int64, error) {
	return 0, errors.New("not implemented")
}

func (m *mockQuotaService) RecalculateUsage(ctx context.Context, pubkey string) error {
	return errors.New("not implemented")
}

// mockExpirationService is a mock implementation of core.ExpirationService for testing
type mockExpirationService struct {
	expirations map[string]time.Time
}

func newMockExpirationService() *mockExpirationService {
	return &mockExpirationService{
		expirations: make(map[string]time.Time),
	}
}

func (m *mockExpirationService) SetExpiration(ctx context.Context, hash string, expiresAt time.Time) error {
	m.expirations[hash] = expiresAt
	return nil
}

func (m *mockExpirationService) SetExpirationTTL(ctx context.Context, hash string, ttl time.Duration) error {
	m.expirations[hash] = time.Now().Add(ttl)
	return nil
}

func (m *mockExpirationService) ClearExpiration(ctx context.Context, hash string) error {
	delete(m.expirations, hash)
	return nil
}

func (m *mockExpirationService) GetExpiredBlobs(ctx context.Context, limit int) ([]core.ExpiredBlob, error) {
	return nil, errors.New("not implemented")
}

func (m *mockExpirationService) CleanupExpired(ctx context.Context) (int, error) {
	return 0, errors.New("not implemented")
}

func (m *mockExpirationService) CountExpired(ctx context.Context) (int64, error) {
	return 0, errors.New("not implemented")
}

func (m *mockExpirationService) ApplyPolicy(ctx context.Context, hash string, mimeType string, size int64, pubkey string) (bool, error) {
	return false, errors.New("not implemented")
}

func (m *mockExpirationService) GetPolicies(ctx context.Context) ([]core.ExpirationPolicy, error) {
	return nil, errors.New("not implemented")
}

func (m *mockExpirationService) CreatePolicy(ctx context.Context, policy *core.ExpirationPolicy) (*core.ExpirationPolicy, error) {
	return nil, errors.New("not implemented")
}

func (m *mockExpirationService) UpdatePolicy(ctx context.Context, policy *core.ExpirationPolicy) error {
	return errors.New("not implemented")
}

func (m *mockExpirationService) DeletePolicy(ctx context.Context, id int32) error {
	return errors.New("not implemented")
}

func (m *mockExpirationService) StartCleanupWorker(ctx context.Context) {}

func (m *mockExpirationService) StopCleanupWorker() {}

// setupBatchTest creates a test environment with mocked dependencies
func setupBatchTest(t *testing.T) (*batchService, *mockBlobStorage, *mockStorageBackend, *mockQuotaService, *mockExpirationService) {
	blobStorage := newMockBlobStorage()
	storageBackend := newMockStorageBackend()
	quotaService := newMockQuotaService(true)
	expirationService := newMockExpirationService()

	config := core.DefaultBatchConfig()
	log, _ := zap.NewDevelopment()

	svc := &batchService{
		config:      config,
		blobStorage: blobStorage,
		storage:     storageBackend,
		quota:       quotaService,
		expiration:  expirationService,
		cdnBaseURL:  "https://files.cloistr.xyz",
		log:         log,
	}

	return svc, blobStorage, storageBackend, quotaService, expirationService
}

// hashData calculates SHA-256 hash of data
func hashData(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

// TestBatchUpload_SingleFile tests uploading a single file
func TestBatchUpload_SingleFile(t *testing.T) {
	svc, _, _, _, _ := setupBatchTest(t)
	ctx := context.Background()

	content := []byte("Hello, batch upload!")
	hash := hashData(content)

	req := &core.BatchUploadRequest{
		Pubkey: "testpubkey",
		Files: []core.BatchUploadFile{
			{
				Filename: "test.txt",
				MimeType: "text/plain",
				Size:     int64(len(content)),
				Reader:   bytes.NewReader(content),
			},
		},
	}

	resp, err := svc.Upload(ctx, req)
	require.NoError(t, err)
	require.NotNil(t, resp)

	assert.Equal(t, 1, resp.TotalFiles)
	assert.Equal(t, 1, resp.SuccessCount)
	assert.Equal(t, 0, resp.FailureCount)
	assert.Len(t, resp.Results, 1)

	result := resp.Results[0]
	assert.True(t, result.Success)
	assert.Equal(t, hash, result.Hash)
	assert.Equal(t, "test.txt", result.Filename)
	assert.Equal(t, int64(len(content)), result.Size)
	assert.Equal(t, "text/plain", result.MimeType)
}

// TestBatchUpload_MultipleFiles tests uploading multiple files
func TestBatchUpload_MultipleFiles(t *testing.T) {
	svc, _, _, _, _ := setupBatchTest(t)
	ctx := context.Background()

	files := []struct {
		name    string
		content []byte
		mime    string
	}{
		{"file1.txt", []byte("First file content"), "text/plain"},
		{"file2.json", []byte(`{"key": "value"}`), "application/json"},
		{"file3.html", []byte("<html></html>"), "text/html"},
	}

	var uploadFiles []core.BatchUploadFile
	for _, f := range files {
		uploadFiles = append(uploadFiles, core.BatchUploadFile{
			Filename: f.name,
			MimeType: f.mime,
			Size:     int64(len(f.content)),
			Reader:   bytes.NewReader(f.content),
		})
	}

	req := &core.BatchUploadRequest{
		Pubkey: "testpubkey",
		Files:  uploadFiles,
	}

	resp, err := svc.Upload(ctx, req)
	require.NoError(t, err)
	require.NotNil(t, resp)

	assert.Equal(t, 3, resp.TotalFiles)
	assert.Equal(t, 3, resp.SuccessCount)
	assert.Equal(t, 0, resp.FailureCount)
	assert.Len(t, resp.Results, 3)

	for i, f := range files {
		result := resp.Results[i]
		assert.True(t, result.Success)
		assert.Equal(t, f.name, result.Filename)
		assert.Equal(t, hashData(f.content), result.Hash)
		assert.Equal(t, f.mime, result.MimeType)
	}
}

// TestBatchUpload_QuotaCheck tests quota enforcement
func TestBatchUpload_QuotaCheck(t *testing.T) {
	svc, _, _, quotaService, _ := setupBatchTest(t)
	ctx := context.Background()

	// Set a small quota limit
	quotaService.SetQuota(ctx, "testpubkey", 100)

	content := []byte(strings.Repeat("x", 200)) // 200 bytes - exceeds quota

	req := &core.BatchUploadRequest{
		Pubkey: "testpubkey",
		Files: []core.BatchUploadFile{
			{
				Filename: "large.txt",
				MimeType: "text/plain",
				Size:     int64(len(content)),
				Reader:   bytes.NewReader(content),
			},
		},
	}

	resp, err := svc.Upload(ctx, req)
	assert.Error(t, err)
	assert.Nil(t, resp)
	assert.Contains(t, err.Error(), "quota check failed")
}

// TestBatchUpload_NoFiles tests error when no files provided
func TestBatchUpload_NoFiles(t *testing.T) {
	svc, _, _, _, _ := setupBatchTest(t)
	ctx := context.Background()

	req := &core.BatchUploadRequest{
		Pubkey: "testpubkey",
		Files:  []core.BatchUploadFile{},
	}

	resp, err := svc.Upload(ctx, req)
	assert.Error(t, err)
	assert.Nil(t, resp)
	assert.Contains(t, err.Error(), "no files provided")
}

// TestBatchUpload_TooManyFiles tests max file limit
func TestBatchUpload_TooManyFiles(t *testing.T) {
	svc, _, _, _, _ := setupBatchTest(t)
	ctx := context.Background()

	// Create more files than allowed
	maxFiles := svc.config.MaxUploadFiles
	var uploadFiles []core.BatchUploadFile
	for i := 0; i < maxFiles+10; i++ {
		content := []byte("test")
		uploadFiles = append(uploadFiles, core.BatchUploadFile{
			Filename: "file.txt",
			MimeType: "text/plain",
			Size:     int64(len(content)),
			Reader:   bytes.NewReader(content),
		})
	}

	req := &core.BatchUploadRequest{
		Pubkey: "testpubkey",
		Files:  uploadFiles,
	}

	resp, err := svc.Upload(ctx, req)
	assert.Error(t, err)
	assert.Nil(t, resp)
	assert.Contains(t, err.Error(), "too many files")
}

// TestBatchUpload_TotalSizeExceeded tests max total size limit
func TestBatchUpload_TotalSizeExceeded(t *testing.T) {
	svc, _, _, _, _ := setupBatchTest(t)
	ctx := context.Background()

	// Disable quota to test size limit independently
	svc.quota = newMockQuotaService(false)

	// Create files that exceed total size limit
	largeContent := make([]byte, svc.config.MaxUploadSizeBytes+1)

	req := &core.BatchUploadRequest{
		Pubkey: "testpubkey",
		Files: []core.BatchUploadFile{
			{
				Filename: "huge.bin",
				MimeType: "application/octet-stream",
				Size:     int64(len(largeContent)),
				Reader:   bytes.NewReader(largeContent),
			},
		},
	}

	resp, err := svc.Upload(ctx, req)
	assert.Error(t, err)
	assert.Nil(t, resp)
	assert.Contains(t, err.Error(), "total size")
	assert.Contains(t, err.Error(), "exceeds max")
}

// TestBatchUpload_PartialSuccess tests when some files succeed and some fail
func TestBatchUpload_PartialSuccess(t *testing.T) {
	svc, _, _, _, _ := setupBatchTest(t)
	ctx := context.Background()

	// Create a reader that will fail
	failingReader := &failReader{shouldFail: true}

	req := &core.BatchUploadRequest{
		Pubkey: "testpubkey",
		Files: []core.BatchUploadFile{
			{
				Filename: "good.txt",
				MimeType: "text/plain",
				Size:     5,
				Reader:   bytes.NewReader([]byte("hello")),
			},
			{
				Filename: "bad.txt",
				MimeType: "text/plain",
				Size:     5,
				Reader:   failingReader,
			},
			{
				Filename: "good2.txt",
				MimeType: "text/plain",
				Size:     5,
				Reader:   bytes.NewReader([]byte("world")),
			},
		},
	}

	resp, err := svc.Upload(ctx, req)
	require.NoError(t, err) // Should not error, but report failures
	require.NotNil(t, resp)

	assert.Equal(t, 3, resp.TotalFiles)
	assert.Equal(t, 2, resp.SuccessCount)
	assert.Equal(t, 1, resp.FailureCount)

	assert.True(t, resp.Results[0].Success)
	assert.False(t, resp.Results[1].Success)
	assert.Contains(t, resp.Results[1].Error, "failed to read")
	assert.True(t, resp.Results[2].Success)
}

// TestBatchUpload_WithExpiration tests setting expiration on uploaded files
func TestBatchUpload_WithExpiration(t *testing.T) {
	svc, _, _, _, expirationService := setupBatchTest(t)
	ctx := context.Background()

	content := []byte("temporary file")

	req := &core.BatchUploadRequest{
		Pubkey:    "testpubkey",
		ExpiresIn: 3600, // 1 hour
		Files: []core.BatchUploadFile{
			{
				Filename: "temp.txt",
				MimeType: "text/plain",
				Size:     int64(len(content)),
				Reader:   bytes.NewReader(content),
			},
		},
	}

	resp, err := svc.Upload(ctx, req)
	require.NoError(t, err)
	require.NotNil(t, resp)

	assert.Equal(t, 1, resp.SuccessCount)

	// Check expiration was set
	hash := resp.Results[0].Hash
	expiresAt, exists := expirationService.expirations[hash]
	assert.True(t, exists)
	assert.True(t, time.Until(expiresAt) > 59*time.Minute)
}

// TestBatchDownload_ZipFormat tests downloading multiple files as ZIP
func TestBatchDownload_ZipFormat(t *testing.T) {
	svc, blobStorage, storageBackend, _, _ := setupBatchTest(t)
	ctx := context.Background()

	// Create test blobs
	files := []struct {
		content  []byte
		mimeType string
	}{
		{[]byte("File 1 content"), "text/plain"},
		{[]byte("File 2 content"), "text/plain"},
	}

	var hashes []string
	for _, f := range files {
		hash := hashData(f.content)
		hashes = append(hashes, hash)

		// Add to blob storage
		blobStorage.Save(ctx, "testpubkey", hash, "https://example.com/"+hash, int64(len(f.content)), f.mimeType, f.content, time.Now().Unix(), core.EncryptionModeNone)
		// Add to storage backend
		storageBackend.Put(ctx, hash, bytes.NewReader(f.content), int64(len(f.content)))
	}

	req := &core.BatchDownloadRequest{
		Hashes: hashes,
		Format: "zip",
	}

	reader, resp, err := svc.Download(ctx, req)
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.NotNil(t, reader)
	defer reader.Close()

	assert.Equal(t, "application/zip", resp.ContentType)
	assert.Equal(t, 2, resp.FileCount)
	assert.Greater(t, resp.Size, int64(0))

	// Verify ZIP archive
	data, err := io.ReadAll(reader)
	require.NoError(t, err)

	zipReader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	require.NoError(t, err)

	assert.Equal(t, 2, len(zipReader.File))
}

// TestBatchDownload_TarFormat tests downloading as TAR
func TestBatchDownload_TarFormat(t *testing.T) {
	svc, blobStorage, storageBackend, _, _ := setupBatchTest(t)
	ctx := context.Background()

	content := []byte("TAR test content")
	hash := hashData(content)

	blobStorage.Save(ctx, "testpubkey", hash, "https://example.com/"+hash, int64(len(content)), "text/plain", content, time.Now().Unix(), core.EncryptionModeNone)
	storageBackend.Put(ctx, hash, bytes.NewReader(content), int64(len(content)))

	req := &core.BatchDownloadRequest{
		Hashes: []string{hash},
		Format: "tar",
	}

	reader, resp, err := svc.Download(ctx, req)
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.NotNil(t, reader)
	defer reader.Close()

	assert.Equal(t, "application/x-tar", resp.ContentType)
	assert.Equal(t, 1, resp.FileCount)

	// Verify TAR archive
	data, err := io.ReadAll(reader)
	require.NoError(t, err)

	tarReader := tar.NewReader(bytes.NewReader(data))
	header, err := tarReader.Next()
	require.NoError(t, err)
	assert.Equal(t, hash+".txt", header.Name)
}

// TestBatchDownload_TarGzFormat tests downloading as TAR.GZ
func TestBatchDownload_TarGzFormat(t *testing.T) {
	svc, blobStorage, storageBackend, _, _ := setupBatchTest(t)
	ctx := context.Background()

	content := []byte("TAR.GZ test content")
	hash := hashData(content)

	blobStorage.Save(ctx, "testpubkey", hash, "https://example.com/"+hash, int64(len(content)), "text/plain", content, time.Now().Unix(), core.EncryptionModeNone)
	storageBackend.Put(ctx, hash, bytes.NewReader(content), int64(len(content)))

	req := &core.BatchDownloadRequest{
		Hashes: []string{hash},
		Format: "tar.gz",
	}

	reader, resp, err := svc.Download(ctx, req)
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.NotNil(t, reader)
	defer reader.Close()

	assert.Equal(t, "application/gzip", resp.ContentType)
	assert.Equal(t, 1, resp.FileCount)

	// Verify TAR.GZ archive
	data, err := io.ReadAll(reader)
	require.NoError(t, err)

	gzReader, err := gzip.NewReader(bytes.NewReader(data))
	require.NoError(t, err)
	defer gzReader.Close()

	tarReader := tar.NewReader(gzReader)
	header, err := tarReader.Next()
	require.NoError(t, err)
	assert.Equal(t, hash+".txt", header.Name)
}

// TestBatchDownload_MissingBlobs tests handling missing blobs
func TestBatchDownload_MissingBlobs(t *testing.T) {
	svc, blobStorage, storageBackend, _, _ := setupBatchTest(t)
	ctx := context.Background()

	// Create one blob
	content := []byte("Exists")
	hash1 := hashData(content)
	blobStorage.Save(ctx, "testpubkey", hash1, "https://example.com/"+hash1, int64(len(content)), "text/plain", content, time.Now().Unix(), core.EncryptionModeNone)
	storageBackend.Put(ctx, hash1, bytes.NewReader(content), int64(len(content)))

	// Request two hashes (one exists, one doesn't)
	req := &core.BatchDownloadRequest{
		Hashes: []string{hash1, "nonexistent"},
		Format: "zip",
	}

	reader, resp, err := svc.Download(ctx, req)
	require.NoError(t, err)
	require.NotNil(t, resp)
	defer reader.Close()

	// Should only include the existing blob
	assert.Equal(t, 1, resp.FileCount)
	assert.Len(t, resp.Results, 2)

	assert.True(t, resp.Results[0].Success)
	assert.False(t, resp.Results[1].Success)
	assert.Contains(t, resp.Results[1].Error, "blob not found")
}

// TestBatchDownload_NoHashes tests error when no hashes provided
func TestBatchDownload_NoHashes(t *testing.T) {
	svc, _, _, _, _ := setupBatchTest(t)
	ctx := context.Background()

	req := &core.BatchDownloadRequest{
		Hashes: []string{},
	}

	reader, resp, err := svc.Download(ctx, req)
	assert.Error(t, err)
	assert.Nil(t, resp)
	assert.Nil(t, reader)
	assert.Contains(t, err.Error(), "no hashes provided")
}

// TestBatchDownload_TooManyFiles tests max file limit
func TestBatchDownload_TooManyFiles(t *testing.T) {
	svc, _, _, _, _ := setupBatchTest(t)
	ctx := context.Background()

	// Create more hashes than allowed
	var hashes []string
	for i := 0; i < svc.config.MaxDownloadFiles+10; i++ {
		hashes = append(hashes, "hash"+string(rune(i)))
	}

	req := &core.BatchDownloadRequest{
		Hashes: hashes,
	}

	reader, resp, err := svc.Download(ctx, req)
	assert.Error(t, err)
	assert.Nil(t, resp)
	assert.Nil(t, reader)
	assert.Contains(t, err.Error(), "too many files")
}

// TestBatchDownload_UnsupportedFormat tests error on unsupported format
func TestBatchDownload_UnsupportedFormat(t *testing.T) {
	svc, _, _, _, _ := setupBatchTest(t)
	ctx := context.Background()

	req := &core.BatchDownloadRequest{
		Hashes: []string{"somehash"},
		Format: "rar",
	}

	reader, resp, err := svc.Download(ctx, req)
	assert.Error(t, err)
	assert.Nil(t, resp)
	assert.Nil(t, reader)
	assert.Contains(t, err.Error(), "unsupported format")
}

// TestBatchDelete_Success tests successful deletion of blobs
func TestBatchDelete_Success(t *testing.T) {
	svc, blobStorage, _, quotaService, _ := setupBatchTest(t)
	ctx := context.Background()

	// Create test blobs
	files := [][]byte{
		[]byte("Delete me 1"),
		[]byte("Delete me 2"),
		[]byte("Delete me 3"),
	}

	var hashes []string
	var totalSize int64
	for _, content := range files {
		hash := hashData(content)
		hashes = append(hashes, hash)
		totalSize += int64(len(content))

		blobStorage.Save(ctx, "testpubkey", hash, "https://example.com/"+hash, int64(len(content)), "text/plain", content, time.Now().Unix(), core.EncryptionModeNone)
	}

	// Set initial quota usage
	quotaService.IncrementUsage(ctx, "testpubkey", totalSize)

	req := &core.BatchDeleteRequest{
		Hashes: hashes,
	}

	resp, err := svc.Delete(ctx, "testpubkey", req)
	require.NoError(t, err)
	require.NotNil(t, resp)

	assert.Equal(t, 3, resp.TotalRequested)
	assert.Equal(t, 3, resp.SuccessCount)
	assert.Equal(t, 0, resp.FailureCount)

	for i, result := range resp.Results {
		assert.True(t, result.Success)
		assert.Equal(t, hashes[i], result.Hash)
	}

	// Verify quota was updated
	assert.Equal(t, int64(0), quotaService.quotaUsage["testpubkey"])
}

// TestBatchDelete_UnauthorizedDelete tests deletion of blob user doesn't own
func TestBatchDelete_UnauthorizedDelete(t *testing.T) {
	svc, blobStorage, _, _, _ := setupBatchTest(t)
	ctx := context.Background()

	// Create blob owned by different user
	content := []byte("Not yours")
	hash := hashData(content)
	blobStorage.Save(ctx, "otherpubkey", hash, "https://example.com/"+hash, int64(len(content)), "text/plain", content, time.Now().Unix(), core.EncryptionModeNone)

	req := &core.BatchDeleteRequest{
		Hashes: []string{hash},
	}

	resp, err := svc.Delete(ctx, "testpubkey", req)
	require.NoError(t, err)
	require.NotNil(t, resp)

	assert.Equal(t, 1, resp.TotalRequested)
	assert.Equal(t, 0, resp.SuccessCount)
	assert.Equal(t, 1, resp.FailureCount)

	assert.False(t, resp.Results[0].Success)
	assert.Contains(t, resp.Results[0].Error, "not found or not authorized")
}

// TestBatchDelete_NonExistentBlob tests deletion of non-existent blob
func TestBatchDelete_NonExistentBlob(t *testing.T) {
	svc, _, _, _, _ := setupBatchTest(t)
	ctx := context.Background()

	req := &core.BatchDeleteRequest{
		Hashes: []string{"nonexistent"},
	}

	resp, err := svc.Delete(ctx, "testpubkey", req)
	require.NoError(t, err)
	require.NotNil(t, resp)

	assert.Equal(t, 1, resp.TotalRequested)
	assert.Equal(t, 0, resp.SuccessCount)
	assert.Equal(t, 1, resp.FailureCount)

	assert.False(t, resp.Results[0].Success)
}

// TestBatchDelete_NoHashes tests error when no hashes provided
func TestBatchDelete_NoHashes(t *testing.T) {
	svc, _, _, _, _ := setupBatchTest(t)
	ctx := context.Background()

	req := &core.BatchDeleteRequest{
		Hashes: []string{},
	}

	resp, err := svc.Delete(ctx, "testpubkey", req)
	assert.Error(t, err)
	assert.Nil(t, resp)
	assert.Contains(t, err.Error(), "no hashes provided")
}

// TestBatchDelete_TooManyFiles tests max file limit
func TestBatchDelete_TooManyFiles(t *testing.T) {
	svc, _, _, _, _ := setupBatchTest(t)
	ctx := context.Background()

	var hashes []string
	for i := 0; i < svc.config.MaxDeleteFiles+10; i++ {
		hashes = append(hashes, "hash"+string(rune(i)))
	}

	req := &core.BatchDeleteRequest{
		Hashes: hashes,
	}

	resp, err := svc.Delete(ctx, "testpubkey", req)
	assert.Error(t, err)
	assert.Nil(t, resp)
	assert.Contains(t, err.Error(), "too many files")
}

// TestBatchStatus_ExistingBlobs tests checking status of existing blobs
func TestBatchStatus_ExistingBlobs(t *testing.T) {
	svc, blobStorage, _, _, _ := setupBatchTest(t)
	ctx := context.Background()

	// Create test blobs
	files := []struct {
		content  []byte
		mimeType string
	}{
		{[]byte("Status test 1"), "text/plain"},
		{[]byte("Status test 2"), "application/json"},
	}

	var hashes []string
	for _, f := range files {
		hash := hashData(f.content)
		hashes = append(hashes, hash)
		blobStorage.Save(ctx, "testpubkey", hash, "https://example.com/"+hash, int64(len(f.content)), f.mimeType, f.content, time.Now().Unix(), core.EncryptionModeNone)
	}

	req := &core.BatchStatusRequest{
		Hashes: hashes,
	}

	resp, err := svc.Status(ctx, req)
	require.NoError(t, err)
	require.NotNil(t, resp)

	assert.Len(t, resp.Items, 2)

	for i, item := range resp.Items {
		assert.Equal(t, hashes[i], item.Hash)
		assert.True(t, item.Exists)
		assert.Equal(t, int64(len(files[i].content)), item.Size)
		assert.Equal(t, files[i].mimeType, item.MimeType)
		assert.Greater(t, item.Created, int64(0))
		assert.NotEmpty(t, item.URL)
	}
}

// TestBatchStatus_NonExistentBlobs tests checking status of non-existent blobs
func TestBatchStatus_NonExistentBlobs(t *testing.T) {
	svc, _, _, _, _ := setupBatchTest(t)
	ctx := context.Background()

	req := &core.BatchStatusRequest{
		Hashes: []string{"nonexistent1", "nonexistent2"},
	}

	resp, err := svc.Status(ctx, req)
	require.NoError(t, err)
	require.NotNil(t, resp)

	assert.Len(t, resp.Items, 2)

	for _, item := range resp.Items {
		assert.False(t, item.Exists)
		assert.Equal(t, int64(0), item.Size)
		assert.Empty(t, item.MimeType)
	}
}

// TestBatchStatus_MixedBlobs tests status check with existing and non-existent blobs
func TestBatchStatus_MixedBlobs(t *testing.T) {
	svc, blobStorage, _, _, _ := setupBatchTest(t)
	ctx := context.Background()

	// Create one existing blob
	content := []byte("Exists")
	existingHash := hashData(content)
	blobStorage.Save(ctx, "testpubkey", existingHash, "https://example.com/"+existingHash, int64(len(content)), "text/plain", content, time.Now().Unix(), core.EncryptionModeNone)

	req := &core.BatchStatusRequest{
		Hashes: []string{existingHash, "nonexistent"},
	}

	resp, err := svc.Status(ctx, req)
	require.NoError(t, err)
	require.NotNil(t, resp)

	assert.Len(t, resp.Items, 2)
	assert.True(t, resp.Items[0].Exists)
	assert.False(t, resp.Items[1].Exists)
}

// TestBatchStatus_NoHashes tests error when no hashes provided
func TestBatchStatus_NoHashes(t *testing.T) {
	svc, _, _, _, _ := setupBatchTest(t)
	ctx := context.Background()

	req := &core.BatchStatusRequest{
		Hashes: []string{},
	}

	resp, err := svc.Status(ctx, req)
	assert.Error(t, err)
	assert.Nil(t, resp)
	assert.Contains(t, err.Error(), "no hashes provided")
}

// TestGetJob_Success tests retrieving a job
func TestGetJob_Success(t *testing.T) {
	svc, _, _, _, _ := setupBatchTest(t)
	ctx := context.Background()

	// Create a test job
	job := &core.BatchJob{
		ID:           "test-job-123",
		Type:         core.BatchOperationUpload,
		Status:       core.BatchStatusComplete,
		Pubkey:       "testpubkey",
		TotalItems:   5,
		SuccessCount: 5,
		Progress:     100,
		CreatedAt:    time.Now().Unix(),
		CompletedAt:  time.Now().Unix(),
	}

	svc.jobs.Store(job.ID, job)

	retrieved, err := svc.GetJob(ctx, job.ID)
	require.NoError(t, err)
	require.NotNil(t, retrieved)

	assert.Equal(t, job.ID, retrieved.ID)
	assert.Equal(t, job.Type, retrieved.Type)
	assert.Equal(t, job.Status, retrieved.Status)
}

// TestGetJob_NotFound tests retrieving non-existent job
func TestGetJob_NotFound(t *testing.T) {
	svc, _, _, _, _ := setupBatchTest(t)
	ctx := context.Background()

	job, err := svc.GetJob(ctx, "nonexistent")
	assert.Error(t, err)
	assert.Nil(t, job)
	assert.Contains(t, err.Error(), "job not found")
}

// TestCancelJob_Success tests canceling a pending job
func TestCancelJob_Success(t *testing.T) {
	svc, _, _, _, _ := setupBatchTest(t)
	ctx := context.Background()

	job := &core.BatchJob{
		ID:         "cancel-job-123",
		Type:       core.BatchOperationUpload,
		Status:     core.BatchStatusPending,
		Pubkey:     "testpubkey",
		TotalItems: 10,
		CreatedAt:  time.Now().Unix(),
	}

	svc.jobs.Store(job.ID, job)

	err := svc.CancelJob(ctx, job.ID)
	require.NoError(t, err)

	// Verify job was cancelled
	retrieved, _ := svc.GetJob(ctx, job.ID)
	assert.Equal(t, core.BatchStatusFailed, retrieved.Status)
	assert.Equal(t, "cancelled", retrieved.Error)
	assert.Greater(t, retrieved.CompletedAt, int64(0))
}

// TestCancelJob_Processing tests canceling a processing job
func TestCancelJob_Processing(t *testing.T) {
	svc, _, _, _, _ := setupBatchTest(t)
	ctx := context.Background()

	job := &core.BatchJob{
		ID:         "cancel-processing-123",
		Type:       core.BatchOperationUpload,
		Status:     core.BatchStatusProcessing,
		Pubkey:     "testpubkey",
		TotalItems: 10,
		CreatedAt:  time.Now().Unix(),
		StartedAt:  time.Now().Unix(),
	}

	svc.jobs.Store(job.ID, job)

	err := svc.CancelJob(ctx, job.ID)
	require.NoError(t, err)

	retrieved, _ := svc.GetJob(ctx, job.ID)
	assert.Equal(t, core.BatchStatusFailed, retrieved.Status)
}

// TestCancelJob_AlreadyComplete tests canceling a completed job
func TestCancelJob_AlreadyComplete(t *testing.T) {
	svc, _, _, _, _ := setupBatchTest(t)
	ctx := context.Background()

	job := &core.BatchJob{
		ID:          "complete-job-123",
		Type:        core.BatchOperationUpload,
		Status:      core.BatchStatusComplete,
		Pubkey:      "testpubkey",
		TotalItems:  10,
		CreatedAt:   time.Now().Unix(),
		CompletedAt: time.Now().Unix(),
	}

	svc.jobs.Store(job.ID, job)

	err := svc.CancelJob(ctx, job.ID)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "cannot be cancelled")
}

// TestCancelJob_NotFound tests canceling non-existent job
func TestCancelJob_NotFound(t *testing.T) {
	svc, _, _, _, _ := setupBatchTest(t)
	ctx := context.Background()

	err := svc.CancelJob(ctx, "nonexistent")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "job not found")
}

// TestCleanupExpiredJobs tests cleanup of old jobs
func TestCleanupExpiredJobs(t *testing.T) {
	svc, _, _, _, _ := setupBatchTest(t)
	ctx := context.Background()

	now := time.Now().Unix()
	oldTime := time.Now().Add(-25 * time.Hour).Unix() // Beyond default 24h TTL

	// Create jobs with different completion times
	jobs := []*core.BatchJob{
		{
			ID:          "old-job-1",
			Status:      core.BatchStatusComplete,
			CreatedAt:   oldTime,
			CompletedAt: oldTime,
		},
		{
			ID:          "old-job-2",
			Status:      core.BatchStatusComplete,
			CreatedAt:   oldTime,
			CompletedAt: oldTime,
		},
		{
			ID:          "recent-job",
			Status:      core.BatchStatusComplete,
			CreatedAt:   now,
			CompletedAt: now,
		},
		{
			ID:          "pending-job",
			Status:      core.BatchStatusPending,
			CreatedAt:   oldTime,
			CompletedAt: 0, // Not completed
		},
	}

	for _, job := range jobs {
		svc.jobs.Store(job.ID, job)
	}

	count, err := svc.CleanupExpiredJobs(ctx)
	require.NoError(t, err)

	// Should clean up 2 old completed jobs
	assert.Equal(t, 2, count)

	// Verify old jobs are gone
	_, err = svc.GetJob(ctx, "old-job-1")
	assert.Error(t, err)
	_, err = svc.GetJob(ctx, "old-job-2")
	assert.Error(t, err)

	// Verify recent and pending jobs remain
	_, err = svc.GetJob(ctx, "recent-job")
	assert.NoError(t, err)
	_, err = svc.GetJob(ctx, "pending-job")
	assert.NoError(t, err)
}

// TestCleanupExpiredJobs_NoExpiredJobs tests cleanup when no jobs are expired
func TestCleanupExpiredJobs_NoExpiredJobs(t *testing.T) {
	svc, _, _, _, _ := setupBatchTest(t)
	ctx := context.Background()

	// Create recent jobs
	job := &core.BatchJob{
		ID:          "recent-job",
		Status:      core.BatchStatusComplete,
		CreatedAt:   time.Now().Unix(),
		CompletedAt: time.Now().Unix(),
	}

	svc.jobs.Store(job.ID, job)

	count, err := svc.CleanupExpiredJobs(ctx)
	require.NoError(t, err)
	assert.Equal(t, 0, count)

	// Job should still exist
	_, err = svc.GetJob(ctx, job.ID)
	assert.NoError(t, err)
}

// TestNewBatchService tests service creation
func TestNewBatchService(t *testing.T) {
	blobStorage := newMockBlobStorage()
	storageBackend := newMockStorageBackend()
	quotaService := newMockQuotaService(true)
	expirationService := newMockExpirationService()

	config := core.DefaultBatchConfig()
	log, _ := zap.NewDevelopment()

	svc, err := NewBatchService(
		blobStorage,
		storageBackend,
		quotaService,
		expirationService,
		config,
		"https://files.cloistr.xyz",
		log,
	)

	require.NoError(t, err)
	require.NotNil(t, svc)

	// Verify it implements the interface
	_, ok := svc.(core.BatchService)
	assert.True(t, ok)
}

// TestDefaultBatchConfig tests default configuration values
func TestDefaultBatchConfig(t *testing.T) {
	config := core.DefaultBatchConfig()

	assert.Equal(t, 50, config.MaxUploadFiles)
	assert.Equal(t, 100, config.MaxDownloadFiles)
	assert.Equal(t, 100, config.MaxDeleteFiles)
	assert.Equal(t, int64(500*1024*1024), config.MaxUploadSizeBytes)
	assert.Equal(t, 10, config.AsyncThreshold)
	assert.Equal(t, 4, config.WorkerCount)
	assert.Equal(t, 24*time.Hour, config.JobTTL)
}

// TestBatchUpload_WithEncryption tests upload with encryption mode
func TestBatchUpload_WithEncryption(t *testing.T) {
	svc, blobStorage, _, _, _ := setupBatchTest(t)
	ctx := context.Background()

	content := []byte("Encrypted content")

	req := &core.BatchUploadRequest{
		Pubkey:         "testpubkey",
		EncryptionMode: string(core.EncryptionModeServer),
		Files: []core.BatchUploadFile{
			{
				Filename: "encrypted.txt",
				MimeType: "text/plain",
				Size:     int64(len(content)),
				Reader:   bytes.NewReader(content),
			},
		},
	}

	resp, err := svc.Upload(ctx, req)
	require.NoError(t, err)
	require.NotNil(t, resp)

	assert.Equal(t, 1, resp.SuccessCount)

	// Verify blob was saved with encryption mode
	hash := resp.Results[0].Hash
	blob, err := blobStorage.GetFromHash(ctx, hash)
	require.NoError(t, err)
	assert.Equal(t, core.EncryptionModeServer, blob.EncryptionMode)
}

// TestBatchDownload_FileExtensions tests that correct extensions are added
func TestBatchDownload_FileExtensions(t *testing.T) {
	svc, blobStorage, storageBackend, _, _ := setupBatchTest(t)
	ctx := context.Background()

	tests := []struct {
		mimeType      string
		expectedExt   string
	}{
		{"image/jpeg", ".jpg"},
		{"image/png", ".png"},
		{"application/pdf", ".pdf"},
		{"text/plain", ".txt"},
		{"application/json", ".json"},
	}

	for _, tt := range tests {
		content := []byte("test content")
		hash := hashData(content)

		blobStorage.Save(ctx, "testpubkey", hash, "https://example.com/"+hash, int64(len(content)), tt.mimeType, content, time.Now().Unix(), core.EncryptionModeNone)
		storageBackend.Put(ctx, hash, bytes.NewReader(content), int64(len(content)))

		req := &core.BatchDownloadRequest{
			Hashes: []string{hash},
			Format: "zip",
		}

		reader, resp, err := svc.Download(ctx, req)
		require.NoError(t, err)
		defer reader.Close()

		data, _ := io.ReadAll(reader)
		zipReader, _ := zip.NewReader(bytes.NewReader(data), int64(len(data)))

		expectedFilename := hash + tt.expectedExt
		assert.Equal(t, expectedFilename, zipReader.File[0].Name, "MIME type: %s", tt.mimeType)
		assert.Equal(t, expectedFilename, resp.Results[0].Filename)
	}
}

// failReader is a mock reader that fails on Read
type failReader struct {
	shouldFail bool
}

func (f *failReader) Read(p []byte) (n int, err error) {
	if f.shouldFail {
		return 0, errors.New("read error")
	}
	return 0, io.EOF
}
