package repository

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrReportReasonNotFound   = errors.New("report reason not found")
	ErrReportReasonLabelTaken = errors.New("report reason label already exists")
	ErrLastReportReason       = errors.New("cannot delete the last report reason")
)

type ReportReason struct {
	ID    string
	Label string
	// RequiresDetails generalizes what used to be a hardcoded
	// reason == "other" check — the app's report sheet requires
	// non-empty free-text details only when the selected reason has
	// this set (seeded true for "Something else", see migration 0037).
	RequiresDetails bool
	// Position controls display order in both the admin's Report Types
	// list and the app's report sheet — admin-reorderable, see Reorder
	// (migration 0038).
	Position int
	// TypeID/TypeLabel/TypeDescription are this reason's parent
	// ReportReasonType (migration 0040) — every reason belongs to
	// exactly one type. TypeLabel/TypeDescription are what
	// NovelReportService.Create snapshots onto a new report (see
	// migration 0041's comment on novel_reports.reason_type).
	TypeID          string
	TypeLabel       string
	TypeDescription string
}

type ReportReasonRepository struct {
	pool *pgxpool.Pool
}

func NewReportReasonRepository(pool *pgxpool.Pool) *ReportReasonRepository {
	return &ReportReasonRepository{pool: pool}
}

const reportReasonColumns = `
	rr.id, rr.label, rr.requires_details, rr.position,
	rt.id, rt.label, rt.description`

const reportReasonFromClause = `
	report_reasons rr
	JOIN report_reason_types rt ON rt.id = rr.type_id`

func scanReportReason(row pgx.Row) (*ReportReason, error) {
	reason := &ReportReason{}
	err := row.Scan(
		&reason.ID, &reason.Label, &reason.RequiresDetails, &reason.Position,
		&reason.TypeID, &reason.TypeLabel, &reason.TypeDescription,
	)
	return reason, err
}

func (repository *ReportReasonRepository) List(ctx context.Context) ([]*ReportReason, error) {
	rows, err := repository.pool.Query(ctx,
		"SELECT "+reportReasonColumns+" FROM "+reportReasonFromClause+" ORDER BY rr.position, rr.created_at")
	if err != nil {
		return nil, fmt.Errorf("list report reasons: %w", err)
	}
	defer rows.Close()

	reasons := []*ReportReason{}
	for rows.Next() {
		reason, err := scanReportReason(rows)
		if err != nil {
			return nil, fmt.Errorf("scan report reason: %w", err)
		}
		reasons = append(reasons, reason)
	}
	return reasons, rows.Err()
}

// GetByLabel looks up a reason by its exact (trimmed) label — used by
// NovelReportService.Create to validate a submitted reason against
// the current list and read its RequiresDetails flag plus its parent
// type's label/description to snapshot onto the new report.
func (repository *ReportReasonRepository) GetByLabel(ctx context.Context, label string) (*ReportReason, error) {
	reason, err := scanReportReason(repository.pool.QueryRow(ctx,
		"SELECT "+reportReasonColumns+" FROM "+reportReasonFromClause+" WHERE rr.label = $1", label))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrReportReasonNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get report reason by label: %w", err)
	}
	return reason, nil
}

// Create appends the new reason to the end of the display order.
// typeID must reference an existing ReportReasonType.
func (repository *ReportReasonRepository) Create(ctx context.Context, label string, requiresDetails bool, typeID string) (*ReportReason, error) {
	var reasonID string
	err := repository.pool.QueryRow(ctx, `
		INSERT INTO report_reasons (label, requires_details, type_id, position)
		VALUES ($1, $2, $3, (SELECT COALESCE(MAX(position), -1) + 1 FROM report_reasons))
		RETURNING id`,
		label, requiresDetails, typeID,
	).Scan(&reasonID)
	if err != nil {
		var postgresError *pgconn.PgError
		if errors.As(err, &postgresError) {
			switch postgresError.Code {
			case pgerrcodeUniqueViolation:
				return nil, ErrReportReasonLabelTaken
			case pgerrcodeForeignKeyViolation:
				return nil, ErrReportReasonTypeNotFound
			}
		}
		return nil, fmt.Errorf("insert report reason: %w", err)
	}
	return repository.getByID(ctx, reasonID)
}

// Update renames a reason, retoggles RequiresDetails, and/or moves it
// to a different type, in place. Reports already submitted under the
// old label/type keep that text verbatim — see migration 0041's
// comment. Position is untouched here; see Reorder.
func (repository *ReportReasonRepository) Update(ctx context.Context, reasonID, label string, requiresDetails bool, typeID string) (*ReportReason, error) {
	commandTag, err := repository.pool.Exec(ctx,
		"UPDATE report_reasons SET label = $1, requires_details = $2, type_id = $3 WHERE id = $4",
		label, requiresDetails, typeID, reasonID,
	)
	if err != nil {
		var postgresError *pgconn.PgError
		if errors.As(err, &postgresError) {
			switch postgresError.Code {
			case pgerrcodeUniqueViolation:
				return nil, ErrReportReasonLabelTaken
			case pgerrcodeForeignKeyViolation:
				return nil, ErrReportReasonTypeNotFound
			}
		}
		return nil, fmt.Errorf("update report reason: %w", err)
	}
	if commandTag.RowsAffected() == 0 {
		return nil, ErrReportReasonNotFound
	}
	return repository.getByID(ctx, reasonID)
}

func (repository *ReportReasonRepository) getByID(ctx context.Context, reasonID string) (*ReportReason, error) {
	reason, err := scanReportReason(repository.pool.QueryRow(ctx,
		"SELECT "+reportReasonColumns+" FROM "+reportReasonFromClause+" WHERE rr.id = $1", reasonID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrReportReasonNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get report reason: %w", err)
	}
	return reason, nil
}

// Reorder sets position = index for each id in orderedIDs, in one
// transaction — the admin dashboard's drag-reorder in the Report
// Types list. IDs not present are left with their current position;
// the dashboard always sends the full list, so this is just a safety
// margin, not the expected path.
func (repository *ReportReasonRepository) Reorder(ctx context.Context, orderedIDs []string) error {
	transaction, err := repository.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin reorder report reasons: %w", err)
	}
	defer transaction.Rollback(ctx)

	for index, reasonID := range orderedIDs {
		if _, err := transaction.Exec(ctx,
			"UPDATE report_reasons SET position = $1 WHERE id = $2", index, reasonID,
		); err != nil {
			return fmt.Errorf("update report reason position: %w", err)
		}
	}
	if err := transaction.Commit(ctx); err != nil {
		return fmt.Errorf("commit reorder report reasons: %w", err)
	}
	return nil
}

// Delete removes a reason type. Blocked (ErrLastReportReason) if it's
// the only row left, so the app's report sheet can never end up with
// zero choices — deleting one already in use elsewhere is otherwise
// safe since novel_reports.reason stores the label text, not a
// foreign key to this table.
func (repository *ReportReasonRepository) Delete(ctx context.Context, reasonID string) error {
	commandTag, err := repository.pool.Exec(ctx,
		"DELETE FROM report_reasons WHERE id = $1 AND (SELECT COUNT(*) FROM report_reasons) > 1", reasonID)
	if err != nil {
		return fmt.Errorf("delete report reason: %w", err)
	}
	if commandTag.RowsAffected() > 0 {
		return nil
	}
	var exists bool
	if err := repository.pool.QueryRow(ctx,
		"SELECT EXISTS (SELECT 1 FROM report_reasons WHERE id = $1)", reasonID,
	).Scan(&exists); err != nil {
		return fmt.Errorf("check report reason exists: %w", err)
	}
	if exists {
		return ErrLastReportReason
	}
	return ErrReportReasonNotFound
}
