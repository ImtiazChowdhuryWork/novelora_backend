package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/ImtiazChowdhuryWork/novelora_backend/internal/push"
	"github.com/ImtiazChowdhuryWork/novelora_backend/internal/realtime"
	"github.com/ImtiazChowdhuryWork/novelora_backend/internal/repository"
)

const maxReportDetailsLength = 2000

// maxReportImages mirrors handler.maxReportImages — kept as a
// separate constant since the service package doesn't import handler
// (wrong dependency direction); enforced again here so a caller that
// isn't the HTTP handler (impossible today, but this rule lives with
// the data, not the transport) can't slip past it either.
const maxReportImages = 3

// allowedReportReasons is the fixed, reader-facing set the app's
// report sheet presents — validated here (not a DB CHECK) so the set
// can change without a migration, same choice as comment body length.
var allowedReportReasons = map[string]bool{
	"spam":          true,
	"plagiarism":    true,
	"inappropriate": true,
	"harassment":    true,
	"broken":        true,
	"other":         true,
}

// NovelReportService is Book Detail's flag-icon "Report this novel"
// feature. Unlike comments, this never broadcasts to the app — only
// the dashboard needs to know a report landed, matching
// user.registered's dashboard-only realtime shape. The one exception
// is UpdateStatus, which notifies the single reporting reader (not a
// broadcast) once an admin actions their report.
type NovelReportService struct {
	reports       *repository.NovelReportRepository
	novels        *repository.NovelRepository
	chapters      *repository.ChapterRepository
	notifications *repository.NotificationRepository
	deviceTokens  *repository.DeviceTokenRepository
	pushNotifier  push.Notifier
	events        realtime.Publisher
}

func NewNovelReportService(
	reports *repository.NovelReportRepository,
	novels *repository.NovelRepository,
	chapters *repository.ChapterRepository,
	notifications *repository.NotificationRepository,
	deviceTokens *repository.DeviceTokenRepository,
	pushNotifier push.Notifier,
	events realtime.Publisher,
) *NovelReportService {
	return &NovelReportService{
		reports:       reports,
		novels:        novels,
		chapters:      chapters,
		notifications: notifications,
		deviceTokens:  deviceTokens,
		pushNotifier:  pushNotifier,
		events:        events,
	}
}

// Create validates and stores a reader's report, then attaches any
// evidence screenshots (already saved to disk by the handler — this
// just records their URLs). "other" requires non-empty details (the
// only free-text reason); every other reason is self-explanatory as a
// category and doesn't need one. chapterID is optional — nil means
// the report is about the whole novel; when given, it must actually
// belong to novelID.
func (service *NovelReportService) Create(
	ctx context.Context, novelID, userID, reason, details string, chapterID *string, imageURLs []string,
) (*repository.NovelReport, error) {
	reason = strings.TrimSpace(reason)
	details = strings.TrimSpace(details)
	if !allowedReportReasons[reason] {
		return nil, &ValidationError{Message: "invalid report reason"}
	}
	if reason == "other" && details == "" {
		return nil, &ValidationError{Message: "please describe the issue"}
	}
	if len(details) > maxReportDetailsLength {
		return nil, &ValidationError{Message: fmt.Sprintf("details too long (max %d characters)", maxReportDetailsLength)}
	}
	if len(imageURLs) > maxReportImages {
		return nil, &ValidationError{Message: fmt.Sprintf("at most %d images allowed", maxReportImages)}
	}
	if _, err := service.novels.GetByID(ctx, novelID); err != nil {
		return nil, err
	}
	if chapterID != nil {
		chapter, err := service.chapters.GetByID(ctx, *chapterID)
		if err != nil {
			return nil, err
		}
		if chapter.NovelID != novelID {
			return nil, &ValidationError{Message: "chapter belongs to a different novel"}
		}
	}

	report, err := service.reports.Create(ctx, novelID, userID, reason, details, chapterID)
	if err != nil {
		return nil, err
	}
	if len(imageURLs) > 0 {
		if err := service.reports.AddImages(ctx, report.ID, imageURLs); err != nil {
			return nil, err
		}
		report.Images, _ = service.reports.ListImages(ctx, report.ID)
	}
	service.events.Publish(realtime.Event{Topic: "report.created", ID: report.ID})
	return report, nil
}

// GetDetail is the dashboard's single-report view (§ admin report
// detail) — the list row's data plus its evidence images.
func (service *NovelReportService) GetDetail(ctx context.Context, reportID string) (*repository.NovelReport, error) {
	report, err := service.reports.GetByID(ctx, reportID)
	if err != nil {
		return nil, err
	}
	report.Images, err = service.reports.ListImages(ctx, reportID)
	if err != nil {
		return nil, err
	}
	return report, nil
}

// List is the dashboard Reports page's data source — one page,
// optionally filtered to a single status.
func (service *NovelReportService) List(ctx context.Context, status string, page, pageSize int) ([]*repository.NovelReport, int, error) {
	if page < 1 {
		page = 1
	}
	switch {
	case pageSize < 1:
		pageSize = 20
	case pageSize > 100:
		pageSize = 100
	}
	return service.reports.List(ctx, status, page, pageSize)
}

// ListForUser is the app's "My Reports" Profile page — the caller's
// own report history, same pagination/clamp rules as List.
func (service *NovelReportService) ListForUser(ctx context.Context, userID string, page, pageSize int) ([]*repository.NovelReport, int, error) {
	if page < 1 {
		page = 1
	}
	switch {
	case pageSize < 1:
		pageSize = 20
	case pageSize > 100:
		pageSize = 100
	}
	return service.reports.ListForUser(ctx, userID, page, pageSize)
}

// HasReported backs Book Detail's flag-icon fill state.
func (service *NovelReportService) HasReported(ctx context.Context, novelID, userID string) (bool, error) {
	return service.reports.HasReported(ctx, novelID, userID)
}

func (service *NovelReportService) CountPending(ctx context.Context) (int, error) {
	return service.reports.CountPending(ctx)
}

// Delete withdraws the caller's own report — ownership is enforced by
// the repository query itself (scoped to userID), so a report id that
// exists but belongs to someone else reads identically to one that
// doesn't exist at all, same non-leaking shape as
// NovelCommentRepository.SoftDelete. Evidence image files are left on
// disk (a known, accepted minor cleanup gap — see the migration's
// ON DELETE CASCADE for the DB rows, which are removed).
func (service *NovelReportService) Delete(ctx context.Context, reportID, userID string) error {
	if err := service.reports.Delete(ctx, reportID, userID); err != nil {
		return err
	}
	// Reuses "report.updated" (no dedicated "report.deleted" topic) —
	// both sides just refetch their report list/pending-count on this
	// topic regardless of payload, same as an admin's status change.
	service.events.Publish(realtime.Event{Topic: "report.updated", ID: reportID})
	return nil
}

var allowedReportStatuses = map[string]bool{"pending": true, "reviewed": true, "dismissed": true}

// UpdateStatus actions a report — the dashboard's moderation button.
// Moving a report off "pending" notifies the reporting reader (inbox
// row + best-effort push, deep-linking to the novel) with the admin's
// note if one was given, or a generic acknowledgement otherwise. There
// is no author-account system yet, so this is deliberately just "we
// reviewed it" — never implies an author was contacted.
func (service *NovelReportService) UpdateStatus(ctx context.Context, reportID, status, note, reviewerID string) (*repository.NovelReport, error) {
	if !allowedReportStatuses[status] {
		return nil, &ValidationError{Message: "invalid report status"}
	}
	note = strings.TrimSpace(note)
	report, err := service.reports.UpdateStatus(ctx, reportID, status, note, reviewerID)
	if err != nil {
		return nil, err
	}
	service.events.Publish(realtime.Event{Topic: "report.updated", ID: report.ID})

	if status != "pending" {
		service.notifyReporter(ctx, report, note)
	}
	return report, nil
}

func (service *NovelReportService) notifyReporter(ctx context.Context, report *repository.NovelReport, note string) {
	const title = "Your report was reviewed"
	body := note
	if body == "" {
		body = fmt.Sprintf("Thanks for flagging \"%s\" — we've reviewed it.", report.NovelTitle)
	}

	if err := service.notifications.CreateForUser(ctx, report.UserID, report.NovelID, title, body); err != nil {
		return
	}
	service.events.Publish(realtime.Event{Topic: "notification.new"})

	tokens, err := service.deviceTokens.ListTokensForUser(ctx, report.UserID)
	if err != nil || len(tokens) == 0 {
		return
	}
	service.pushNotifier.NotifyNovelHighlight(ctx, tokens, report.NovelID, report.NovelTitle, body)
}
