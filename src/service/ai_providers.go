package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"go.uber.org/zap"

	"git.coldforge.xyz/coldforge/cloistr-blossom/src/core"
)

// HashBlocklistProvider is a fast local provider that checks against known bad hashes.
// This is useful for NCMEC/PhotoDNA hash lists.
type HashBlocklistProvider struct {
	name       string
	hashList   map[string]string // hash -> reason
	log        *zap.Logger
}

// NewHashBlocklistProvider creates a new hash blocklist provider.
func NewHashBlocklistProvider(log *zap.Logger) *HashBlocklistProvider {
	return &HashBlocklistProvider{
		name:     "hash_blocklist",
		hashList: make(map[string]string),
		log:      log,
	}
}

// Name returns the provider name.
func (p *HashBlocklistProvider) Name() string {
	return p.name
}

// SupportedMimeTypes returns MIME types this provider can scan.
func (p *HashBlocklistProvider) SupportedMimeTypes() []string {
	// Hash checking works for any file type
	return []string{
		"image/",
		"video/",
	}
}

// SupportedCategories returns content categories this provider detects.
func (p *HashBlocklistProvider) SupportedCategories() []core.ContentCategory {
	return []core.ContentCategory{
		core.CategoryCSAM,
		core.CategoryCopyrightRisk,
	}
}

// AddHash adds a hash to the blocklist.
func (p *HashBlocklistProvider) AddHash(hash, reason string) {
	p.hashList[strings.ToLower(hash)] = reason
}

// LoadHashList loads hashes from a list (format: hash,reason per line).
func (p *HashBlocklistProvider) LoadHashList(data []byte) error {
	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, ",", 2)
		if len(parts) >= 1 {
			hash := strings.ToLower(strings.TrimSpace(parts[0]))
			reason := "blocklist match"
			if len(parts) >= 2 {
				reason = strings.TrimSpace(parts[1])
			}
			p.hashList[hash] = reason
		}
	}
	p.log.Info("loaded hash blocklist", zap.Int("count", len(p.hashList)))
	return nil
}

// Scan performs hash matching.
func (p *HashBlocklistProvider) Scan(ctx context.Context, req *core.ScanRequest) (*core.ScanResult, error) {
	start := time.Now()

	// Check the content hash
	contentHash := req.Hash
	if contentHash == "" && len(req.Data) > 0 {
		h := sha256.Sum256(req.Data)
		contentHash = hex.EncodeToString(h[:])
	}

	result := &core.ScanResult{
		Hash:         req.Hash,
		Provider:     p.name,
		Detections:   []core.ContentDetection{},
		ScanDuration: time.Since(start),
		ScannedAt:    time.Now().Unix(),
	}

	// Check against blocklist
	if reason, found := p.hashList[strings.ToLower(contentHash)]; found {
		result.Detections = append(result.Detections, core.ContentDetection{
			Category:    core.CategoryCSAM,
			Confidence:  100,
			Description: reason,
		})
		result.RecommendedAction = core.ScanActionBlock
		result.Confidence = core.ConfidenceVeryHigh
		return result, nil
	}

	// Also check perceptual hashes if available (pHash, dHash)
	// This would require computing perceptual hashes and comparing
	// For now, just do exact hash matching

	result.RecommendedAction = core.ScanActionAllow
	result.Confidence = core.ConfidenceVeryLow
	return result, nil
}

// IsAvailable checks if the provider is operational.
func (p *HashBlocklistProvider) IsAvailable(ctx context.Context) bool {
	return true
}

// Ensure interface compliance
var _ core.AIContentProvider = (*HashBlocklistProvider)(nil)

// -------------------------------------------------------------------
// AWS Rekognition Provider
// -------------------------------------------------------------------

// AWSRekognitionProvider uses AWS Rekognition for content moderation.
type AWSRekognitionProvider struct {
	name        string
	accessKey   string
	secretKey   string
	region      string
	httpClient  *http.Client
	log         *zap.Logger
}

// NewAWSRekognitionProvider creates a new AWS Rekognition provider.
func NewAWSRekognitionProvider(accessKey, secretKey, region string, log *zap.Logger) *AWSRekognitionProvider {
	return &AWSRekognitionProvider{
		name:       "aws_rekognition",
		accessKey:  accessKey,
		secretKey:  secretKey,
		region:     region,
		httpClient: &http.Client{Timeout: 30 * time.Second},
		log:        log,
	}
}

// Name returns the provider name.
func (p *AWSRekognitionProvider) Name() string {
	return p.name
}

// SupportedMimeTypes returns MIME types this provider can scan.
func (p *AWSRekognitionProvider) SupportedMimeTypes() []string {
	return []string{
		"image/jpeg",
		"image/png",
	}
}

// SupportedCategories returns content categories this provider detects.
func (p *AWSRekognitionProvider) SupportedCategories() []core.ContentCategory {
	return []core.ContentCategory{
		core.CategoryExplicitAdult,
		core.CategoryViolence,
		core.CategoryHate,
		core.CategoryDrugs,
		core.CategoryWeapons,
	}
}

// Scan performs content moderation using AWS Rekognition.
func (p *AWSRekognitionProvider) Scan(ctx context.Context, req *core.ScanRequest) (*core.ScanResult, error) {
	start := time.Now()

	result := &core.ScanResult{
		Hash:         req.Hash,
		Provider:     p.name,
		Detections:   []core.ContentDetection{},
		ScanDuration: time.Since(start),
		ScannedAt:    time.Now().Unix(),
	}

	if p.accessKey == "" || p.secretKey == "" {
		result.Error = "AWS credentials not configured"
		result.RecommendedAction = core.ScanActionAllow
		return result, nil
	}

	// In a real implementation, you would:
	// 1. Create a Rekognition client using the AWS SDK
	// 2. Call DetectModerationLabels
	// 3. Parse the response and map to our categories

	// Stub implementation - returns no detections
	// Real implementation would use:
	// cfg, _ := config.LoadDefaultConfig(ctx, config.WithRegion(p.region))
	// client := rekognition.NewFromConfig(cfg)
	// output, _ := client.DetectModerationLabels(ctx, &rekognition.DetectModerationLabelsInput{
	//     Image: &types.Image{Bytes: req.Data},
	// })

	p.log.Debug("AWS Rekognition scan (stub)",
		zap.String("hash", req.Hash),
		zap.String("mime_type", req.MimeType))

	result.ScanDuration = time.Since(start)
	result.RecommendedAction = core.ScanActionAllow
	result.Confidence = core.ConfidenceVeryLow
	return result, nil
}

// IsAvailable checks if the provider is operational.
func (p *AWSRekognitionProvider) IsAvailable(ctx context.Context) bool {
	return p.accessKey != "" && p.secretKey != ""
}

// Ensure interface compliance
var _ core.AIContentProvider = (*AWSRekognitionProvider)(nil)

// -------------------------------------------------------------------
// Google Cloud Vision Provider
// -------------------------------------------------------------------

// GoogleVisionProvider uses Google Cloud Vision for content moderation.
type GoogleVisionProvider struct {
	name       string
	apiKey     string
	projectID  string
	httpClient *http.Client
	log        *zap.Logger
}

// NewGoogleVisionProvider creates a new Google Cloud Vision provider.
func NewGoogleVisionProvider(apiKey, projectID string, log *zap.Logger) *GoogleVisionProvider {
	return &GoogleVisionProvider{
		name:       "google_vision",
		apiKey:     apiKey,
		projectID:  projectID,
		httpClient: &http.Client{Timeout: 30 * time.Second},
		log:        log,
	}
}

// Name returns the provider name.
func (p *GoogleVisionProvider) Name() string {
	return p.name
}

// SupportedMimeTypes returns MIME types this provider can scan.
func (p *GoogleVisionProvider) SupportedMimeTypes() []string {
	return []string{
		"image/jpeg",
		"image/png",
		"image/gif",
		"image/webp",
	}
}

// SupportedCategories returns content categories this provider detects.
func (p *GoogleVisionProvider) SupportedCategories() []core.ContentCategory {
	return []core.ContentCategory{
		core.CategoryExplicitAdult,
		core.CategoryViolence,
		core.CategoryHate,
	}
}

// Scan performs content moderation using Google Cloud Vision.
func (p *GoogleVisionProvider) Scan(ctx context.Context, req *core.ScanRequest) (*core.ScanResult, error) {
	start := time.Now()

	result := &core.ScanResult{
		Hash:         req.Hash,
		Provider:     p.name,
		Detections:   []core.ContentDetection{},
		ScanDuration: time.Since(start),
		ScannedAt:    time.Now().Unix(),
	}

	if p.apiKey == "" {
		result.Error = "Google API key not configured"
		result.RecommendedAction = core.ScanActionAllow
		return result, nil
	}

	// In a real implementation, you would call the Vision API SafeSearch
	// See: https://cloud.google.com/vision/docs/detecting-safe-search

	p.log.Debug("Google Vision scan (stub)",
		zap.String("hash", req.Hash),
		zap.String("mime_type", req.MimeType))

	result.ScanDuration = time.Since(start)
	result.RecommendedAction = core.ScanActionAllow
	result.Confidence = core.ConfidenceVeryLow
	return result, nil
}

// IsAvailable checks if the provider is operational.
func (p *GoogleVisionProvider) IsAvailable(ctx context.Context) bool {
	return p.apiKey != ""
}

// Ensure interface compliance
var _ core.AIContentProvider = (*GoogleVisionProvider)(nil)

// -------------------------------------------------------------------
// Custom API Provider (for self-hosted models)
// -------------------------------------------------------------------

// CustomAPIProvider allows integration with custom moderation APIs.
type CustomAPIProvider struct {
	name       string
	endpoint   string
	apiKey     string
	mimeTypes  []string
	categories []core.ContentCategory
	httpClient *http.Client
	log        *zap.Logger
}

// NewCustomAPIProvider creates a new custom API provider.
func NewCustomAPIProvider(name, endpoint, apiKey string, mimeTypes []string, log *zap.Logger) *CustomAPIProvider {
	return &CustomAPIProvider{
		name:       name,
		endpoint:   endpoint,
		apiKey:     apiKey,
		mimeTypes:  mimeTypes,
		categories: []core.ContentCategory{core.CategoryCSAM, core.CategoryExplicitAdult, core.CategoryViolence},
		httpClient: &http.Client{Timeout: 60 * time.Second},
		log:        log,
	}
}

// Name returns the provider name.
func (p *CustomAPIProvider) Name() string {
	return p.name
}

// SupportedMimeTypes returns MIME types this provider can scan.
func (p *CustomAPIProvider) SupportedMimeTypes() []string {
	return p.mimeTypes
}

// SupportedCategories returns content categories this provider detects.
func (p *CustomAPIProvider) SupportedCategories() []core.ContentCategory {
	return p.categories
}

// CustomAPIRequest is the request format for custom APIs.
type CustomAPIRequest struct {
	Data     []byte `json:"data"`
	MimeType string `json:"mime_type"`
	Hash     string `json:"hash"`
}

// CustomAPIResponse is the response format for custom APIs.
type CustomAPIResponse struct {
	Detections []struct {
		Category   string  `json:"category"`
		Confidence float64 `json:"confidence"`
		Label      string  `json:"label"`
	} `json:"detections"`
	Error string `json:"error,omitempty"`
}

// Scan performs content moderation using a custom API.
func (p *CustomAPIProvider) Scan(ctx context.Context, req *core.ScanRequest) (*core.ScanResult, error) {
	start := time.Now()

	result := &core.ScanResult{
		Hash:       req.Hash,
		Provider:   p.name,
		Detections: []core.ContentDetection{},
		ScannedAt:  time.Now().Unix(),
	}

	if p.endpoint == "" {
		result.Error = "endpoint not configured"
		result.RecommendedAction = core.ScanActionAllow
		result.ScanDuration = time.Since(start)
		return result, nil
	}

	// Prepare request
	apiReq := CustomAPIRequest{
		Data:     req.Data,
		MimeType: req.MimeType,
		Hash:     req.Hash,
	}

	body, err := json.Marshal(apiReq)
	if err != nil {
		result.Error = fmt.Sprintf("failed to marshal request: %v", err)
		result.RecommendedAction = core.ScanActionAllow
		result.ScanDuration = time.Since(start)
		return result, nil
	}

	// Make HTTP request
	httpReq, err := http.NewRequestWithContext(ctx, "POST", p.endpoint, bytes.NewReader(body))
	if err != nil {
		result.Error = fmt.Sprintf("failed to create request: %v", err)
		result.RecommendedAction = core.ScanActionAllow
		result.ScanDuration = time.Since(start)
		return result, nil
	}

	httpReq.Header.Set("Content-Type", "application/json")
	if p.apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)
	}

	resp, err := p.httpClient.Do(httpReq)
	if err != nil {
		result.Error = fmt.Sprintf("request failed: %v", err)
		result.RecommendedAction = core.ScanActionAllow
		result.ScanDuration = time.Since(start)
		return result, nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		result.Error = fmt.Sprintf("API returned %d: %s", resp.StatusCode, string(respBody))
		result.RecommendedAction = core.ScanActionAllow
		result.ScanDuration = time.Since(start)
		return result, nil
	}

	// Parse response
	var apiResp CustomAPIResponse
	if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
		result.Error = fmt.Sprintf("failed to decode response: %v", err)
		result.RecommendedAction = core.ScanActionAllow
		result.ScanDuration = time.Since(start)
		return result, nil
	}

	// Map detections
	var maxConfidence float64
	for _, det := range apiResp.Detections {
		category := core.ContentCategory(det.Category)
		result.Detections = append(result.Detections, core.ContentDetection{
			Category:    category,
			Confidence:  det.Confidence,
			Description: det.Label,
		})
		if det.Confidence > maxConfidence {
			maxConfidence = det.Confidence
		}
	}

	result.Confidence = core.ConfidenceToLevel(maxConfidence)
	result.ScanDuration = time.Since(start)

	// Determine action based on detections
	if len(result.Detections) > 0 {
		for _, det := range result.Detections {
			if core.IsCriticalCategory(det.Category) && det.Confidence >= 80 {
				result.RecommendedAction = core.ScanActionBlock
				return result, nil
			}
		}
		if maxConfidence >= 50 {
			result.RecommendedAction = core.ScanActionQuarantine
		} else {
			result.RecommendedAction = core.ScanActionFlag
		}
	} else {
		result.RecommendedAction = core.ScanActionAllow
	}

	return result, nil
}

// IsAvailable checks if the provider is operational.
func (p *CustomAPIProvider) IsAvailable(ctx context.Context) bool {
	return p.endpoint != ""
}

// Ensure interface compliance
var _ core.AIContentProvider = (*CustomAPIProvider)(nil)
