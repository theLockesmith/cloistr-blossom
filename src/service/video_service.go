package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"

	"git.coldforge.xyz/coldforge/cloistr-blossom/internal/cache"
	"git.coldforge.xyz/coldforge/cloistr-blossom/internal/storage"
	"git.coldforge.xyz/coldforge/cloistr-blossom/src/core"
)

const (
	hlsSegmentDuration  = 6 // seconds
	dashSegmentDuration = 6 // seconds
	jobCachePrefix      = "video_job:"
	jobCacheTTL         = 24 * time.Hour
)

type videoService struct {
	storage      storage.StorageBackend
	cache        cache.Cache
	workDir      string
	ffmpegPath   string
	log          *zap.Logger
	jobs         map[string]*core.TranscodeJob
	jobsMu       sync.RWMutex
	cdnBaseUrl   string
}

// VideoConfig holds configuration for the video service.
type VideoConfig struct {
	WorkDir    string // Directory for temporary transcoding files
	FFmpegPath string // Path to FFmpeg binary (empty = auto-detect)
	CDNBaseUrl string // Base URL for serving segments
}

// NewVideoService creates a new video transcoding service.
func NewVideoService(
	storageBackend storage.StorageBackend,
	appCache cache.Cache,
	conf VideoConfig,
	log *zap.Logger,
) (core.VideoService, error) {
	// Auto-detect FFmpeg if not specified
	ffmpegPath := conf.FFmpegPath
	if ffmpegPath == "" {
		path, err := exec.LookPath("ffmpeg")
		if err != nil {
			log.Warn("FFmpeg not found in PATH", zap.Error(err))
		} else {
			ffmpegPath = path
		}
	}

	// Create work directory if it doesn't exist
	workDir := conf.WorkDir
	if workDir == "" {
		workDir = filepath.Join(os.TempDir(), "blossom-transcode")
	}
	if err := os.MkdirAll(workDir, 0755); err != nil {
		return nil, fmt.Errorf("create work directory: %w", err)
	}

	return &videoService{
		storage:    storageBackend,
		cache:      appCache,
		workDir:    workDir,
		ffmpegPath: ffmpegPath,
		log:        log,
		jobs:       make(map[string]*core.TranscodeJob),
		cdnBaseUrl: conf.CDNBaseUrl,
	}, nil
}

// IsSupported returns true if the MIME type is a supported video format.
func (s *videoService) IsSupported(mimeType string) bool {
	switch mimeType {
	case "video/mp4", "video/webm", "video/quicktime", "video/x-msvideo",
		"video/x-matroska", "video/mpeg", "video/ogg", "video/3gpp":
		return true
	default:
		return false
	}
}

// IsFFmpegAvailable checks if FFmpeg is installed and available.
func (s *videoService) IsFFmpegAvailable() bool {
	if s.ffmpegPath == "" {
		return false
	}
	cmd := exec.Command(s.ffmpegPath, "-version")
	return cmd.Run() == nil
}

// StartTranscode starts a transcoding job for a blob.
func (s *videoService) StartTranscode(ctx context.Context, blobHash string, qualities []core.VideoQuality) (*core.TranscodeJob, error) {
	if !s.IsFFmpegAvailable() {
		return nil, core.ErrFFmpegNotFound
	}

	// Check if job already exists
	s.jobsMu.RLock()
	existingJob, exists := s.jobs[blobHash]
	s.jobsMu.RUnlock()

	if exists && existingJob.Status == core.TranscodeStatusProcessing {
		return nil, core.ErrTranscodeInProgress
	}

	// Use default qualities if none specified
	if len(qualities) == 0 {
		qualities = core.DefaultQualities
	}

	// Create output directory
	outputDir := filepath.Join(s.workDir, blobHash)
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return nil, fmt.Errorf("create output directory: %w", err)
	}

	// Create job
	now := time.Now().Unix()
	job := &core.TranscodeJob{
		ID:        blobHash,
		BlobHash:  blobHash,
		Status:    core.TranscodeStatusPending,
		Progress:  0,
		Qualities: qualities,
		OutputDir: outputDir,
		CreatedAt: now,
		UpdatedAt: now,
	}

	// Store job
	s.jobsMu.Lock()
	s.jobs[blobHash] = job
	s.jobsMu.Unlock()

	// Start transcoding in background
	go s.runTranscode(context.Background(), job)

	return job, nil
}

// runTranscode performs the actual transcoding work.
func (s *videoService) runTranscode(ctx context.Context, job *core.TranscodeJob) {
	s.updateJobStatus(job, core.TranscodeStatusProcessing, 0, "")

	// Get original video from storage
	reader, err := s.storage.Get(ctx, job.BlobHash)
	if err != nil {
		s.updateJobStatus(job, core.TranscodeStatusFailed, 0, fmt.Sprintf("get blob: %v", err))
		return
	}
	defer reader.Close()

	// Write to temp file for FFmpeg
	inputPath := filepath.Join(job.OutputDir, "input.tmp")
	inputFile, err := os.Create(inputPath)
	if err != nil {
		s.updateJobStatus(job, core.TranscodeStatusFailed, 0, fmt.Sprintf("create input file: %v", err))
		return
	}

	if _, err := inputFile.ReadFrom(reader); err != nil {
		inputFile.Close()
		s.updateJobStatus(job, core.TranscodeStatusFailed, 0, fmt.Sprintf("write input file: %v", err))
		return
	}
	inputFile.Close()
	defer os.Remove(inputPath)

	// Transcode to each quality
	totalQualities := len(job.Qualities)
	for i, quality := range job.Qualities {
		progress := (i * 100) / totalQualities
		s.updateJobStatus(job, core.TranscodeStatusProcessing, progress, "")

		qualityDir := filepath.Join(job.OutputDir, quality.Name)
		if err := os.MkdirAll(qualityDir, 0755); err != nil {
			s.updateJobStatus(job, core.TranscodeStatusFailed, progress, fmt.Sprintf("create quality dir: %v", err))
			return
		}

		if err := s.transcodeToHLS(inputPath, qualityDir, quality); err != nil {
			s.log.Error("transcode failed",
				zap.String("hash", job.BlobHash),
				zap.String("quality", quality.Name),
				zap.Error(err))
			s.updateJobStatus(job, core.TranscodeStatusFailed, progress, fmt.Sprintf("transcode %s: %v", quality.Name, err))
			return
		}

		s.log.Info("transcoded quality",
			zap.String("hash", job.BlobHash),
			zap.String("quality", quality.Name))
	}

	// Generate HLS master playlist
	if err := s.generateMasterPlaylist(job); err != nil {
		s.updateJobStatus(job, core.TranscodeStatusFailed, 85, fmt.Sprintf("generate HLS master playlist: %v", err))
		return
	}

	// Generate DASH output
	s.updateJobStatus(job, core.TranscodeStatusProcessing, 87, "")
	if err := s.transcodeToDASH(inputPath, job); err != nil {
		s.log.Error("DASH transcoding failed",
			zap.String("hash", job.BlobHash),
			zap.Error(err))
		// Continue even if DASH fails - HLS is the primary format
	} else {
		s.log.Info("DASH transcoding complete", zap.String("hash", job.BlobHash))
	}

	// Upload transcoded files to storage
	if err := s.uploadTranscodedFiles(ctx, job); err != nil {
		s.updateJobStatus(job, core.TranscodeStatusFailed, 95, fmt.Sprintf("upload files: %v", err))
		return
	}

	// Mark complete
	job.CompletedAt = time.Now().Unix()
	s.updateJobStatus(job, core.TranscodeStatusComplete, 100, "")

	s.log.Info("transcoding complete",
		zap.String("hash", job.BlobHash),
		zap.Int("qualities", len(job.Qualities)))
}

// transcodeToHLS transcodes video to HLS format at specified quality.
func (s *videoService) transcodeToHLS(inputPath, outputDir string, quality core.VideoQuality) error {
	outputPath := filepath.Join(outputDir, "stream.m3u8")

	// Build FFmpeg command
	args := []string{
		"-i", inputPath,
		"-c:v", "libx264",
		"-preset", "veryfast",
		"-crf", "23",
		"-vf", fmt.Sprintf("scale=%d:%d:force_original_aspect_ratio=decrease,pad=%d:%d:(ow-iw)/2:(oh-ih)/2",
			quality.Width, quality.Height, quality.Width, quality.Height),
		"-b:v", fmt.Sprintf("%dk", quality.VideoBitrate),
		"-maxrate", fmt.Sprintf("%dk", int(float64(quality.VideoBitrate)*1.5)),
		"-bufsize", fmt.Sprintf("%dk", quality.VideoBitrate*2),
		"-c:a", "aac",
		"-b:a", fmt.Sprintf("%dk", quality.AudioBitrate),
		"-ar", "44100",
		"-f", "hls",
		"-hls_time", fmt.Sprintf("%d", hlsSegmentDuration),
		"-hls_playlist_type", "vod",
		"-hls_segment_filename", filepath.Join(outputDir, "segment_%03d.ts"),
		"-y",
		outputPath,
	}

	cmd := exec.Command(s.ffmpegPath, args...)

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("ffmpeg error: %v, stderr: %s", err, stderr.String())
	}

	return nil
}

// generateMasterPlaylist creates the master HLS playlist.
func (s *videoService) generateMasterPlaylist(job *core.TranscodeJob) error {
	var playlist strings.Builder
	playlist.WriteString("#EXTM3U\n")
	playlist.WriteString("#EXT-X-VERSION:3\n")

	for _, quality := range job.Qualities {
		bandwidth := (quality.VideoBitrate + quality.AudioBitrate) * 1000
		playlist.WriteString(fmt.Sprintf("#EXT-X-STREAM-INF:BANDWIDTH=%d,RESOLUTION=%dx%d,NAME=\"%s\"\n",
			bandwidth, quality.Width, quality.Height, quality.Name))
		playlist.WriteString(fmt.Sprintf("%s/stream.m3u8\n", quality.Name))
	}

	masterPath := filepath.Join(job.OutputDir, "master.m3u8")
	return os.WriteFile(masterPath, []byte(playlist.String()), 0644)
}

// uploadTranscodedFiles uploads all transcoded files to storage.
func (s *videoService) uploadTranscodedFiles(ctx context.Context, job *core.TranscodeJob) error {
	// Walk the output directory and upload all files
	return filepath.Walk(job.OutputDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}

		// Skip input file
		if strings.HasSuffix(path, ".tmp") {
			return nil
		}

		// Calculate storage key
		relPath, err := filepath.Rel(job.OutputDir, path)
		if err != nil {
			return err
		}
		storageKey := fmt.Sprintf("hls/%s/%s", job.BlobHash, relPath)

		// Read file
		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read file %s: %w", path, err)
		}

		// Upload to storage
		if err := s.storage.Put(ctx, storageKey, bytes.NewReader(data), int64(len(data))); err != nil {
			return fmt.Errorf("upload %s: %w", storageKey, err)
		}

		return nil
	})
}

// updateJobStatus updates the job status and caches it.
func (s *videoService) updateJobStatus(job *core.TranscodeJob, status core.TranscodeStatus, progress int, errMsg string) {
	s.jobsMu.Lock()
	job.Status = status
	job.Progress = progress
	job.Error = errMsg
	job.UpdatedAt = time.Now().Unix()
	s.jobsMu.Unlock()

	// Cache job status
	if s.cache != nil {
		data, _ := json.Marshal(job)
		s.cache.Set(context.Background(), jobCachePrefix+job.BlobHash, data, jobCacheTTL)
	}
}

// GetTranscodeStatus returns the current status of a transcoding job.
func (s *videoService) GetTranscodeStatus(ctx context.Context, blobHash string) (*core.TranscodeJob, error) {
	// Check in-memory jobs first
	s.jobsMu.RLock()
	job, exists := s.jobs[blobHash]
	s.jobsMu.RUnlock()

	if exists {
		return job, nil
	}

	// Check cache
	if s.cache != nil {
		if data, ok := s.cache.Get(ctx, jobCachePrefix+blobHash); ok {
			var cachedJob core.TranscodeJob
			if err := json.Unmarshal(data, &cachedJob); err == nil {
				return &cachedJob, nil
			}
		}
	}

	// Check if transcoded files exist
	manifestKey := fmt.Sprintf("hls/%s/master.m3u8", blobHash)
	if exists, _ := s.storage.Exists(ctx, manifestKey); exists {
		return &core.TranscodeJob{
			ID:        blobHash,
			BlobHash:  blobHash,
			Status:    core.TranscodeStatusComplete,
			Progress:  100,
			Qualities: core.DefaultQualities,
		}, nil
	}

	return nil, core.ErrTranscodeNotFound
}

// GetHLSManifest returns the HLS manifest for a transcoded video.
func (s *videoService) GetHLSManifest(ctx context.Context, blobHash string) (*core.HLSManifest, error) {
	// Get master playlist
	masterKey := fmt.Sprintf("hls/%s/master.m3u8", blobHash)
	masterReader, err := s.storage.Get(ctx, masterKey)
	if err != nil {
		return nil, core.ErrTranscodeNotFound
	}
	defer masterReader.Close()

	var masterBuf bytes.Buffer
	if _, err := masterBuf.ReadFrom(masterReader); err != nil {
		return nil, fmt.Errorf("read master playlist: %w", err)
	}

	manifest := &core.HLSManifest{
		MasterPlaylist: masterBuf.String(),
		Variants:       make(map[string]string),
	}

	// Get variant playlists
	for _, quality := range core.DefaultQualities {
		variantKey := fmt.Sprintf("hls/%s/%s/stream.m3u8", blobHash, quality.Name)
		variantReader, err := s.storage.Get(ctx, variantKey)
		if err != nil {
			continue // Skip missing qualities
		}

		var variantBuf bytes.Buffer
		variantBuf.ReadFrom(variantReader)
		variantReader.Close()

		manifest.Variants[quality.Name] = variantBuf.String()
	}

	return manifest, nil
}

// GetSegment returns a video segment file.
func (s *videoService) GetSegment(ctx context.Context, blobHash, quality, segmentName string) ([]byte, error) {
	segmentKey := fmt.Sprintf("hls/%s/%s/%s", blobHash, quality, segmentName)

	reader, err := s.storage.Get(ctx, segmentKey)
	if err != nil {
		return nil, fmt.Errorf("get segment: %w", err)
	}
	defer reader.Close()

	var buf bytes.Buffer
	if _, err := buf.ReadFrom(reader); err != nil {
		return nil, fmt.Errorf("read segment: %w", err)
	}

	return buf.Bytes(), nil
}

// DeleteTranscodedFiles removes all transcoded files for a blob.
func (s *videoService) DeleteTranscodedFiles(ctx context.Context, blobHash string) error {
	// Delete HLS files
	for _, quality := range core.DefaultQualities {
		// Delete playlist
		playlistKey := fmt.Sprintf("hls/%s/%s/stream.m3u8", blobHash, quality.Name)
		_ = s.storage.Delete(ctx, playlistKey)

		// Delete segments (assume max 1000 segments)
		for i := 0; i < 1000; i++ {
			segmentKey := fmt.Sprintf("hls/%s/%s/segment_%03d.ts", blobHash, quality.Name, i)
			if err := s.storage.Delete(ctx, segmentKey); err != nil {
				break // No more segments
			}
		}
	}

	// Delete HLS master playlist
	masterKey := fmt.Sprintf("hls/%s/master.m3u8", blobHash)
	_ = s.storage.Delete(ctx, masterKey)

	// Delete DASH files
	dashMpdKey := fmt.Sprintf("dash/%s/manifest.mpd", blobHash)
	_ = s.storage.Delete(ctx, dashMpdKey)

	// Delete DASH init and segment files
	for i := 0; i < 1000; i++ {
		// Init segments
		initKey := fmt.Sprintf("dash/%s/init-stream%d.m4s", blobHash, i)
		_ = s.storage.Delete(ctx, initKey)

		// Media segments
		for j := 0; j < 1000; j++ {
			segmentKey := fmt.Sprintf("dash/%s/chunk-stream%d-%05d.m4s", blobHash, i, j+1)
			if err := s.storage.Delete(ctx, segmentKey); err != nil {
				break
			}
		}
	}

	// Remove from jobs
	s.jobsMu.Lock()
	delete(s.jobs, blobHash)
	s.jobsMu.Unlock()

	// Remove from cache
	if s.cache != nil {
		s.cache.Delete(ctx, jobCachePrefix+blobHash)
	}

	// Clean up local work directory
	outputDir := filepath.Join(s.workDir, blobHash)
	os.RemoveAll(outputDir)

	return nil
}

// transcodeToDASH transcodes video to DASH format with all qualities in one pass.
func (s *videoService) transcodeToDASH(inputPath string, job *core.TranscodeJob) error {
	dashDir := filepath.Join(job.OutputDir, "dash")
	if err := os.MkdirAll(dashDir, 0755); err != nil {
		return fmt.Errorf("create DASH directory: %w", err)
	}

	outputPath := filepath.Join(dashDir, "manifest.mpd")

	// Build FFmpeg command for multi-bitrate DASH
	// Use filter_complex for multiple outputs
	args := []string{
		"-i", inputPath,
	}

	// Add video streams for each quality
	var maps []string
	var adaptationSet []string

	for i, quality := range job.Qualities {
		// Video filter for this quality
		args = append(args,
			"-map", "0:v:0",
			"-map", "0:a:0",
		)
		maps = append(maps, fmt.Sprintf("-c:v:%d", i), "libx264",
			fmt.Sprintf("-b:v:%d", i), fmt.Sprintf("%dk", quality.VideoBitrate),
			fmt.Sprintf("-s:v:%d", i), fmt.Sprintf("%dx%d", quality.Width, quality.Height),
			fmt.Sprintf("-c:a:%d", i), "aac",
			fmt.Sprintf("-b:a:%d", i), fmt.Sprintf("%dk", quality.AudioBitrate),
		)
		adaptationSet = append(adaptationSet, fmt.Sprintf("id=%d,streams=%d", i, i))
	}

	args = append(args, maps...)
	args = append(args,
		"-preset", "veryfast",
		"-f", "dash",
		"-seg_duration", fmt.Sprintf("%d", dashSegmentDuration),
		"-use_template", "1",
		"-use_timeline", "1",
		"-init_seg_name", "init-stream$RepresentationID$.m4s",
		"-media_seg_name", "chunk-stream$RepresentationID$-$Number%05d$.m4s",
		"-adaptation_sets", strings.Join(adaptationSet, " "),
		"-y",
		outputPath,
	)

	cmd := exec.Command(s.ffmpegPath, args...)

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("ffmpeg DASH error: %v, stderr: %s", err, stderr.String())
	}

	return nil
}

// GetDASHManifest returns the DASH manifest for a transcoded video.
func (s *videoService) GetDASHManifest(ctx context.Context, blobHash string) (*core.DASHManifest, error) {
	mpdKey := fmt.Sprintf("dash/%s/manifest.mpd", blobHash)
	reader, err := s.storage.Get(ctx, mpdKey)
	if err != nil {
		return nil, core.ErrTranscodeNotFound
	}
	defer reader.Close()

	var buf bytes.Buffer
	if _, err := buf.ReadFrom(reader); err != nil {
		return nil, fmt.Errorf("read MPD: %w", err)
	}

	return &core.DASHManifest{
		MPD: buf.String(),
	}, nil
}

// GetDASHSegment returns a DASH segment file (.m4s or init.mp4).
func (s *videoService) GetDASHSegment(ctx context.Context, blobHash, segmentName string) ([]byte, error) {
	segmentKey := fmt.Sprintf("dash/%s/%s", blobHash, segmentName)

	reader, err := s.storage.Get(ctx, segmentKey)
	if err != nil {
		return nil, fmt.Errorf("get DASH segment: %w", err)
	}
	defer reader.Close()

	var buf bytes.Buffer
	if _, err := buf.ReadFrom(reader); err != nil {
		return nil, fmt.Errorf("read DASH segment: %w", err)
	}

	return buf.Bytes(), nil
}
