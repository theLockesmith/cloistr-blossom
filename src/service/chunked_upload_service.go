package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"git.coldforge.xyz/coldforge/cloistr-blossom/db"
	"git.coldforge.xyz/coldforge/cloistr-blossom/internal/storage"
	"git.coldforge.xyz/coldforge/cloistr-blossom/src/core"
	"git.coldforge.xyz/coldforge/cloistr-blossom/src/pkg/config"
)

type chunkedUploadService struct {
	db         *sql.DB
	queries    *db.Queries
	storage    storage.StorageBackend
	blobSvc    core.BlobStorage
	quotaSvc   core.QuotaService
	config     *config.ChunkedUploadConfig
	cdnBaseURL string
	tempDir    string
	log        *zap.Logger
}

// NewChunkedUploadService creates a new chunked upload service.
func NewChunkedUploadService(
	database *sql.DB,
	queries *db.Queries,
	storageBackend storage.StorageBackend,
	blobSvc core.BlobStorage,
	quotaSvc core.QuotaService,
	conf *config.ChunkedUploadConfig,
	cdnBaseURL string,
	log *zap.Logger,
) (core.ChunkedUploadService, error) {
	tempDir := conf.TempDir
	if tempDir == "" {
		tempDir = "/tmp/blossom-chunks"
	}

	// Ensure temp directory exists
	if err := os.MkdirAll(tempDir, 0755); err != nil {
		return nil, fmt.Errorf("create temp dir: %w", err)
	}

	return &chunkedUploadService{
		db:         database,
		queries:    queries,
		storage:    storageBackend,
		blobSvc:    blobSvc,
		quotaSvc:   quotaSvc,
		config:     conf,
		cdnBaseURL: cdnBaseURL,
		tempDir:    tempDir,
		log:        log,
	}, nil
}

// CreateSession creates a new upload session.
func (s *chunkedUploadService) CreateSession(ctx context.Context, req *core.CreateSessionRequest) (*core.CreateSessionResponse, error) {
	// Validate request
	if req.TotalSize <= 0 {
		return nil, fmt.Errorf("total_size must be positive")
	}

	// Determine chunk size
	chunkSize := req.ChunkSize
	if chunkSize == 0 {
		chunkSize = s.config.DefaultChunkSize
	}
	if chunkSize < s.config.MinChunkSize {
		chunkSize = s.config.MinChunkSize
	}
	if chunkSize > s.config.MaxChunkSize {
		chunkSize = s.config.MaxChunkSize
	}

	// Calculate total chunks
	totalChunks := int((req.TotalSize + chunkSize - 1) / chunkSize)

	// Parse TTLs
	defaultTTL, _ := time.ParseDuration(s.config.DefaultSessionTTL)
	maxTTL, _ := time.ParseDuration(s.config.MaxSessionTTL)
	if defaultTTL == 0 {
		defaultTTL = 1 * time.Hour
	}
	if maxTTL == 0 {
		maxTTL = 24 * time.Hour
	}

	// Determine session TTL
	sessionTTL := defaultTTL
	if req.TTL > 0 {
		requestedTTL := time.Duration(req.TTL) * time.Second
		if requestedTTL < maxTTL {
			sessionTTL = requestedTTL
		} else {
			sessionTTL = maxTTL
		}
	}

	// Check quota if quota service is available
	if s.quotaSvc != nil {
		if err := s.quotaSvc.CheckQuota(ctx, req.Pubkey, req.TotalSize); err != nil {
			return nil, fmt.Errorf("quota check failed: %w", err)
		}
	}

	// Generate session ID
	sessionID := uuid.New().String()

	// Determine encryption mode
	encryptionMode := req.EncryptionMode
	if encryptionMode == "" {
		encryptionMode = "none"
	}

	now := time.Now().Unix()
	expiresAt := now + int64(sessionTTL.Seconds())

	// Create session in database
	dbSession, err := s.queries.CreateUploadSession(ctx, db.CreateUploadSessionParams{
		ID:             sessionID,
		Pubkey:         req.Pubkey,
		Hash:           sql.NullString{String: req.Hash, Valid: req.Hash != ""},
		TotalSize:      req.TotalSize,
		ChunkSize:      chunkSize,
		MimeType:       sql.NullString{String: req.MimeType, Valid: req.MimeType != ""},
		ChunksReceived: 0,
		BytesReceived:  0,
		Status:         "active",
		EncryptionMode: encryptionMode,
		Created:        now,
		Updated:        now,
		ExpiresAt:      expiresAt,
	})
	if err != nil {
		return nil, fmt.Errorf("create session: %w", err)
	}

	// Create session directory for chunks
	sessionDir := filepath.Join(s.tempDir, sessionID)
	if err := os.MkdirAll(sessionDir, 0755); err != nil {
		// Clean up database record
		_ = s.queries.DeleteUploadSession(ctx, sessionID)
		return nil, fmt.Errorf("create session directory: %w", err)
	}

	session := s.dbSessionToSession(dbSession)
	session.TotalChunks = totalChunks

	s.log.Info("created chunked upload session",
		zap.String("session_id", sessionID),
		zap.String("pubkey", req.Pubkey),
		zap.Int64("total_size", req.TotalSize),
		zap.Int64("chunk_size", chunkSize),
		zap.Int("total_chunks", totalChunks))

	return &core.CreateSessionResponse{
		Session:     session,
		ChunkSize:   chunkSize,
		TotalChunks: totalChunks,
		ExpiresAt:   expiresAt,
		UploadURL:   fmt.Sprintf("%s/upload/chunked/%s", s.cdnBaseURL, sessionID),
	}, nil
}

// UploadChunk uploads a single chunk to a session.
func (s *chunkedUploadService) UploadChunk(ctx context.Context, sessionID string, chunkNum int, data []byte) (*core.UploadChunk, error) {
	// Get session
	dbSession, err := s.queries.GetUploadSession(ctx, sessionID)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, core.ErrSessionNotFound
		}
		return nil, fmt.Errorf("get session: %w", err)
	}

	// Validate session state
	if dbSession.Status != "active" {
		switch dbSession.Status {
		case "complete":
			return nil, core.ErrSessionComplete
		case "expired":
			return nil, core.ErrSessionExpired
		case "aborted":
			return nil, core.ErrSessionAborted
		}
	}

	// Check expiration
	if time.Now().Unix() > dbSession.ExpiresAt {
		_ = s.queries.UpdateUploadSessionStatus(ctx, db.UpdateUploadSessionStatusParams{
			ID:      sessionID,
			Status:  "expired",
			Updated: time.Now().Unix(),
		})
		return nil, core.ErrSessionExpired
	}

	// Calculate total chunks
	totalChunks := int((dbSession.TotalSize + dbSession.ChunkSize - 1) / dbSession.ChunkSize)

	// Validate chunk number
	if chunkNum < 0 || chunkNum >= totalChunks {
		return nil, core.ErrChunkOutOfRange
	}

	// Check if chunk already uploaded
	_, err = s.queries.GetUploadChunk(ctx, db.GetUploadChunkParams{
		SessionID: sessionID,
		ChunkNum:  int32(chunkNum),
	})
	if err == nil {
		return nil, core.ErrChunkAlreadyUploaded
	}

	// Validate chunk size
	expectedSize := dbSession.ChunkSize
	isLastChunk := chunkNum == totalChunks-1
	if isLastChunk {
		// Last chunk may be smaller
		expectedSize = dbSession.TotalSize - (int64(chunkNum) * dbSession.ChunkSize)
	}
	if int64(len(data)) != expectedSize {
		// Allow some tolerance for last chunk
		if !isLastChunk || int64(len(data)) > dbSession.ChunkSize {
			return nil, fmt.Errorf("%w: expected %d bytes, got %d", core.ErrChunkSizeMismatch, expectedSize, len(data))
		}
	}

	// Calculate chunk hash
	hash := sha256.Sum256(data)
	hashStr := hex.EncodeToString(hash[:])

	// Write chunk to temp file
	chunkPath := filepath.Join(s.tempDir, sessionID, fmt.Sprintf("chunk_%06d", chunkNum))
	if err := os.WriteFile(chunkPath, data, 0644); err != nil {
		return nil, fmt.Errorf("write chunk: %w", err)
	}

	// Calculate offset
	offset := int64(chunkNum) * dbSession.ChunkSize
	now := time.Now().Unix()

	// Record chunk in database
	_, err = s.queries.CreateUploadChunk(ctx, db.CreateUploadChunkParams{
		SessionID:   sessionID,
		ChunkNum:    int32(chunkNum),
		Size:        int64(len(data)),
		OffsetBytes: offset,
		Hash:        hashStr,
		ReceivedAt:  now,
	})
	if err != nil {
		// Clean up file on failure
		_ = os.Remove(chunkPath)
		return nil, fmt.Errorf("record chunk: %w", err)
	}

	// Update session progress
	newChunksReceived := dbSession.ChunksReceived + 1
	newBytesReceived := dbSession.BytesReceived + int64(len(data))

	if err := s.queries.UpdateUploadSessionProgress(ctx, db.UpdateUploadSessionProgressParams{
		ID:             sessionID,
		ChunksReceived: newChunksReceived,
		BytesReceived:  newBytesReceived,
		Updated:        now,
	}); err != nil {
		s.log.Warn("failed to update session progress", zap.Error(err))
	}

	s.log.Debug("chunk uploaded",
		zap.String("session_id", sessionID),
		zap.Int("chunk_num", chunkNum),
		zap.Int("size", len(data)),
		zap.Int32("chunks_received", newChunksReceived),
		zap.Int("total_chunks", totalChunks))

	return &core.UploadChunk{
		SessionID:  sessionID,
		ChunkNum:   chunkNum,
		Size:       int64(len(data)),
		Offset:     offset,
		Hash:       hashStr,
		ReceivedAt: now,
	}, nil
}

// CompleteUpload finalizes the upload and creates the blob.
func (s *chunkedUploadService) CompleteUpload(ctx context.Context, sessionID string, expectedHash string) (*core.CompleteUploadResponse, error) {
	// Get session
	dbSession, err := s.queries.GetUploadSession(ctx, sessionID)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, core.ErrSessionNotFound
		}
		return nil, fmt.Errorf("get session: %w", err)
	}

	// Validate session state
	if dbSession.Status != "active" {
		switch dbSession.Status {
		case "complete":
			return nil, core.ErrSessionComplete
		case "expired":
			return nil, core.ErrSessionExpired
		case "aborted":
			return nil, core.ErrSessionAborted
		}
	}

	// Calculate expected chunks
	totalChunks := int((dbSession.TotalSize + dbSession.ChunkSize - 1) / dbSession.ChunkSize)

	// Verify all chunks received
	if int(dbSession.ChunksReceived) != totalChunks {
		return nil, fmt.Errorf("%w: received %d of %d chunks",
			core.ErrUploadIncomplete, dbSession.ChunksReceived, totalChunks)
	}

	// Get all chunks in order
	dbChunks, err := s.queries.GetUploadChunks(ctx, sessionID)
	if err != nil {
		return nil, fmt.Errorf("get chunks: %w", err)
	}

	// Verify chunk count
	if len(dbChunks) != totalChunks {
		return nil, fmt.Errorf("%w: database has %d chunks, expected %d",
			core.ErrUploadIncomplete, len(dbChunks), totalChunks)
	}

	// Assemble chunks and calculate final hash
	sessionDir := filepath.Join(s.tempDir, sessionID)

	// Create hasher for final hash calculation
	hasher := sha256.New()
	var assembledData bytes.Buffer

	for i := 0; i < totalChunks; i++ {
		chunkPath := filepath.Join(sessionDir, fmt.Sprintf("chunk_%06d", i))
		chunkData, err := os.ReadFile(chunkPath)
		if err != nil {
			return nil, fmt.Errorf("read chunk %d: %w", i, err)
		}

		// Verify chunk hash
		chunkHash := sha256.Sum256(chunkData)
		if hex.EncodeToString(chunkHash[:]) != dbChunks[i].Hash {
			return nil, fmt.Errorf("chunk %d hash mismatch", i)
		}

		hasher.Write(chunkData)
		assembledData.Write(chunkData)
	}

	finalHash := hex.EncodeToString(hasher.Sum(nil))

	// Verify final hash if provided
	hashToUse := expectedHash
	if hashToUse == "" && dbSession.Hash.Valid {
		hashToUse = dbSession.Hash.String
	}
	if hashToUse != "" && finalHash != hashToUse {
		return nil, fmt.Errorf("%w: expected %s, got %s", core.ErrHashMismatch, hashToUse, finalHash)
	}

	// Determine MIME type
	mimeType := "application/octet-stream"
	if dbSession.MimeType.Valid && dbSession.MimeType.String != "" {
		mimeType = dbSession.MimeType.String
	}

	// Save blob using blob service
	now := time.Now().Unix()
	url := fmt.Sprintf("%s/%s", s.cdnBaseURL, finalHash)

	blob, _, err := s.blobSvc.SaveWithDedup(
		ctx,
		dbSession.Pubkey,
		finalHash,
		url,
		int64(assembledData.Len()),
		mimeType,
		assembledData.Bytes(),
		now,
		core.EncryptionMode(dbSession.EncryptionMode),
	)
	if err != nil {
		return nil, fmt.Errorf("save blob: %w", err)
	}

	// Update quota
	if s.quotaSvc != nil {
		if err := s.quotaSvc.IncrementUsage(ctx, dbSession.Pubkey, int64(assembledData.Len())); err != nil {
			s.log.Warn("failed to update quota", zap.Error(err))
		}
	}

	// Mark session as complete
	if err := s.queries.UpdateUploadSessionStatus(ctx, db.UpdateUploadSessionStatusParams{
		ID:      sessionID,
		Status:  "complete",
		Updated: now,
	}); err != nil {
		s.log.Warn("failed to mark session complete", zap.Error(err))
	}

	// Update session hash if it wasn't set
	if !dbSession.Hash.Valid {
		_ = s.queries.UpdateUploadSessionHash(ctx, db.UpdateUploadSessionHashParams{
			ID:      sessionID,
			Hash:    sql.NullString{String: finalHash, Valid: true},
			Updated: now,
		})
	}

	// Clean up chunk files
	go func() {
		if err := os.RemoveAll(sessionDir); err != nil {
			s.log.Warn("failed to clean up chunk directory",
				zap.String("session_id", sessionID),
				zap.Error(err))
		}
	}()

	s.log.Info("chunked upload completed",
		zap.String("session_id", sessionID),
		zap.String("hash", finalHash),
		zap.Int64("size", int64(assembledData.Len())),
		zap.String("pubkey", dbSession.Pubkey))

	return &core.CompleteUploadResponse{
		Blob: blob,
		Hash: finalHash,
		Size: int64(assembledData.Len()),
		URL:  url,
	}, nil
}

// AbortUpload cancels an upload session and cleans up resources.
func (s *chunkedUploadService) AbortUpload(ctx context.Context, sessionID string) error {
	// Get session to verify it exists
	_, err := s.queries.GetUploadSession(ctx, sessionID)
	if err != nil {
		if err == sql.ErrNoRows {
			return core.ErrSessionNotFound
		}
		return fmt.Errorf("get session: %w", err)
	}

	// Mark session as aborted
	if err := s.queries.UpdateUploadSessionStatus(ctx, db.UpdateUploadSessionStatusParams{
		ID:      sessionID,
		Status:  "aborted",
		Updated: time.Now().Unix(),
	}); err != nil {
		return fmt.Errorf("update session status: %w", err)
	}

	// Clean up chunk files
	sessionDir := filepath.Join(s.tempDir, sessionID)
	if err := os.RemoveAll(sessionDir); err != nil {
		s.log.Warn("failed to clean up chunk directory",
			zap.String("session_id", sessionID),
			zap.Error(err))
	}

	// Delete chunks from database
	if err := s.queries.DeleteUploadChunks(ctx, sessionID); err != nil {
		s.log.Warn("failed to delete chunks from database",
			zap.String("session_id", sessionID),
			zap.Error(err))
	}

	s.log.Info("chunked upload aborted", zap.String("session_id", sessionID))

	return nil
}

// GetSession retrieves session information.
func (s *chunkedUploadService) GetSession(ctx context.Context, sessionID string) (*core.UploadSession, error) {
	dbSession, err := s.queries.GetUploadSession(ctx, sessionID)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, core.ErrSessionNotFound
		}
		return nil, fmt.Errorf("get session: %w", err)
	}

	session := s.dbSessionToSession(dbSession)

	// Calculate total chunks
	session.TotalChunks = int((dbSession.TotalSize + dbSession.ChunkSize - 1) / dbSession.ChunkSize)

	return session, nil
}

// GetChunks retrieves all chunks for a session.
func (s *chunkedUploadService) GetChunks(ctx context.Context, sessionID string) ([]core.UploadChunk, error) {
	dbChunks, err := s.queries.GetUploadChunks(ctx, sessionID)
	if err != nil {
		return nil, fmt.Errorf("get chunks: %w", err)
	}

	chunks := make([]core.UploadChunk, len(dbChunks))
	for i, dbc := range dbChunks {
		chunks[i] = core.UploadChunk{
			SessionID:  dbc.SessionID,
			ChunkNum:   int(dbc.ChunkNum),
			Size:       dbc.Size,
			Offset:     dbc.OffsetBytes,
			Hash:       dbc.Hash,
			ReceivedAt: dbc.ReceivedAt,
		}
	}

	return chunks, nil
}

// CleanupExpiredSessions removes expired sessions and their data.
func (s *chunkedUploadService) CleanupExpiredSessions(ctx context.Context) (int, error) {
	now := time.Now().Unix()

	// Get and delete expired sessions
	deletedIDs, err := s.queries.DeleteExpiredSessions(ctx, now)
	if err != nil {
		return 0, fmt.Errorf("delete expired sessions: %w", err)
	}

	// Clean up chunk directories
	for _, id := range deletedIDs {
		sessionDir := filepath.Join(s.tempDir, id)
		if err := os.RemoveAll(sessionDir); err != nil {
			s.log.Warn("failed to clean up expired session directory",
				zap.String("session_id", id),
				zap.Error(err))
		}
	}

	if len(deletedIDs) > 0 {
		s.log.Info("cleaned up expired sessions", zap.Int("count", len(deletedIDs)))
	}

	return len(deletedIDs), nil
}

// dbSessionToSession converts a database session to a core session.
func (s *chunkedUploadService) dbSessionToSession(dbs db.UploadSession) *core.UploadSession {
	session := &core.UploadSession{
		ID:             dbs.ID,
		Pubkey:         dbs.Pubkey,
		TotalSize:      dbs.TotalSize,
		ChunkSize:      dbs.ChunkSize,
		ChunksReceived: int(dbs.ChunksReceived),
		BytesReceived:  dbs.BytesReceived,
		Status:         core.UploadSessionStatus(dbs.Status),
		EncryptionMode: dbs.EncryptionMode,
		Created:        dbs.Created,
		Updated:        dbs.Updated,
		ExpiresAt:      dbs.ExpiresAt,
	}

	if dbs.Hash.Valid {
		session.Hash = dbs.Hash.String
	}
	if dbs.MimeType.Valid {
		session.MimeType = dbs.MimeType.String
	}

	return session
}

// StartCleanupWorker starts a background worker to clean up expired sessions.
func (s *chunkedUploadService) StartCleanupWorker(ctx context.Context, interval time.Duration) {
	if interval == 0 {
		interval = 5 * time.Minute
	}

	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if _, err := s.CleanupExpiredSessions(ctx); err != nil {
					s.log.Error("cleanup expired sessions failed", zap.Error(err))
				}
			}
		}
	}()

	s.log.Info("started chunked upload cleanup worker", zap.Duration("interval", interval))
}

// Ensure interface compliance
var _ core.ChunkedUploadService = (*chunkedUploadService)(nil)

// StreamingChunkReader allows streaming chunks for memory efficiency with large uploads.
type StreamingChunkReader struct {
	sessionDir  string
	totalChunks int
	chunkSize   int64
	currentIdx  int
	currentFile *os.File
}

// NewStreamingChunkReader creates a reader that streams chunks from disk.
func NewStreamingChunkReader(sessionDir string, totalChunks int, chunkSize int64) *StreamingChunkReader {
	return &StreamingChunkReader{
		sessionDir:  sessionDir,
		totalChunks: totalChunks,
		chunkSize:   chunkSize,
		currentIdx:  0,
	}
}

// Read implements io.Reader for streaming chunks.
func (r *StreamingChunkReader) Read(p []byte) (n int, err error) {
	for {
		// If we have an open file, try to read from it
		if r.currentFile != nil {
			n, err = r.currentFile.Read(p)
			if err == io.EOF {
				// Close current file and move to next chunk
				r.currentFile.Close()
				r.currentFile = nil
				r.currentIdx++
				if n > 0 {
					return n, nil
				}
				continue
			}
			return n, err
		}

		// Check if we've read all chunks
		if r.currentIdx >= r.totalChunks {
			return 0, io.EOF
		}

		// Open next chunk file
		chunkPath := filepath.Join(r.sessionDir, fmt.Sprintf("chunk_%06d", r.currentIdx))
		f, err := os.Open(chunkPath)
		if err != nil {
			return 0, fmt.Errorf("open chunk %d: %w", r.currentIdx, err)
		}
		r.currentFile = f
	}
}

// Close closes any open file handles.
func (r *StreamingChunkReader) Close() error {
	if r.currentFile != nil {
		return r.currentFile.Close()
	}
	return nil
}
