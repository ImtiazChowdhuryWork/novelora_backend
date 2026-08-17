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
	ID         string
	NovelID    string
	NovelTitle string
	AuthorName string
	// OwnerUserID is the novel's real author account, if it has one
	// (nil for admin-uploaded/unclaimed novels) — see migration 0030.
	// Lets the moderation panel key off the real account when possible
	// instead of always falling back to the free-text AuthorName.
	OwnerUserID    *string
	UserID         string
	Username       string
	Reason         string
	Details        string
	ChapterID      *string
	ChapterTitle   *string
	Status         string
	ResolutionNote string
	// AuthorResponse is the author's own message when they resubmit a
	// report for re-review after fixing what the admin's
	// ResolutionNote asked for — see Resubmit. Reset to '' whenever an
	// admin issues a fresh note via UpdateStatus.
	AuthorResponse string
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
	// ChapterStatus is the chapter's *live* status (nil when the report
	// isn't about a specific chapter), and NovelHidden is the novel's
	// live soft-delete state — both let the report detail drawer show
	// "Unpublish"/"Republish" and "Hide"/"Restore" correctly instead of
	// only ever offering the one-way action.
	ChapterStatus *string
	NovelHidden   bool
	// ShareReporterEvidence is set once, at hold time (see
	// NovelReportService.HoldChapter/HoldNovel) — an admin's explicit
	// per-hold choice, never automatic, since the reporter's images may
	// contain the reporter's own identifying content. Gates whether
	// ListForOwner populates Images for the author; GetDetail (the
	// admin's own view) always populates Images regardless. Lives on the
	// report row (one flag, not one per hold) because the current state
	// machine only ever lets a report be held once — under_review is the
	// only status HoldChapter/HoldNovel accept from, and there's no path
	// back to under_review after a hold. If that ever changes, this
	// needs to move onto moderation_actions/moderation_action_images to
	// stay correctly scoped to one hold instead of the whole report.
	ShareReporterEvidence bool
	// AdminEvidenceImages is the admin's own proof attached to whichever
	// hold_chapter/hold_novel moderation action is this report's most
	// recent — separate from the reporter's Images, always shown to the
	// author once attached (the admin chose to attach them specifically
	// to show the author, unlike the reporter's evidence). Populated by
	// ModerationActionRepository.LatestHoldImages, not scanReport.
	AdminEvidenceImages []string
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
	r.id, r.novel_id, n.title, n.author_name, n.owner_user_id, r.user_id, u.username,
	r.reason, r.details, r.chapter_id, c.title,
	r.status, r.resolution_note, r.author_response, r.reviewed_by, coalesce(reviewer.username, ''),
	r.reviewed_at, r.created_at,
	(SELECT count(*) FROM novel_report_images ri WHERE ri.report_id = r.id),
	c.status, (n.deleted_at IS NOT NULL), r.share_reporter_evidence`

func scanReport(row pgx.Row) (*NovelReport, error) {
	report := &NovelReport{}
	err := row.Scan(
		&report.ID, &report.NovelID, &report.NovelTitle, &report.AuthorName, &report.OwnerUserID, &report.UserID, &report.Username,
		&report.Reason, &report.Details, &report.ChapterID, &report.ChapterTitle,
		&report.Status, &report.ResolutionNote, &report.AuthorResponse, &report.ReviewedBy, &report.ReviewedByName,
		&report.ReviewedAt, &report.CreatedAt, &report.ImageCount,
		&report.ChapterStatus, &report.NovelHidden, &report.ShareReporterEvidence,
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

// CountPending backs the Overview page's pending-reports tile — every
// status short of the two terminal ones (resolved/rejected) still
// needs an admin to look at it.
func (repository *NovelReportRepository) CountPending(ctx context.Context) (int, error) {
	var count int
	err := repository.pool.QueryRow(ctx,
		"SELECT count(*) FROM novel_reports WHERE status NOT IN ('resolved', 'rejected')").Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count pending reports: %w", err)
	}
	return count, nil
}

// UpdateStatus actions a report (action_required/reviewed/dismissed) —
// any status value other than "pending" stamps reviewedBy/reviewed_at,
// stores the admin's note (may be empty unless status is
// "action_required" — see NovelReportService), and clears
// author_response, since a fresh admin note starts a new cycle and a
// reply from a previous round-trip shouldn't linger next to it.
// Setting it back to "pending" (rare, but not disallowed) clears
// reviewedBy/reviewed_at and both note fields.
func (repository *NovelReportRepository) UpdateStatus(ctx context.Context, reportID, status, note, reviewerID string) (*NovelReport, error) {
	var commandTag pgconn.CommandTag
	var err error
	if status == "pending" {
		commandTag, err = repository.pool.Exec(ctx, `
			UPDATE novel_reports SET status = $2, resolution_note = '', author_response = '', reviewed_by = NULL, reviewed_at = NULL
			WHERE id = $1`, reportID, status)
	} else {
		commandTag, err = repository.pool.Exec(ctx, `
			UPDATE novel_reports SET status = $2, resolution_note = $3, author_response = '', reviewed_by = $4, reviewed_at = now()
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

// SetStatus is a bare status transition for moves that shouldn't touch
// resolution_note/reviewed_by — UpdateStatus's non-pending branch
// always overwrites both, which is right for an admin decision but
// wrong for the automatic submitted->under_review step and the
// author's own SubmitReleaseRequest (their explanation lives on
// release_requests now, not this row).
func (repository *NovelReportRepository) SetStatus(ctx context.Context, reportID, status string) (*NovelReport, error) {
	commandTag, err := repository.pool.Exec(ctx,
		"UPDATE novel_reports SET status = $2 WHERE id = $1", reportID, status)
	if err != nil {
		return nil, fmt.Errorf("set report status: %w", err)
	}
	if commandTag.RowsAffected() == 0 {
		return nil, ErrReportNotFound
	}
	return repository.GetByID(ctx, reportID)
}

// SetShareReporterEvidence records the admin's per-hold choice to show
// the reporter's evidence images to the author — see HoldChapter/
// HoldNovel. Only ever called with true (the column's own DEFAULT
// false covers "no").
func (repository *NovelReportRepository) SetShareReporterEvidence(ctx context.Context, reportID string, share bool) error {
	if _, err := repository.pool.Exec(ctx,
		"UPDATE novel_reports SET share_reporter_evidence = $2 WHERE id = $1", reportID, share); err != nil {
		return fmt.Errorf("set share reporter evidence: %w", err)
	}
	return nil
}

// ListForOwner is the author-facing "Notices" list — every report
// against a novel owned by ownerUserID, optionally filtered to one
// status ("" = all).
func (repository *NovelReportRepository) ListForOwner(ctx context.Context, ownerUserID, status string, page, pageSize int) ([]*NovelReport, int, error) {
	var total int
	if err := repository.pool.QueryRow(ctx, `
		SELECT count(*) FROM novel_reports r JOIN novels n ON n.id = r.novel_id
		WHERE n.owner_user_id = $1 AND ($2 = '' OR r.status = $2)`,
		ownerUserID, status).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count owner reports: %w", err)
	}

	rows, err := repository.pool.Query(ctx, `
		SELECT `+reportColumns+`
		FROM `+reportFromClause+`
		WHERE n.owner_user_id = $1 AND ($2 = '' OR r.status = $2)
		ORDER BY r.created_at DESC
		LIMIT $3 OFFSET $4`, ownerUserID, status, pageSize, (page-1)*pageSize)
	if err != nil {
		return nil, 0, fmt.Errorf("list owner reports: %w", err)
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

// ListForAuthorName returns every report against novels by this author
// name, newest first — the admin report detail drawer's report-history
// section, for authors with no real account (mirrors
// AuthorStrikeRepository.ListForAuthor). Unpaginated: same reasoning
// as strikes/other-novels — small per-author datasets.
func (repository *NovelReportRepository) ListForAuthorName(ctx context.Context, authorName string) ([]*NovelReport, error) {
	rows, err := repository.pool.Query(ctx,
		"SELECT "+reportColumns+" FROM "+reportFromClause+" WHERE n.author_name = $1 ORDER BY r.created_at DESC",
		authorName)
	if err != nil {
		return nil, fmt.Errorf("list reports for author name: %w", err)
	}
	defer rows.Close()

	reports := []*NovelReport{}
	for rows.Next() {
		report, err := scanReport(rows)
		if err != nil {
			return nil, fmt.Errorf("scan report: %w", err)
		}
		reports = append(reports, report)
	}
	return reports, rows.Err()
}

// ListForOwnerAll is ListForAuthorName's real-account counterpart —
// distinct from ListForOwner, which is the paginated author-dashboard
// "Notices" list.
func (repository *NovelReportRepository) ListForOwnerAll(ctx context.Context, ownerUserID string) ([]*NovelReport, error) {
	rows, err := repository.pool.Query(ctx,
		"SELECT "+reportColumns+" FROM "+reportFromClause+" WHERE n.owner_user_id = $1 ORDER BY r.created_at DESC",
		ownerUserID)
	if err != nil {
		return nil, fmt.Errorf("list reports for owner: %w", err)
	}
	defer rows.Close()

	reports := []*NovelReport{}
	for rows.Next() {
		report, err := scanReport(rows)
		if err != nil {
			return nil, fmt.Errorf("scan report: %w", err)
		}
		reports = append(reports, report)
	}
	return reports, rows.Err()
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
