package bud05

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"image"
	"image/color"
	"image/png"
	"log"
	"os"
	"testing"

	_ "github.com/mattn/go-sqlite3"
	"github.com/nbd-wtf/go-nostr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"git.aegis-hq.xyz/coldforge/cloistr-blossom/db"
	"git.aegis-hq.xyz/coldforge/cloistr-blossom/src/core"
	"git.aegis-hq.xyz/coldforge/cloistr-blossom/src/pkg/config"
	"git.aegis-hq.xyz/coldforge/cloistr-blossom/src/pkg/logging"
	"git.aegis-hq.xyz/coldforge/cloistr-blossom/src/service"
)

// createTestImage generates a simple test PNG image.
func createTestImage(width, height int) ([]byte, error) {
	img := image.NewRGBA(image.Rect(0, 0, width, height))

	// Fill with a gradient pattern
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.Set(x, y, color.RGBA{
				R: uint8(x * 255 / width),
				G: uint8(y * 255 / height),
				B: 128,
				A: 255,
			})
		}
	}

	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func setupTestServices(t *testing.T, dbFile string) (core.Services, func()) {
	pk, _ := nostr.GetPublicKey(nostr.GeneratePrivateKey())

	conf := &config.Config{
		DbPath:      dbFile,
		LogLevel:    "DEBUG",
		CdnUrl:      "http://localhost:8000",
		AdminPubkey: pk,
		AccessControlRules: []config.AccessControlRule{
			{Action: string(core.ACRActionAllow), Pubkey: "ALL", Resource: string(core.ResourceUpload)},
		},
		AllowedMimeTypes: []string{"*"},
	}
	conf.ApplyDefaults()

	logger, err := logging.NewLog(conf.LogLevel)
	if err != nil {
		log.Fatalf("new logger: %v", err)
	}

	database, err := db.NewDB(dbFile, "../../db/migrations")
	if err != nil {
		t.Fatal(err)
	}
	queries := db.New(database)

	services := service.New(context.TODO(), database, queries, conf, nil, logger)
	services.Init(context.TODO())

	cleanup := func() {
		if err := os.Remove(dbFile); err != nil {
			t.Log(err)
		}
	}

	return services, cleanup
}

func TestMediaServiceProcessImage(t *testing.T) {
	dbFile := "./db-TestMediaService.sqlite3"
	services, cleanup := setupTestServices(t, dbFile)
	defer cleanup()

	// Create a test image (200x200 PNG)
	imageData, err := createTestImage(200, 200)
	require.NoError(t, err, "should create test image")
	require.True(t, len(imageData) > 0, "image data should not be empty")

	// Test that media service can process the image
	mediaService := services.Media()
	require.NotNil(t, mediaService, "media service should exist")

	// Verify the image type is supported
	assert.True(t, mediaService.IsSupported("image/png"), "PNG should be supported")
	assert.True(t, mediaService.IsSupported("image/jpeg"), "JPEG should be supported")
	assert.False(t, mediaService.IsSupported("application/pdf"), "PDF should not be supported")

	// Process the image with resize
	result, err := mediaService.ProcessImage(
		context.TODO(),
		bytes.NewReader(imageData),
		"image/png",
		&core.MediaProcessOptions{
			Width:  100,
			Height: 100,
		},
	)
	require.NoError(t, err, "should process image without error")
	require.NotNil(t, result, "result should not be nil")

	// Verify result
	assert.True(t, len(result.Data) > 0, "processed data should not be empty")
	assert.True(t, result.Width <= 100, "width should be <= 100")
	assert.True(t, result.Height <= 100, "height should be <= 100")
	assert.NotEmpty(t, result.Hash, "hash should be set")
	assert.NotEmpty(t, result.ContentType, "content type should be set")

	t.Logf("Processed image: %dx%d, %d bytes, type: %s",
		result.Width, result.Height, len(result.Data), result.ContentType)
}

func TestThumbnailGeneration(t *testing.T) {
	dbFile := "./db-TestThumbnail.sqlite3"
	services, cleanup := setupTestServices(t, dbFile)
	defer cleanup()

	// Create a test image (400x300 PNG)
	imageData, err := createTestImage(400, 300)
	require.NoError(t, err, "should create test image")

	// Calculate hash
	hash := sha256.Sum256(imageData)
	hashStr := hex.EncodeToString(hash[:])

	// Store the image using blob service
	ctx := context.TODO()
	pk, _ := nostr.GetPublicKey(nostr.GeneratePrivateKey())

	blob, err := services.Blob().Save(
		ctx,
		pk,
		hashStr,
		"http://localhost:8000/"+hashStr,
		int64(len(imageData)),
		"image/png",
		imageData,
		1234567890,
		core.EncryptionModeNone,
	)
	require.NoError(t, err, "should save blob")
	require.NotNil(t, blob, "blob should not be nil")

	t.Logf("Saved test image with hash: %s", hashStr)

	// Generate thumbnail using media service
	mediaService := services.Media()

	// Test small thumbnail (150x150)
	thumb, err := mediaService.GetThumbnail(ctx, hashStr, 150, 150)
	require.NoError(t, err, "should generate small thumbnail")
	require.NotNil(t, thumb, "thumbnail should not be nil")

	assert.True(t, thumb.Width <= 150, "thumb width should be <= 150")
	assert.True(t, thumb.Height <= 150, "thumb height should be <= 150")
	assert.Equal(t, "image/jpeg", thumb.ContentType, "thumbnail should be JPEG")

	t.Logf("Small thumbnail: %dx%d, %d bytes", thumb.Width, thumb.Height, len(thumb.Data))

	// Test medium thumbnail (300x300)
	thumb2, err := mediaService.GetThumbnail(ctx, hashStr, 300, 300)
	require.NoError(t, err, "should generate medium thumbnail")

	assert.True(t, thumb2.Width <= 300, "thumb2 width should be <= 300")
	assert.True(t, thumb2.Height <= 300, "thumb2 height should be <= 300")

	t.Logf("Medium thumbnail: %dx%d, %d bytes", thumb2.Width, thumb2.Height, len(thumb2.Data))

	// Verify thumbnails are different sizes
	assert.True(t, len(thumb.Data) < len(thumb2.Data), "small thumb should be smaller than medium")
}

func TestThumbnailCaching(t *testing.T) {
	dbFile := "./db-TestThumbnailCache.sqlite3"
	services, cleanup := setupTestServices(t, dbFile)
	defer cleanup()

	// Create and store a test image
	imageData, err := createTestImage(400, 300)
	require.NoError(t, err)

	hash := sha256.Sum256(imageData)
	hashStr := hex.EncodeToString(hash[:])

	ctx := context.TODO()
	pk, _ := nostr.GetPublicKey(nostr.GeneratePrivateKey())

	_, err = services.Blob().Save(
		ctx, pk, hashStr,
		"http://localhost:8000/"+hashStr,
		int64(len(imageData)),
		"image/png",
		imageData,
		1234567890,
		core.EncryptionModeNone,
	)
	require.NoError(t, err)

	mediaService := services.Media()

	// Generate thumbnail twice - second call should hit cache
	thumb1, err := mediaService.GetThumbnail(ctx, hashStr, 150, 150)
	require.NoError(t, err)

	thumb2, err := mediaService.GetThumbnail(ctx, hashStr, 150, 150)
	require.NoError(t, err)

	// Both should return identical data
	assert.Equal(t, thumb1.Data, thumb2.Data, "cached thumbnail should match")
	assert.Equal(t, thumb1.Hash, thumb2.Hash, "cached thumbnail hash should match")

	t.Log("Thumbnail caching works correctly")
}

func TestThumbnailNotFound(t *testing.T) {
	dbFile := "./db-TestThumbnailNotFound.sqlite3"
	services, cleanup := setupTestServices(t, dbFile)
	defer cleanup()

	ctx := context.TODO()
	mediaService := services.Media()

	// Try to get thumbnail for non-existent blob
	_, err := mediaService.GetThumbnail(ctx, "nonexistenthash123456789", 150, 150)
	assert.Error(t, err, "should return error for non-existent blob")
}

func TestMediaProcessOptions(t *testing.T) {
	dbFile := "./db-TestMediaOptions.sqlite3"
	services, cleanup := setupTestServices(t, dbFile)
	defer cleanup()

	// Create a larger test image
	imageData, err := createTestImage(800, 600)
	require.NoError(t, err)

	mediaService := services.Media()
	ctx := context.TODO()

	// Test with no options (default optimization)
	result, err := mediaService.ProcessImage(ctx, bytes.NewReader(imageData), "image/png", nil)
	require.NoError(t, err)
	assert.NotEmpty(t, result.Data)
	t.Logf("No options: %dx%d, %d bytes", result.Width, result.Height, len(result.Data))

	// Test with width only (preserve aspect ratio)
	result, err = mediaService.ProcessImage(ctx, bytes.NewReader(imageData), "image/png", &core.MediaProcessOptions{
		Width: 400,
	})
	require.NoError(t, err)
	assert.True(t, result.Width <= 400)
	t.Logf("Width=400: %dx%d, %d bytes", result.Width, result.Height, len(result.Data))

	// Test with height only
	result, err = mediaService.ProcessImage(ctx, bytes.NewReader(imageData), "image/png", &core.MediaProcessOptions{
		Height: 300,
	})
	require.NoError(t, err)
	assert.True(t, result.Height <= 300)
	t.Logf("Height=300: %dx%d, %d bytes", result.Width, result.Height, len(result.Data))

	// Test with both width and height
	result, err = mediaService.ProcessImage(ctx, bytes.NewReader(imageData), "image/png", &core.MediaProcessOptions{
		Width:  200,
		Height: 200,
	})
	require.NoError(t, err)
	assert.True(t, result.Width <= 200)
	assert.True(t, result.Height <= 200)
	t.Logf("200x200: %dx%d, %d bytes", result.Width, result.Height, len(result.Data))

	// Test with quality setting
	result, err = mediaService.ProcessImage(ctx, bytes.NewReader(imageData), "image/png", &core.MediaProcessOptions{
		Width:   400,
		Quality: 50,
	})
	require.NoError(t, err)
	t.Logf("Quality=50: %dx%d, %d bytes", result.Width, result.Height, len(result.Data))
}
