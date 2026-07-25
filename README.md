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
| GET    | `/api/v1/users/me`      | ✅     | Bearer JWT → 200 current user (401 otherwise)|
| PUT    | `/api/v1/users/me/device-token` | ✅ | `{token, platform}` → 204; registers an FCM token for push |
| GET    | `/ws`                   | ✅     | Realtime events; JWT via `?token=` (WS upgrade)|
| *      | `/api/v1/admin/novels…` | ✅     | Admin-only novels CRUD + cover upload (list/create/get/update/soft-delete) |
| *      | `/api/v1/admin/novels/{id}/chapters…` | ✅ | Admin-only chapter create/import/list; `/admin/chapters/{id}` get/update/status/delete |
| GET    | `/api/v1/novels…`, `/api/v1/chapters/{id}` | ✅ | Public catalog — published content only |

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
- [ ] `RankingNotificationService`'s tracked section list only covers Trending + Comedy's Most Read — extend as more categories get their own dedicated sections
- [ ] No admin UI to manage the ranking section list or view push delivery success/failure — both are code-only today
- [ ] Zero automated Go test coverage anywhere in this repo — the app (`novelora_app`) has cubit-level tests; the backend has none
