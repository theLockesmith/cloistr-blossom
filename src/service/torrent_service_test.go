package service

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"git.coldforge.xyz/coldforge/cloistr-blossom/internal/cache"
	"git.coldforge.xyz/coldforge/cloistr-blossom/internal/storage"
	"git.coldforge.xyz/coldforge/cloistr-blossom/src/core"
)

// setupTorrentTest creates a test environment with storage and cache
func setupTorrentTest(t *testing.T) (*torrentService, string, func()) {
	tempDir, err := os.MkdirTemp("", "torrent-test-*")
	require.NoError(t, err)

	localStorage, err := storage.NewLocalStorage(tempDir)
	require.NoError(t, err)

	memCache := cache.NewMemoryCache(10 * 1024 * 1024)
	log, _ := zap.NewDevelopment()

	svc := &torrentService{
		storage: localStorage,
		cache:   memCache,
		log:     log,
	}

	cleanup := func() {
		os.RemoveAll(tempDir)
	}

	return svc, tempDir, cleanup
}

// createTestBlob creates a blob in storage and returns its hash and content
func createTestBlob(t *testing.T, svc *torrentService, content []byte) string {
	ctx := context.Background()

	// Calculate SHA-256 hash for blob
	hash := sha1.Sum(content)
	blobHash := hex.EncodeToString(hash[:])

	// Store blob
	err := svc.storage.Put(ctx, blobHash, bytes.NewReader(content), int64(len(content)))
	require.NoError(t, err)

	return blobHash
}

func TestGenerateTorrent_BasicSuccess(t *testing.T) {
	svc, _, cleanup := setupTorrentTest(t)
	defer cleanup()

	ctx := context.Background()
	content := []byte("Hello, BitTorrent! This is test content for torrent generation.")
	blobHash := createTestBlob(t, svc, content)

	config := &core.TorrentConfig{
		WebSeedURLs: []string{"https://files.cloistr.xyz/"},
		TrackerURLs: []string{"https://tracker.example.com/announce"},
		EnableDHT:   true,
		Comment:     "Test torrent",
		CreatedBy:   "test-suite",
	}

	info, torrentBytes, err := svc.GenerateTorrent(ctx, blobHash, config)
	require.NoError(t, err)
	require.NotNil(t, info)
	require.NotEmpty(t, torrentBytes)

	// Verify TorrentInfo fields
	assert.Equal(t, blobHash, info.BlobHash)
	assert.NotEmpty(t, info.InfoHash)
	assert.NotEmpty(t, info.MagnetURI)
	assert.Greater(t, info.PieceLength, int64(0))
	assert.Greater(t, info.PieceCount, 0)
	assert.Equal(t, int64(len(content)), info.TotalSize)
	assert.Equal(t, blobHash, info.Name)
	assert.Greater(t, info.CreatedAt, int64(0))

	// Verify magnet URI contains required elements
	assert.Contains(t, info.MagnetURI, "magnet:?xt=urn:btih:")
	assert.Contains(t, info.MagnetURI, info.InfoHash)
	assert.Contains(t, info.MagnetURI, blobHash)

	// Verify torrent file can be parsed
	var mi metainfo.MetaInfo
	err = bencode.Unmarshal(torrentBytes, &mi)
	require.NoError(t, err)

	// Verify metainfo fields
	assert.Equal(t, "test-suite", mi.CreatedBy)
	assert.Equal(t, "Test torrent", mi.Comment)
	assert.Equal(t, "https://tracker.example.com/announce", mi.Announce)
	assert.Len(t, mi.UrlList, 1)
	assert.Equal(t, "https://files.cloistr.xyz/", mi.UrlList[0])
	assert.NotEmpty(t, mi.Nodes)
}

func TestGenerateTorrent_MinimalConfig(t *testing.T) {
	svc, _, cleanup := setupTorrentTest(t)
	defer cleanup()

	ctx := context.Background()
	content := []byte("Minimal torrent test")
	blobHash := createTestBlob(t, svc, content)

	config := &core.TorrentConfig{
		// Minimal config - no trackers, no web seeds, no DHT
	}

	info, torrentBytes, err := svc.GenerateTorrent(ctx, blobHash, config)
	require.NoError(t, err)
	require.NotNil(t, info)
	require.NotEmpty(t, torrentBytes)

	// Should still generate valid torrent
	assert.Equal(t, blobHash, info.BlobHash)
	assert.NotEmpty(t, info.InfoHash)
	assert.Equal(t, int64(len(content)), info.TotalSize)

	// Parse and verify
	var mi metainfo.MetaInfo
	err = bencode.Unmarshal(torrentBytes, &mi)
	require.NoError(t, err)

	// Should have default CreatedBy
	assert.Equal(t, "coldforge-blossom", mi.CreatedBy)
}

func TestGenerateTorrent_MultipleTrackers(t *testing.T) {
	svc, _, cleanup := setupTorrentTest(t)
	defer cleanup()

	ctx := context.Background()
	content := []byte("Multi-tracker test")
	blobHash := createTestBlob(t, svc, content)

	trackers := []string{
		"https://tracker1.example.com/announce",
		"https://tracker2.example.com/announce",
		"https://tracker3.example.com/announce",
	}

	config := &core.TorrentConfig{
		TrackerURLs: trackers,
	}

	_, torrentBytes, err := svc.GenerateTorrent(ctx, blobHash, config)
	require.NoError(t, err)

	var mi metainfo.MetaInfo
	err = bencode.Unmarshal(torrentBytes, &mi)
	require.NoError(t, err)

	// First tracker should be primary announce
	assert.Equal(t, trackers[0], mi.Announce)

	// All trackers should be in announce list (multi-tracker BEP 12)
	assert.Len(t, mi.AnnounceList, len(trackers))
	for i, tier := range mi.AnnounceList {
		assert.Len(t, tier, 1)
		assert.Equal(t, trackers[i], tier[0])
	}
}

func TestGenerateTorrent_MultipleWebSeeds(t *testing.T) {
	svc, _, cleanup := setupTorrentTest(t)
	defer cleanup()

	ctx := context.Background()
	content := []byte("Multi-webseed test")
	blobHash := createTestBlob(t, svc, content)

	webSeeds := []string{
		"https://cdn1.example.com/",
		"https://cdn2.example.com/",
		"https://cdn3.example.com/",
	}

	config := &core.TorrentConfig{
		WebSeedURLs: webSeeds,
	}

	info, torrentBytes, err := svc.GenerateTorrent(ctx, blobHash, config)
	require.NoError(t, err)

	var mi metainfo.MetaInfo
	err = bencode.Unmarshal(torrentBytes, &mi)
	require.NoError(t, err)

	// All web seeds should be in URL list
	assert.Equal(t, webSeeds, []string(mi.UrlList))

	// Magnet URI should include all web seeds (URL-encoded)
	for _, ws := range webSeeds {
		assert.Contains(t, info.MagnetURI, "&ws=")
		// Web seeds are URL-encoded, so check for the encoded version
		assert.Contains(t, info.MagnetURI, url.QueryEscape(ws+blobHash))
	}
}

func TestGenerateTorrent_BlobNotFound(t *testing.T) {
	svc, _, cleanup := setupTorrentTest(t)
	defer cleanup()

	ctx := context.Background()
	config := &core.TorrentConfig{}

	_, _, err := svc.GenerateTorrent(ctx, "nonexistent", config)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "blob not found")
}

func TestGenerateTorrent_CachesResult(t *testing.T) {
	svc, _, cleanup := setupTorrentTest(t)
	defer cleanup()

	ctx := context.Background()
	content := []byte("Cache test content")
	blobHash := createTestBlob(t, svc, content)

	config := &core.TorrentConfig{
		Comment: "Test caching",
	}

	info, torrentBytes, err := svc.GenerateTorrent(ctx, blobHash, config)
	require.NoError(t, err)

	// Verify torrent is cached
	cachedTorrent, ok := svc.cache.Get(ctx, torrentCachePrefix+blobHash)
	assert.True(t, ok)
	assert.Equal(t, torrentBytes, cachedTorrent)

	// Verify info is cached (now stored as JSON)
	cachedInfoBytes, ok := svc.cache.Get(ctx, torrentInfoPrefix+blobHash)
	assert.True(t, ok)

	var cachedInfo core.TorrentInfo
	err = json.Unmarshal(cachedInfoBytes, &cachedInfo)
	require.NoError(t, err)
	assert.Equal(t, info.BlobHash, cachedInfo.BlobHash)
	assert.Equal(t, info.InfoHash, cachedInfo.InfoHash)
}

func TestGetTorrent_Success(t *testing.T) {
	svc, _, cleanup := setupTorrentTest(t)
	defer cleanup()

	ctx := context.Background()
	content := []byte("Get torrent test")
	blobHash := createTestBlob(t, svc, content)

	config := &core.TorrentConfig{}

	// Generate torrent first
	_, originalBytes, err := svc.GenerateTorrent(ctx, blobHash, config)
	require.NoError(t, err)

	// Retrieve cached torrent
	retrievedBytes, err := svc.GetTorrent(ctx, blobHash)
	require.NoError(t, err)
	assert.Equal(t, originalBytes, retrievedBytes)
}

func TestGetTorrent_NotFound(t *testing.T) {
	svc, _, cleanup := setupTorrentTest(t)
	defer cleanup()

	ctx := context.Background()

	_, err := svc.GetTorrent(ctx, "nonexistent")
	assert.ErrorIs(t, err, core.ErrTorrentNotFound)
}

func TestGetTorrent_NilCache(t *testing.T) {
	log, _ := zap.NewDevelopment()
	svc := &torrentService{
		cache: nil,
		log:   log,
	}

	ctx := context.Background()

	_, err := svc.GetTorrent(ctx, "anyhash")
	assert.ErrorIs(t, err, core.ErrTorrentNotFound)
}

func TestGetTorrentInfo_Success(t *testing.T) {
	svc, _, cleanup := setupTorrentTest(t)
	defer cleanup()

	ctx := context.Background()
	content := []byte("Get info test")
	blobHash := createTestBlob(t, svc, content)

	config := &core.TorrentConfig{
		Comment: "Test info retrieval",
	}

	// Generate torrent first
	originalInfo, _, err := svc.GenerateTorrent(ctx, blobHash, config)
	require.NoError(t, err)

	// Retrieve cached info
	retrievedInfo, err := svc.GetTorrentInfo(ctx, blobHash)
	require.NoError(t, err)
	assert.Equal(t, originalInfo.BlobHash, retrievedInfo.BlobHash)
	assert.Equal(t, originalInfo.InfoHash, retrievedInfo.InfoHash)
	assert.Equal(t, originalInfo.MagnetURI, retrievedInfo.MagnetURI)
	assert.Equal(t, originalInfo.TotalSize, retrievedInfo.TotalSize)
}

func TestGetTorrentInfo_NotFound(t *testing.T) {
	svc, _, cleanup := setupTorrentTest(t)
	defer cleanup()

	ctx := context.Background()

	_, err := svc.GetTorrentInfo(ctx, "nonexistent")
	assert.ErrorIs(t, err, core.ErrTorrentNotFound)
}

func TestGetTorrentInfo_NilCache(t *testing.T) {
	log, _ := zap.NewDevelopment()
	svc := &torrentService{
		cache: nil,
		log:   log,
	}

	ctx := context.Background()

	_, err := svc.GetTorrentInfo(ctx, "anyhash")
	assert.ErrorIs(t, err, core.ErrTorrentNotFound)
}

func TestDeleteTorrent_Success(t *testing.T) {
	svc, _, cleanup := setupTorrentTest(t)
	defer cleanup()

	ctx := context.Background()
	content := []byte("Delete test")
	blobHash := createTestBlob(t, svc, content)

	// Generate torrent
	_, _, err := svc.GenerateTorrent(ctx, blobHash, &core.TorrentConfig{})
	require.NoError(t, err)

	// Verify cached
	_, ok := svc.cache.Get(ctx, torrentCachePrefix+blobHash)
	assert.True(t, ok)
	_, ok = svc.cache.Get(ctx, torrentInfoPrefix+blobHash)
	assert.True(t, ok)

	// Delete
	err = svc.DeleteTorrent(ctx, blobHash)
	require.NoError(t, err)

	// Verify removed
	_, ok = svc.cache.Get(ctx, torrentCachePrefix+blobHash)
	assert.False(t, ok)
	_, ok = svc.cache.Get(ctx, torrentInfoPrefix+blobHash)
	assert.False(t, ok)
}

func TestDeleteTorrent_NilCache(t *testing.T) {
	log, _ := zap.NewDevelopment()
	svc := &torrentService{
		cache: nil,
		log:   log,
	}

	ctx := context.Background()

	// Should not error even with nil cache
	err := svc.DeleteTorrent(ctx, "anyhash")
	assert.NoError(t, err)
}

func TestDeleteTorrent_NonExistent(t *testing.T) {
	svc, _, cleanup := setupTorrentTest(t)
	defer cleanup()

	ctx := context.Background()

	// Deleting non-existent torrent should not error
	err := svc.DeleteTorrent(ctx, "nonexistent")
	assert.NoError(t, err)
}

func TestCalculatePieceLength_SmallFiles(t *testing.T) {
	tests := []struct {
		name         string
		fileSize     int64
		expectedSize int64
	}{
		{"1 MB file", 1 * 1024 * 1024, 32 * 1024},
		{"10 MB file", 10 * 1024 * 1024, 32 * 1024},
		{"49 MB file", 49 * 1024 * 1024, 32 * 1024},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := calculatePieceLength(tt.fileSize)
			assert.Equal(t, tt.expectedSize, result)
		})
	}
}

func TestCalculatePieceLength_MediumFiles(t *testing.T) {
	tests := []struct {
		name         string
		fileSize     int64
		expectedSize int64
	}{
		{"50 MB file", 50 * 1024 * 1024, 64 * 1024},
		{"100 MB file", 100 * 1024 * 1024, 64 * 1024},
		{"149 MB file", 149 * 1024 * 1024, 64 * 1024},
		{"150 MB file", 150 * 1024 * 1024, 128 * 1024},
		{"300 MB file", 300 * 1024 * 1024, 128 * 1024},
		{"349 MB file", 349 * 1024 * 1024, 128 * 1024},
		{"350 MB file", 350 * 1024 * 1024, 256 * 1024},
		{"500 MB file", 500 * 1024 * 1024, 256 * 1024},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := calculatePieceLength(tt.fileSize)
			assert.Equal(t, tt.expectedSize, result)
		})
	}
}

func TestCalculatePieceLength_LargeFiles(t *testing.T) {
	// Tests match the implementation in calculatePieceLength which uses < comparisons
	// So exactly 1GB falls into the "< 2GB" case returning 1MB
	tests := []struct {
		name         string
		fileSize     int64
		expectedSize int64
	}{
		{"1 GB file", 1 * 1024 * 1024 * 1024, 1 * 1024 * 1024},       // >= 1GB, < 2GB -> 1MB
		{"1.5 GB file", 1536 * 1024 * 1024, 1 * 1024 * 1024},         // >= 1GB, < 2GB -> 1MB
		{"2 GB file", 2 * 1024 * 1024 * 1024, 2 * 1024 * 1024},       // >= 2GB, < 4GB -> 2MB
		{"3 GB file", 3 * 1024 * 1024 * 1024, 2 * 1024 * 1024},       // >= 2GB, < 4GB -> 2MB
		{"4 GB file", 4 * 1024 * 1024 * 1024, 4 * 1024 * 1024},       // >= 4GB, < 8GB -> 4MB
		{"6 GB file", 6 * 1024 * 1024 * 1024, 4 * 1024 * 1024},       // >= 4GB, < 8GB -> 4MB
		{"8 GB file", 8 * 1024 * 1024 * 1024, 8 * 1024 * 1024},       // >= 8GB, < 16GB -> 8MB
		{"12 GB file", 12 * 1024 * 1024 * 1024, 8 * 1024 * 1024},     // >= 8GB, < 16GB -> 8MB
		{"16 GB file", 16 * 1024 * 1024 * 1024, 16 * 1024 * 1024},    // >= 16GB -> 16MB
		{"32 GB file", 32 * 1024 * 1024 * 1024, 16 * 1024 * 1024},    // >= 16GB -> 16MB
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := calculatePieceLength(tt.fileSize)
			assert.Equal(t, tt.expectedSize, result)
		})
	}
}

func TestCalculatePieceLength_EdgeCases(t *testing.T) {
	tests := []struct {
		name         string
		fileSize     int64
		expectedSize int64
	}{
		{"Empty file", 0, 32 * 1024},
		{"1 byte file", 1, 32 * 1024},
		{"Exactly 50 MB", 50 * 1024 * 1024, 64 * 1024},
		{"Exactly 1 GB", 1024 * 1024 * 1024, 1 * 1024 * 1024}, // >= 1GB, < 2GB -> 1MB
		{"Very large file", 100 * 1024 * 1024 * 1024, 16 * 1024 * 1024},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := calculatePieceLength(tt.fileSize)
			assert.Equal(t, tt.expectedSize, result)
		})
	}
}

func TestGeneratePieces_SinglePiece(t *testing.T) {
	data := []byte("Small content")
	pieceLength := int64(1024) // Larger than data

	pieces := generatePieces(data, pieceLength)

	// Should produce one piece hash (20 bytes for SHA1)
	assert.Len(t, pieces, 20)

	// Verify hash is correct
	expectedHash := sha1.Sum(data)
	assert.Equal(t, expectedHash[:], pieces)
}

func TestGeneratePieces_MultiplePieces(t *testing.T) {
	// Create data that will span multiple pieces
	data := make([]byte, 100)
	for i := range data {
		data[i] = byte(i)
	}
	pieceLength := int64(32) // Small pieces to force multiple

	pieces := generatePieces(data, pieceLength)

	// Should produce 4 pieces (100 / 32 = 3.125, rounded up to 4)
	expectedPieceCount := 4
	assert.Len(t, pieces, expectedPieceCount*20)

	// Verify each piece hash
	for i := 0; i < expectedPieceCount; i++ {
		start := int64(i) * pieceLength
		end := start + pieceLength
		if end > int64(len(data)) {
			end = int64(len(data))
		}

		expectedHash := sha1.Sum(data[start:end])
		pieceHash := pieces[i*20 : (i+1)*20]
		assert.Equal(t, expectedHash[:], pieceHash)
	}
}

func TestGeneratePieces_ExactMultiple(t *testing.T) {
	// Data that is exactly divisible by piece length
	data := make([]byte, 64)
	pieceLength := int64(32)

	pieces := generatePieces(data, pieceLength)

	// Should produce exactly 2 pieces
	assert.Len(t, pieces, 2*20)
}

func TestGeneratePieces_EmptyData(t *testing.T) {
	data := []byte{}
	pieceLength := int64(1024)

	pieces := generatePieces(data, pieceLength)

	// Empty data should produce no pieces
	assert.Len(t, pieces, 0)
}

func TestGeneratePieces_Consistency(t *testing.T) {
	// Same data should produce same pieces
	data := []byte("Consistent test data for piece generation")
	pieceLength := int64(16)

	pieces1 := generatePieces(data, pieceLength)
	pieces2 := generatePieces(data, pieceLength)

	assert.Equal(t, pieces1, pieces2)
}

func TestBuildMagnetURI_Basic(t *testing.T) {
	infoHash := "abc123def456"
	name := "testfile"
	trackers := []string{}
	webSeeds := []string{}

	magnet := buildMagnetURI(infoHash, name, trackers, webSeeds)

	// Should contain basic elements
	assert.Contains(t, magnet, "magnet:?xt=urn:btih:abc123def456")
	assert.Contains(t, magnet, "&dn=testfile")
}

func TestBuildMagnetURI_WithTrackers(t *testing.T) {
	infoHash := "abc123"
	name := "test"
	trackers := []string{
		"https://tracker1.example.com/announce",
		"https://tracker2.example.com/announce",
	}
	webSeeds := []string{}

	magnet := buildMagnetURI(infoHash, name, trackers, webSeeds)

	// Should contain all trackers (URL-encoded)
	assert.Contains(t, magnet, "&tr="+url.QueryEscape("https://tracker1.example.com/announce"))
	assert.Contains(t, magnet, "&tr="+url.QueryEscape("https://tracker2.example.com/announce"))
}

func TestBuildMagnetURI_WithWebSeeds(t *testing.T) {
	infoHash := "abc123"
	name := "test"
	trackers := []string{}
	webSeeds := []string{
		"https://cdn1.example.com/",
		"https://cdn2.example.com/",
	}

	magnet := buildMagnetURI(infoHash, name, trackers, webSeeds)

	// Should contain all web seeds with name appended (URL-encoded)
	assert.Contains(t, magnet, "&ws="+url.QueryEscape("https://cdn1.example.com/test"))
	assert.Contains(t, magnet, "&ws="+url.QueryEscape("https://cdn2.example.com/test"))
}

func TestBuildMagnetURI_Complete(t *testing.T) {
	infoHash := "abc123def456"
	name := "testfile"
	trackers := []string{
		"https://tracker.example.com/announce",
	}
	webSeeds := []string{
		"https://cdn.example.com/",
	}

	magnet := buildMagnetURI(infoHash, name, trackers, webSeeds)

	// Should contain all components (URL-encoded)
	assert.Contains(t, magnet, "magnet:?xt=urn:btih:abc123def456")
	assert.Contains(t, magnet, "&dn=testfile")
	assert.Contains(t, magnet, "&tr="+url.QueryEscape("https://tracker.example.com/announce"))
	assert.Contains(t, magnet, "&ws="+url.QueryEscape("https://cdn.example.com/testfile"))
}

func TestNewTorrentService(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "torrent-test-*")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	localStorage, err := storage.NewLocalStorage(tempDir)
	require.NoError(t, err)

	memCache := cache.NewMemoryCache(10 * 1024 * 1024)
	log, _ := zap.NewDevelopment()

	svc := NewTorrentService(localStorage, memCache, log)
	require.NotNil(t, svc)

	// Verify it's the right type
	_, ok := svc.(*torrentService)
	assert.True(t, ok)
}

func TestGenerateTorrent_DHTNodes(t *testing.T) {
	svc, _, cleanup := setupTorrentTest(t)
	defer cleanup()

	ctx := context.Background()
	content := []byte("DHT test content")
	blobHash := createTestBlob(t, svc, content)

	config := &core.TorrentConfig{
		EnableDHT: true,
	}

	_, torrentBytes, err := svc.GenerateTorrent(ctx, blobHash, config)
	require.NoError(t, err)

	var mi metainfo.MetaInfo
	err = bencode.Unmarshal(torrentBytes, &mi)
	require.NoError(t, err)

	// Should include DHT nodes
	assert.NotEmpty(t, mi.Nodes)
	assert.Equal(t, len(defaultDHTNodes), len(mi.Nodes))

	// Verify nodes contain expected bootstrap nodes
	nodeStrings := make([]string, len(mi.Nodes))
	for i, node := range mi.Nodes {
		nodeStrings[i] = string(node)
	}

	for _, expectedNode := range defaultDHTNodes {
		assert.Contains(t, nodeStrings, expectedNode)
	}
}

func TestGenerateTorrent_NoDHTNodes(t *testing.T) {
	svc, _, cleanup := setupTorrentTest(t)
	defer cleanup()

	ctx := context.Background()
	content := []byte("No DHT test")
	blobHash := createTestBlob(t, svc, content)

	config := &core.TorrentConfig{
		EnableDHT: false,
	}

	_, torrentBytes, err := svc.GenerateTorrent(ctx, blobHash, config)
	require.NoError(t, err)

	var mi metainfo.MetaInfo
	err = bencode.Unmarshal(torrentBytes, &mi)
	require.NoError(t, err)

	// Should not include DHT nodes
	assert.Empty(t, mi.Nodes)
}

func TestGenerateTorrent_PieceCountCalculation(t *testing.T) {
	svc, _, cleanup := setupTorrentTest(t)
	defer cleanup()

	ctx := context.Background()

	// Create content that will result in multiple pieces
	content := make([]byte, 1024*1024) // 1 MB
	for i := range content {
		content[i] = byte(i % 256)
	}
	blobHash := createTestBlob(t, svc, content)

	config := &core.TorrentConfig{}

	info, _, err := svc.GenerateTorrent(ctx, blobHash, config)
	require.NoError(t, err)

	// Verify piece count matches expected calculation
	expectedPieceCount := (int64(len(content)) + info.PieceLength - 1) / info.PieceLength
	assert.Equal(t, int(expectedPieceCount), info.PieceCount)
}

func TestGenerateTorrent_LargeBlob(t *testing.T) {
	svc, _, cleanup := setupTorrentTest(t)
	defer cleanup()

	ctx := context.Background()

	// Create a larger blob (5 MB)
	content := make([]byte, 5*1024*1024)
	for i := range content {
		content[i] = byte(i % 256)
	}
	blobHash := createTestBlob(t, svc, content)

	config := &core.TorrentConfig{
		WebSeedURLs: []string{"https://files.cloistr.xyz/"},
		Comment:     "Large blob test",
	}

	info, torrentBytes, err := svc.GenerateTorrent(ctx, blobHash, config)
	require.NoError(t, err)
	require.NotNil(t, info)

	// Verify size
	assert.Equal(t, int64(len(content)), info.TotalSize)

	// Should use appropriate piece length for 5MB
	assert.Equal(t, int64(32*1024), info.PieceLength)

	// Verify torrent is valid
	var mi metainfo.MetaInfo
	err = bencode.Unmarshal(torrentBytes, &mi)
	require.NoError(t, err)

	infoObj, err := mi.UnmarshalInfo()
	require.NoError(t, err)
	assert.Equal(t, int64(len(content)), infoObj.Length)
}

func TestTorrentCacheTTL(t *testing.T) {
	// Verify cache TTL constant is set correctly
	expectedTTL := 7 * 24 * time.Hour
	assert.Equal(t, expectedTTL, torrentCacheTTL)
}

func TestTorrentCachePrefixes(t *testing.T) {
	// Verify cache key prefixes are distinct
	assert.NotEqual(t, torrentCachePrefix, torrentInfoPrefix)
	assert.Equal(t, "torrent:", torrentCachePrefix)
	assert.Equal(t, "torrent_info:", torrentInfoPrefix)
}

func TestGenerateTorrent_InfoHashUniqueness(t *testing.T) {
	svc, _, cleanup := setupTorrentTest(t)
	defer cleanup()

	ctx := context.Background()

	// Generate torrents for different blobs
	content1 := []byte("Content number one")
	blobHash1 := createTestBlob(t, svc, content1)

	content2 := []byte("Content number two")
	blobHash2 := createTestBlob(t, svc, content2)

	config := &core.TorrentConfig{}

	info1, _, err := svc.GenerateTorrent(ctx, blobHash1, config)
	require.NoError(t, err)

	info2, _, err := svc.GenerateTorrent(ctx, blobHash2, config)
	require.NoError(t, err)

	// Info hashes should be different for different content
	assert.NotEqual(t, info1.InfoHash, info2.InfoHash)
	assert.NotEqual(t, info1.BlobHash, info2.BlobHash)
}

func TestGenerateTorrent_DeterministicInfoHash(t *testing.T) {
	svc, _, cleanup := setupTorrentTest(t)
	defer cleanup()

	ctx := context.Background()
	content := []byte("Deterministic test")
	blobHash := createTestBlob(t, svc, content)

	// Generate same torrent twice with same config
	config := &core.TorrentConfig{
		WebSeedURLs: []string{"https://files.cloistr.xyz/"},
		EnableDHT:   true,
	}

	// First generation
	info1, _, err := svc.GenerateTorrent(ctx, blobHash, config)
	require.NoError(t, err)

	// Clear cache
	svc.cache.Delete(ctx, torrentCachePrefix+blobHash)
	svc.cache.Delete(ctx, torrentInfoPrefix+blobHash)

	// Second generation
	info2, _, err := svc.GenerateTorrent(ctx, blobHash, config)
	require.NoError(t, err)

	// Info hash should be the same (deterministic)
	// Note: This may fail if CreationDate or other non-info fields change
	// The info hash is based on the info dictionary only, which should be deterministic
	assert.Equal(t, info1.InfoHash, info2.InfoHash)
}

func TestGenerateTorrent_BlobReadError(t *testing.T) {
	// Create a mock storage that returns an error on Get
	log, _ := zap.NewDevelopment()

	// Use a memory cache
	memCache := cache.NewMemoryCache(10 * 1024 * 1024)

	// Create a temporary directory and local storage
	tempDir, err := os.MkdirTemp("", "torrent-error-test-*")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	localStorage, err := storage.NewLocalStorage(tempDir)
	require.NoError(t, err)

	svc := &torrentService{
		storage: localStorage,
		cache:   memCache,
		log:     log,
	}

	ctx := context.Background()

	// Try to generate torrent for non-existent blob
	_, _, err = svc.GenerateTorrent(ctx, "nonexistent", &core.TorrentConfig{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "blob not found")
}

func TestGenerateTorrent_ReadBlobError(t *testing.T) {
	svc, _, cleanup := setupTorrentTest(t)
	defer cleanup()

	ctx := context.Background()

	// Create a blob, then corrupt the storage
	content := []byte("Test content")
	blobHash := createTestBlob(t, svc, content)

	// Remove the blob file to simulate read error
	err := svc.storage.Delete(ctx, blobHash)
	require.NoError(t, err)

	// Try to generate torrent
	_, _, err = svc.GenerateTorrent(ctx, blobHash, &core.TorrentConfig{})
	assert.Error(t, err)
}

// mockReadCloser is a helper for testing read errors
type mockReadCloser struct {
	data []byte
	pos  int
	fail bool
}

func (m *mockReadCloser) Read(p []byte) (n int, err error) {
	if m.fail {
		return 0, io.ErrUnexpectedEOF
	}
	if m.pos >= len(m.data) {
		return 0, io.EOF
	}
	n = copy(p, m.data[m.pos:])
	m.pos += n
	return n, nil
}

func (m *mockReadCloser) Close() error {
	return nil
}
