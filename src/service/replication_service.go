package service

import (
	"context"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"git.coldforge.xyz/coldforge/cloistr-blossom/internal/storage"
	"git.coldforge.xyz/coldforge/cloistr-blossom/src/core"
)

type replicationService struct {
	config         core.ReplicationConfig
	primaryStorage storage.StorageBackend
	regionStorages map[string]storage.StorageBackend // regionID -> storage
	regions        map[string]*core.ReplicationRegion
	jobs           sync.Map // jobID -> *core.ReplicationJob
	jobQueue       chan *core.ReplicationJob
	blobRegions    sync.Map // blobHash -> []string (region IDs)
	regionHealth   sync.Map // regionID -> bool
	log            *zap.Logger
	stopCh         chan struct{}
	wg             sync.WaitGroup
}

// NewReplicationService creates a new replication service.
func NewReplicationService(
	primaryStorage storage.StorageBackend,
	config core.ReplicationConfig,
	log *zap.Logger,
) (core.ReplicationService, error) {
	svc := &replicationService{
		config:         config,
		primaryStorage: primaryStorage,
		regionStorages: make(map[string]storage.StorageBackend),
		regions:        make(map[string]*core.ReplicationRegion),
		jobQueue:       make(chan *core.ReplicationJob, config.BatchSize*2),
		log:            log,
		stopCh:         make(chan struct{}),
	}

	// Initialize region storage backends
	for i := range config.Regions {
		region := &config.Regions[i]
		if !region.Enabled {
			continue
		}

		svc.regions[region.ID] = region

		// Create S3 storage for this region
		regionStorage, err := storage.NewS3Storage(context.Background(), storage.S3Config{
			Endpoint:  region.Endpoint,
			Bucket:    region.Bucket,
			Region:    region.Region,
			AccessKey: region.AccessKey,
			SecretKey: region.SecretKey,
			PathStyle: true,
		})
		if err != nil {
			log.Error("failed to initialize region storage",
				zap.String("region", region.ID),
				zap.Error(err))
			continue
		}

		svc.regionStorages[region.ID] = regionStorage
		svc.regionHealth.Store(region.ID, true) // Assume healthy initially

		log.Info("initialized replication region",
			zap.String("region", region.ID),
			zap.String("endpoint", region.Endpoint))
	}

	return svc, nil
}

// ReplicateBlob schedules a blob for replication to all configured regions.
func (s *replicationService) ReplicateBlob(ctx context.Context, hash string) error {
	for regionID := range s.regionStorages {
		if regionID == s.config.PrimaryRegion {
			continue // Don't replicate to primary
		}

		_, err := s.ReplicateBlobToRegion(ctx, hash, regionID)
		if err != nil {
			s.log.Error("failed to schedule replication",
				zap.String("hash", hash),
				zap.String("region", regionID),
				zap.Error(err))
		}
	}

	return nil
}

// ReplicateBlobToRegion schedules replication to a specific region.
func (s *replicationService) ReplicateBlobToRegion(ctx context.Context, hash string, targetRegion string) (*core.ReplicationJob, error) {
	if _, ok := s.regionStorages[targetRegion]; !ok {
		return nil, fmt.Errorf("unknown region: %s", targetRegion)
	}

	job := &core.ReplicationJob{
		ID:           uuid.New().String(),
		BlobHash:     hash,
		SourceRegion: s.config.PrimaryRegion,
		TargetRegion: targetRegion,
		Status:       core.ReplicationStatusPending,
		CreatedAt:    time.Now().Unix(),
	}

	s.jobs.Store(job.ID, job)

	// Queue for async processing
	select {
	case s.jobQueue <- job:
		s.log.Debug("replication job queued",
			zap.String("job_id", job.ID),
			zap.String("hash", hash),
			zap.String("target", targetRegion))
	default:
		// Queue full - try sync if configured
		if s.config.SyncMode == "sync" {
			return s.executeJob(ctx, job)
		}
		return nil, fmt.Errorf("replication queue full")
	}

	return job, nil
}

// executeJob performs the actual replication.
func (s *replicationService) executeJob(ctx context.Context, job *core.ReplicationJob) (*core.ReplicationJob, error) {
	job.Status = core.ReplicationStatusInProgress
	job.StartedAt = time.Now().Unix()

	// Get source storage
	sourceStorage := s.primaryStorage
	if job.SourceRegion != s.config.PrimaryRegion {
		if src, ok := s.regionStorages[job.SourceRegion]; ok {
			sourceStorage = src
		}
	}

	// Get target storage
	targetStorage, ok := s.regionStorages[job.TargetRegion]
	if !ok {
		job.Status = core.ReplicationStatusFailed
		job.Error = "target region not found"
		return job, fmt.Errorf("target region not found: %s", job.TargetRegion)
	}

	// Read from source
	reader, err := sourceStorage.Get(ctx, job.BlobHash)
	if err != nil {
		job.Status = core.ReplicationStatusFailed
		job.Error = fmt.Sprintf("read source: %v", err)
		return job, err
	}
	defer reader.Close()

	// Get size
	size, err := sourceStorage.Size(ctx, job.BlobHash)
	if err != nil {
		job.Status = core.ReplicationStatusFailed
		job.Error = fmt.Sprintf("get size: %v", err)
		return job, err
	}

	// Write to target
	if err := targetStorage.Put(ctx, job.BlobHash, reader, size); err != nil {
		job.Status = core.ReplicationStatusFailed
		job.Error = fmt.Sprintf("write target: %v", err)
		return job, err
	}

	job.Status = core.ReplicationStatusComplete
	job.CompletedAt = time.Now().Unix()
	job.Progress = 100

	// Update blob regions
	s.addBlobRegion(job.BlobHash, job.TargetRegion)

	s.log.Info("blob replicated",
		zap.String("hash", job.BlobHash),
		zap.String("target", job.TargetRegion),
		zap.Duration("duration", time.Since(time.Unix(job.StartedAt, 0))))

	return job, nil
}

// addBlobRegion adds a region to a blob's region list.
func (s *replicationService) addBlobRegion(hash, region string) {
	existing, _ := s.blobRegions.LoadOrStore(hash, []string{region})
	if regions, ok := existing.([]string); ok {
		for _, r := range regions {
			if r == region {
				return // Already have this region
			}
		}
		s.blobRegions.Store(hash, append(regions, region))
	}
}

// GetReplicationStatus returns the replication status for a blob.
func (s *replicationService) GetReplicationStatus(ctx context.Context, hash string) (*core.BlobRegionStatus, error) {
	status := &core.BlobRegionStatus{
		BlobHash: hash,
		Primary:  s.config.PrimaryRegion,
		Healthy:  true,
	}

	// Check which regions have the blob
	var regions []string
	regions = append(regions, s.config.PrimaryRegion) // Primary always has it

	// Check each region
	for regionID, storage := range s.regionStorages {
		if regionID == s.config.PrimaryRegion {
			continue
		}

		exists, err := storage.Exists(ctx, hash)
		if err == nil && exists {
			regions = append(regions, regionID)
		}
	}

	status.Regions = regions
	status.Replicas = len(regions)
	status.Healthy = status.Replicas >= s.config.ReplicaCount

	return status, nil
}

// GetJob returns a specific replication job.
func (s *replicationService) GetJob(ctx context.Context, jobID string) (*core.ReplicationJob, error) {
	if job, ok := s.jobs.Load(jobID); ok {
		return job.(*core.ReplicationJob), nil
	}
	return nil, fmt.Errorf("job not found: %s", jobID)
}

// GetPendingJobs returns pending replication jobs.
func (s *replicationService) GetPendingJobs(ctx context.Context, limit int) ([]core.ReplicationJob, error) {
	var jobs []core.ReplicationJob
	count := 0

	s.jobs.Range(func(key, value interface{}) bool {
		job := value.(*core.ReplicationJob)
		if job.Status == core.ReplicationStatusPending || job.Status == core.ReplicationStatusInProgress {
			jobs = append(jobs, *job)
			count++
			if count >= limit {
				return false
			}
		}
		return true
	})

	return jobs, nil
}

// CancelJob cancels a pending replication job.
func (s *replicationService) CancelJob(ctx context.Context, jobID string) error {
	if job, ok := s.jobs.Load(jobID); ok {
		j := job.(*core.ReplicationJob)
		if j.Status == core.ReplicationStatusPending {
			j.Status = core.ReplicationStatusFailed
			j.Error = "cancelled"
			return nil
		}
		return fmt.Errorf("job cannot be cancelled in status: %s", j.Status)
	}
	return fmt.Errorf("job not found: %s", jobID)
}

// GetRegions returns all configured regions.
func (s *replicationService) GetRegions(ctx context.Context) ([]core.ReplicationRegion, error) {
	regions := make([]core.ReplicationRegion, 0, len(s.regions))
	for _, region := range s.regions {
		regions = append(regions, *region)
	}
	return regions, nil
}

// GetHealthyRegions returns only healthy regions.
func (s *replicationService) GetHealthyRegions(ctx context.Context) ([]core.ReplicationRegion, error) {
	var regions []core.ReplicationRegion
	for _, region := range s.regions {
		if healthy, ok := s.regionHealth.Load(region.ID); ok && healthy.(bool) {
			regions = append(regions, *region)
		}
	}
	return regions, nil
}

// GetBestRegion returns the best region for reading a blob.
func (s *replicationService) GetBestRegion(ctx context.Context, hash string) (*core.ReplicationRegion, error) {
	// Check which regions have the blob
	status, err := s.GetReplicationStatus(ctx, hash)
	if err != nil {
		return nil, err
	}

	if len(status.Regions) == 0 {
		return nil, fmt.Errorf("blob not found in any region")
	}

	// Find the healthiest region with lowest priority
	var best *core.ReplicationRegion
	for _, regionID := range status.Regions {
		region, ok := s.regions[regionID]
		if !ok {
			continue
		}

		healthy, _ := s.regionHealth.Load(regionID)
		if healthy == nil || !healthy.(bool) {
			continue
		}

		if best == nil || region.Priority < best.Priority {
			best = region
		}
	}

	if best == nil {
		// Fall back to primary
		if region, ok := s.regions[s.config.PrimaryRegion]; ok {
			return region, nil
		}
		return nil, fmt.Errorf("no healthy region found")
	}

	return best, nil
}

// SyncRegion triggers a full sync of a region.
func (s *replicationService) SyncRegion(ctx context.Context, regionID string) error {
	// This would typically iterate all blobs and replicate to the specified region
	// For now, this is a placeholder
	s.log.Info("region sync triggered", zap.String("region", regionID))
	return nil
}

// Start starts the replication workers.
func (s *replicationService) Start(ctx context.Context) {
	if !s.config.Enabled {
		s.log.Info("replication service disabled")
		return
	}

	// Start replication workers
	for i := 0; i < s.config.WorkerCount; i++ {
		s.wg.Add(1)
		go s.worker(ctx, i)
	}

	// Start health check worker
	s.wg.Add(1)
	go s.healthCheckWorker(ctx)

	s.log.Info("replication service started",
		zap.Int("workers", s.config.WorkerCount),
		zap.Int("regions", len(s.regionStorages)))
}

// worker processes replication jobs.
func (s *replicationService) worker(ctx context.Context, id int) {
	defer s.wg.Done()

	s.log.Debug("replication worker started", zap.Int("worker_id", id))

	for {
		select {
		case <-ctx.Done():
			return
		case <-s.stopCh:
			return
		case job := <-s.jobQueue:
			if job.Status != core.ReplicationStatusPending {
				continue
			}

			_, err := s.executeJob(ctx, job)
			if err != nil {
				s.log.Error("replication job failed",
					zap.String("job_id", job.ID),
					zap.Error(err))

				// Retry if allowed
				if job.Retries < s.config.RetryAttempts {
					job.Retries++
					job.Status = core.ReplicationStatusPending
					time.Sleep(s.config.RetryDelay)
					select {
					case s.jobQueue <- job:
					default:
						s.log.Warn("could not requeue failed job", zap.String("job_id", job.ID))
					}
				}
			}
		}
	}
}

// healthCheckWorker periodically checks region health.
func (s *replicationService) healthCheckWorker(ctx context.Context) {
	defer s.wg.Done()

	ticker := time.NewTicker(s.config.HealthCheckInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-s.stopCh:
			return
		case <-ticker.C:
			s.checkRegionHealth(ctx)
		}
	}
}

// checkRegionHealth checks the health of all regions.
func (s *replicationService) checkRegionHealth(ctx context.Context) {
	for regionID, storage := range s.regionStorages {
		// Simple health check - try to check if a known file exists
		start := time.Now()
		_, err := storage.Exists(ctx, "health-check")
		latency := time.Since(start)

		healthy := err == nil || err == io.EOF // No error or empty file is OK

		s.regionHealth.Store(regionID, healthy)

		s.log.Debug("region health check",
			zap.String("region", regionID),
			zap.Bool("healthy", healthy),
			zap.Duration("latency", latency))
	}
}

// Stop stops the replication workers.
func (s *replicationService) Stop() {
	close(s.stopCh)
	s.wg.Wait()
	s.log.Info("replication service stopped")
}

// Stats returns replication statistics.
func (s *replicationService) Stats(ctx context.Context) (*core.ReplicationStats, error) {
	stats := &core.ReplicationStats{
		RegionStats: make(map[string]*core.RegionStats),
	}

	// Count jobs by status
	s.jobs.Range(func(key, value interface{}) bool {
		job := value.(*core.ReplicationJob)
		stats.TotalJobs++

		switch job.Status {
		case core.ReplicationStatusPending:
			stats.PendingJobs++
		case core.ReplicationStatusInProgress:
			stats.InProgressJobs++
		case core.ReplicationStatusComplete:
			stats.CompletedJobs++
		case core.ReplicationStatusFailed:
			stats.FailedJobs++
		}

		return true
	})

	// Add region stats
	for regionID, region := range s.regions {
		healthy, _ := s.regionHealth.Load(regionID)
		healthVal := false
		if healthy != nil {
			healthVal = healthy.(bool)
		}

		stats.RegionStats[regionID] = &core.RegionStats{
			RegionID:    regionID,
			Healthy:     healthVal,
			LastChecked: time.Now().Unix(),
		}

		// Mark primary
		if region.ID == s.config.PrimaryRegion {
			stats.RegionStats[regionID].BlobCount = -1 // Unknown, needs DB query
		}
	}

	return stats, nil
}

// Ensure interface compliance
var _ core.ReplicationService = (*replicationService)(nil)
