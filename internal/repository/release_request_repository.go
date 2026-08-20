package repository

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrReleaseRequestNotFound = errors.New("release request not found")

// ReleaseRequest is the author's side of a hold — an explanation
// (+ optional proof images) asking the admin to restore
// chapter_on_hold/novel_on_hold content. Replaces the old bare-message
// Resubmit. Images is populated separately (see ListImages), never by
// scanReleaseRequest — same "attach after" shape as NovelReport.Images.
type ReleaseRequest struct {
	ID           string
	ReportID     string
	AuthorID     *string
	Explanation  string
	Status       string
	AdminComment string
	CreatedAt    time.Time
	ReviewedAt   *time.Time
	Images       []ReleaseRequestImage
}

type ReleaseRequestImage struct {
	ID               string
	ReleaseRequestID string
	ImageURL         string
	CreatedAt        time.Time
}

type ReleaseRequestRepository struct {
	pool *pgxpool.Pool
}

func NewReleaseRequestRepository(pool *pgxpool.Pool) *ReleaseRequestRepository {
	return &ReleaseRequestRepository{pool: pool}
}

const releaseRequestColumns = `
	r.id, r.report_id, r.author_id, r.explanation, r.status, r.admin_comment, r.created_at, r.reviewed_at`

func scanReleaseRequest(row pgx.Row) (*ReleaseRequest, error) {
	request := &ReleaseRequest{}
	err := row.Scan(&request.ID, &request.ReportID, &request.AuthorID, &request.Explanation,
		&request.Status, &request.AdminComment, &request.CreatedAt, &request.ReviewedAt)
	return request, err
}

// Create stores a release request; evidence images are added
// separately via AddImages, once the row (and its id) exist.
func (repository *ReleaseRequestRepository) Create(ctx context.Context, reportID, authorID, explanation string) (*ReleaseRequest, error) {
	var requestID string
	err := repository.pool.QueryRow(ctx, `
		INSERT INTO release_requests (report_id, author_id, explanation)
		VALUES ($1, $2, $3) RETURNING id`,
		reportID, authorID, explanation,
	).Scan(&requestID)
	if err != nil {
		return nil, fmt.Errorf("create release request: %w", err)
	}
	return repository.GetByID(ctx, requestID)
}

func (repository *ReleaseRequestRepository) GetByID(ctx context.Context, requestID string) (*ReleaseRequest, error) {
	request, err := scanReleaseRequest(repository.pool.QueryRow(ctx,
		"SELECT "+releaseRequestColumns+" FROM release_requests r WHERE r.id = $1", requestID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrReleaseRequestNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get release request: %w", err)
	}
	return request, nil
}

// AddImages attaches evidence screenshots to an already-created release request.
func (repository *ReleaseRequestRepository) AddImages(ctx context.Context, requestID string, imageURLs []string) error {
	for _, imageURL := range imageURLs {
		if _, err := repository.pool.Exec(ctx,
			"INSERT INTO release_request_images (release_request_id, image_url) VALUES ($1, $2)",
			requestID, imageURL); err != nil {
			return fmt.Errorf("add release request image: %w", err)
		}
	}
	return nil
}

// ListImages returns a release request's evidence screenshots, oldest first.
func (repository *ReleaseRequestRepository) ListImages(ctx context.Context, requestID string) ([]ReleaseRequestImage, error) {
	rows, err := repository.pool.Query(ctx, `
		SELECT id, release_request_id, image_url, created_at
		FROM release_request_images WHERE release_request_id = $1 ORDER BY created_at ASC`, requestID)
	if err != nil {
		return nil, fmt.Errorf("list release request images: %w", err)
	}
	defer rows.Close()

	images := []ReleaseRequestImage{}
	for rows.Next() {
		var image ReleaseRequestImage
		if err := rows.Scan(&image.ID, &image.ReleaseRequestID, &image.ImageURL, &image.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan release request image: %w", err)
		}
		images = append(images, image)
	}
	return images, rows.Err()
}

// ListForReport is the report's release-request history, newest first
// — both the admin drawer's "pending review" card and the author's
// Reports page (to show a previous rejection's admin_comment) read
// this the same way.
func (repository *ReleaseRequestRepository) ListForReport(ctx context.Context, reportID string) ([]*ReleaseRequest, error) {
	rows, err := repository.pool.Query(ctx,
		"SELECT "+releaseRequestColumns+" FROM release_requests r WHERE r.report_id = $1 ORDER BY r.created_at DESC",
		reportID)
	if err != nil {
		return nil, fmt.Errorf("list release requests: %w", err)
	}
	defer rows.Close()

	requests := []*ReleaseRequest{}
	for rows.Next() {
		request, err := scanReleaseRequest(rows)
		if err != nil {
			return nil, fmt.Errorf("scan release request: %w", err)
		}
		requests = append(requests, request)
	}
	return requests, rows.Err()
}

// UpdateStatus actions a release request (approved/rejected) —
// stamps reviewed_at and the admin's comment (empty on approve).
func (repository *ReleaseRequestRepository) UpdateStatus(ctx context.Context, requestID, status, adminComment string) (*ReleaseRequest, error) {
	commandTag, err := repository.pool.Exec(ctx, `
		UPDATE release_requests SET status = $2, admin_comment = $3, reviewed_at = now()
		WHERE id = $1`, requestID, status, adminComment)
	if err != nil {
		return nil, fmt.Errorf("update release request status: %w", err)
	}
	if commandTag.RowsAffected() == 0 {
		return nil, ErrReleaseRequestNotFound
	}
	return repository.GetByID(ctx, requestID)
}
