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
	hwAccel      core.HWAccelConfig
	encoder      string          // detected encoder (libx264, h264_nvenc, hevc_nvenc, etc.)
	codec        core.VideoCodec // selected codec (h264, hevc, av1)
}

// VideoConfig holds configuration for the video service.
type VideoConfig struct {
	WorkDir    string            // Directory for temporary transcoding files
	FFmpegPath string            // Path to FFmpeg binary (empty = auto-detect)
	CDNBaseUrl string            // Base URL for serving segments
	HWAccel    core.HWAccelConfig // Hardware acceleration configuration
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

	// Detect and configure encoder based on codec and hardware
	codec := conf.HWAccel.Codec
	if codec == "" {
		codec = core.CodecH264 // default to H.264 for best compatibility
	}
	encoder := detectEncoder(ffmpegPath, conf.HWAccel, codec, log)

	svc := &videoService{
		storage:    storageBackend,
		cache:      appCache,
		workDir:    workDir,
		ffmpegPath: ffmpegPath,
		log:        log,
		jobs:       make(map[string]*core.TranscodeJob),
		cdnBaseUrl: conf.CDNBaseUrl,
		hwAccel:    conf.HWAccel,
		encoder:    encoder,
		codec:      codec,
	}

	log.Info("video service initialized",
		zap.String("encoder", encoder),
		zap.String("codec", string(codec)),
		zap.String("hwaccel_type", string(conf.HWAccel.Type)))

	return svc, nil
}

// detectEncoder determines the best available encoder based on configuration, codec, and hardware.
func detectEncoder(ffmpegPath string, hwAccel core.HWAccelConfig, codec core.VideoCodec, log *zap.Logger) string {
	// Get software fallback encoder for this codec
	softwareEncoder := getSoftwareEncoder(codec)

	// If hardware acceleration is explicitly disabled, use software
	if hwAccel.Type == core.HWAccelNone || hwAccel.Type == "" {
		return softwareEncoder
	}

	// If specific type requested, try that first
	switch hwAccel.Type {
	case core.HWAccelNVENC:
		encoder := getNVENCEncoder(codec)
		if checkEncoderAvailable(ffmpegPath, encoder) {
			log.Info("using NVIDIA NVENC hardware encoder",
				zap.String("encoder", encoder),
				zap.String("codec", string(codec)))
			return encoder
		}
		log.Warn("NVENC requested but not available, falling back to software",
			zap.String("codec", string(codec)))
		return softwareEncoder

	case core.HWAccelQSV:
		encoder := getQSVEncoder(codec)
		if checkEncoderAvailable(ffmpegPath, encoder) {
			log.Info("using Intel Quick Sync Video hardware encoder",
				zap.String("encoder", encoder),
				zap.String("codec", string(codec)))
			return encoder
		}
		log.Warn("QSV requested but not available, falling back to software",
			zap.String("codec", string(codec)))
		return softwareEncoder

	case core.HWAccelVAAPI:
		encoder := getVAAPIEncoder(codec)
		if checkEncoderAvailable(ffmpegPath, encoder) {
			log.Info("using VAAPI hardware encoder",
				zap.String("encoder", encoder),
				zap.String("codec", string(codec)))
			return encoder
		}
		log.Warn("VAAPI requested but not available, falling back to software",
			zap.String("codec", string(codec)))
		return softwareEncoder

	case core.HWAccelAuto:
		// Try encoders in order of preference for the selected codec
		encoders := getEncodersByPriority(codec)

		for _, e := range encoders {
			if checkEncoderAvailable(ffmpegPath, e.encoder) {
				log.Info("auto-detected hardware encoder",
					zap.String("name", e.name),
					zap.String("encoder", e.encoder),
					zap.String("codec", string(codec)))
				return e.encoder
			}
		}

		log.Info("no hardware encoder available, using software encoding",
			zap.String("codec", string(codec)))
		return softwareEncoder

	default:
		return softwareEncoder
	}
}

// getSoftwareEncoder returns the software encoder for a given codec.
func getSoftwareEncoder(codec core.VideoCodec) string {
	switch codec {
	case core.CodecHEVC:
		return "libx265"
	case core.CodecAV1:
		return "libsvtav1" // SVT-AV1 is faster than libaom-av1
	default:
		return "libx264"
	}
}

// getNVENCEncoder returns the NVENC encoder for a given codec.
func getNVENCEncoder(codec core.VideoCodec) string {
	switch codec {
	case core.CodecHEVC:
		return "hevc_nvenc"
	case core.CodecAV1:
		return "av1_nvenc" // Requires RTX 40-series or newer
	default:
		return "h264_nvenc"
	}
}

// getQSVEncoder returns the QSV encoder for a given codec.
func getQSVEncoder(codec core.VideoCodec) string {
	switch codec {
	case core.CodecHEVC:
		return "hevc_qsv"
	case core.CodecAV1:
		return "av1_qsv" // Requires Intel Arc or 12th+ gen
	default:
		return "h264_qsv"
	}
}

// getVAAPIEncoder returns the VAAPI encoder for a given codec.
func getVAAPIEncoder(codec core.VideoCodec) string {
	switch codec {
	case core.CodecHEVC:
		return "hevc_vaapi"
	case core.CodecAV1:
		return "av1_vaapi" // Limited hardware support
	default:
		return "h264_vaapi"
	}
}

// encoderInfo holds information about an encoder for auto-detection.
type encoderInfo struct {
	name    string
	encoder string
}

// getEncodersByPriority returns encoders in priority order for auto-detection.
func getEncodersByPriority(codec core.VideoCodec) []encoderInfo {
	switch codec {
	case core.CodecHEVC:
		return []encoderInfo{
			{"NVIDIA NVENC HEVC", "hevc_nvenc"},
			{"Intel QSV HEVC", "hevc_qsv"},
			{"VAAPI HEVC", "hevc_vaapi"},
		}
	case core.CodecAV1:
		return []encoderInfo{
			{"NVIDIA NVENC AV1", "av1_nvenc"},
			{"Intel QSV AV1", "av1_qsv"},
			{"VAAPI AV1", "av1_vaapi"},
		}
	default:
		return []encoderInfo{
			{"NVIDIA NVENC H.264", "h264_nvenc"},
			{"Intel QSV H.264", "h264_qsv"},
			{"VAAPI H.264", "h264_vaapi"},
		}
	}
}

// checkEncoderAvailable tests if an FFmpeg encoder is available.
func checkEncoderAvailable(ffmpegPath, encoder string) bool {
	if ffmpegPath == "" {
		return false
	}

	// Use ffmpeg -encoders to list available encoders
	cmd := exec.Command(ffmpegPath, "-hide_banner", "-encoders")
	output, err := cmd.Output()
	if err != nil {
		return false
	}

	// Check if encoder is in the output
	return strings.Contains(string(output), encoder)
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

	// Use default qualities if none specified (codec-specific)
	if len(qualities) == 0 {
		qualities = core.GetDefaultQualities(s.codec)
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

	// Build FFmpeg command with hardware acceleration support
	args := s.buildTranscodeArgs(inputPath, outputPath, outputDir, quality)

	cmd := exec.Command(s.ffmpegPath, args...)

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("ffmpeg error: %v, stderr: %s", err, stderr.String())
	}

	return nil
}

// buildTranscodeArgs builds FFmpeg arguments based on encoder configuration.
func (s *videoService) buildTranscodeArgs(inputPath, outputPath, outputDir string, quality core.VideoQuality) []string {
	var args []string

	// Add hardware decode acceleration if using GPU encoder
	switch s.encoder {
	case "h264_nvenc", "hevc_nvenc", "av1_nvenc":
		// Use CUDA for hardware decoding with NVENC
		args = append(args, "-hwaccel", "cuda", "-hwaccel_output_format", "cuda")
	case "h264_qsv", "hevc_qsv", "av1_qsv":
		// Use QSV for hardware decoding with QSV encoder
		args = append(args, "-hwaccel", "qsv", "-hwaccel_output_format", "qsv")
	case "h264_vaapi", "hevc_vaapi", "av1_vaapi":
		// Use VAAPI device for decoding
		device := s.hwAccel.Device
		if device == "" {
			device = "/dev/dri/renderD128"
		}
		args = append(args, "-vaapi_device", device, "-hwaccel", "vaapi", "-hwaccel_output_format", "vaapi")
	}

	// Input file
	args = append(args, "-i", inputPath)

	// Video codec and encoder-specific settings
	args = append(args, "-c:v", s.encoder)

	// Encoder-specific quality settings
	switch s.encoder {
	// NVENC encoders (NVIDIA)
	case "h264_nvenc", "hevc_nvenc", "av1_nvenc":
		preset := s.hwAccel.Preset
		if preset == "" {
			preset = "p4" // balanced preset (p1=fastest, p7=slowest/best quality)
		}
		args = append(args,
			"-preset", preset,
			"-rc", "vbr", // Variable bitrate
			"-cq", "23",  // Constant quality (similar to CRF)
		)
		if s.hwAccel.LookAhead > 0 {
			// Clamp to maximum supported by most NVENC models
			lookAhead := s.hwAccel.LookAhead
			if lookAhead > 32 {
				lookAhead = 32
			}
			args = append(args, "-rc-lookahead", fmt.Sprintf("%d", lookAhead))
		}

	// QSV encoders (Intel)
	case "h264_qsv", "hevc_qsv", "av1_qsv":
		preset := s.hwAccel.Preset
		if preset == "" {
			preset = "medium"
		}
		args = append(args,
			"-preset", preset,
			"-global_quality", "23",
		)

	// VAAPI encoders (Linux AMD/Intel)
	case "h264_vaapi", "hevc_vaapi", "av1_vaapi":
		args = append(args,
			"-qp", "23", // Quantization parameter
		)

	// Software x265 (HEVC)
	case "libx265":
		preset := s.hwAccel.Preset
		if preset == "" {
			preset = "medium" // x265 presets: ultrafast, superfast, veryfast, faster, fast, medium, slow, slower, veryslow
		}
		args = append(args,
			"-preset", preset,
			"-crf", "23",
			"-tag:v", "hvc1", // Required for Apple device compatibility
		)

	// Software SVT-AV1
	case "libsvtav1":
		preset := s.hwAccel.Preset
		if preset == "" {
			preset = "6" // SVT-AV1 presets 0-13 (0=slowest/best, 13=fastest)
		}
		args = append(args,
			"-preset", preset,
			"-crf", "30", // AV1 CRF scale differs from H.264/HEVC
			"-svtav1-params", "tune=0", // Visual quality tuning
		)

	// Software libaom-av1 (slower but higher quality reference encoder)
	case "libaom-av1":
		args = append(args,
			"-cpu-used", "4", // 0-8, higher = faster
			"-crf", "30",
			"-b:v", "0", // Required for CRF mode
		)

	// Software x264 (default)
	default:
		preset := s.hwAccel.Preset
		if preset == "" {
			preset = "veryfast"
		}
		args = append(args,
			"-preset", preset,
			"-crf", "23",
		)
	}

	// Video filter for scaling
	args = append(args, "-vf", s.buildScaleFilter(quality))

	// Bitrate settings
	args = append(args,
		"-b:v", fmt.Sprintf("%dk", quality.VideoBitrate),
		"-maxrate", fmt.Sprintf("%dk", int(float64(quality.VideoBitrate)*1.5)),
		"-bufsize", fmt.Sprintf("%dk", quality.VideoBitrate*2),
	)

	// Audio settings
	args = append(args,
		"-c:a", "aac",
		"-b:a", fmt.Sprintf("%dk", quality.AudioBitrate),
		"-ar", "44100",
	)

	// HLS output settings
	args = append(args,
		"-f", "hls",
		"-hls_time", fmt.Sprintf("%d", hlsSegmentDuration),
		"-hls_playlist_type", "vod",
		"-hls_segment_filename", filepath.Join(outputDir, "segment_%03d.ts"),
		"-y",
		outputPath,
	)

	return args
}

// buildScaleFilter builds the appropriate scale filter based on encoder.
func (s *videoService) buildScaleFilter(quality core.VideoQuality) string {
	switch s.encoder {
	case "h264_nvenc", "hevc_nvenc", "av1_nvenc":
		// CUDA scale filter for NVENC - stays on GPU entirely
		return fmt.Sprintf("scale_cuda=%d:%d:force_original_aspect_ratio=decrease",
			quality.Width, quality.Height)
	case "h264_qsv", "hevc_qsv", "av1_qsv":
		// QSV scale filter
		return fmt.Sprintf("scale_qsv=w=%d:h=%d",
			quality.Width, quality.Height)
	case "h264_vaapi", "hevc_vaapi", "av1_vaapi":
		// VAAPI scale filter
		return fmt.Sprintf("scale_vaapi=w=%d:h=%d",
			quality.Width, quality.Height)
	default:
		// Software scale filter with padding for aspect ratio preservation
		return fmt.Sprintf("scale=%d:%d:force_original_aspect_ratio=decrease,pad=%d:%d:(ow-iw)/2:(oh-ih)/2",
			quality.Width, quality.Height, quality.Width, quality.Height)
	}
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
			Qualities: core.GetDefaultQualities(s.codec),
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

	masterPlaylist := masterBuf.String()

	// Check for subtitles and inject them into the manifest
	subtitles, _ := s.ListSubtitles(ctx, blobHash)
	if len(subtitles) > 0 {
		masterPlaylist = s.injectHLSSubtitles(masterPlaylist, subtitles)
	}

	manifest := &core.HLSManifest{
		MasterPlaylist: masterPlaylist,
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

// injectHLSSubtitles adds subtitle tracks to an HLS master playlist.
func (s *videoService) injectHLSSubtitles(playlist string, subtitles []core.SubtitleTrack) string {
	lines := strings.Split(playlist, "\n")
	var result []string

	// Find where to insert subtitle declarations (after #EXTM3U and #EXT-X-VERSION)
	insertIdx := 0
	for i, line := range lines {
		if strings.HasPrefix(line, "#EXTM3U") || strings.HasPrefix(line, "#EXT-X-VERSION") {
			insertIdx = i + 1
		}
	}

	// Build subtitle media declarations
	var subtitleLines []string
	for _, sub := range subtitles {
		defaultVal := "NO"
		if sub.Default {
			defaultVal = "YES"
		}
		forcedVal := "NO"
		if sub.Forced {
			forcedVal = "YES"
		}

		subtitleLines = append(subtitleLines, fmt.Sprintf(
			"#EXT-X-MEDIA:TYPE=SUBTITLES,GROUP-ID=\"subs\",NAME=\"%s\",DEFAULT=%s,AUTOSELECT=YES,FORCED=%s,LANGUAGE=\"%s\",URI=\"subtitles/%s.vtt\"",
			sub.Label, defaultVal, forcedVal, sub.Language, sub.Language,
		))
	}

	// Insert subtitle lines and update stream-inf to reference subtitles
	for i, line := range lines {
		if i == insertIdx && len(subtitleLines) > 0 {
			result = append(result, subtitleLines...)
		}
		// Add SUBTITLES reference to stream-inf lines
		if strings.HasPrefix(line, "#EXT-X-STREAM-INF:") && len(subtitles) > 0 {
			if !strings.Contains(line, "SUBTITLES=") {
				line = strings.TrimSuffix(line, "\n") + ",SUBTITLES=\"subs\""
			}
		}
		result = append(result, line)
	}

	return strings.Join(result, "\n")
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

	// Build FFmpeg command for multi-bitrate DASH with hardware acceleration
	args := s.buildDASHArgs(inputPath, outputPath, job.Qualities)

	cmd := exec.Command(s.ffmpegPath, args...)

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("ffmpeg DASH error: %v, stderr: %s", err, stderr.String())
	}

	return nil
}

// buildDASHArgs builds FFmpeg arguments for DASH transcoding with hardware acceleration.
func (s *videoService) buildDASHArgs(inputPath, outputPath string, qualities []core.VideoQuality) []string {
	var args []string

	// Add hardware decode acceleration if using GPU encoder
	switch s.encoder {
	case "h264_nvenc", "hevc_nvenc", "av1_nvenc":
		args = append(args, "-hwaccel", "cuda")
	case "h264_qsv", "hevc_qsv", "av1_qsv":
		args = append(args, "-hwaccel", "qsv")
	case "h264_vaapi", "hevc_vaapi", "av1_vaapi":
		device := s.hwAccel.Device
		if device == "" {
			device = "/dev/dri/renderD128"
		}
		args = append(args, "-vaapi_device", device, "-hwaccel", "vaapi")
	}

	args = append(args, "-i", inputPath)

	// Add video streams for each quality
	var maps []string
	var adaptationSet []string

	for i, quality := range qualities {
		args = append(args,
			"-map", "0:v:0",
			"-map", "0:a:0",
		)
		maps = append(maps,
			fmt.Sprintf("-c:v:%d", i), s.encoder,
			fmt.Sprintf("-b:v:%d", i), fmt.Sprintf("%dk", quality.VideoBitrate),
			fmt.Sprintf("-s:v:%d", i), fmt.Sprintf("%dx%d", quality.Width, quality.Height),
			fmt.Sprintf("-c:a:%d", i), "aac",
			fmt.Sprintf("-b:a:%d", i), fmt.Sprintf("%dk", quality.AudioBitrate),
		)
		adaptationSet = append(adaptationSet, fmt.Sprintf("id=%d,streams=%d", i, i))
	}

	args = append(args, maps...)

	// Add encoder-specific quality settings
	switch s.encoder {
	// NVENC encoders (NVIDIA)
	case "h264_nvenc", "hevc_nvenc", "av1_nvenc":
		preset := s.hwAccel.Preset
		if preset == "" {
			preset = "p4"
		}
		args = append(args, "-preset", preset, "-rc", "vbr", "-cq", "23")

	// QSV encoders (Intel)
	case "h264_qsv", "hevc_qsv", "av1_qsv":
		preset := s.hwAccel.Preset
		if preset == "" {
			preset = "medium"
		}
		args = append(args, "-preset", preset, "-global_quality", "23")

	// VAAPI encoders (Linux AMD/Intel)
	case "h264_vaapi", "hevc_vaapi", "av1_vaapi":
		args = append(args, "-qp", "23")

	// Software x265 (HEVC)
	case "libx265":
		preset := s.hwAccel.Preset
		if preset == "" {
			preset = "medium"
		}
		args = append(args, "-preset", preset, "-tag:v", "hvc1")

	// Software SVT-AV1
	case "libsvtav1":
		preset := s.hwAccel.Preset
		if preset == "" {
			preset = "6"
		}
		args = append(args, "-preset", preset)

	// Software libaom-av1
	case "libaom-av1":
		args = append(args, "-cpu-used", "4")

	// Software x264 (default)
	default:
		preset := s.hwAccel.Preset
		if preset == "" {
			preset = "veryfast"
		}
		args = append(args, "-preset", preset)
	}

	args = append(args,
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

	return args
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

	mpd := buf.String()

	// Check for subtitles and inject them into the MPD
	subtitles, _ := s.ListSubtitles(ctx, blobHash)
	if len(subtitles) > 0 {
		mpd = s.injectDASHSubtitles(mpd, subtitles)
	}

	return &core.DASHManifest{
		MPD: mpd,
	}, nil
}

// injectDASHSubtitles adds subtitle tracks to a DASH MPD.
func (s *videoService) injectDASHSubtitles(mpd string, subtitles []core.SubtitleTrack) string {
	// Find the closing </Period> tag and insert subtitle AdaptationSets before it
	closingPeriod := "</Period>"
	idx := strings.LastIndex(mpd, closingPeriod)
	if idx == -1 {
		return mpd
	}

	var subtitleSets []string
	for i, sub := range subtitles {
		subtitleSets = append(subtitleSets, fmt.Sprintf(`    <AdaptationSet id="%d" contentType="text" mimeType="text/vtt" lang="%s">
      <Label>%s</Label>
      <Representation id="subtitle_%s" bandwidth="256">
        <BaseURL>subtitles/%s.vtt</BaseURL>
      </Representation>
    </AdaptationSet>`,
			100+i, sub.Language, sub.Label, sub.Language, sub.Language,
		))
	}

	// Insert subtitle AdaptationSets before </Period>
	return mpd[:idx] + strings.Join(subtitleSets, "\n") + "\n  " + mpd[idx:]
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

// AddSubtitle adds a subtitle track to a video.
func (s *videoService) AddSubtitle(ctx context.Context, blobHash string, subtitle core.Subtitle, content []byte) error {
	// Validate WebVTT format
	if !isValidWebVTT(content) {
		return core.ErrInvalidSubtitleFormat
	}

	// Store subtitle file
	subtitleKey := fmt.Sprintf("subtitles/%s/%s.vtt", blobHash, subtitle.Language)
	if err := s.storage.Put(ctx, subtitleKey, bytes.NewReader(content), int64(len(content))); err != nil {
		return fmt.Errorf("store subtitle: %w", err)
	}

	// Store subtitle metadata
	track := core.SubtitleTrack{
		Subtitle:  subtitle,
		BlobHash:  blobHash,
		CreatedAt: time.Now().Unix(),
	}

	metaKey := fmt.Sprintf("subtitles/%s/%s.json", blobHash, subtitle.Language)
	metaData, _ := json.Marshal(track)
	if err := s.storage.Put(ctx, metaKey, bytes.NewReader(metaData), int64(len(metaData))); err != nil {
		return fmt.Errorf("store subtitle metadata: %w", err)
	}

	s.log.Info("subtitle added",
		zap.String("hash", blobHash),
		zap.String("language", subtitle.Language),
		zap.String("label", subtitle.Label))

	return nil
}

// GetSubtitle retrieves a subtitle track for a video.
func (s *videoService) GetSubtitle(ctx context.Context, blobHash, language string) ([]byte, error) {
	subtitleKey := fmt.Sprintf("subtitles/%s/%s.vtt", blobHash, language)

	reader, err := s.storage.Get(ctx, subtitleKey)
	if err != nil {
		return nil, core.ErrSubtitleNotFound
	}
	defer reader.Close()

	var buf bytes.Buffer
	if _, err := buf.ReadFrom(reader); err != nil {
		return nil, fmt.Errorf("read subtitle: %w", err)
	}

	return buf.Bytes(), nil
}

// ListSubtitles returns all subtitle tracks for a video.
func (s *videoService) ListSubtitles(ctx context.Context, blobHash string) ([]core.SubtitleTrack, error) {
	var tracks []core.SubtitleTrack

	// Common languages to check for
	languages := []string{"en", "es", "fr", "de", "it", "pt", "ru", "ja", "ko", "zh", "ar", "hi", "nl", "pl", "tr", "vi", "th", "id", "sv", "da", "no", "fi"}

	for _, lang := range languages {
		metaKey := fmt.Sprintf("subtitles/%s/%s.json", blobHash, lang)
		reader, err := s.storage.Get(ctx, metaKey)
		if err != nil {
			continue // Subtitle doesn't exist for this language
		}

		var buf bytes.Buffer
		buf.ReadFrom(reader)
		reader.Close()

		var track core.SubtitleTrack
		if err := json.Unmarshal(buf.Bytes(), &track); err == nil {
			tracks = append(tracks, track)
		}
	}

	return tracks, nil
}

// DeleteSubtitle removes a subtitle track from a video.
func (s *videoService) DeleteSubtitle(ctx context.Context, blobHash, language string) error {
	// Delete subtitle file
	subtitleKey := fmt.Sprintf("subtitles/%s/%s.vtt", blobHash, language)
	_ = s.storage.Delete(ctx, subtitleKey)

	// Delete metadata
	metaKey := fmt.Sprintf("subtitles/%s/%s.json", blobHash, language)
	_ = s.storage.Delete(ctx, metaKey)

	s.log.Info("subtitle deleted",
		zap.String("hash", blobHash),
		zap.String("language", language))

	return nil
}

// isValidWebVTT checks if the content is valid WebVTT format.
func isValidWebVTT(content []byte) bool {
	// WebVTT files must start with "WEBVTT" (with optional BOM)
	text := string(content)

	// Remove BOM if present
	if strings.HasPrefix(text, "\ufeff") {
		text = strings.TrimPrefix(text, "\ufeff")
	}

	// Check for WEBVTT signature
	// Per spec, "WEBVTT" must be followed by space, tab, newline, or EOF
	if !strings.HasPrefix(text, "WEBVTT") {
		return false
	}

	// Check character after "WEBVTT"
	if len(text) == 6 {
		return true // Just "WEBVTT" is valid
	}

	nextChar := text[6]
	return nextChar == ' ' || nextChar == '\t' || nextChar == '\n' || nextChar == '\r'
}
