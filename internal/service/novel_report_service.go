package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/ImtiazChowdhuryWork/novelora_backend/internal/push"
	"github.com/ImtiazChowdhuryWork/novelora_backend/internal/realtime"
	"github.com/ImtiazChowdhuryWork/novelora_backend/internal/repository"
)

const maxReportDetailsLength = 2000

// maxReportImages mirrors handler.maxReportImages — kept as a
// separate constant since the service package doesn't import handler
// (wrong dependency direction); enforced again here so a caller that
// isn't the HTTP handler (impossible today, but this rule lives with
// the data, not the transport) can't slip past it either. Reused as
// the same cap for release-request proof images.
const maxReportImages = 3

// The moderation state machine's full status vocabulary. Every
// transition is its own method below (MarkUnderReview is private,
// called automatically from GetDetail) rather than one generic setter
// — see the package doc on NovelReportService.
const (
	statusSubmitted            = "submitted"
	statusUnderReview          = "under_review"
	statusRejected             = "rejected"
	statusChapterOnHold        = "chapter_on_hold"
	statusNovelOnHold          = "novel_on_hold"
	statusPendingReleaseReview = "pending_release_review"
	statusResolved             = "resolved"
)

// moderation_actions.action_type values — the admin-side audit trail.
// The author's own step (SubmitReleaseRequest) isn't logged here; the
// release_requests table is its own audit trail for that half.
const (
	actionMarkUnderReview = "mark_under_review"
	actionRejectReport    = "reject_report"
	actionHoldChapter     = "hold_chapter"
	actionHoldNovel       = "hold_novel"
	actionResolveDirect   = "resolve_direct"
	actionApproveRelease  = "approve_release"
	actionRejectRelease   = "reject_release"
)

// NovelReportService is Book Detail's flag-icon "Report this novel"
// feature, and — as of the moderation workflow v2 redesign — the full
// Reporter/Admin/Author state machine described in the moderation
// spec: every report moves through well-defined statuses, and every
// transition is its own method that performs the underlying content
// action (if any), writes a moderation_actions row, and fires the
// right notifications, all in one place rather than three loosely
// coordinated call sites.
type NovelReportService struct {
	reports           *repository.NovelReportRepository
	reasons           *repository.ReportReasonRepository
	novels            *repository.NovelRepository
	chapters          *repository.ChapterRepository
	moderationActions *repository.ModerationActionRepository
	releaseRequests   *repository.ReleaseRequestRepository
	novelService      *NovelService
	chapterService    *ChapterService
	notifications     *repository.NotificationRepository
	deviceTokens      *repository.DeviceTokenRepository
	pushNotifier      push.Notifier
	events            realtime.Publisher
}

func NewNovelReportService(
	reports *repository.NovelReportRepository,
	reasons *repository.ReportReasonRepository,
	novels *repository.NovelRepository,
	chapters *repository.ChapterRepository,
	moderationActions *repository.ModerationActionRepository,
	releaseRequests *repository.ReleaseRequestRepository,
	novelService *NovelService,
	chapterService *ChapterService,
	notifications *repository.NotificationRepository,
	deviceTokens *repository.DeviceTokenRepository,
	pushNotifier push.Notifier,
	events realtime.Publisher,
) *NovelReportService {
	return &NovelReportService{
		reports:           reports,
		reasons:           reasons,
		novels:            novels,
		chapters:          chapters,
		moderationActions: moderationActions,
		releaseRequests:   releaseRequests,
		novelService:      novelService,
		chapterService:    chapterService,
		notifications:     notifications,
		deviceTokens:      deviceTokens,
		pushNotifier:      pushNotifier,
		events:            events,
	}
}

// Create validates and stores a reader's report, then attaches any
// evidence screenshots (already saved to disk by the handler — this
// just records their URLs). reason must match a currently-configured
// ReportReason's label exactly (admin-managed, see
// ReportReasonRepository); that row's RequiresDetails flag decides
// whether details must be non-empty — generalizes what used to be a
// hardcoded reason == "other" check. chapterID is optional — nil
// means the report is about the whole novel; when given, it must
// actually belong to novelID. Confirms receipt to the reporter
// immediately — "Report submitted" — the first of the spec's
// reporter notifications.
func (service *NovelReportService) Create(
	ctx context.Context, novelID, userID, reason, details string, chapterID *string, imageURLs []string,
) (*repository.NovelReport, error) {
	reason = strings.TrimSpace(reason)
	details = strings.TrimSpace(details)
	reasonRow, err := service.reasons.GetByLabel(ctx, reason)
	if err != nil {
		return nil, &ValidationError{Message: "invalid report reason"}
	}
	if reasonRow.RequiresDetails && details == "" {
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

	report, err := service.reports.Create(
		ctx, novelID, userID, reason, reasonRow.TypeLabel, reasonRow.TypeDescription, details, chapterID)
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
	service.notifyReporterSubmitted(ctx, report)
	return report, nil
}

// GetDetail is the dashboard's single-report view — the list row's
// data plus its evidence images. The first time an admin opens a
// still-"submitted" report, this is also the "admin received the
// report" moment the spec calls for: it auto-transitions to
// under_review, notifies the reporter it's under review, and gives the
// author a neutral heads-up that their content was reported (not a
// punishment — no hold has happened yet).
func (service *NovelReportService) GetDetail(ctx context.Context, reportID, adminID string) (*repository.NovelReport, error) {
	report, err := service.reports.GetByID(ctx, reportID)
	if err != nil {
		return nil, err
	}
	report.Images, err = service.reports.ListImages(ctx, reportID)
	if err != nil {
		return nil, err
	}
	report.AdminEvidenceImages, err = service.moderationActions.LatestHoldImages(ctx, reportID)
	if err != nil {
		return nil, err
	}

	if report.Status == statusSubmitted {
		report, err = service.markUnderReview(ctx, report, adminID)
		if err != nil {
			return nil, err
		}
		report.Images, err = service.reports.ListImages(ctx, reportID)
		if err != nil {
			return nil, err
		}
	}
	return report, nil
}

func (service *NovelReportService) markUnderReview(ctx context.Context, report *repository.NovelReport, adminID string) (*repository.NovelReport, error) {
	updated, err := service.reports.SetStatus(ctx, report.ID, statusUnderReview)
	if err != nil {
		return nil, err
	}
	if _, err := service.moderationActions.Create(ctx, report.ID, actionMarkUnderReview, adminID, ""); err != nil {
		return nil, err
	}
	service.events.Publish(realtime.Event{Topic: "report.updated", ID: report.ID})
	service.notifyReporterUnderReview(ctx, updated)
	service.notifyAuthorInformed(ctx, updated)
	return updated, nil
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

// ListForOwner is the author dashboard's Reports page — every report
// against a novel the caller owns, same pagination/clamp rules as List.
func (service *NovelReportService) ListForOwner(ctx context.Context, ownerUserID, status string, page, pageSize int) ([]*repository.NovelReport, int, error) {
	if page < 1 {
		page = 1
	}
	switch {
	case pageSize < 1:
		pageSize = 20
	case pageSize > 100:
		pageSize = 100
	}
	reports, total, err := service.reports.ListForOwner(ctx, ownerUserID, status, page, pageSize)
	if err != nil {
		return nil, 0, err
	}
	for _, report := range reports {
		// Reporter evidence only reaches the author when an admin
		// explicitly chose to share it at hold time — never automatic
		// (see HoldChapter/HoldNovel). The admin's own attached evidence
		// has no such gate: attaching it to a hold *is* the admin
		// choosing to show the author, so it's always included.
		if report.ShareReporterEvidence {
			report.Images, err = service.reports.ListImages(ctx, report.ID)
			if err != nil {
				return nil, 0, err
			}
		}
		report.AdminEvidenceImages, err = service.moderationActions.LatestHoldImages(ctx, report.ID)
		if err != nil {
			return nil, 0, err
		}
	}
	return reports, total, nil
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
	service.events.Publish(realtime.Event{Topic: "report.updated", ID: reportID})
	return nil
}

// ModerationActions is the drawer's History timeline — each action's
// own attached evidence (if any) comes along too, so an approve/reject
// decision's proof shows inline at the step it was attached, not just
// a hold's. A hold_chapter/hold_novel entry also carries the
// reporter's own evidence (ReporterImages) when the report's
// ShareReporterEvidence is set, so that one entry is a complete record
// of the decision — both what the reporter submitted and what the
// admin attached — instead of the reporter's half only ever showing
// in the report's separate evidence section.
func (service *NovelReportService) ModerationActions(ctx context.Context, reportID string) ([]*repository.ModerationAction, error) {
	actions, err := service.moderationActions.ListForReport(ctx, reportID)
	if err != nil {
		return nil, err
	}
	var reporterImages []string
	var reporterImagesLoaded bool
	for _, action := range actions {
		action.Images, err = service.moderationActions.ListImages(ctx, action.ID)
		if err != nil {
			return nil, err
		}
		if action.ActionType != actionHoldChapter && action.ActionType != actionHoldNovel {
			continue
		}
		if !reporterImagesLoaded {
			report, err := service.reports.GetByID(ctx, reportID)
			if err != nil {
				return nil, err
			}
			if report.ShareReporterEvidence {
				reporterEvidence, err := service.reports.ListImages(ctx, reportID)
				if err != nil {
					return nil, err
				}
				reporterImages = make([]string, 0, len(reporterEvidence))
				for _, image := range reporterEvidence {
					reporterImages = append(reporterImages, image.ImageURL)
				}
			}
			reporterImagesLoaded = true
		}
		action.ReporterImages = reporterImages
	}
	return actions, nil
}

// ModerationActionsForOwner is ModerationActions scoped to a report the
// caller's novel actually owns — the author dashboard's own history
// timeline, same ownership check as ReleaseRequestsForOwner.
func (service *NovelReportService) ModerationActionsForOwner(ctx context.Context, reportID, callerUserID string) ([]*repository.ModerationAction, error) {
	report, err := service.reports.GetByID(ctx, reportID)
	if err != nil {
		return nil, err
	}
	if report.OwnerUserID == nil || *report.OwnerUserID != callerUserID {
		return nil, repository.ErrReportNotFound
	}
	return service.ModerationActions(ctx, reportID)
}

// AdminDelete soft-deletes a report from the admin Reports list.
// Refused while a report is actively holding content down (or awaiting
// the admin's own decision on a release request) — deleting one of
// those would strand the hold: the chapter/novel would stay hidden
// with nothing left explaining why, and no report for the author to
// request a release against. Deleting a submitted/under_review/
// rejected/resolved report is always safe — none of those states have
// anything currently depending on the row's continued existence.
func (service *NovelReportService) AdminDelete(ctx context.Context, reportID string) error {
	report, err := service.reports.GetByID(ctx, reportID)
	if err != nil {
		return err
	}
	switch report.Status {
	case statusChapterOnHold, statusNovelOnHold, statusPendingReleaseReview:
		return &ValidationError{Message: "can't delete a report that's currently holding content — resolve or approve/reject the release first"}
	}
	return service.reports.SoftDelete(ctx, reportID)
}

// AdminBulkDelete clears out an entire terminal status at once — the
// admin's "declutter old resolved/rejected reports" action, rather
// than deleting a backlog one row at a time. Restricted to resolved/
// rejected: anything else either still needs attention (submitted/
// under_review) or is actively holding content (blocked the same way
// AdminDelete blocks a single one).
func (service *NovelReportService) AdminBulkDelete(ctx context.Context, status string) (int, error) {
	if status != statusResolved && status != statusRejected {
		return 0, &ValidationError{Message: "bulk delete only supports resolved or rejected reports"}
	}
	return service.reports.BulkSoftDeleteByStatus(ctx, status)
}

// PurgeOldDeleted hard-deletes reports soft-deleted more than 30 days
// ago — see runReportPurgeTicker in main.go.
func (service *NovelReportService) PurgeOldDeleted(ctx context.Context) (int, error) {
	return service.reports.PurgeDeletedBefore(ctx, time.Now().AddDate(0, 0, -30))
}

// ReleaseRequests is a report's full release-request history — the
// admin drawer's "pending review" card and the author dashboard's
// Reports page (to show a previous rejection's admin_comment) both
// read this.
func (service *NovelReportService) ReleaseRequests(ctx context.Context, reportID string) ([]*repository.ReleaseRequest, error) {
	requests, err := service.releaseRequests.ListForReport(ctx, reportID)
	if err != nil {
		return nil, err
	}
	for _, request := range requests {
		request.Images, err = service.releaseRequests.ListImages(ctx, request.ID)
		if err != nil {
			return nil, err
		}
	}
	return requests, nil
}

// ReleaseRequestsForOwner is the author's own report's release-request
// history — same non-leaking ownership shape as SubmitReleaseRequest,
// so the author's Reports page can show a previous rejection's
// admin_comment before they try again.
func (service *NovelReportService) ReleaseRequestsForOwner(ctx context.Context, reportID, callerUserID string) ([]*repository.ReleaseRequest, error) {
	report, err := service.reports.GetByID(ctx, reportID)
	if err != nil {
		return nil, err
	}
	if report.OwnerUserID == nil || *report.OwnerUserID != callerUserID {
		return nil, repository.ErrReportNotFound
	}
	return service.ReleaseRequests(ctx, reportID)
}

// Reject is Action A — the report is invalid, nothing to do.
func (service *NovelReportService) Reject(ctx context.Context, reportID, adminID, notes string) (*repository.NovelReport, error) {
	report, err := service.reports.GetByID(ctx, reportID)
	if err != nil {
		return nil, err
	}
	if report.Status != statusUnderReview {
		return nil, &ValidationError{Message: "this report isn't awaiting a decision"}
	}
	updated, err := service.reports.UpdateStatus(ctx, reportID, statusRejected, strings.TrimSpace(notes), adminID)
	if err != nil {
		return nil, err
	}
	if _, err := service.moderationActions.Create(ctx, reportID, actionRejectReport, adminID, notes); err != nil {
		return nil, err
	}
	service.events.Publish(realtime.Event{Topic: "report.updated", ID: reportID})
	service.notifyReporterRejected(ctx, updated, notes)
	return updated, nil
}

// ResolveDirect closes a report as handled without holding anything —
// e.g. the admin resolved it out-of-band. Not in the literal spec, but
// a real everyday need alongside the three named actions.
func (service *NovelReportService) ResolveDirect(ctx context.Context, reportID, adminID, notes string) (*repository.NovelReport, error) {
	report, err := service.reports.GetByID(ctx, reportID)
	if err != nil {
		return nil, err
	}
	if report.Status != statusUnderReview {
		return nil, &ValidationError{Message: "this report isn't awaiting a decision"}
	}
	updated, err := service.reports.UpdateStatus(ctx, reportID, statusResolved, strings.TrimSpace(notes), adminID)
	if err != nil {
		return nil, err
	}
	if _, err := service.moderationActions.Create(ctx, reportID, actionResolveDirect, adminID, notes); err != nil {
		return nil, err
	}
	service.events.Publish(realtime.Event{Topic: "report.updated", ID: reportID})
	service.notifyReporterResolved(ctx, updated)
	service.notifyAuthorResolvedNoAction(ctx, updated)
	return updated, nil
}

// HoldChapter is Action B — takes the reported chapter down (via the
// same ChapterService.UpdateStatus every other unpublish already uses,
// so realtime/outbox behave identically) and puts the report on hold,
// atomically, as one admin decision rather than two independent clicks.
// shareReporterEvidence is the admin's explicit per-hold choice to show
// the reporter's own evidence images to the author — never automatic,
// since those images may contain the reporter's own identifying
// content; see NovelReport.ShareReporterEvidence. adminImageURLs are
// the admin's own proof, attached to this specific hold decision and
// always shown to the author once attached (attaching them *is* the
// admin choosing to share).
func (service *NovelReportService) HoldChapter(
	ctx context.Context, reportID, adminID, notes string, shareReporterEvidence bool, adminImageURLs []string,
) (*repository.NovelReport, error) {
	notes = strings.TrimSpace(notes)
	if notes == "" {
		return nil, &ValidationError{Message: "a note explaining the hold is required"}
	}
	if len(adminImageURLs) > maxReportImages {
		return nil, &ValidationError{Message: fmt.Sprintf("at most %d images allowed", maxReportImages)}
	}
	report, err := service.reports.GetByID(ctx, reportID)
	if err != nil {
		return nil, err
	}
	if report.Status != statusUnderReview {
		return nil, &ValidationError{Message: "this report isn't awaiting a decision"}
	}
	if report.ChapterID == nil {
		return nil, &ValidationError{Message: "this report has no chapter to hold"}
	}
	if _, err := service.chapterService.UpdateStatus(ctx, *report.ChapterID, "draft"); err != nil {
		return nil, err
	}
	// A pending schedule set before the hold survives a plain draft
	// transition (SetScheduledAt only clears on publish) — left alone,
	// the background auto-publish ticker would republish held content
	// on its own, with no action from the author at all.
	if _, err := service.chapterService.Unschedule(ctx, *report.ChapterID); err != nil {
		return nil, err
	}
	updated, err := service.reports.UpdateStatus(ctx, reportID, statusChapterOnHold, notes, adminID)
	if err != nil {
		return nil, err
	}
	if shareReporterEvidence {
		if err := service.reports.SetShareReporterEvidence(ctx, reportID, true); err != nil {
			return nil, err
		}
		updated.ShareReporterEvidence = true
	}
	action, err := service.moderationActions.Create(ctx, reportID, actionHoldChapter, adminID, notes)
	if err != nil {
		return nil, err
	}
	if len(adminImageURLs) > 0 {
		if err := service.moderationActions.AddImages(ctx, action.ID, adminImageURLs); err != nil {
			return nil, err
		}
		updated.AdminEvidenceImages = adminImageURLs
	}
	service.events.Publish(realtime.Event{Topic: "report.updated", ID: reportID})
	service.notifyAuthorHold(ctx, updated, "A chapter", notes)
	return updated, nil
}

// HoldNovel is Action C — takes the entire novel down (via
// NovelService.Delete, same soft-delete every other hide already uses).
// See HoldChapter's doc comment for shareReporterEvidence/adminImageURLs.
func (service *NovelReportService) HoldNovel(
	ctx context.Context, reportID, adminID, notes string, shareReporterEvidence bool, adminImageURLs []string,
) (*repository.NovelReport, error) {
	notes = strings.TrimSpace(notes)
	if notes == "" {
		return nil, &ValidationError{Message: "a note explaining the hold is required"}
	}
	if len(adminImageURLs) > maxReportImages {
		return nil, &ValidationError{Message: fmt.Sprintf("at most %d images allowed", maxReportImages)}
	}
	report, err := service.reports.GetByID(ctx, reportID)
	if err != nil {
		return nil, err
	}
	if report.Status != statusUnderReview {
		return nil, &ValidationError{Message: "this report isn't awaiting a decision"}
	}
	if err := service.novelService.Delete(ctx, report.NovelID); err != nil {
		return nil, err
	}
	updated, err := service.reports.UpdateStatus(ctx, reportID, statusNovelOnHold, notes, adminID)
	if err != nil {
		return nil, err
	}
	if shareReporterEvidence {
		if err := service.reports.SetShareReporterEvidence(ctx, reportID, true); err != nil {
			return nil, err
		}
		updated.ShareReporterEvidence = true
	}
	action, err := service.moderationActions.Create(ctx, reportID, actionHoldNovel, adminID, notes)
	if err != nil {
		return nil, err
	}
	if len(adminImageURLs) > 0 {
		if err := service.moderationActions.AddImages(ctx, action.ID, adminImageURLs); err != nil {
			return nil, err
		}
		updated.AdminEvidenceImages = adminImageURLs
	}
	service.events.Publish(realtime.Event{Topic: "report.updated", ID: reportID})
	service.notifyAuthorHold(ctx, updated, "Your novel", notes)
	return updated, nil
}

// SubmitReleaseRequest is the author's side of a hold — they've fixed
// what the hold's note asked for and want it looked at again.
// Ownership is enforced here (not the repository), same non-leaking
// 404 shape as loadOwnedNovel: a report that exists but belongs to a
// novel the caller doesn't own reads identically to one that doesn't
// exist. Replaces the old bare-message Resubmit — explanation is
// required and up to maxReportImages proof screenshots are supported,
// same cap as the original report's own evidence.
func (service *NovelReportService) SubmitReleaseRequest(
	ctx context.Context, reportID, callerUserID, explanation string, imageURLs []string,
) (*repository.NovelReport, error) {
	report, err := service.reports.GetByID(ctx, reportID)
	if err != nil {
		return nil, err
	}
	if report.OwnerUserID == nil || *report.OwnerUserID != callerUserID {
		return nil, repository.ErrReportNotFound
	}
	if report.Status != statusChapterOnHold && report.Status != statusNovelOnHold {
		return nil, &ValidationError{Message: "this report isn't on hold"}
	}
	explanation = strings.TrimSpace(explanation)
	if explanation == "" {
		return nil, &ValidationError{Message: "please explain what you fixed"}
	}
	if len(imageURLs) > maxReportImages {
		return nil, &ValidationError{Message: fmt.Sprintf("at most %d images allowed", maxReportImages)}
	}

	request, err := service.releaseRequests.Create(ctx, reportID, callerUserID, explanation)
	if err != nil {
		return nil, err
	}
	if len(imageURLs) > 0 {
		if err := service.releaseRequests.AddImages(ctx, request.ID, imageURLs); err != nil {
			return nil, err
		}
	}
	updated, err := service.reports.SetStatus(ctx, reportID, statusPendingReleaseReview)
	if err != nil {
		return nil, err
	}
	service.events.Publish(realtime.Event{Topic: "report.updated", ID: reportID})
	return updated, nil
}

// ApproveRelease approves the given release request: restores whatever
// is currently on hold (the novel, if NovelHidden; otherwise the
// chapter, if its live status is still draft — the report's own
// current live-state fields already tell us which, no need to guess
// from history) and resolves the report. Notifies both the author and
// the original reporter, per the spec.
func (service *NovelReportService) ApproveRelease(
	ctx context.Context, reportID, releaseRequestID, adminID, notes string, adminImageURLs []string,
) (*repository.NovelReport, error) {
	if len(adminImageURLs) > maxReportImages {
		return nil, &ValidationError{Message: fmt.Sprintf("at most %d images allowed", maxReportImages)}
	}
	report, err := service.reports.GetByID(ctx, reportID)
	if err != nil {
		return nil, err
	}
	if report.Status != statusPendingReleaseReview {
		return nil, &ValidationError{Message: "this report has no pending release request"}
	}
	if err := service.restoreHeldContent(ctx, report); err != nil {
		return nil, err
	}
	if _, err := service.releaseRequests.UpdateStatus(ctx, releaseRequestID, "approved", ""); err != nil {
		return nil, err
	}
	updated, err := service.reports.UpdateStatus(ctx, reportID, statusResolved, strings.TrimSpace(notes), adminID)
	if err != nil {
		return nil, err
	}
	action, err := service.moderationActions.Create(ctx, reportID, actionApproveRelease, adminID, notes)
	if err != nil {
		return nil, err
	}
	if len(adminImageURLs) > 0 {
		if err := service.moderationActions.AddImages(ctx, action.ID, adminImageURLs); err != nil {
			return nil, err
		}
	}
	service.events.Publish(realtime.Event{Topic: "report.updated", ID: reportID})
	service.notifyAuthorResolved(ctx, updated)
	service.notifyReporterResolved(ctx, updated)
	return updated, nil
}

// RejectRelease sends a release request back — the hold stays in
// effect (content is not restored) and the report reverts to whichever
// hold status it came from, preserving the original hold reason so the
// author still sees why, alongside the new rejection comment on the
// release request itself.
func (service *NovelReportService) RejectRelease(
	ctx context.Context, reportID, releaseRequestID, adminID, comment string, adminImageURLs []string,
) (*repository.NovelReport, error) {
	if len(adminImageURLs) > maxReportImages {
		return nil, &ValidationError{Message: fmt.Sprintf("at most %d images allowed", maxReportImages)}
	}
	report, err := service.reports.GetByID(ctx, reportID)
	if err != nil {
		return nil, err
	}
	if report.Status != statusPendingReleaseReview {
		return nil, &ValidationError{Message: "this report has no pending release request"}
	}
	comment = strings.TrimSpace(comment)
	if comment == "" {
		return nil, &ValidationError{Message: "a comment explaining what's still wrong is required"}
	}
	holdStatus := statusChapterOnHold
	if report.NovelHidden {
		holdStatus = statusNovelOnHold
	}
	if _, err := service.releaseRequests.UpdateStatus(ctx, releaseRequestID, "rejected", comment); err != nil {
		return nil, err
	}
	updated, err := service.reports.UpdateStatus(ctx, reportID, holdStatus, report.ResolutionNote, adminID)
	if err != nil {
		return nil, err
	}
	action, err := service.moderationActions.Create(ctx, reportID, actionRejectRelease, adminID, comment)
	if err != nil {
		return nil, err
	}
	if len(adminImageURLs) > 0 {
		if err := service.moderationActions.AddImages(ctx, action.ID, adminImageURLs); err != nil {
			return nil, err
		}
	}
	service.events.Publish(realtime.Event{Topic: "report.updated", ID: reportID})
	service.notifyAuthorReleaseRejected(ctx, updated, comment)
	return updated, nil
}

// restoreHeldContent brings back whatever ApproveRelease's report is
// currently holding down — the novel takes priority if somehow both
// look hidden (shouldn't happen: a report only ever holds one thing),
// otherwise the chapter.
func (service *NovelReportService) restoreHeldContent(ctx context.Context, report *repository.NovelReport) error {
	if report.NovelHidden {
		return service.novelService.Restore(ctx, report.NovelID)
	}
	if report.ChapterID != nil && report.ChapterStatus != nil && *report.ChapterStatus == "draft" {
		_, err := service.chapterService.UpdateStatus(ctx, *report.ChapterID, "published")
		return err
	}
	return nil
}

func (service *NovelReportService) notify(ctx context.Context, userID, novelID, novelTitle, title, body string) {
	if err := service.notifications.CreateForUser(ctx, userID, novelID, title, body); err != nil {
		return
	}
	service.events.Publish(realtime.Event{Topic: "notification.new"})

	tokens, err := service.deviceTokens.ListTokensForUser(ctx, userID)
	if err != nil || len(tokens) == 0 {
		return
	}
	service.pushNotifier.NotifyNovelHighlight(ctx, tokens, novelID, novelTitle, body)
}

func (service *NovelReportService) notifyReporterSubmitted(ctx context.Context, report *repository.NovelReport) {
	service.notify(ctx, report.UserID, report.NovelID, report.NovelTitle, "Report submitted",
		fmt.Sprintf("Thank you for your report on %q. Novelora's moderation team has received it and will review it soon.", report.NovelTitle))
}

func (service *NovelReportService) notifyReporterUnderReview(ctx context.Context, report *repository.NovelReport) {
	service.notify(ctx, report.UserID, report.NovelID, report.NovelTitle, "Report under review",
		fmt.Sprintf("Your report on %q is currently under review by the Novelora moderation team.", report.NovelTitle))
}

func (service *NovelReportService) notifyReporterRejected(ctx context.Context, report *repository.NovelReport, notes string) {
	body := strings.TrimSpace(notes)
	if body == "" {
		body = fmt.Sprintf("Thanks for flagging %q — after review, no action was needed.", report.NovelTitle)
	}
	service.notify(ctx, report.UserID, report.NovelID, report.NovelTitle, "Report reviewed", body)
}

func (service *NovelReportService) notifyReporterResolved(ctx context.Context, report *repository.NovelReport) {
	service.notify(ctx, report.UserID, report.NovelID, report.NovelTitle, "Report resolved",
		fmt.Sprintf("Your report on %q has been resolved — appropriate action has been taken.", report.NovelTitle))
}

// notifyAuthorInformed is the spec's "this is NOT a punishment" step —
// fires once, the first time an admin opens the report, independent of
// whatever decision follows.
func (service *NovelReportService) notifyAuthorInformed(ctx context.Context, report *repository.NovelReport) {
	if report.OwnerUserID == nil {
		return
	}
	detail := report.Details
	if detail == "" {
		detail = "No additional details were provided."
	}
	body := fmt.Sprintf("A reader reported %q (reason: %s). %s No action has been taken yet — this is just to keep you informed.",
		report.NovelTitle, report.Reason, detail)
	service.notify(ctx, *report.OwnerUserID, report.NovelID, report.NovelTitle,
		fmt.Sprintf("Your novel %q was reported", report.NovelTitle), body)
}

func (service *NovelReportService) notifyAuthorHold(ctx context.Context, report *repository.NovelReport, what, notes string) {
	if report.OwnerUserID == nil {
		return
	}
	service.notify(ctx, *report.OwnerUserID, report.NovelID, report.NovelTitle,
		fmt.Sprintf("%s of %q was placed on hold", what, report.NovelTitle), notes)
}

func (service *NovelReportService) notifyAuthorReleaseRejected(ctx context.Context, report *repository.NovelReport, comment string) {
	if report.OwnerUserID == nil {
		return
	}
	service.notify(ctx, *report.OwnerUserID, report.NovelID, report.NovelTitle,
		fmt.Sprintf("Release request for %q needs more work", report.NovelTitle), comment)
}

func (service *NovelReportService) notifyAuthorResolved(ctx context.Context, report *repository.NovelReport) {
	if report.OwnerUserID == nil {
		return
	}
	service.notify(ctx, *report.OwnerUserID, report.NovelID, report.NovelTitle,
		fmt.Sprintf("%q is live again", report.NovelTitle),
		"Your release request was approved — the content is visible to readers again.")
}

func (service *NovelReportService) notifyAuthorResolvedNoAction(ctx context.Context, report *repository.NovelReport) {
	if report.OwnerUserID == nil {
		return
	}
	service.notify(ctx, *report.OwnerUserID, report.NovelID, report.NovelTitle,
		fmt.Sprintf("Report about %q resolved", report.NovelTitle),
		"The report was reviewed and closed — no action was needed.")
}
