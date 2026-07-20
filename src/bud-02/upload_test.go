package bud02

import (
	"context"
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
	"git.aegis-hq.xyz/coldforge/cloistr-blossom/src/pkg/hashing"
	"git.aegis-hq.xyz/coldforge/cloistr-blossom/src/pkg/logging"
	"git.aegis-hq.xyz/coldforge/cloistr-blossom/src/service"
)

// newTestServices creates a minimal services instance backed by a SQLite DB.
// The caller is responsible for removing dbFile on cleanup.
func newTestServices(t *testing.T, dbFile string, conf *config.Config) core.Services {
	t.Helper()
	conf.ApplyDefaults()

	logger, err := logging.NewLog("ERROR") // quiet during tests
	require.NoError(t, err)

	database, err := db.NewDB(dbFile, "../../db/migrations")
	require.NoError(t, err)

	queries := db.New(database)
	svc := service.New(context.Background(), database, queries, conf, nil, logger)
	require.NoError(t, svc.Init(context.Background()))
	return svc
}

// jpegMagic returns the minimum bytes that mimetype.Detect recognises as image/jpeg.
// JPEG starts with SOI marker FF D8 FF.
var jpegMagic = []byte{0xff, 0xd8, 0xff, 0xe0, 0x00, 0x10}

// pngMagic returns PNG magic bytes.
var pngMagic = []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a}

// textBytes returns plain text bytes that mimetype.Detect will see as text/plain.
var textBytes = []byte("hello world, just plain text")

func TestUpload(t *testing.T) {
	dbFile := "./db-TestUpload.sqlite3"
	t.Cleanup(func() { os.Remove(dbFile) })

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

	svc := newTestServices(t, dbFile, conf)

	blobBytes := []byte{}
	authHash, _ := hashing.Hash(blobBytes)

	_, err := UploadBlob(context.Background(), svc, conf.CdnUrl, authHash, pk, blobBytes, core.EncryptionModeNone)
	assert.NoError(t, err)
}

func TestUnauthUpload(t *testing.T) {
	dbFile := "./db-TestUnauthUpload.sqlite3"
	t.Cleanup(func() { os.Remove(dbFile) })

	pk, _ := nostr.GetPublicKey(nostr.GeneratePrivateKey())

	conf := &config.Config{
		DbPath:      dbFile,
		LogLevel:    "DEBUG",
		CdnUrl:      "http://localhost:8000",
		AdminPubkey: pk,
	}

	conf.ApplyDefaults()
	logger, err := logging.NewLog(conf.LogLevel)
	if err != nil {
		log.Fatalf("new logger: %v", err)
	}

	database, err := db.NewDB(dbFile, "../../db/migrations")
	require.NoError(t, err)
	queries := db.New(database)

	svc := service.New(context.Background(), database, queries, conf, nil, logger)

	blobBytes := []byte{}
	authHash, _ := hashing.Hash(blobBytes)

	_, err = UploadBlob(context.Background(), svc, conf.CdnUrl, authHash, pk, blobBytes, core.EncryptionModeNone)
	assert.Error(t, err, "expected unauthorized error")
}

// ----- Tier-aware upload limits tests -----

// baseConf returns a config with ACR=allow-all, allowedMimeTypes=*, upload_limits disabled.
// MaxUploadSizeBytes is set to 10 MB so the legacy size check doesn't interfere with tests
// that exercise the upload_limits path.
func baseConf(dbFile, pk string) *config.Config {
	return &config.Config{
		DbPath:             dbFile,
		CdnUrl:             "http://localhost:8000",
		AdminPubkey:        pk,
		MaxUploadSizeBytes: 10 * 1024 * 1024, // 10 MB — generous ceiling for tests
		AccessControlRules: []config.AccessControlRule{
			{Action: string(core.ACRActionAllow), Pubkey: "ALL", Resource: string(core.ResourceUpload)},
		},
		AllowedMimeTypes: []string{"*"},
		// UploadLimits.Enabled defaults to false
	}
}

// TestUploadLimits_Disabled verifies that with enabled:false the upload path
// is completely unrestricted — no mime check, no size check, no day cap.
func TestUploadLimits_Disabled(t *testing.T) {
	dbFile := "./db-TestUploadLimits_Disabled.sqlite3"
	t.Cleanup(func() { os.Remove(dbFile) })

	pk, _ := nostr.GetPublicKey(nostr.GeneratePrivateKey())
	conf := baseConf(dbFile, pk)
	// UploadLimits explicitly disabled (this is the zero-value default).
	conf.UploadLimits = config.UploadLimitsConfig{Enabled: false}

	svc := newTestServices(t, dbFile, conf)

	// textBytes would be blocked by the default image-only allowlist if enabled.
	data := textBytes
	hash, _ := hashing.Hash(data)
	_, err := UploadBlob(context.Background(), svc, conf.CdnUrl, hash, pk, data, core.EncryptionModeNone)
	assert.NoError(t, err, "disabled upload_limits must not restrict any upload")
}

// TestUploadLimits_MimeType_AllowedPrefixMatches verifies that a detected JPEG
// passes when "image/" is in the tier's AllowedTypePrefixes.
func TestUploadLimits_MimeType_AllowedPrefixMatches(t *testing.T) {
	dbFile := "./db-TestUploadLimits_MimeType_Allowed.sqlite3"
	t.Cleanup(func() { os.Remove(dbFile) })

	pk, _ := nostr.GetPublicKey(nostr.GeneratePrivateKey())
	conf := baseConf(dbFile, pk)
	conf.UploadLimits = config.UploadLimitsConfig{
		Enabled: true,
		Named: config.UploadTierLimitsConfig{
			AllowedTypePrefixes: []string{"image/", "video/", "audio/", "application/pdf"},
		},
	}

	svc := newTestServices(t, dbFile, conf)

	data := jpegMagic
	hash, _ := hashing.Hash(data)
	_, err := UploadBlob(context.Background(), svc, conf.CdnUrl, hash, pk, data, core.EncryptionModeNone)
	assert.NoError(t, err, "JPEG should pass when image/ prefix is allowed for named tier")
}

// TestUploadLimits_MimeType_BlockedPrefix verifies that a MIME type not in the
// allowed prefixes is rejected.
func TestUploadLimits_MimeType_BlockedPrefix(t *testing.T) {
	dbFile := "./db-TestUploadLimits_MimeType_Blocked.sqlite3"
	t.Cleanup(func() { os.Remove(dbFile) })

	pk, _ := nostr.GetPublicKey(nostr.GeneratePrivateKey())
	conf := baseConf(dbFile, pk)
	conf.UploadLimits = config.UploadLimitsConfig{
		Enabled: true,
		Named: config.UploadTierLimitsConfig{
			AllowedTypePrefixes: []string{"image/", "video/"}, // text/ not in list
		},
	}

	svc := newTestServices(t, dbFile, conf)

	data := textBytes
	hash, _ := hashing.Hash(data)
	_, err := UploadBlob(context.Background(), svc, conf.CdnUrl, hash, pk, data, core.EncryptionModeNone)
	assert.Error(t, err, "text/plain should be rejected when only image/ and video/ are allowed")
}

// TestUploadLimits_MimeType_MagicByteWins is the "header lies, magic-byte wins" test.
//
// Scenario: a client could claim any Content-Type header, but the upload
// pipeline ignores it — mimetype.Detect runs on the raw bytes. Here we send
// real JPEG magic bytes while the allowlist only permits "image/". Even though
// a hypothetical client header might lie, magic-byte detection correctly
// identifies image/jpeg and the upload is allowed.
//
// Conversely, plain text bytes are always rejected regardless of what a client
// header would claim.
func TestUploadLimits_MimeType_MagicByteWins(t *testing.T) {
	dbFile := "./db-TestUploadLimits_MagicByteWins.sqlite3"
	t.Cleanup(func() { os.Remove(dbFile) })

	pk, _ := nostr.GetPublicKey(nostr.GeneratePrivateKey())

	tests := []struct {
		name        string
		data        []byte
		wantErr     bool
		description string
	}{
		{
			name:        "jpeg bytes detected as image/jpeg, allowed",
			data:        jpegMagic,
			wantErr:     false,
			description: "magic bytes → image/jpeg → passes image/ prefix check",
		},
		{
			name:        "text bytes detected as text/plain, blocked",
			data:        textBytes,
			wantErr:     true,
			description: "magic bytes → text/plain → rejected, image/ prefix does not match",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Each sub-test uses its own DB to avoid cross-contamination.
			subDB := dbFile + "_" + tt.name + ".sqlite3"
			t.Cleanup(func() { os.Remove(subDB) })

			conf := baseConf(subDB, pk)
			conf.UploadLimits = config.UploadLimitsConfig{
				Enabled: true,
				Named: config.UploadTierLimitsConfig{
					AllowedTypePrefixes: []string{"image/"},
				},
			}
			svc := newTestServices(t, subDB, conf)

			hash, _ := hashing.Hash(tt.data)
			_, err := UploadBlob(context.Background(), svc, conf.CdnUrl, hash, pk, tt.data, core.EncryptionModeNone)
			if tt.wantErr {
				assert.Error(t, err, tt.description)
			} else {
				assert.NoError(t, err, tt.description)
			}
		})
	}
}

// TestUploadLimits_FileSize_AtAndOverLimit tests size boundary conditions for
// the named tier.
func TestUploadLimits_FileSize_AtAndOverLimit(t *testing.T) {
	dbFile := "./db-TestUploadLimits_FileSize.sqlite3"
	t.Cleanup(func() { os.Remove(dbFile) })

	pk, _ := nostr.GetPublicKey(nostr.GeneratePrivateKey())

	const cap = 20 // 20-byte cap for the named tier

	tests := []struct {
		name    string
		size    int
		wantErr bool
	}{
		{name: "under cap", size: cap - 1, wantErr: false},
		{name: "at cap", size: cap, wantErr: false},
		{name: "over cap", size: cap + 1, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			subDB := dbFile + "_" + tt.name + ".sqlite3"
			t.Cleanup(func() { os.Remove(subDB) })

			conf := baseConf(subDB, pk)
			conf.UploadLimits = config.UploadLimitsConfig{
				Enabled: true,
				Named: config.UploadTierLimitsConfig{
					MaxFileBytes: int64(cap),
					// No mime restriction so any bytes are accepted type-wise.
				},
			}
			svc := newTestServices(t, subDB, conf)

			// Use JPEG magic + padding so mimetype detection is stable.
			data := make([]byte, tt.size)
			copy(data, jpegMagic)
			hash, _ := hashing.Hash(data)
			_, err := UploadBlob(context.Background(), svc, conf.CdnUrl, hash, pk, data, core.EncryptionModeNone)
			if tt.wantErr {
				assert.Error(t, err, "upload over cap must fail")
			} else {
				assert.NoError(t, err, "upload within cap must succeed")
			}
		})
	}
}

// TestUploadLimits_UploadsPerDay verifies that the daily counter allows uploads
// up to the limit and rejects the next one.
func TestUploadLimits_UploadsPerDay(t *testing.T) {
	dbFile := "./db-TestUploadLimits_UploadsPerDay.sqlite3"
	t.Cleanup(func() { os.Remove(dbFile) })

	pk, _ := nostr.GetPublicKey(nostr.GeneratePrivateKey())
	conf := baseConf(dbFile, pk)
	conf.UploadLimits = config.UploadLimitsConfig{
		Enabled: true,
		Named: config.UploadTierLimitsConfig{
			UploadsPerDay: 2, // only 2 uploads per day for named tier
		},
	}
	svc := newTestServices(t, dbFile, conf)

	// First upload — unique bytes so hash ≠ authHash mismatch doesn't bite us.
	data1 := []byte{0xde, 0xad}
	h1, _ := hashing.Hash(data1)
	_, err := UploadBlob(context.Background(), svc, conf.CdnUrl, h1, pk, data1, core.EncryptionModeNone)
	assert.NoError(t, err, "first upload should succeed (1 of 2)")

	data2 := []byte{0xbe, 0xef}
	h2, _ := hashing.Hash(data2)
	_, err = UploadBlob(context.Background(), svc, conf.CdnUrl, h2, pk, data2, core.EncryptionModeNone)
	assert.NoError(t, err, "second upload should succeed (2 of 2)")

	data3 := []byte{0xca, 0xfe}
	h3, _ := hashing.Hash(data3)
	_, err = UploadBlob(context.Background(), svc, conf.CdnUrl, h3, pk, data3, core.EncryptionModeNone)
	assert.Error(t, err, "third upload should fail (daily limit of 2 reached)")
	assert.Contains(t, err.Error(), "daily upload limit", "error message must mention daily limit")
}

// TestUploadLimits_TierFallback_AnonymousMoreRestrictive verifies that the
// anonymous tier config applies when UploadLimits is enabled but named/paid
// tiers are more permissive.
//
// In standalone mode, GetTier always returns TierNamed, so this test exercises
// the Named config. The anonymous config exists for documentation; in a real
// platform-mode deployment it would be exercised via the DB tier function.
func TestUploadLimits_AnonymousTier_Config(t *testing.T) {
	dbFile := "./db-TestUploadLimits_AnonymousTier.sqlite3"
	t.Cleanup(func() { os.Remove(dbFile) })

	pk, _ := nostr.GetPublicKey(nostr.GeneratePrivateKey())
	conf := baseConf(dbFile, pk)
	conf.UploadLimits = config.UploadLimitsConfig{
		Enabled: true,
		Anonymous: config.UploadTierLimitsConfig{
			// Anonymous has tighter limits — but in standalone mode, GetTier
			// returns TierNamed, so these limits don't apply directly.
			MaxFileBytes:  5,
			UploadsPerDay: 1,
		},
		Named: config.UploadTierLimitsConfig{
			// Named tier is more permissive.
			MaxFileBytes: 1024,
		},
	}
	svc := newTestServices(t, dbFile, conf)

	// Standalone → TierNamed → named.MaxFileBytes = 1024, so a 10-byte upload passes.
	data := jpegMagic
	hash, _ := hashing.Hash(data)
	_, err := UploadBlob(context.Background(), svc, conf.CdnUrl, hash, pk, data, core.EncryptionModeNone)
	assert.NoError(t, err, "standalone mode resolves to TierNamed, so named-tier limits apply")
}
