package service

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/ImtiazChowdhuryWork/novelora_backend/internal/push"
	"github.com/ImtiazChowdhuryWork/novelora_backend/internal/realtime"
	"github.com/ImtiazChowdhuryWork/novelora_backend/internal/repository"
)

const (
	maxBroadcastTitleLength = 255
	maxBroadcastBodyLength  = 2000
)

// BroadcastService is the admin notification composer's send path:
// a general announcement to every account, independent of any
// specific novel/chapter (unlike the automatic chapter-publish
// notification in ChapterService).
type BroadcastService struct {
	deviceTokens  *repository.DeviceTokenRepository
	notifications *repository.NotificationRepository
	notifier      push.Notifier
	events        realtime.Publisher
}

func NewBroadcastService(
	deviceTokens *repository.DeviceTokenRepository,
	notifications *repository.NotificationRepository,
	notifier push.Notifier,
	events realtime.Publisher,
) *BroadcastService {
	return &BroadcastService{
		deviceTokens:  deviceTokens,
		notifications: notifications,
		notifier:      notifier,
		events:        events,
	}
}

// Send populates every account's inbox, then best-effort pushes to
// every registered device. The inbox write is the source of truth —
// if loading device tokens fails, the announcement still landed.
func (broadcastService *BroadcastService) Send(ctx context.Context, title, body string) error {
	title = strings.TrimSpace(title)
	body = strings.TrimSpace(body)
	if title == "" || len(title) > maxBroadcastTitleLength {
		return &ValidationError{Message: fmt.Sprintf("title is required (max %d characters)", maxBroadcastTitleLength)}
	}
	if body == "" || len(body) > maxBroadcastBodyLength {
		return &ValidationError{Message: fmt.Sprintf("body is required (max %d characters)", maxBroadcastBodyLength)}
	}

	if err := broadcastService.notifications.CreateBroadcastForAllUsers(ctx, title, body); err != nil {
		return err
	}
	broadcastService.events.Publish(realtime.Event{Topic: "notification.new"})

	tokens, err := broadcastService.deviceTokens.ListAllTokens(ctx)
	if err != nil {
		log.Printf("push: could not load device tokens for broadcast: %v", err)
		return nil
	}
	broadcastService.notifier.NotifyBroadcast(ctx, tokens, title, body)
	return nil
}
