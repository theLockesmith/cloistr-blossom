package service

import (
	"context"

	"go.uber.org/zap"

	"git.aegis-hq.xyz/coldforge/cloistr-blossom/src/core"
	"git.aegis-hq.xyz/coldforge/cloistr-common/platform"
)

// platformACRService implements ACRStorage using the unified platform database.
// In platform mode, access is determined by the has_service_access() PostgreSQL function.
type platformACRService struct {
	client *platform.Client
	log    *zap.Logger
}

// NewPlatformACRService creates an ACR service backed by the unified platform database.
func NewPlatformACRService(
	client *platform.Client,
	log *zap.Logger,
) (core.ACRStorage, error) {
	return &platformACRService{
		client: client,
		log:    log,
	}, nil
}

// Validate checks if a pubkey has access to the blossom service.
// In platform mode, we check has_service_access() which considers:
// - Whether the user exists and is active
// - Whether they have access to "blossom" service
// - Whether they're banned
func (s *platformACRService) Validate(
	ctx context.Context,
	pubkey string,
	resource core.ACRResource,
) error {
	// Check if user has access to blossom service
	hasAccess, err := s.client.HasAccess(ctx, pubkey)
	if err != nil {
		s.log.Error("failed to check platform access",
			zap.String("pubkey", pubkey),
			zap.String("resource", string(resource)),
			zap.Error(err))
		// In case of error, deny access for safety
		return ErrUnauthorized
	}

	if !hasAccess {
		s.log.Debug("platform access denied",
			zap.String("pubkey", pubkey),
			zap.String("resource", string(resource)))
		return ErrUnauthorized
	}

	return nil
}
