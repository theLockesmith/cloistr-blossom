package service

import (
	"context"
	"sync"

	"go.uber.org/zap"

	"git.coldforge.xyz/coldforge/cloistr-blossom/src/core"
)

// notificationService implements core.NotificationService using in-memory pub/sub.
type notificationService struct {
	mu          sync.RWMutex
	subscribers map[string]map[chan *core.Notification]struct{} // pubkey -> set of channels
	allSubs     map[chan *core.Notification]struct{}            // for broadcast to all
	log         *zap.Logger
}

// NewNotificationService creates a new notification service.
func NewNotificationService(log *zap.Logger) core.NotificationService {
	return &notificationService{
		subscribers: make(map[string]map[chan *core.Notification]struct{}),
		allSubs:     make(map[chan *core.Notification]struct{}),
		log:         log,
	}
}

// Subscribe registers a client for notifications.
func (s *notificationService) Subscribe(ctx context.Context, pubkey string) (<-chan *core.Notification, func()) {
	ch := make(chan *core.Notification, 100) // Buffer to prevent blocking

	s.mu.Lock()
	// Add to pubkey-specific subscribers
	if s.subscribers[pubkey] == nil {
		s.subscribers[pubkey] = make(map[chan *core.Notification]struct{})
	}
	s.subscribers[pubkey][ch] = struct{}{}

	// Add to all subscribers
	s.allSubs[ch] = struct{}{}
	s.mu.Unlock()

	s.log.Debug("client subscribed to notifications",
		zap.String("pubkey", pubkey))

	// Return unsubscribe function
	unsubscribe := func() {
		s.mu.Lock()
		defer s.mu.Unlock()

		// Remove from pubkey subscribers
		if subs, ok := s.subscribers[pubkey]; ok {
			delete(subs, ch)
			if len(subs) == 0 {
				delete(s.subscribers, pubkey)
			}
		}

		// Remove from all subscribers
		delete(s.allSubs, ch)

		close(ch)

		s.log.Debug("client unsubscribed from notifications",
			zap.String("pubkey", pubkey))
	}

	return ch, unsubscribe
}

// Publish sends a notification to all subscribers for a pubkey.
func (s *notificationService) Publish(ctx context.Context, notification *core.Notification) {
	s.mu.RLock()
	subs, ok := s.subscribers[notification.Pubkey]
	if !ok || len(subs) == 0 {
		s.mu.RUnlock()
		return
	}

	// Copy channels to avoid holding lock during send
	channels := make([]chan *core.Notification, 0, len(subs))
	for ch := range subs {
		channels = append(channels, ch)
	}
	s.mu.RUnlock()

	// Send to all subscribers
	for _, ch := range channels {
		select {
		case ch <- notification:
			// Sent successfully
		default:
			// Channel full, skip (client is slow)
			s.log.Warn("notification channel full, skipping",
				zap.String("pubkey", notification.Pubkey),
				zap.String("type", string(notification.Type)))
		}
	}

	s.log.Debug("notification published",
		zap.String("pubkey", notification.Pubkey),
		zap.String("type", string(notification.Type)),
		zap.Int("recipients", len(channels)))
}

// PublishToAll sends a notification to all connected clients.
func (s *notificationService) PublishToAll(ctx context.Context, notification *core.Notification) {
	s.mu.RLock()
	if len(s.allSubs) == 0 {
		s.mu.RUnlock()
		return
	}

	// Copy channels
	channels := make([]chan *core.Notification, 0, len(s.allSubs))
	for ch := range s.allSubs {
		channels = append(channels, ch)
	}
	s.mu.RUnlock()

	// Send to all
	for _, ch := range channels {
		select {
		case ch <- notification:
		default:
			// Skip slow clients
		}
	}

	s.log.Debug("broadcast notification published",
		zap.String("type", string(notification.Type)),
		zap.Int("recipients", len(channels)))
}

// GetConnectedClients returns the total number of connected clients.
func (s *notificationService) GetConnectedClients() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.allSubs)
}

// GetClientCount returns the number of connected clients for a pubkey.
func (s *notificationService) GetClientCount(pubkey string) int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if subs, ok := s.subscribers[pubkey]; ok {
		return len(subs)
	}
	return 0
}

// Ensure interface compliance
var _ core.NotificationService = (*notificationService)(nil)
