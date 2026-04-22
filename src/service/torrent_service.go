package service

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"time"

	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"
	"go.uber.org/zap"

	"git.aegis-hq.xyz/coldforge/cloistr-blossom/internal/cache"
	"git.aegis-hq.xyz/coldforge/cloistr-blossom/internal/storage"
	"git.aegis-hq.xyz/coldforge/cloistr-blossom/src/core"
)

const (
	// maxTorrentFileSize limits memory usage during torrent generation.
	// Files larger than this will be streamed.
	maxTorrentFileSize = 100 * 1024 * 1024 // 100 MB
)

const (
	torrentCachePrefix = "torrent:"
	torrentInfoPrefix  = "torrent_info:"
	torrentCacheTTL    = 7 * 24 * time.Hour // 1 week
)

// DHT bootstrap nodes for tracker-less operation (BEP 5)
var defaultDHTNodes = []string{
	"router.bittorrent.com:6881",
	"router.utorrent.com:6881",
	"dht.transmissionbt.com:6881",
}

type torrentService struct {
	storage storage.StorageBackend
	cache   cache.Cache
	log     *zap.Logger
}

// TorrentServiceConfig holds configuration for the torrent service.
type TorrentServiceConfig struct {
	DefaultWebSeedURL string // Base URL for web seeds (e.g., https://files.cloistr.xyz)
	DefaultTrackerURL string // Default tracker URL (optional)
}

// NewTorrentService creates a new torrent generation service.
func NewTorrentService(
	storageBackend storage.StorageBackend,
	appCache cache.Cache,
	log *zap.Logger,
) core.TorrentService {
	return &torrentService{
		storage: storageBackend,
		cache:   appCache,
		log:     log,
	}
}

// GenerateTorrent creates a .torrent file for a blob.
func (s *torrentService) GenerateTorrent(ctx context.Context, blobHash string, config *core.TorrentConfig) (*core.TorrentInfo, []byte, error) {
	// Get blob size first to determine piece length
	totalSize, err := s.storage.Size(ctx, blobHash)
	if err != nil {
		return nil, nil, fmt.Errorf("blob not found: %w", err)
	}

	pieceLength := calculatePieceLength(totalSize)

	// Generate piece hashes using streaming to avoid loading entire file into memory
	pieces, pieceCount, err := s.generatePiecesStreaming(ctx, blobHash, pieceLength)
	if err != nil {
		return nil, nil, fmt.Errorf("generate pieces: %w", err)
	}

	// Create info dictionary
	info := metainfo.Info{
		Name:        blobHash, // Use blob hash as name for WebSeed compatibility
		PieceLength: pieceLength,
		Pieces:      pieces,
		Length:      totalSize,
	}

	// Encode info to get info hash
	infoBytes, err := bencode.Marshal(info)
	if err != nil {
		return nil, nil, fmt.Errorf("encode info: %w", err)
	}

	infoHashBytes := sha1.Sum(infoBytes)
	infoHash := hex.EncodeToString(infoHashBytes[:])

	// Create metainfo
	mi := metainfo.MetaInfo{
		InfoBytes: infoBytes,
	}

	// Set creation metadata
	mi.CreatedBy = config.CreatedBy
	if mi.CreatedBy == "" {
		mi.CreatedBy = "cloistr-blossom"
	}
	mi.CreationDate = time.Now().Unix()
	mi.Comment = config.Comment

	// Set trackers (BEP 12)
	if len(config.TrackerURLs) > 0 {
		mi.Announce = config.TrackerURLs[0]
		if len(config.TrackerURLs) > 1 {
			// Multi-tracker setup - each tier is a separate slice
			var tiers [][]string
			for _, url := range config.TrackerURLs {
				tiers = append(tiers, []string{url})
			}
			mi.AnnounceList = tiers
		}
	}

	// Set web seeds (BEP 19)
	if len(config.WebSeedURLs) > 0 {
		mi.UrlList = config.WebSeedURLs
	}

	// Set DHT nodes (BEP 5)
	if config.EnableDHT {
		nodes := make([]metainfo.Node, len(defaultDHTNodes))
		for i, n := range defaultDHTNodes {
			nodes[i] = metainfo.Node(n)
		}
		mi.Nodes = nodes
	}

	// Encode torrent file
	var buf bytes.Buffer
	if err := mi.Write(&buf); err != nil {
		return nil, nil, fmt.Errorf("encode torrent: %w", err)
	}

	torrentBytes := buf.Bytes()

	// Build magnet URI
	magnetURI := buildMagnetURI(infoHash, blobHash, config.TrackerURLs, config.WebSeedURLs)

	// Create torrent info
	torrentInfo := &core.TorrentInfo{
		BlobHash:    blobHash,
		InfoHash:    infoHash,
		MagnetURI:   magnetURI,
		PieceLength: pieceLength,
		PieceCount:  pieceCount,
		TotalSize:   totalSize,
		Name:        blobHash,
		CreatedAt:   time.Now().Unix(),
	}

	// Cache the torrent and metadata
	if s.cache != nil {
		if err := s.cache.Set(ctx, torrentCachePrefix+blobHash, torrentBytes, torrentCacheTTL); err != nil {
			s.log.Warn("failed to cache torrent file", zap.Error(err))
		}

		// Cache info as JSON (not bencode) for easier debugging
		infoJSON, err := json.Marshal(torrentInfo)
		if err != nil {
			s.log.Warn("failed to marshal torrent info", zap.Error(err))
		} else if err := s.cache.Set(ctx, torrentInfoPrefix+blobHash, infoJSON, torrentCacheTTL); err != nil {
			s.log.Warn("failed to cache torrent info", zap.Error(err))
		}
	}

	s.log.Info("torrent generated",
		zap.String("blob_hash", blobHash),
		zap.String("info_hash", infoHash),
		zap.Int64("size", totalSize),
		zap.Int64("piece_length", pieceLength),
		zap.Int("piece_count", torrentInfo.PieceCount))

	return torrentInfo, torrentBytes, nil
}

// GetTorrent retrieves a previously generated torrent file.
func (s *torrentService) GetTorrent(ctx context.Context, blobHash string) ([]byte, error) {
	if s.cache == nil {
		return nil, core.ErrTorrentNotFound
	}

	data, ok := s.cache.Get(ctx, torrentCachePrefix+blobHash)
	if !ok {
		return nil, core.ErrTorrentNotFound
	}

	return data, nil
}

// GetTorrentInfo retrieves metadata about a generated torrent.
func (s *torrentService) GetTorrentInfo(ctx context.Context, blobHash string) (*core.TorrentInfo, error) {
	if s.cache == nil {
		return nil, core.ErrTorrentNotFound
	}

	data, ok := s.cache.Get(ctx, torrentInfoPrefix+blobHash)
	if !ok {
		return nil, core.ErrTorrentNotFound
	}

	var info core.TorrentInfo
	if err := json.Unmarshal(data, &info); err != nil {
		return nil, fmt.Errorf("decode torrent info: %w", err)
	}

	return &info, nil
}

// DeleteTorrent removes a cached torrent file.
func (s *torrentService) DeleteTorrent(ctx context.Context, blobHash string) error {
	if s.cache != nil {
		s.cache.Delete(ctx, torrentCachePrefix+blobHash)
		s.cache.Delete(ctx, torrentInfoPrefix+blobHash)
	}
	return nil
}

// calculatePieceLength determines optimal piece size based on file size.
// Goal: Keep piece count between 1,200-2,200 for optimal swarm performance.
func calculatePieceLength(fileSize int64) int64 {
	const (
		KB = 1024
		MB = 1024 * KB
		GB = 1024 * MB
	)

	switch {
	case fileSize < 50*MB:
		return 32 * KB
	case fileSize < 150*MB:
		return 64 * KB
	case fileSize < 350*MB:
		return 128 * KB
	case fileSize < 512*MB:
		return 256 * KB
	case fileSize < 1*GB:
		return 512 * KB
	case fileSize < 2*GB:
		return 1 * MB
	case fileSize < 4*GB:
		return 2 * MB
	case fileSize < 8*GB:
		return 4 * MB
	case fileSize < 16*GB:
		return 8 * MB
	default:
		return 16 * MB
	}
}

// generatePiecesStreaming computes SHA1 hashes for each piece by streaming
// the file from storage. This avoids loading the entire file into memory.
func (s *torrentService) generatePiecesStreaming(ctx context.Context, blobHash string, pieceLength int64) ([]byte, int, error) {
	reader, err := s.storage.Get(ctx, blobHash)
	if err != nil {
		return nil, 0, err
	}
	defer reader.Close()

	var pieces []byte
	pieceCount := 0
	buffer := make([]byte, pieceLength)

	for {
		// Check context cancellation for long-running operations
		if ctx.Err() != nil {
			return nil, 0, ctx.Err()
		}

		n, err := io.ReadFull(reader, buffer)
		if n > 0 {
			hash := sha1.Sum(buffer[:n])
			pieces = append(pieces, hash[:]...)
			pieceCount++
		}

		if err == io.EOF || err == io.ErrUnexpectedEOF {
			break
		}
		if err != nil {
			return nil, 0, fmt.Errorf("read piece: %w", err)
		}
	}

	return pieces, pieceCount, nil
}

// generatePieces computes SHA1 hashes for each piece (in-memory version for testing).
func generatePieces(data []byte, pieceLength int64) []byte {
	var pieces []byte

	for i := int64(0); i < int64(len(data)); i += pieceLength {
		end := i + pieceLength
		if end > int64(len(data)) {
			end = int64(len(data))
		}

		hash := sha1.Sum(data[i:end])
		pieces = append(pieces, hash[:]...)
	}

	return pieces
}

// buildMagnetURI constructs a magnet link for the torrent.
// URLs are properly encoded to handle special characters.
func buildMagnetURI(infoHash, name string, trackers, webSeeds []string) string {
	magnet := fmt.Sprintf("magnet:?xt=urn:btih:%s&dn=%s", infoHash, url.QueryEscape(name))

	for _, tracker := range trackers {
		magnet += "&tr=" + url.QueryEscape(tracker)
	}

	for _, ws := range webSeeds {
		magnet += "&ws=" + url.QueryEscape(ws+name)
	}

	return magnet
}
