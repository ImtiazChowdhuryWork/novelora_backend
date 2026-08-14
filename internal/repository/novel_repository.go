package repository

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrNovelNotFound = errors.New("novel not found")

// minTrustedRatingCount is Phase 5b's threshold: below this many real
// votes, a novel's average_rating isn't trusted for sorting/display
// purposes yet — the admin-typed rating is used instead, same as an
// under-rated novel getting excluded from a ranked shelf rather than
// sorted at face value (see the plan's Phase 5b note).
const minTrustedRatingCount = 3

// Novel is a row in the novels table (soft-deleted rows are never returned).
type Novel struct {
	ID            string
	Title         string
	AuthorName    string
	Synopsis      string
	CoverURL      string
	Status        string // "ongoing" | "completed"
	IsShort       bool
	IsRecommended bool
	IsExclusive   bool
	Rating        *float64
	// AverageRating and RatingCount are Phase 5b's real reader-rating
	// aggregate, recomputed by NovelRatingRepository on every vote —
	// AverageRating is on the same 0-10 scale as Rating (a 1-5 star vote
	// doubled), so the two are directly comparable; see the "rating"
	// sort case below for how they combine.
	AverageRating *float64
	RatingCount   int
	// SupportCount is Phase 5d's free "Support" tap tally, recomputed by
	// NovelSupportRepository on every add/remove — a like/favorite, not
	// a monetary gift (see the plan's Phase 5d note).
	SupportCount      int
	ViewCount         int64
	PublishedChapters int
	TotalChapters     int
	// SortOrder is the admin's manual display order (lower = earlier).
	// New novels are appended to the end; see Reorder for changing it.
	SortOrder int
	CreatedAt time.Time
	UpdatedAt time.Time
	// OwnerUserID is nil for every admin-uploaded novel (all of them,
	// historically) — only set for novels created through the
	// author-scoped endpoints. AuthorName stays the reader-facing
	// display field either way; this is purely an ACL/ownership field,
	// see migration 0030.
	OwnerUserID *string
	// Genres is populated by NovelService, not this repository — see
	// GenreRepository.ListForNovel(s). Nil until attached.
	Genres []*Genre
}

// NovelListFilter narrows and pages the novels list.
type NovelListFilter struct {
	Search        string
	Status        string // "", "ongoing", "completed"
	IsShort       *bool  // nil = both
	IsRecommended *bool  // nil = both
	IsExclusive   *bool  // nil = both
	GenreID       string // "" = any genre/tag; single-value filter used by the public catalog API
	// GenreIDs is the admin dashboard's multi-select genre/tag filter,
	// independent of GenreID above. Combined per GenreMatchMode.
	GenreIDs       []string
	GenreMatchMode string // "any" (default, OR) | "all" (AND) — only relevant when GenreIDs is non-empty
	// Sort: "" (manual order), "views" (lifetime view_count), "trending"
	// (7-day recent-activity score, see the List switch below), "rating",
	// "new"
	Sort string
	// OwnerUserID scopes to one author's own novels — nil (the default,
	// used by every admin/public list today) means no ownership
	// filtering at all.
	OwnerUserID *string
	Page        int // 1-based
	PageSize    int
}

// NovelWrite is the mutable subset used by Create and Update.
type NovelWrite struct {
	Title         string
	AuthorName    string
	Synopsis      string
	Status        string
	IsShort       bool
	IsRecommended bool
	IsExclusive   bool
	Rating        *float64
	// ViewCount is the admin-editable override of the lifetime view
	// counter — always writable, additive to the automatic increments
	// RecordView makes on real reads (never made read-only for either
	// source).
	ViewCount int64
	// OwnerUserID is only read by Create (Update leaves ownership
	// immutable post-creation) — nil for admin-created novels.
	OwnerUserID *string
}

type NovelRepository struct {
	pool *pgxpool.Pool
}

func NewNovelRepository(pool *pgxpool.Pool) *NovelRepository {
	return &NovelRepository{pool: pool}
}

const novelColumns = `
	n.id, n.title, n.author_name, n.synopsis, coalesce(n.cover_url, ''),
	n.status, n.is_short, n.is_recommended, n.is_exclusive, n.rating,
	n.average_rating, n.rating_count, n.support_count, n.view_count,
	(SELECT count(*) FROM chapters c WHERE c.novel_id = n.id AND c.status = 'published'),
	(SELECT count(*) FROM chapters c WHERE c.novel_id = n.id),
	n.sort_order, n.created_at, n.updated_at, n.owner_user_id`

func scanNovel(row pgx.Row) (*Novel, error) {
	novel := &Novel{}
	err := row.Scan(
		&novel.ID, &novel.Title, &novel.AuthorName, &novel.Synopsis, &novel.CoverURL,
		&novel.Status, &novel.IsShort, &novel.IsRecommended, &novel.IsExclusive, &novel.Rating,
		&novel.AverageRating, &novel.RatingCount, &novel.SupportCount, &novel.ViewCount,
		&novel.PublishedChapters, &novel.TotalChapters,
		&novel.SortOrder, &novel.CreatedAt, &novel.UpdatedAt, &novel.OwnerUserID,
	)
	return novel, err
}

// List returns one page of novels plus the total row count for the filter.
func (repository *NovelRepository) List(ctx context.Context, filter NovelListFilter) ([]*Novel, int, error) {
	conditions := []string{"n.deleted_at IS NULL"}
	arguments := []any{}

	if filter.Search != "" {
		arguments = append(arguments, "%"+strings.ToLower(filter.Search)+"%")
		idx := len(arguments)
		// Matches title/author OR any genre/tag name the novel carries
		// (e.g. searching "Comedy" surfaces every novel tagged with that
		// genre, not just one literally titled "Comedy"). Separate ng2/g2
		// aliases avoid clashing with the ng alias filter.GenreID's join
		// below already uses.
		conditions = append(conditions, fmt.Sprintf(`(lower(n.title) LIKE $%d OR lower(n.author_name) LIKE $%d OR EXISTS (
			SELECT 1 FROM novel_genres ng2
			JOIN genres g2 ON g2.id = ng2.genre_id
			WHERE ng2.novel_id = n.id AND lower(g2.name) LIKE $%d
		))`, idx, idx, idx))
	}
	if filter.Status != "" {
		arguments = append(arguments, filter.Status)
		conditions = append(conditions, fmt.Sprintf("n.status = $%d", len(arguments)))
	}
	if filter.IsShort != nil {
		arguments = append(arguments, *filter.IsShort)
		conditions = append(conditions, fmt.Sprintf("n.is_short = $%d", len(arguments)))
	}
	if filter.IsRecommended != nil {
		arguments = append(arguments, *filter.IsRecommended)
		conditions = append(conditions, fmt.Sprintf("n.is_recommended = $%d", len(arguments)))
	}
	if filter.IsExclusive != nil {
		arguments = append(arguments, *filter.IsExclusive)
		conditions = append(conditions, fmt.Sprintf("n.is_exclusive = $%d", len(arguments)))
	}
	if filter.OwnerUserID != nil {
		arguments = append(arguments, *filter.OwnerUserID)
		conditions = append(conditions, fmt.Sprintf("n.owner_user_id = $%d", len(arguments)))
	}
	if len(filter.GenreIDs) > 0 {
		arguments = append(arguments, filter.GenreIDs)
		idsIndex := len(arguments)
		if filter.GenreMatchMode == "all" {
			arguments = append(arguments, len(filter.GenreIDs))
			countIndex := len(arguments)
			// "All": the novel must carry every selected genre/tag, so the
			// distinct count of matches has to equal how many were selected.
			conditions = append(conditions, fmt.Sprintf(`(
				SELECT count(DISTINCT ng3.genre_id) FROM novel_genres ng3
				WHERE ng3.novel_id = n.id AND ng3.genre_id = ANY($%d::uuid[])
			) = $%d`, idsIndex, countIndex))
		} else {
			// "Any" (default): the novel just needs to carry at least one
			// of the selected genres/tags.
			conditions = append(conditions, fmt.Sprintf(`EXISTS (
				SELECT 1 FROM novel_genres ng3
				WHERE ng3.novel_id = n.id AND ng3.genre_id = ANY($%d::uuid[])
			)`, idsIndex))
		}
	}
	joinClause := ""
	if filter.GenreID != "" {
		arguments = append(arguments, filter.GenreID)
		joinClause = fmt.Sprintf("JOIN novel_genres ng ON ng.novel_id = n.id AND ng.genre_id = $%d", len(arguments))
	}
	whereClause := strings.Join(conditions, " AND ")

	var total int
	err := repository.pool.QueryRow(ctx,
		fmt.Sprintf("SELECT count(*) FROM novels n %s WHERE %s", joinClause, whereClause), arguments...,
	).Scan(&total)
	if err != nil {
		return nil, 0, fmt.Errorf("count novels: %w", err)
	}

	orderClause := "n.sort_order ASC, n.created_at DESC"
	switch filter.Sort {
	case "views":
		orderClause = "n.view_count DESC, n.created_at DESC"
	case "trending":
		// Real, transparent 7-day-activity score — recent views (from
		// novel_daily_views, see NovelRepository.RecordView) decayed by
		// age, Hacker-News-"hot"-style, so a novel that was popular once
		// doesn't rank #1 forever and a brand-new novel isn't unfairly
		// buried against one with a longer history. No hidden formula:
		// one tunable gravity constant (1.5). Ties (typically two novels
		// with zero recent views) fall back to lifetime view_count for a
		// stable order instead of an arbitrary one.
		orderClause = `(
			coalesce((
				SELECT sum(v.views) FROM novel_daily_views v
				WHERE v.novel_id = n.id AND v.day >= current_date - interval '7 days'
			), 0)
			/ power(extract(epoch FROM (now() - n.created_at)) / 86400.0 + 2, 1.5)
		) DESC, n.view_count DESC`
	case "rating":
		// Real reader ratings once there are enough of them to trust;
		// the admin-typed rating is the fallback below that threshold
		// (also covers novels nobody has rated yet, average_rating NULL).
		orderClause = fmt.Sprintf(`(
			CASE WHEN n.rating_count >= %d THEN n.average_rating ELSE n.rating END
		) DESC NULLS LAST, n.view_count DESC`, minTrustedRatingCount)
	case "new":
		orderClause = "n.created_at DESC"
	}

	arguments = append(arguments, filter.PageSize, (filter.Page-1)*filter.PageSize)
	rows, err := repository.pool.Query(ctx, fmt.Sprintf(
		"SELECT %s FROM novels n %s WHERE %s ORDER BY %s LIMIT $%d OFFSET $%d",
		novelColumns, joinClause, whereClause, orderClause, len(arguments)-1, len(arguments)), arguments...)
	if err != nil {
		return nil, 0, fmt.Errorf("list novels: %w", err)
	}
	defer rows.Close()

	novels := []*Novel{}
	for rows.Next() {
		novel, err := scanNovel(rows)
		if err != nil {
			return nil, 0, fmt.Errorf("scan novel: %w", err)
		}
		novels = append(novels, novel)
	}
	return novels, total, rows.Err()
}

// ListByIDs batches a lookup for a specific set of novels — used to
// materialize pinned overrides (see DiscoverSectionService.Resolve) into
// full Novel records. Order is not guaranteed to match ids; callers that
// care about order (pinned position) re-sort themselves. Deleted novels
// and unknown ids are silently omitted, not errors.
func (repository *NovelRepository) ListByIDs(ctx context.Context, ids []string) ([]*Novel, error) {
	if len(ids) == 0 {
		return []*Novel{}, nil
	}
	rows, err := repository.pool.Query(ctx,
		"SELECT "+novelColumns+" FROM novels n WHERE n.id = ANY($1) AND n.deleted_at IS NULL", ids)
	if err != nil {
		return nil, fmt.Errorf("list novels by ids: %w", err)
	}
	defer rows.Close()

	novels := []*Novel{}
	for rows.Next() {
		novel, err := scanNovel(rows)
		if err != nil {
			return nil, fmt.Errorf("scan novel: %w", err)
		}
		novels = append(novels, novel)
	}
	return novels, rows.Err()
}

func (repository *NovelRepository) GetByID(ctx context.Context, novelID string) (*Novel, error) {
	novel, err := scanNovel(repository.pool.QueryRow(ctx,
		"SELECT "+novelColumns+" FROM novels n WHERE n.id = $1 AND n.deleted_at IS NULL",
		novelID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNovelNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get novel: %w", err)
	}
	return novel, nil
}

// Create appends the new novel to the end of the manual display order.
func (repository *NovelRepository) Create(ctx context.Context, write NovelWrite) (*Novel, error) {
	novel, err := scanNovel(repository.pool.QueryRow(ctx, `
		WITH inserted AS (
			INSERT INTO novels (title, author_name, synopsis, status, is_short, is_recommended, is_exclusive, rating, view_count, owner_user_id, sort_order)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, (SELECT coalesce(max(sort_order), 0) + 1 FROM novels))
			RETURNING *
		)
		SELECT `+novelColumns+` FROM inserted n`,
		write.Title, write.AuthorName, write.Synopsis, write.Status,
		write.IsShort, write.IsRecommended, write.IsExclusive, write.Rating, write.ViewCount, write.OwnerUserID))
	if err != nil {
		return nil, fmt.Errorf("insert novel: %w", err)
	}
	return novel, nil
}

// NovelPosition is one novel's new place in the manual display order.
type NovelPosition struct {
	NovelID   string
	SortOrder int
}

// Reorder sets sort_order = SortOrder for each given novel, exactly as
// provided — the caller (not this method) decides what those values
// mean. This matters because the dashboard's drag-to-reorder only ever
// has one page of novels loaded at a time: it computes each dragged
// novel's absolute sort_order (page offset + local position) rather
// than relying on array index, so reordering within page 2 can't
// collide with page 1's untouched sort_order values.
func (repository *NovelRepository) Reorder(ctx context.Context, positions []NovelPosition) error {
	transaction, err := repository.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin reorder: %w", err)
	}
	defer transaction.Rollback(ctx)

	for _, position := range positions {
		if _, err := transaction.Exec(ctx,
			"UPDATE novels SET sort_order = $1 WHERE id = $2 AND deleted_at IS NULL",
			position.SortOrder, position.NovelID,
		); err != nil {
			return fmt.Errorf("reorder novel %s: %w", position.NovelID, err)
		}
	}
	if err := transaction.Commit(ctx); err != nil {
		return fmt.Errorf("commit reorder: %w", err)
	}
	return nil
}

func (repository *NovelRepository) Update(ctx context.Context, novelID string, write NovelWrite) (*Novel, error) {
	novel, err := scanNovel(repository.pool.QueryRow(ctx, `
		WITH updated AS (
			UPDATE novels SET
				title = $2, author_name = $3, synopsis = $4, status = $5,
				is_short = $6, is_recommended = $7, is_exclusive = $8, rating = $9, view_count = $10, updated_at = now()
			WHERE id = $1 AND deleted_at IS NULL
			RETURNING *
		)
		SELECT `+novelColumns+` FROM updated n`,
		novelID, write.Title, write.AuthorName, write.Synopsis, write.Status,
		write.IsShort, write.IsRecommended, write.IsExclusive, write.Rating, write.ViewCount))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNovelNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("update novel: %w", err)
	}
	return novel, nil
}

// UpdateCoverURL replaces the cover; empty clears it.
func (repository *NovelRepository) UpdateCoverURL(ctx context.Context, novelID, coverURL string) error {
	commandTag, err := repository.pool.Exec(ctx,
		`UPDATE novels SET cover_url = nullif($2, ''), updated_at = now()
		 WHERE id = $1 AND deleted_at IS NULL`,
		novelID, coverURL)
	if err != nil {
		return fmt.Errorf("update novel cover: %w", err)
	}
	if commandTag.RowsAffected() == 0 {
		return ErrNovelNotFound
	}
	return nil
}

// RecordView increments a novel's lifetime view_count and today's
// rolling daily-view row (novel_daily_views) — the automatic signal
// the trending score is computed from. Additive to the admin-editable
// ViewCount in NovelWrite, never a replacement for it: an admin can
// still set view_count to anything, and real reads keep incrementing
// from whatever value is currently there. Best-effort by design —
// callers should log-and-continue rather than fail the read that
// triggered this.
func (repository *NovelRepository) RecordView(ctx context.Context, novelID string) error {
	transaction, err := repository.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin record view: %w", err)
	}
	defer transaction.Rollback(ctx)

	commandTag, err := transaction.Exec(ctx,
		"UPDATE novels SET view_count = view_count + 1 WHERE id = $1 AND deleted_at IS NULL",
		novelID)
	if err != nil {
		return fmt.Errorf("increment view_count: %w", err)
	}
	if commandTag.RowsAffected() == 0 {
		return ErrNovelNotFound
	}

	if _, err := transaction.Exec(ctx, `
		INSERT INTO novel_daily_views (novel_id, day, views)
		VALUES ($1, current_date, 1)
		ON CONFLICT (novel_id, day) DO UPDATE SET views = novel_daily_views.views + 1`,
		novelID); err != nil {
		return fmt.Errorf("record daily view: %w", err)
	}

	if err := transaction.Commit(ctx); err != nil {
		return fmt.Errorf("commit record view: %w", err)
	}
	return nil
}

// SoftDelete hides the novel (and its chapters via queries) without
// destroying data; recoverable by clearing deleted_at manually.
func (repository *NovelRepository) SoftDelete(ctx context.Context, novelID string) error {
	commandTag, err := repository.pool.Exec(ctx,
		"UPDATE novels SET deleted_at = now() WHERE id = $1 AND deleted_at IS NULL",
		novelID)
	if err != nil {
		return fmt.Errorf("soft delete novel: %w", err)
	}
	if commandTag.RowsAffected() == 0 {
		return ErrNovelNotFound
	}
	return nil
}

// ListByAuthorName is the admin report detail view's "other novels by
// this author" panel — an exact, case-sensitive match against the same
// free-text author_name every novel already carries (there's no
// author-account system to join against instead). Newest first.
func (repository *NovelRepository) ListByAuthorName(ctx context.Context, authorName string) ([]*Novel, error) {
	rows, err := repository.pool.Query(ctx, `
		SELECT `+novelColumns+`
		FROM novels n
		WHERE n.author_name = $1 AND n.deleted_at IS NULL
		ORDER BY n.created_at DESC`, authorName)
	if err != nil {
		return nil, fmt.Errorf("list novels by author: %w", err)
	}
	defer rows.Close()

	novels := []*Novel{}
	for rows.Next() {
		novel, err := scanNovel(rows)
		if err != nil {
			return nil, fmt.Errorf("scan novel: %w", err)
		}
		novels = append(novels, novel)
	}
	return novels, rows.Err()
}

// BulkHideByAuthorName soft-deletes every non-deleted novel by an
// exact author-name match in one action — the admin "take action
// against the author" bulk-hide button. Returns the ids that were
// hidden, so the caller can publish a realtime "novel.deleted" event
// per novel the same way a single-novel delete does.
func (repository *NovelRepository) BulkHideByAuthorName(ctx context.Context, authorName string) ([]string, error) {
	rows, err := repository.pool.Query(ctx,
		"UPDATE novels SET deleted_at = now() WHERE author_name = $1 AND deleted_at IS NULL RETURNING id",
		authorName)
	if err != nil {
		return nil, fmt.Errorf("bulk hide novels by author: %w", err)
	}
	defer rows.Close()

	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan bulk-hidden novel id: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
