package push

import (
	"context"
	"fmt"
	"log"

	firebase "firebase.google.com/go/v4"
	"firebase.google.com/go/v4/messaging"
	"google.golang.org/api/option"
)

// fcmBatchSize is FCM's per-call limit for multicast sends.
const fcmBatchSize = 500

// FCMNotifier sends real pushes via Firebase Cloud Messaging.
type FCMNotifier struct {
	client *messaging.Client
}

// NewFCMNotifier initializes the Firebase Admin SDK from a service
// account JSON key (Firebase Console → Project Settings → Service
// Accounts → Generate new private key).
func NewFCMNotifier(ctx context.Context, credentialsPath string) (*FCMNotifier, error) {
	app, err := firebase.NewApp(ctx, nil, option.WithCredentialsFile(credentialsPath))
	if err != nil {
		return nil, fmt.Errorf("init firebase app: %w", err)
	}
	client, err := app.Messaging(ctx)
	if err != nil {
		return nil, fmt.Errorf("init messaging client: %w", err)
	}
	return &FCMNotifier{client: client}, nil
}

func (notifier *FCMNotifier) NotifyNewChapter(ctx context.Context, tokens []string, novelTitle, chapterTitle string, chapterNumber int) {
	if len(tokens) == 0 {
		return
	}

	body := fmt.Sprintf("Chapter %d is now available", chapterNumber)
	if chapterTitle != "" {
		body += ": " + chapterTitle
	}

	for start := 0; start < len(tokens); start += fcmBatchSize {
		end := min(start+fcmBatchSize, len(tokens))
		batch := tokens[start:end]

		response, err := notifier.client.SendEachForMulticast(ctx, &messaging.MulticastMessage{
			Tokens: batch,
			Notification: &messaging.Notification{
				Title: novelTitle,
				Body:  body,
			},
		})
		if err != nil {
			log.Printf("push: multicast send failed: %v", err)
			continue
		}
		if response.FailureCount > 0 {
			log.Printf("push: %d/%d deliveries failed in batch", response.FailureCount, len(batch))
		}
	}
}
