package repository

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ModerationAction is one step in a report's state-machine history —
// the per-report audit trail audit_logs can't provide (no entity_id
// filter). Every transition NovelReportService drives writes one row
// here; the admin drawer's History section reads it back verbatim.
type ModerationAction struct {
	ID         string
	ReportID   string
	ActionType string
	AdminID    *string
	AdminName  string
	Notes      string
	CreatedAt  time.Time
	// Images is never scanned by the row query — same "attach after"
	// shape as NovelReport.Images/AdminEvidenceImages — populated by
	// NovelReportService.ModerationActions via ListImages, one query
	// per action (small N, same tradeoff ReleaseRequests already makes).
	// This is the admin's own evidence attached to this action.
	Images []string
	// ReporterImages is the *reporter's* original evidence, surfaced
	// on this same history entry when it's the hold_chapter/hold_novel
	// step and the report's ShareReporterEvidence is set — so the
	// history reads as a complete record of that decision on its own,
	// not split across this timeline and the report's own evidence
	// section. Kept separate from Images (never merged) so the caller
	// can still label whose evidence is whose. Empty for every other
	// action type, and for a hold the admin chose not to share
	// reporter evidence on.
	ReporterImages []string
}

type ModerationActionRepository struct {
	pool *pgxpool.Pool
}

func NewModerationActionRepository(pool *pgxpool.Pool) *ModerationActionRepository {
	return &ModerationActionRepository{pool: pool}
}

const moderationActionColumns = `
	a.id, a.report_id, a.action_type, a.admin_id, coalesce(u.username, ''), a.notes, a.created_at`

const moderationActionFromClause = `moderation_actions a LEFT JOIN users u ON u.id = a.admin_id`

func scanModerationAction(row pgx.Row) (*ModerationAction, error) {
	action := &ModerationAction{}
	err := row.Scan(&action.ID, &action.ReportID, &action.ActionType, &action.AdminID,
		&action.AdminName, &action.Notes, &action.CreatedAt)
	return action, err
}

// Create records one state-machine transition against reportID.
func (repository *ModerationActionRepository) Create(ctx context.Context, reportID, actionType, adminID, notes string) (*ModerationAction, error) {
	var actionID string
	err := repository.pool.QueryRow(ctx, `
		INSERT INTO moderation_actions (report_id, action_type, admin_id, notes)
		VALUES ($1, $2, $3, $4) RETURNING id`,
		reportID, actionType, adminID, notes,
	).Scan(&actionID)
	if err != nil {
		return nil, fmt.Errorf("create moderation action: %w", err)
	}
	action, err := scanModerationAction(repository.pool.QueryRow(ctx,
		"SELECT "+moderationActionColumns+" FROM "+moderationActionFromClause+" WHERE a.id = $1", actionID))
	if err != nil {
		return nil, fmt.Errorf("get created moderation action: %w", err)
	}
	return action, nil
}

// AddImages attaches the admin's own evidence to a hold decision —
// separate from the reporter's novel_report_images, see
// NovelReportService.HoldChapter/HoldNovel.
func (repository *ModerationActionRepository) AddImages(ctx context.Context, actionID string, imageURLs []string) error {
	for _, imageURL := range imageURLs {
		if _, err := repository.pool.Exec(ctx,
			"INSERT INTO moderation_action_images (moderation_action_id, image_url) VALUES ($1, $2)",
			actionID, imageURL); err != nil {
			return fmt.Errorf("add moderation action image: %w", err)
		}
	}
	return nil
}

// LatestHoldImages returns the admin's attached evidence for a report's
// most recent hold_chapter/hold_novel action (oldest-first, submission
// order) — a report is only ever on hold from one such action at a
// time in the current state machine, so "most recent" is unambiguous.
// Empty for a report that's never been held.
func (repository *ModerationActionRepository) LatestHoldImages(ctx context.Context, reportID string) ([]string, error) {
	rows, err := repository.pool.Query(ctx, `
		SELECT i.image_url FROM moderation_action_images i
		WHERE i.moderation_action_id = (
			SELECT a.id FROM moderation_actions a
			WHERE a.report_id = $1 AND a.action_type IN ('hold_chapter', 'hold_novel')
			ORDER BY a.created_at DESC LIMIT 1
		)
		ORDER BY i.created_at ASC`, reportID)
	if err != nil {
		return nil, fmt.Errorf("list latest hold images: %w", err)
	}
	defer rows.Close()

	urls := []string{}
	for rows.Next() {
		var url string
		if err := rows.Scan(&url); err != nil {
			return nil, fmt.Errorf("scan moderation action image: %w", err)
		}
		urls = append(urls, url)
	}
	return urls, rows.Err()
}

// ListImages returns one action's own attached evidence, oldest first
// — unlike LatestHoldImages (scoped to whichever hold is currently in
// effect), this is per-action, so the History timeline can show what
// was attached at each step, not just the most recent hold.
func (repository *ModerationActionRepository) ListImages(ctx context.Context, actionID string) ([]string, error) {
	rows, err := repository.pool.Query(ctx,
		"SELECT image_url FROM moderation_action_images WHERE moderation_action_id = $1 ORDER BY created_at ASC",
		actionID)
	if err != nil {
		return nil, fmt.Errorf("list moderation action images: %w", err)
	}
	defer rows.Close()

	urls := []string{}
	for rows.Next() {
		var url string
		if err := rows.Scan(&url); err != nil {
			return nil, fmt.Errorf("scan moderation action image: %w", err)
		}
		urls = append(urls, url)
	}
	return urls, rows.Err()
}

// ListForReport is the drawer's History timeline — every action taken
// on this report, newest first.
func (repository *ModerationActionRepository) ListForReport(ctx context.Context, reportID string) ([]*ModerationAction, error) {
	rows, err := repository.pool.Query(ctx,
		"SELECT "+moderationActionColumns+" FROM "+moderationActionFromClause+" WHERE a.report_id = $1 ORDER BY a.created_at DESC",
		reportID)
	if err != nil {
		return nil, fmt.Errorf("list moderation actions: %w", err)
	}
	defer rows.Close()

	actions := []*ModerationAction{}
	for rows.Next() {
		action, err := scanModerationAction(rows)
		if err != nil {
			return nil, fmt.Errorf("scan moderation action: %w", err)
		}
		actions = append(actions, action)
	}
	return actions, rows.Err()
}
