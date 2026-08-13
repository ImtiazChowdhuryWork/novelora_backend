package repository

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrReportNotFound = errors.New("report not found")

// NovelReport is one reader's report of a novel (Book Detail's flag
// icon). ReviewedBy/ReviewedAt stay nil until an admin actions it.
// ChapterID/ChapterTitle are nil when the report is about the whole
// novel, not one specific chapter. Images is populated separately (see
// ListImages) — never by scanReport — same "attach after" shape as
// NovelComment.Replies.
type NovelReport struct {
	ID             string
	NovelID        string
	NovelTitle     string
	AuthorName     string
	UserID         string
	Username       string
	Reason         string
	Details        string
	ChapterID      *string
	ChapterTitle   *string
	Status         string
	ResolutionNote string
	ReviewedBy     *string
	ReviewedByName string
	ReviewedAt     *time.Time
	CreatedAt      time.Time
	// ImageCount is always accurate (a cheap correlated-count subquery in
	// scanReport), unlike Images which List/ListForUser leave empty to
	// avoid an N+1 query — it's what a report list row uses to show an
	// evidence indicator without fetching every image's URL.
	ImageCount int
	Images     []NovelReportImage
}

// NovelReportImage is one screenshot attached as evidence — up to
// NovelReportService.maxReportImages per report.
type NovelReportImage struct {
	ID        string
	ReportID  string
	ImageURL  string
	CreatedAt time.Time
}

type NovelReportRepository struct {
	pool *pgxpool.Pool
}

func NewNovelReportRepository(pool *pgxpool.Pool) *NovelReportRepository {
	return &NovelReportRepository{pool: pool}
}

const reportColumns = `
	r.id, r.novel_id, n.title, n.author_name, r.user_id, u.username,
	r.reason, r.details, r.chapter_id, c.title,
	r.status, r.resolution_note, r.reviewed_by, coalesce(reviewer.username, ''),
	r.reviewed_at, r.created_at,
	(SELECT count(*) FROM novel_report_images ri WHERE ri.report_id = r.id)`

func scanReport(row pgx.Row) (*NovelReport, error) {
	report := &NovelReport{}
	err := row.Scan(
		&report.ID, &report.NovelID, &report.NovelTitle, &report.AuthorName, &report.UserID, &report.Username,
		&report.Reason, &report.Details, &report.ChapterID, &report.ChapterTitle,
		&report.Status, &report.ResolutionNote, &report.ReviewedBy, &report.ReviewedByName,
		&report.ReviewedAt, &report.CreatedAt, &report.ImageCount,
	)
	return report, err
}

const reportFromClause = `
	novel_reports r
	JOIN novels n ON n.id = r.novel_id
	JOIN users u ON u.id = r.user_id
	LEFT JOIN chapters c ON c.id = r.chapter_id
	LEFT JOIN users reviewer ON reviewer.id = r.reviewed_by`

// Create stores a report, optionally naming the specific chapter it's
// about (nil = the whole novel). Evidence images are added separately
// via AddImages, once the report row (and its id) exist.
func (repository *NovelReportRepository) Create(ctx context.Context, novelID, userID, reason, details string, chapterID *string) (*NovelReport, error) {
	var reportID string
	err := repository.pool.QueryRow(ctx, `
		INSERT INTO novel_reports (novel_id, user_id, reason, details, chapter_id)
		VALUES ($1, $2, $3, $4, $5) RETURNING id`,
		novelID, userID, reason, details, chapterID,
	).Scan(&reportID)
	if err != nil {
		return nil, fmt.Errorf("create report: %w", err)
	}
	return repository.GetByID(ctx, reportID)
}

func (repository *NovelReportRepository) GetByID(ctx context.Context, reportID string) (*NovelReport, error) {
	report, err := scanReport(repository.pool.QueryRow(ctx,
		"SELECT "+reportColumns+" FROM "+reportFromClause+" WHERE r.id = $1", reportID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrReportNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get report: %w", err)
	}
	return report, nil
}

// AddImages attaches evidence screenshots to an already-created report.
func (repository *NovelReportRepository) AddImages(ctx context.Context, reportID string, imageURLs []string) error {
	for _, imageURL := range imageURLs {
		if _, err := repository.pool.Exec(ctx,
			"INSERT INTO novel_report_images (report_id, image_url) VALUES ($1, $2)",
			reportID, imageURL); err != nil {
			return fmt.Errorf("add report image: %w", err)
		}
	}
	return nil
}

// ListImages returns a report's evidence screenshots, oldest first
// (submission order).
func (repository *NovelReportRepository) ListImages(ctx context.Context, reportID string) ([]NovelReportImage, error) {
	rows, err := repository.pool.Query(ctx, `
		SELECT id, report_id, image_url, created_at
		FROM novel_report_images WHERE report_id = $1 ORDER BY created_at ASC`, reportID)
	if err != nil {
		return nil, fmt.Errorf("list report images: %w", err)
	}
	defer rows.Close()

	images := []NovelReportImage{}
	for rows.Next() {
		var image NovelReportImage
		if err := rows.Scan(&image.ID, &image.ReportID, &image.ImageURL, &image.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan report image: %w", err)
		}
		images = append(images, image)
	}
	return images, rows.Err()
}

// List returns one page of reports, newest first, optionally filtered
// to a single status ("" = all statuses) — the backing query for the
// dashboard's Reports moderation page.
func (repository *NovelReportRepository) List(ctx context.Context, status string, page, pageSize int) ([]*NovelReport, int, error) {
	var total int
	if err := repository.pool.QueryRow(ctx,
		"SELECT count(*) FROM novel_reports r WHERE ($1 = '' OR r.status = $1)",
		status).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count reports: %w", err)
	}

	rows, err := repository.pool.Query(ctx, `
		SELECT `+reportColumns+`
		FROM `+reportFromClause+`
		WHERE ($1 = '' OR r.status = $1)
		ORDER BY r.created_at DESC
		LIMIT $2 OFFSET $3`, status, pageSize, (page-1)*pageSize)
	if err != nil {
		return nil, 0, fmt.Errorf("list reports: %w", err)
	}
	defer rows.Close()

	reports := []*NovelReport{}
	for rows.Next() {
		report, err := scanReport(rows)
		if err != nil {
			return nil, 0, fmt.Errorf("scan report: %w", err)
		}
		reports = append(reports, report)
	}
	return reports, total, rows.Err()
}

// CountPending backs the Overview page's pending-reports tile.
func (repository *NovelReportRepository) CountPending(ctx context.Context) (int, error) {
	var count int
	err := repository.pool.QueryRow(ctx,
		"SELECT count(*) FROM novel_reports WHERE status = 'pending'").Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count pending reports: %w", err)
	}
	return count, nil
}

// UpdateStatus actions a report (reviewed/dismissed) — any status
// value other than "pending" stamps reviewedBy/reviewed_at and stores
// the admin's note (may be empty); setting it back to "pending" (rare,
// but not disallowed) clears reviewedBy/reviewed_at and the note.
func (repository *NovelReportRepository) UpdateStatus(ctx context.Context, reportID, status, note, reviewerID string) (*NovelReport, error) {
	var commandTag pgconn.CommandTag
	var err error
	if status == "pending" {
		commandTag, err = repository.pool.Exec(ctx, `
			UPDATE novel_reports SET status = $2, resolution_note = '', reviewed_by = NULL, reviewed_at = NULL
			WHERE id = $1`, reportID, status)
	} else {
		commandTag, err = repository.pool.Exec(ctx, `
			UPDATE novel_reports SET status = $2, resolution_note = $3, reviewed_by = $4, reviewed_at = now()
			WHERE id = $1`, reportID, status, note, reviewerID)
	}
	if err != nil {
		return nil, fmt.Errorf("update report status: %w", err)
	}
	if commandTag.RowsAffected() == 0 {
		return nil, ErrReportNotFound
	}
	return repository.GetByID(ctx, reportID)
}

// HasReported is the reader-facing "did I already report this novel"
// check that backs Book Detail's flag-icon fill state — see
// PublicNovelHandler.Get's is_reported_by_me, same shape as
// NovelSupportRepository.IsSupportedByUser.
func (repository *NovelReportRepository) HasReported(ctx context.Context, novelID, userID string) (bool, error) {
	var exists bool
	err := repository.pool.QueryRow(ctx,
		"SELECT EXISTS (SELECT 1 FROM novel_reports WHERE novel_id = $1 AND user_id = $2)",
		novelID, userID).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("check has reported: %w", err)
	}
	return exists, nil
}

// ListForUser is the reporter's own report history — the app's "My
// Reports" Profile page.
func (repository *NovelReportRepository) ListForUser(ctx context.Context, userID string, page, pageSize int) ([]*NovelReport, int, error) {
	var total int
	if err := repository.pool.QueryRow(ctx,
		"SELECT count(*) FROM novel_reports WHERE user_id = $1", userID).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count user reports: %w", err)
	}

	rows, err := repository.pool.Query(ctx, `
		SELECT `+reportColumns+`
		FROM `+reportFromClause+`
		WHERE r.user_id = $1
		ORDER BY r.created_at DESC
		LIMIT $2 OFFSET $3`, userID, pageSize, (page-1)*pageSize)
	if err != nil {
		return nil, 0, fmt.Errorf("list user reports: %w", err)
	}
	defer rows.Close()

	reports := []*NovelReport{}
	for rows.Next() {
		report, err := scanReport(rows)
		if err != nil {
			return nil, 0, fmt.Errorf("scan report: %w", err)
		}
		reports = append(reports, report)
	}
	return reports, total, rows.Err()
}

// Delete withdraws reportID — the caller must own it (a reader
// deleting their own report). Evidence images are removed via ON
// DELETE CASCADE; the caller (service) still needs to unlink the
// files on disk since Postgres doesn't know about those.
func (repository *NovelReportRepository) Delete(ctx context.Context, reportID, userID string) error {
	commandTag, err := repository.pool.Exec(ctx,
		"DELETE FROM novel_reports WHERE id = $1 AND user_id = $2",
		reportID, userID)
	if err != nil {
		return fmt.Errorf("delete report: %w", err)
	}
	if commandTag.RowsAffected() == 0 {
		return ErrReportNotFound
	}
	return nil
}
