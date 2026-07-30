package repository

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrCommentNotFound = errors.New("comment not found")

// NovelComment is one comment or reply. IsDeleted mirrors deleted_at —
// a deleted comment's Body is always "" (see commentColumns); the row
// stays so a deleted top-level comment's replies keep a parent to hang
// off, same as any soft-deleted row elsewhere in this codebase.
type NovelComment struct {
	ID              string
	NovelID         string
	UserID          string
	Username        string
	AvatarURL       string
	ParentCommentID *string
	Body            string
	IsDeleted       bool
	CreatedAt       time.Time
	UpdatedAt       time.Time
	// Replies is populated only by ListTopLevelForNovel (via
	// ListRepliesForParents) — nil everywhere else, including on a
	// reply itself (this codebase supports exactly one nesting level).
	Replies []*NovelComment
}

type NovelCommentRepository struct {
	pool *pgxpool.Pool
}

func NewNovelCommentRepository(pool *pgxpool.Pool) *NovelCommentRepository {
	return &NovelCommentRepository{pool: pool}
}

const commentColumns = `
	c.id, c.novel_id, c.user_id, u.username, coalesce(u.avatar_url, ''),
	c.parent_comment_id,
	CASE WHEN c.deleted_at IS NULL THEN c.body ELSE '' END,
	c.deleted_at IS NOT NULL,
	c.created_at, c.updated_at`

func scanComment(row pgx.Row) (*NovelComment, error) {
	comment := &NovelComment{}
	err := row.Scan(
		&comment.ID, &comment.NovelID, &comment.UserID, &comment.Username, &comment.AvatarURL,
		&comment.ParentCommentID, &comment.Body, &comment.IsDeleted,
		&comment.CreatedAt, &comment.UpdatedAt,
	)
	return comment, err
}

// Create posts a new top-level comment (parentCommentID nil) or reply.
func (repository *NovelCommentRepository) Create(ctx context.Context, novelID, userID string, parentCommentID *string, body string) (*NovelComment, error) {
	var commentID string
	err := repository.pool.QueryRow(ctx, `
		INSERT INTO novel_comments (novel_id, user_id, parent_comment_id, body)
		VALUES ($1, $2, $3, $4) RETURNING id`,
		novelID, userID, parentCommentID, body,
	).Scan(&commentID)
	if err != nil {
		return nil, fmt.Errorf("create comment: %w", err)
	}
	return repository.GetByID(ctx, commentID)
}

func (repository *NovelCommentRepository) GetByID(ctx context.Context, commentID string) (*NovelComment, error) {
	comment, err := scanComment(repository.pool.QueryRow(ctx, `
		SELECT `+commentColumns+`
		FROM novel_comments c JOIN users u ON u.id = c.user_id
		WHERE c.id = $1`, commentID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrCommentNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get comment: %w", err)
	}
	return comment, nil
}

// Update overwrites a comment's body — scoped to userID owning it and
// it not already being deleted, so this can never resurrect or hijack
// a comment. RowsAffected() == 0 covers "doesn't exist", "not yours",
// and "already deleted" alike, without leaking which.
func (repository *NovelCommentRepository) Update(ctx context.Context, commentID, userID, body string) (*NovelComment, error) {
	commandTag, err := repository.pool.Exec(ctx, `
		UPDATE novel_comments SET body = $3, updated_at = now()
		WHERE id = $1 AND user_id = $2 AND deleted_at IS NULL`,
		commentID, userID, body)
	if err != nil {
		return nil, fmt.Errorf("update comment: %w", err)
	}
	if commandTag.RowsAffected() == 0 {
		return nil, ErrCommentNotFound
	}
	return repository.GetByID(ctx, commentID)
}

// SoftDelete removes commentID — the caller must own it (a reader
// deleting their own comment).
func (repository *NovelCommentRepository) SoftDelete(ctx context.Context, commentID, userID string) error {
	commandTag, err := repository.pool.Exec(ctx, `
		UPDATE novel_comments SET deleted_at = now()
		WHERE id = $1 AND user_id = $2 AND deleted_at IS NULL`,
		commentID, userID)
	if err != nil {
		return fmt.Errorf("delete comment: %w", err)
	}
	if commandTag.RowsAffected() == 0 {
		return ErrCommentNotFound
	}
	return nil
}

// AdminSoftDelete is the moderation counterpart of SoftDelete — no
// ownership check, any comment.
func (repository *NovelCommentRepository) AdminSoftDelete(ctx context.Context, commentID string) error {
	commandTag, err := repository.pool.Exec(ctx,
		"UPDATE novel_comments SET deleted_at = now() WHERE id = $1 AND deleted_at IS NULL",
		commentID)
	if err != nil {
		return fmt.Errorf("admin delete comment: %w", err)
	}
	if commandTag.RowsAffected() == 0 {
		return ErrCommentNotFound
	}
	return nil
}

// ListTopLevelForNovel returns one page of top-level comments (deleted
// ones included, blanked — see NovelComment's doc comment), newest
// first, plus the total top-level count for pagination.
func (repository *NovelCommentRepository) ListTopLevelForNovel(ctx context.Context, novelID string, page, pageSize int) ([]*NovelComment, int, error) {
	var total int
	if err := repository.pool.QueryRow(ctx,
		"SELECT count(*) FROM novel_comments WHERE novel_id = $1 AND parent_comment_id IS NULL",
		novelID).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count comments: %w", err)
	}

	rows, err := repository.pool.Query(ctx, `
		SELECT `+commentColumns+`
		FROM novel_comments c JOIN users u ON u.id = c.user_id
		WHERE c.novel_id = $1 AND c.parent_comment_id IS NULL
		ORDER BY c.created_at DESC
		LIMIT $2 OFFSET $3`, novelID, pageSize, (page-1)*pageSize)
	if err != nil {
		return nil, 0, fmt.Errorf("list comments: %w", err)
	}
	defer rows.Close()

	comments := []*NovelComment{}
	for rows.Next() {
		comment, err := scanComment(rows)
		if err != nil {
			return nil, 0, fmt.Errorf("scan comment: %w", err)
		}
		comments = append(comments, comment)
	}
	return comments, total, rows.Err()
}

// ListRepliesForParents batches every reply under the given top-level
// comment ids (an already-loaded page), oldest first within each
// parent — one round trip for a whole page instead of one per comment.
func (repository *NovelCommentRepository) ListRepliesForParents(ctx context.Context, parentIDs []string) (map[string][]*NovelComment, error) {
	byParent := make(map[string][]*NovelComment, len(parentIDs))
	if len(parentIDs) == 0 {
		return byParent, nil
	}

	rows, err := repository.pool.Query(ctx, `
		SELECT `+commentColumns+`
		FROM novel_comments c JOIN users u ON u.id = c.user_id
		WHERE c.parent_comment_id = ANY($1)
		ORDER BY c.created_at ASC`, parentIDs)
	if err != nil {
		return nil, fmt.Errorf("list replies: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		reply, err := scanComment(rows)
		if err != nil {
			return nil, fmt.Errorf("scan reply: %w", err)
		}
		byParent[*reply.ParentCommentID] = append(byParent[*reply.ParentCommentID], reply)
	}
	return byParent, rows.Err()
}
