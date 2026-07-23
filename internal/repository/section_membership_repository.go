package repository

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

type SectionMembershipRepository struct {
	pool *pgxpool.Pool
}

func NewSectionMembershipRepository(pool *pgxpool.Pool) *SectionMembershipRepository {
	return &SectionMembershipRepository{pool: pool}
}

// Sync reconciles a ranked section's recorded membership with its
// current top-N novel ids: ids present now but not previously recorded
// are inserted and returned (the caller notifies only these — they're
// the genuinely new entries since the last check); ids no longer
// present are removed with no notification; ids present both times are
// left untouched.
func (repository *SectionMembershipRepository) Sync(ctx context.Context, sectionKey string, currentNovelIDs []string) ([]string, error) {
	rows, err := repository.pool.Query(ctx,
		"SELECT novel_id FROM novel_section_memberships WHERE section_key = $1", sectionKey)
	if err != nil {
		return nil, fmt.Errorf("list section memberships: %w", err)
	}
	existing := map[string]bool{}
	for rows.Next() {
		var novelID string
		if err := rows.Scan(&novelID); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan section membership: %w", err)
		}
		existing[novelID] = true
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list section memberships: %w", err)
	}

	current := make(map[string]bool, len(currentNovelIDs))
	var newIDs []string
	for _, novelID := range currentNovelIDs {
		current[novelID] = true
		if !existing[novelID] {
			newIDs = append(newIDs, novelID)
		}
	}

	transaction, err := repository.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin sync section memberships: %w", err)
	}
	defer transaction.Rollback(ctx)

	for novelID := range existing {
		if current[novelID] {
			continue
		}
		if _, err := transaction.Exec(ctx,
			"DELETE FROM novel_section_memberships WHERE section_key = $1 AND novel_id = $2",
			sectionKey, novelID,
		); err != nil {
			return nil, fmt.Errorf("remove stale section membership: %w", err)
		}
	}
	for _, novelID := range newIDs {
		if _, err := transaction.Exec(ctx,
			"INSERT INTO novel_section_memberships (novel_id, section_key) VALUES ($1, $2)",
			novelID, sectionKey,
		); err != nil {
			return nil, fmt.Errorf("insert section membership: %w", err)
		}
	}
	if err := transaction.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit sync section memberships: %w", err)
	}
	return newIDs, nil
}
