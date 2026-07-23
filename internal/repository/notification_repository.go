package repository

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrNotificationNotFound = errors.New("notification not found")

type Notification struct {
	ID string
	// Nil for an admin broadcast not tied to a specific chapter.
	NovelID   *string
	ChapterID *string
	Title     string
	Body      string
	IsRead    bool
	CreatedAt time.Time
}

type NotificationRepository struct {
	pool *pgxpool.Pool
}

func NewNotificationRepository(pool *pgxpool.Pool) *NotificationRepository {
	return &NotificationRepository{pool: pool}
}

// CreateForAllUsers fans a chapter-publish notification out to every
// registered account's inbox. Novelora has no per-novel "following"
// list yet (see DeviceTokenRepository.ListAllTokens), so this mirrors
// that same broadcast-to-everyone model.
func (repository *NotificationRepository) CreateForAllUsers(ctx context.Context, novelID, chapterID, title, body string) error {
	_, err := repository.pool.Exec(ctx, `
		INSERT INTO notifications (user_id, novel_id, chapter_id, title, body)
		SELECT id, $1, $2, $3, $4 FROM users`,
		novelID, chapterID, title, body)
	if err != nil {
		return fmt.Errorf("create notifications: %w", err)
	}
	return nil
}

// CreateNovelNotificationForAllUsers fans a novel-level notification
// (just added, just entered a ranked section) out to every account's
// inbox — same broadcast-to-everyone model as CreateForAllUsers, just
// with no chapter behind it (chapter_id stays NULL; both novel_id and
// chapter_id are nullable as of migration 0013).
func (repository *NotificationRepository) CreateNovelNotificationForAllUsers(ctx context.Context, novelID, title, body string) error {
	_, err := repository.pool.Exec(ctx, `
		INSERT INTO notifications (user_id, novel_id, title, body)
		SELECT id, $1, $2, $3 FROM users`,
		novelID, title, body)
	if err != nil {
		return fmt.Errorf("create novel notifications: %w", err)
	}
	return nil
}

// CreateBroadcastForAllUsers is the admin composer's send: a general
// announcement with no novel/chapter behind it, so a tap just opens
// the inbox instead of navigating anywhere.
func (repository *NotificationRepository) CreateBroadcastForAllUsers(ctx context.Context, title, body string) error {
	_, err := repository.pool.Exec(ctx, `
		INSERT INTO notifications (user_id, title, body)
		SELECT id, $1, $2 FROM users`,
		title, body)
	if err != nil {
		return fmt.Errorf("create broadcast notifications: %w", err)
	}
	return nil
}

// ListForUser returns the newest-first page for a user alongside the
// total row count, for pagination.
func (repository *NotificationRepository) ListForUser(ctx context.Context, userID string, page, pageSize int) ([]*Notification, int, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}
	offset := (page - 1) * pageSize

	var total int
	if err := repository.pool.QueryRow(ctx,
		`SELECT count(*) FROM notifications WHERE user_id = $1`, userID,
	).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count notifications: %w", err)
	}

	rows, err := repository.pool.Query(ctx, `
		SELECT id, novel_id, chapter_id, title, body, is_read, created_at
		FROM notifications
		WHERE user_id = $1
		ORDER BY created_at DESC
		LIMIT $2 OFFSET $3`,
		userID, pageSize, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("list notifications: %w", err)
	}
	defer rows.Close()

	notifications := []*Notification{}
	for rows.Next() {
		notification := &Notification{}
		if err := rows.Scan(
			&notification.ID, &notification.NovelID, &notification.ChapterID,
			&notification.Title, &notification.Body, &notification.IsRead, &notification.CreatedAt,
		); err != nil {
			return nil, 0, fmt.Errorf("scan notification: %w", err)
		}
		notifications = append(notifications, notification)
	}
	return notifications, total, rows.Err()
}

func (repository *NotificationRepository) UnreadCount(ctx context.Context, userID string) (int, error) {
	var count int
	err := repository.pool.QueryRow(ctx,
		`SELECT count(*) FROM notifications WHERE user_id = $1 AND is_read = false`, userID,
	).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count unread notifications: %w", err)
	}
	return count, nil
}

// MarkRead is scoped to userID so one account can't mark another's
// notification as read by guessing an id.
func (repository *NotificationRepository) MarkRead(ctx context.Context, userID, notificationID string) error {
	tag, err := repository.pool.Exec(ctx,
		`UPDATE notifications SET is_read = true WHERE id = $1 AND user_id = $2`,
		notificationID, userID)
	if err != nil {
		return fmt.Errorf("mark notification read: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotificationNotFound
	}
	return nil
}

func (repository *NotificationRepository) MarkAllRead(ctx context.Context, userID string) error {
	_, err := repository.pool.Exec(ctx,
		`UPDATE notifications SET is_read = true WHERE user_id = $1 AND is_read = false`, userID)
	if err != nil {
		return fmt.Errorf("mark all notifications read: %w", err)
	}
	return nil
}
