package service

import (
	"context"
	"sync"

	"go.uber.org/zap"

	"git.aegis-hq.xyz/coldforge/cloistr-blossom/src/core"
	"git.aegis-hq.xyz/coldforge/cloistr-common/platform"
)

// platformACRService implements ACRStorage using the unified platform database.
// In platform mode, access is determined by the has_service_access() PostgreSQL function.
type platformACRService struct {
	client *platform.Client
	log    *zap.Logger

	// ensured records pubkeys whose users row this process has already
	// provisioned, so EnsureUser costs one DB write per pubkey per pod rather
	// than one per request. Rows are never deleted in normal operation; if one
	// is, has_service_access() still denies, so a stale entry fails closed.
	ensured sync.Map
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
	// has_service_access() denies a pubkey with no users row, and only cloistr-me
	// creates that row. Provision it here so keys that never touched cloistr-me
	// (extension users, headless keys) can use blossom. Existing rows, including
	// disabled users, are left untouched. On failure we fall through: the access
	// check below still denies a pubkey with no row.
	if _, done := s.ensured.Load(pubkey); !done {
		if err := s.client.EnsureUser(ctx, pubkey); err != nil {
			s.log.Warn("failed to ensure platform user",
				zap.String("pubkey", pubkey),
				zap.Error(err))
		} else {
			s.ensured.Store(pubkey, struct{}{})
		}
	}

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
