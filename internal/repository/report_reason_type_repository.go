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
	ErrReportReasonTypeNotFound   = errors.New("report reason type not found")
	ErrReportReasonTypeLabelTaken = errors.New("report reason type label already exists")
	ErrReportReasonTypeInUse      = errors.New("cannot delete a report reason type that reasons still belong to")
)

// ReportReasonType groups report_reasons into a broader category —
// what the author dashboard shows on a report (with Description as
// the info-tap copy) instead of the reader's specific reason. See
// migration 0039.
type ReportReasonType struct {
	ID          string
	Label       string
	Description string
	Position    int
}

type ReportReasonTypeRepository struct {
	pool *pgxpool.Pool
}

func NewReportReasonTypeRepository(pool *pgxpool.Pool) *ReportReasonTypeRepository {
	return &ReportReasonTypeRepository{pool: pool}
}

func (repository *ReportReasonTypeRepository) List(ctx context.Context) ([]*ReportReasonType, error) {
	rows, err := repository.pool.Query(ctx,
		"SELECT id, label, description, position FROM report_reason_types ORDER BY position, created_at")
	if err != nil {
		return nil, fmt.Errorf("list report reason types: %w", err)
	}
	defer rows.Close()

	types := []*ReportReasonType{}
	for rows.Next() {
		reasonType := &ReportReasonType{}
		if err := rows.Scan(&reasonType.ID, &reasonType.Label, &reasonType.Description, &reasonType.Position); err != nil {
			return nil, fmt.Errorf("scan report reason type: %w", err)
		}
		types = append(types, reasonType)
	}
	return types, rows.Err()
}

// Create appends the new type to the end of the display order.
func (repository *ReportReasonTypeRepository) Create(ctx context.Context, label, description string) (*ReportReasonType, error) {
	reasonType := &ReportReasonType{}
	err := repository.pool.QueryRow(ctx, `
		INSERT INTO report_reason_types (label, description, position)
		VALUES ($1, $2, (SELECT COALESCE(MAX(position), -1) + 1 FROM report_reason_types))
		RETURNING id, label, description, position`,
		label, description,
	).Scan(&reasonType.ID, &reasonType.Label, &reasonType.Description, &reasonType.Position)
	if err != nil {
		var postgresError *pgconn.PgError
		if errors.As(err, &postgresError) && postgresError.Code == pgerrcodeUniqueViolation {
			return nil, ErrReportReasonTypeLabelTaken
		}
		return nil, fmt.Errorf("insert report reason type: %w", err)
	}
	return reasonType, nil
}

// Update relabels and/or redescribes a type in place. Reports already
// submitted under it keep the old label/description verbatim — see
// migration 0041's comment.
func (repository *ReportReasonTypeRepository) Update(ctx context.Context, typeID, label, description string) (*ReportReasonType, error) {
	reasonType := &ReportReasonType{}
	err := repository.pool.QueryRow(ctx,
		"UPDATE report_reason_types SET label = $1, description = $2 WHERE id = $3 RETURNING id, label, description, position",
		label, description, typeID,
	).Scan(&reasonType.ID, &reasonType.Label, &reasonType.Description, &reasonType.Position)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrReportReasonTypeNotFound
	}
	if err != nil {
		var postgresError *pgconn.PgError
		if errors.As(err, &postgresError) && postgresError.Code == pgerrcodeUniqueViolation {
			return nil, ErrReportReasonTypeLabelTaken
		}
		return nil, fmt.Errorf("update report reason type: %w", err)
	}
	return reasonType, nil
}

// Delete removes a type. Blocked (ErrReportReasonTypeInUse) while any
// report_reasons row still points at it — a reason must always belong
// to some type, so reassign its reasons to a different type first.
func (repository *ReportReasonTypeRepository) Delete(ctx context.Context, typeID string) error {
	commandTag, err := repository.pool.Exec(ctx, "DELETE FROM report_reason_types WHERE id = $1", typeID)
	if err != nil {
		var postgresError *pgconn.PgError
		if errors.As(err, &postgresError) && postgresError.Code == pgerrcodeForeignKeyViolation {
			return ErrReportReasonTypeInUse
		}
		return fmt.Errorf("delete report reason type: %w", err)
	}
	if commandTag.RowsAffected() == 0 {
		return ErrReportReasonTypeNotFound
	}
	return nil
}

// Reorder sets position = index for each id in orderedIDs, in one
// transaction — the admin dashboard's drag-reorder.
func (repository *ReportReasonTypeRepository) Reorder(ctx context.Context, orderedIDs []string) error {
	transaction, err := repository.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin reorder report reason types: %w", err)
	}
	defer transaction.Rollback(ctx)

	for index, typeID := range orderedIDs {
		if _, err := transaction.Exec(ctx,
			"UPDATE report_reason_types SET position = $1 WHERE id = $2", index, typeID,
		); err != nil {
			return fmt.Errorf("update report reason type position: %w", err)
		}
	}
	if err := transaction.Commit(ctx); err != nil {
		return fmt.Errorf("commit reorder report reason types: %w", err)
	}
	return nil
}
