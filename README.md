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
| GET    | `/ws`                   | ✅     | Realtime events; JWT via `?token=` (WS upgrade)|

Success responses: `{user: {id, username, email, created_at}, access_token, refresh_token, expires_in}`.
Errors: `{"error": "message"}` with 400 (validation/bad JSON), 401 (bad credentials / bad refresh token), 409 (email/username taken).

Paths mirror `ApiEndpoints` in the Flutter app (`lib/core/network/api_endpoints.dart`), which points at `http://<host>:8080`.

## Auth Design

- Access token: HS256 JWT (`sub` = user id), 15-minute TTL.
- Refresh token: opaque 32-byte random hex, 30-day TTL, stored as SHA-256 hash; rotated on every refresh and revoked on logout (reuse of an old token returns 401).
- Validation limits mirror the app's `AppConstants` (username 3–50, password 8–128).

## Conventions

- Explicit names, no cryptic abbreviations (`responseWriter`, not `w` — project rule)
- Handlers stay thin; business logic lives in `internal/service/`, storage in `internal/repository/`
- Every endpoint returns JSON

## Next Steps

- [ ] Auth middleware (validate Bearer JWT) for protected routes
- [ ] Novels/chapters endpoints for the app's Discover, Library, and reader
- [ ] Admin endpoints for the dashboard (framework decision still pending)
