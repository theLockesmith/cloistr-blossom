package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

// S3Config holds configuration for S3 storage backend.
type S3Config struct {
	Endpoint  string // S3-compatible endpoint (e.g., https://s3.example.com)
	Bucket    string // Bucket name
	Region    string // AWS region
	AccessKey string // Access key ID
	SecretKey string // Secret access key
	PathStyle bool   // Use path-style addressing (required for MinIO/Ceph)

	// CDN configuration
	PublicURL        string // Public URL for direct access (e.g., https://cdn.example.com)
	PresignedEnabled bool   // Enable presigned URL generation
}

// S3Storage implements StorageBackend using S3-compatible object storage.
type S3Storage struct {
	client    *s3.Client
	presigner *s3.PresignClient
	bucket    string
	publicURL string
}

// NewS3Storage creates a new S3Storage instance.
func NewS3Storage(ctx context.Context, cfg S3Config) (*S3Storage, error) {
	// Build AWS config with custom endpoint and credentials
	resolver := aws.EndpointResolverWithOptionsFunc(
		func(service, region string, options ...interface{}) (aws.Endpoint, error) {
			if cfg.Endpoint != "" {
				return aws.Endpoint{
					URL:               cfg.Endpoint,
					HostnameImmutable: true,
					SigningRegion:     cfg.Region,
				}, nil
			}
			return aws.Endpoint{}, &aws.EndpointNotFoundError{}
		},
	)

	awsCfg, err := config.LoadDefaultConfig(ctx,
		config.WithRegion(cfg.Region),
		config.WithEndpointResolverWithOptions(resolver),
		config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(
			cfg.AccessKey,
			cfg.SecretKey,
			"",
		)),
	)
	if err != nil {
		return nil, fmt.Errorf("load AWS config: %w", err)
	}

	client := s3.NewFromConfig(awsCfg, func(o *s3.Options) {
		o.UsePathStyle = cfg.PathStyle
	})

	// Create presign client for generating presigned URLs
	presigner := s3.NewPresignClient(client)

	return &S3Storage{
		client:    client,
		presigner: presigner,
		bucket:    cfg.Bucket,
		publicURL: cfg.PublicURL,
	}, nil
}

// objectKey returns the S3 object key for a given hash.
// Uses a prefix structure to organize objects: blobs/ab/cd/abcdef...
func (s *S3Storage) objectKey(hash string) string {
	if len(hash) < 4 {
		return "blobs/" + hash
	}
	return fmt.Sprintf("blobs/%s/%s/%s", hash[:2], hash[2:4], hash)
}

func (s *S3Storage) Put(ctx context.Context, hash string, data io.Reader, size int64) error {
	key := s.objectKey(hash)

	input := &s3.PutObjectInput{
		Bucket:        aws.String(s.bucket),
		Key:           aws.String(key),
		Body:          data,
		ContentLength: aws.Int64(size),
	}

	_, err := s.client.PutObject(ctx, input)
	if err != nil {
		return fmt.Errorf("put S3 object: %w", err)
	}

	return nil
}

func (s *S3Storage) Get(ctx context.Context, hash string) (io.ReadCloser, error) {
	key := s.objectKey(hash)

	output, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		var noSuchKey *types.NoSuchKey
		if errors.As(err, &noSuchKey) {
			return nil, ErrBlobNotFound
		}
		return nil, fmt.Errorf("get S3 object: %w", err)
	}

	return output.Body, nil
}

func (s *S3Storage) Delete(ctx context.Context, hash string) error {
	key := s.objectKey(hash)

	_, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		// S3 delete is idempotent, but check for other errors
		var noSuchKey *types.NoSuchKey
		if errors.As(err, &noSuchKey) {
			return nil
		}
		return fmt.Errorf("delete S3 object: %w", err)
	}

	return nil
}

func (s *S3Storage) Exists(ctx context.Context, hash string) (bool, error) {
	key := s.objectKey(hash)

	_, err := s.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		var notFound *types.NotFound
		if errors.As(err, &notFound) {
			return false, nil
		}
		return false, fmt.Errorf("head S3 object: %w", err)
	}

	return true, nil
}

func (s *S3Storage) Size(ctx context.Context, hash string) (int64, error) {
	key := s.objectKey(hash)

	output, err := s.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		var notFound *types.NotFound
		if errors.As(err, &notFound) {
			return 0, ErrBlobNotFound
		}
		return 0, fmt.Errorf("head S3 object: %w", err)
	}

	if output.ContentLength == nil {
		return 0, nil
	}

	return *output.ContentLength, nil
}

// GetPresignedURL generates a presigned URL for direct access to a blob.
// The URL is valid for the specified duration.
func (s *S3Storage) GetPresignedURL(ctx context.Context, hash string, expiry time.Duration) (string, error) {
	key := s.objectKey(hash)

	request, err := s.presigner.PresignGetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	}, s3.WithPresignExpires(expiry))
	if err != nil {
		return "", fmt.Errorf("presign get object: %w", err)
	}

	return request.URL, nil
}

// GetPublicURL returns the public URL for a blob if a public URL base is configured.
// Returns empty string if public access is not configured.
func (s *S3Storage) GetPublicURL(hash string) string {
	if s.publicURL == "" {
		return ""
	}
	key := s.objectKey(hash)
	return fmt.Sprintf("%s/%s", s.publicURL, key)
}

// SupportsPresignedURLs returns true if the storage backend supports presigned URLs.
func (s *S3Storage) SupportsPresignedURLs() bool {
	return s.presigner != nil
}
