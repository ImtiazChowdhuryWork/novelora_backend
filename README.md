# Novelora Backend

Go REST API for the Novelora novel-reading platform. Serves the Flutter app (`novelora_app`) and the admin dashboard (`novelora_dashboard`).

## Stack

- Go (standard library `net/http`, Go 1.22+ method-pattern routing)
- PostgreSQL via `jackc/pgx/v5` (migrations embedded in the binary)
- `golang-jwt/jwt/v5` for access tokens, `x/crypto/bcrypt` for password hashing

## Project Structure

```
novelora_backend/
├── cmd/
│   └── server/
│       └── main.go              # Entry point: config → db → wiring → routes
├── internal/
│   ├── config/                  # Env / .env configuration loading
│   ├── database/                # Connection pool + embedded SQL migrations
│   │   └── migrations/          # 0001_create_users.sql, 0002_create_refresh_tokens.sql
│   ├── repository/              # SQL access (users, refresh tokens)
│   ├── service/                 # Business logic (auth: bcrypt, JWT, token rotation)
│   ├── handler/                 # Thin HTTP handlers, JSON in/out
│   └── middleware/              # Request logging
├── .env.example                 # Template — copy to .env
├── go.mod
└── README.md
```

## Setup & Run

1. PostgreSQL must be running with a `novelora` database/role (local dev uses PostgreSQL 17).
2. Copy `.env.example` to `.env` and set `DATABASE_URL` + `JWT_SECRET`.
3. Migrations run automatically at startup.

```bash
go run ./cmd/server
# listens on :8080 (override with PORT env var)
```

If the local Postgres Windows service can't start without admin rights, run it directly:

```powershell
& "C:\Program Files\PostgreSQL\17\bin\pg_ctl.exe" start -D "C:\Program Files\PostgreSQL\17\data"
```

## Endpoints

| Method | Path                    | Status | Notes                                        |
|--------|-------------------------|--------|----------------------------------------------|
| GET    | `/health`               | ✅     | Liveness check                               |
| POST   | `/api/v1/auth/register` | ✅     | `{username, email, password}` → 201 + tokens |
| POST   | `/api/v1/auth/login`    | ✅     | `{email, password}` → 200 + tokens           |
| POST   | `/api/v1/auth/google`   | ✅     | `{id_token}` → 200 + tokens (503 if GOOGLE_CLIENT_ID unset) |
| POST   | `/api/v1/auth/refresh`  | ✅     | `{refresh_token}` → 200, rotates the token   |
| POST   | `/api/v1/auth/logout`   | ✅     | `{refresh_token}` → 204, revokes the token   |
| GET    | `/api/v1/users/me`      | ✅     | Bearer JWT → 200 current user, now includes `is_author`/`pen_name` (401 otherwise)|
| POST   | `/api/v1/users/me/author-profile` | ✅ | Bearer JWT; `{pen_name}` → 201 + rotated tokens (the new access token's `is_author` claim flips immediately). 409 if the caller already has an author profile |
| PUT    | `/api/v1/users/me/device-token` | ✅ | `{token, platform}` → 204; registers an FCM token for push |
| GET    | `/ws`                   | ✅     | Realtime events; JWT via `?token=` (WS upgrade)|
| *      | `/api/v1/admin/novels…` | ✅     | Admin-only novels CRUD + cover upload (list/create/get/update/soft-delete) |
| *      | `/api/v1/admin/novels/{id}/chapters…` | ✅ | Admin-only chapter create/import/list; `/admin/chapters/{id}` get/update/status/delete |
| *      | `/api/v1/author/novels…` | ✅ | Requires an author profile (`is_author` JWT claim); own-novels-only CRUD + cover upload — no delete, no reorder, editorial flags (`is_recommended`/`is_exclusive`/`rating`/`view_count`) always zeroed or preserved, never author-writable |
| *      | `/api/v1/author/novels/{id}/chapters…` | ✅ | Requires an author profile and ownership of the novel; `/author/chapters/{id}` get/update/status/schedule/delete, all ownership-checked via the chapter's novel |
| GET    | `/api/v1/novels…`, `/api/v1/chapters/{id}` | ✅ | Public catalog — published content only |
| POST   | `/api/v1/novels/{id}/report` | ✅ | Multipart: `reason`, `details`, optional `chapter_id`, up to 3 `images` files → 201; requires login. `reason` ∈ spam/plagiarism/inappropriate/harassment/broken/other; `details` required only for `other`; `chapter_id` must belong to the novel |
| DELETE | `/api/v1/reports/{id}` | ✅ | Requires login; own report only (ownership enforced by the query, not a 403 — a report id that isn't yours reads as 404). Withdraws the caller's own report; publishes `report.updated` |
| GET    | `/api/v1/users/me/reports` | ✅ | Bearer JWT → the caller's own report history, paginated; backs the app's "My Reports" Profile page |
| GET    | `/api/v1/admin/reports` | ✅ | Admin-only; `?status=&page=&page_size=`, paginated. Rows carry `image_count` (cheap correlated count) but leave `images` empty — only the single-report `GET` below fetches evidence URLs, avoiding an N+1 on the list |
| GET    | `/api/v1/admin/reports/{id}` | ✅ | Admin-only; full report record + evidence image URLs — backs the dashboard's report detail drawer |
| PUT    | `/api/v1/admin/reports/{id}/status` | ✅ | Admin-only; `{status, note}` ∈ pending/reviewed/dismissed → 200, audit-logged. Moving off `pending` writes a single-recipient inbox notification (+ best-effort push) to the reporting reader — the note verbatim if given, else a generic acknowledgement (see `NovelReportService.notifyReporter`) |
| GET    | `/api/v1/admin/authors/{authorName}/novels` | ✅ | Admin-only; `?exclude=<novelId>` — an author's other published novels, keyed by the free-text `author_name` string (no author-account system yet) |
| GET    | `/api/v1/admin/authors/{authorName}/strikes` | ✅ | Admin-only; strike history recorded against an author name |
| POST   | `/api/v1/admin/authors/{authorName}/strikes` | ✅ | Admin-only; `{note}` → 201, audit-logged |
| POST   | `/api/v1/admin/authors/{authorName}/hide-novels` | ✅ | Admin-only; soft-deletes every novel by that author name in one action → `{hidden_count}`, audit-logged, publishes `novel.deleted` per hidden novel (same event a single-novel delete fires) |

### Novel list filtering (`GET /admin/novels`, `GET /novels`)

Both list endpoints share `NovelRepository.List`/`NovelListFilter` (`internal/repository/novel_repository.go`), but expose different query params:

| Param | Admin (`/admin/novels`) | Public (`/novels`) | Notes |
|---|---|---|---|
| `search` | ✅ | — | Matches `title`/`author_name` **and** any assigned genre/tag name via an `EXISTS` subquery (added 2026-07-23 — searching "Comedy" now surfaces Comedy-tagged novels, not just literal title matches) |
| `sort` | ✅ (`views`/`rating`/`new`) | ✅ | `""` = manual `sort_order` |
| `genre_id` | — | ✅ | Single-value filter, joins `novel_genres` once — this is what the app's `GetNovelsUseCase` uses everywhere (Discover sections, category tabs) |
| `genre_ids` + `genre_match` | ✅ | — | Comma-separated ids, `genre_match=any` (OR, default) or `all` (AND) — dashboard-only multi-select filter, independent of `genre_id` |
| `status` | ✅ | ✅ | `ongoing`/`completed` |
| `recommended` | — | ✅ | Backs Readers' Choice-style filters app-wide |

`genre_id` (singular) and `genre_ids` (plural) are deliberately separate filter paths in `NovelListFilter` — the admin dashboard's multi-select never touches the public API's single-genre join, so extending one can't silently break the other.

Success responses: `{user: {id, username, email, created_at}, access_token, refresh_token, expires_in}`.
Errors: `{"error": "message"}` with 400 (validation/bad JSON), 401 (bad credentials / bad refresh token), 409 (email/username taken).

Paths mirror `ApiEndpoints` in the Flutter app (`lib/core/network/api_endpoints.dart`), which points at `http://<host>:8080`.

## Auth Design

- Access token: HS256 JWT (`sub` = user id), 15-minute TTL.
- Refresh token: opaque 32-byte random hex, 30-day TTL, stored as SHA-256 hash; rotated on every refresh and revoked on logout (reuse of an old token returns 401).
- Validation limits mirror the app's `AppConstants` (username 3–50, password 8–128).

## Push Notifications

Three independent triggers, all broadcasting to every registered device token (no per-novel/per-genre
"following" concept yet — see `internal/push`). Disabled gracefully until configured:

1. Firebase Console → Project Settings → Service Accounts → Generate new private key
2. Save the JSON file somewhere outside the repo and set `FIREBASE_CREDENTIALS_JSON=<path>` in `.env`
3. Restart the server — logs `push: FCM notifications enabled` instead of the no-op message

**What triggers a push** (added to 2026-07-23; see `internal/service/novel_service.go` and `ranking_notification_service.go`):

1. **Chapter publish** — unchanged, see below.
2. **Novel created** (`NovelService.Create` → `notifyNewNovelCreated`) — fires the moment a novel is saved via the dashboard's real API, even before it has any published chapters. Deliberately does *not* fire for novels inserted directly via SQL (bulk seeding) — only the real `POST /admin/novels` path triggers it.
3. **Novel enters a tracked ranked section** (`RankingNotificationService.DetectAndNotify`, run by a 45-minute ticker in `main.go`) — periodically diffs each registered section's current top-N against `novel_section_memberships` (new table, migration `0016`) and notifies only on genuinely new entries, never for a novel still sitting in a section it was already in. The section list (`defaultRankingSections` in `ranking_notification_service.go`) currently tracks Trending (global) and Comedy's Most Read; adding a future category's section is one list entry, not new code. Multiple novels entering the same section in one check are batched into a single push (`NotifyBroadcast`, no specific novel to deep-link to) rather than one push per novel — a single-entry check still deep-links straight to that novel (`NotifyNovelHighlight`). The in-app inbox always gets one row per novel regardless, so full detail survives even when the push itself is a digest.
   - **Cold-start note**: the very first time this ticker runs, every novel currently in a tracked section counts as a "new" entry (nothing was recorded before), so expect a one-time notification burst rather than a trickle. Every run after that only fires for genuine new entries.

## Conventions

- Explicit names, no cryptic abbreviations (`responseWriter`, not `w` — project rule)
- Handlers stay thin; business logic lives in `internal/service/`, storage in `internal/repository/`
- Every endpoint returns JSON

## Next Steps

- [x] Auth middleware (validate Bearer JWT) for protected routes
- [x] Novels/chapters endpoints for the app's Discover, Library, and reader
- [x] Admin endpoints for the dashboard
- [x] Novel search broadened to genre/tag names; admin multi-select genre filter (`genre_ids`/`genre_match`)
- [x] Novel-created and ranked-section-entry push/inbox notifications (2026-07-23)
- [x] Reader reports (`novel_reports`, migration `0027`) — reader-facing create + admin list/moderate, `report.created`/`report.updated` realtime events, `PendingReports` on `/admin/stats` (2026-08-14)
- [x] Reader reports, extended same day: `resolution_note` (migration `0028`), `GET /users/me/reports`, `HasReported` (backs the app's `is_reported_by_me`), and per-reporter inbox notification + push on resolution reusing the existing single-user notification path (`NotificationRepository.CreateForUser`, `DeviceTokenRepository.ListTokensForUser` — both new, first targeted-not-broadcast notification methods in the codebase)
- [x] Reader reports, round 3 (migration `0029`, 2026-08-14): reports can now reference a specific chapter (`chapter_id`) and carry up to 3 evidence screenshots (`novel_report_images`, multipart upload reusing the image-upload package's content-sniffing helper); readers can withdraw their own report (`DELETE /reports/{id}`); the dashboard's report list is a click-through to a full detail view (`GET /admin/reports/{id}`) instead of an inline row-Select. New author-moderation panel (`AuthorModerationService`/`AuthorModerationHandler`), keyed by the free-text `author_name` string (no author-account system yet — see the migration's comment): an author's other published novels, a persistent strike history (`author_strikes`), and a one-click bulk-hide of every novel by that author name (soft-delete, publishes `novel.deleted` per novel so both the app and dashboard refresh immediately, same as a single novel delete)
- [x] **Author accounts, Phase 1 (migration `0030`, 2026-08-14):** "author" is an additive capability, not a role swap — `users.role` stays `reader`/`admin` untouched; a new `author_profiles` table is the source of truth, surfaced as an `is_author` JWT claim (alongside the existing `role` claim) and a new `RequireAuthor` middleware. `POST /users/me/author-profile` is the "become an author" upgrade (reissues tokens immediately via the existing `issueTokens`, so `is_author` flips without a re-login) — both the opt-in-from-an-existing-account and direct-signup (register, then this) paths use it, no separate registration endpoint needed. `novels` gained a nullable `owner_user_id` (every existing admin-uploaded novel stays `NULL`, `author_name` untouched either way); new author-scoped `/api/v1/author/novels...`/`/api/v1/author/chapters...` endpoints reuse the existing `NovelService`/`ChapterService` unchanged, with ownership enforced in the handler (404, not 403, for a novel/chapter that isn't yours — same non-leaking shape as the report feature's own-report-delete). Editorial flags (`is_recommended`, `is_exclusive`, the admin-typed `rating`, the manual `view_count` override) stay admin-only — the author-scoped handler zeroes them on create and preserves the existing value on every update regardless of request body. New sibling repo `novelora_author_dashboard` (React/Vite/TS/AntD, same stack as the admin dashboard) is the author-facing surface — see its own README for the frontend side. Verified end-to-end in a real browser: signed up, created a novel, published a chapter, confirmed it live in the public `GET /novels` API exactly like an admin-uploaded novel; verified a second author can't see or touch the first author's novel.
- [ ] `RankingNotificationService`'s tracked section list only covers Trending + Comedy's Most Read — extend as more categories get their own dedicated sections
- [ ] No admin UI to manage the ranking section list or view push delivery success/failure — both are code-only today
- [ ] Zero automated Go test coverage anywhere in this repo — the app (`novelora_app`) has cubit-level tests; the backend has none
