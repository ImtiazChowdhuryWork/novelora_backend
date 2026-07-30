package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/ImtiazChowdhuryWork/novelora_backend/internal/realtime"
	"github.com/ImtiazChowdhuryWork/novelora_backend/internal/repository"
)

const maxCommentBodyLength = 2000

// NovelCommentService is Phase 5c: novel-level comments, one nesting
// level (top-level comments + direct replies). Unlike views/ratings
// (Phase 1, 5b), this *is* realtime-broadcast per event — a new comment
// on a novel genuinely benefits anyone else currently viewing that same
// novel's detail page, matching the plan's Phase 5c note.
type NovelCommentService struct {
	comments *repository.NovelCommentRepository
	novels   *repository.NovelRepository
	events   realtime.Publisher
}

func NewNovelCommentService(
	comments *repository.NovelCommentRepository,
	novels *repository.NovelRepository,
	events realtime.Publisher,
) *NovelCommentService {
	return &NovelCommentService{comments: comments, novels: novels, events: events}
}

// ListForNovel returns one page of top-level comments with their
// replies attached — the backing data for Book Detail's comment section.
func (service *NovelCommentService) ListForNovel(ctx context.Context, novelID string, page, pageSize int) ([]*repository.NovelComment, int, error) {
	if page < 1 {
		page = 1
	}
	switch {
	case pageSize < 1:
		pageSize = 20
	case pageSize > 100:
		pageSize = 100
	}

	topLevel, total, err := service.comments.ListTopLevelForNovel(ctx, novelID, page, pageSize)
	if err != nil {
		return nil, 0, err
	}
	if len(topLevel) == 0 {
		return topLevel, total, nil
	}

	parentIDs := make([]string, len(topLevel))
	for index, comment := range topLevel {
		parentIDs[index] = comment.ID
	}
	repliesByParent, err := service.comments.ListRepliesForParents(ctx, parentIDs)
	if err != nil {
		return nil, 0, err
	}
	for _, comment := range topLevel {
		comment.Replies = repliesByParent[comment.ID]
	}
	return topLevel, total, nil
}

// Create posts a top-level comment (parentCommentID nil) or a reply. A
// reply's parent must already be a top-level comment on the same novel
// — enforces exactly one nesting level and rules out cross-novel
// parenting.
func (service *NovelCommentService) Create(ctx context.Context, novelID, userID string, parentCommentID *string, body string) (*repository.NovelComment, error) {
	body = strings.TrimSpace(body)
	if err := validateCommentBody(body); err != nil {
		return nil, err
	}
	if _, err := service.novels.GetByID(ctx, novelID); err != nil {
		return nil, err
	}
	if parentCommentID != nil {
		parent, err := service.comments.GetByID(ctx, *parentCommentID)
		if err != nil {
			return nil, err
		}
		if parent.NovelID != novelID {
			return nil, &ValidationError{Message: "parent comment belongs to a different novel"}
		}
		if parent.ParentCommentID != nil {
			return nil, &ValidationError{Message: "cannot reply to a reply"}
		}
	}

	comment, err := service.comments.Create(ctx, novelID, userID, parentCommentID, body)
	if err != nil {
		return nil, err
	}
	service.events.Publish(realtime.Event{Topic: "comment.created", ID: novelID})
	return comment, nil
}

// Update overwrites the caller's own comment.
func (service *NovelCommentService) Update(ctx context.Context, commentID, userID, body string) (*repository.NovelComment, error) {
	body = strings.TrimSpace(body)
	if err := validateCommentBody(body); err != nil {
		return nil, err
	}
	comment, err := service.comments.Update(ctx, commentID, userID, body)
	if err != nil {
		return nil, err
	}
	service.events.Publish(realtime.Event{Topic: "comment.updated", ID: comment.NovelID})
	return comment, nil
}

// Delete withdraws the caller's own comment.
func (service *NovelCommentService) Delete(ctx context.Context, commentID, userID string) error {
	comment, err := service.comments.GetByID(ctx, commentID)
	if err != nil {
		return err
	}
	if err := service.comments.SoftDelete(ctx, commentID, userID); err != nil {
		return err
	}
	service.events.Publish(realtime.Event{Topic: "comment.deleted", ID: comment.NovelID})
	return nil
}

// AdminDelete is the moderation counterpart of Delete — no ownership
// check, any comment; never edits, only removes (see the plan's Phase
// 5c note on not hand-editing user-generated content).
func (service *NovelCommentService) AdminDelete(ctx context.Context, commentID string) error {
	comment, err := service.comments.GetByID(ctx, commentID)
	if err != nil {
		return err
	}
	if err := service.comments.AdminSoftDelete(ctx, commentID); err != nil {
		return err
	}
	service.events.Publish(realtime.Event{Topic: "comment.deleted", ID: comment.NovelID})
	return nil
}

func validateCommentBody(body string) error {
	if body == "" {
		return &ValidationError{Message: "comment cannot be empty"}
	}
	if len(body) > maxCommentBodyLength {
		return &ValidationError{Message: fmt.Sprintf("comment too long (max %d characters)", maxCommentBodyLength)}
	}
	return nil
}
