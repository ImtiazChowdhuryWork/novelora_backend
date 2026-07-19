package repository

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrUserNotFound  = errors.New("user not found")
	ErrEmailTaken    = errors.New("email is already registered")
	ErrUsernameTaken = errors.New("username is already taken")
)

// User is a row in the users table.
type User struct {
	ID           string
	Username     string
	Email        string
	PasswordHash string
	AvatarURL    string // empty when the user has no avatar
	Role         string // "reader" or "admin"
	IsBanned     bool
	CreatedAt    time.Time
}

type UserRepository struct {
	pool *pgxpool.Pool
}

func NewUserRepository(pool *pgxpool.Pool) *UserRepository {
	return &UserRepository{pool: pool}
}

const userColumns = `id, username, email, password_hash, coalesce(avatar_url, ''), role, is_banned, created_at`

// Create inserts a new user and returns the stored row. Duplicate
// username/email surface as ErrUsernameTaken / ErrEmailTaken.
func (repository *UserRepository) Create(ctx context.Context, username, email, passwordHash string) (*User, error) {
	user := &User{}
	err := repository.pool.QueryRow(ctx,
		`INSERT INTO users (username, email, password_hash)
		 VALUES ($1, $2, $3)
		 RETURNING `+userColumns,
		username, email, passwordHash,
	).Scan(&user.ID, &user.Username, &user.Email, &user.PasswordHash, &user.AvatarURL, &user.Role, &user.IsBanned, &user.CreatedAt)
	if err != nil {
		var postgresError *pgconn.PgError
		if errors.As(err, &postgresError) && postgresError.Code == pgerrcodeUniqueViolation {
			switch postgresError.ConstraintName {
			case "users_email_unique":
				return nil, ErrEmailTaken
			case "users_username_unique":
				return nil, ErrUsernameTaken
			}
		}
		return nil, fmt.Errorf("insert user: %w", err)
	}
	return user, nil
}

func (repository *UserRepository) FindByEmail(ctx context.Context, email string) (*User, error) {
	user := &User{}
	err := repository.pool.QueryRow(ctx,
		`SELECT `+userColumns+` FROM users WHERE lower(email) = lower($1)`,
		email,
	).Scan(&user.ID, &user.Username, &user.Email, &user.PasswordHash, &user.AvatarURL, &user.Role, &user.IsBanned, &user.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrUserNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("find user by email: %w", err)
	}
	return user, nil
}

// CreateWithGoogle inserts a user backed by a Google account (no password).
func (repository *UserRepository) CreateWithGoogle(ctx context.Context, username, email, googleID, avatarURL string) (*User, error) {
	user := &User{}
	err := repository.pool.QueryRow(ctx,
		`INSERT INTO users (username, email, password_hash, google_id, avatar_url)
		 VALUES ($1, $2, '', $3, nullif($4, ''))
		 RETURNING `+userColumns,
		username, email, googleID, avatarURL,
	).Scan(&user.ID, &user.Username, &user.Email, &user.PasswordHash, &user.AvatarURL, &user.Role, &user.IsBanned, &user.CreatedAt)
	if err != nil {
		var postgresError *pgconn.PgError
		if errors.As(err, &postgresError) && postgresError.Code == pgerrcodeUniqueViolation {
			switch postgresError.ConstraintName {
			case "users_email_unique":
				return nil, ErrEmailTaken
			case "users_username_unique":
				return nil, ErrUsernameTaken
			}
		}
		return nil, fmt.Errorf("insert google user: %w", err)
	}
	return user, nil
}

func (repository *UserRepository) FindByGoogleID(ctx context.Context, googleID string) (*User, error) {
	user := &User{}
	err := repository.pool.QueryRow(ctx,
		`SELECT `+userColumns+` FROM users WHERE google_id = $1`,
		googleID,
	).Scan(&user.ID, &user.Username, &user.Email, &user.PasswordHash, &user.AvatarURL, &user.Role, &user.IsBanned, &user.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrUserNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("find user by google id: %w", err)
	}
	return user, nil
}

// UpdateAvatarURL replaces the user's avatar; empty clears it.
func (repository *UserRepository) UpdateAvatarURL(ctx context.Context, userID, avatarURL string) error {
	_, err := repository.pool.Exec(ctx,
		`UPDATE users SET avatar_url = nullif($1, ''), updated_at = now() WHERE id = $2`,
		avatarURL, userID,
	)
	if err != nil {
		return fmt.Errorf("update avatar url: %w", err)
	}
	return nil
}

// SetGoogleID links a Google account to an existing user.
func (repository *UserRepository) SetGoogleID(ctx context.Context, userID, googleID string) error {
	_, err := repository.pool.Exec(ctx,
		`UPDATE users SET google_id = $1, updated_at = now() WHERE id = $2`,
		googleID, userID,
	)
	if err != nil {
		return fmt.Errorf("set google id: %w", err)
	}
	return nil
}

// UserListFilter narrows and pages the admin users list.
type UserListFilter struct {
	Search   string
	Page     int
	PageSize int
}

// List returns one page of users plus the total row count for the filter.
func (repository *UserRepository) List(ctx context.Context, filter UserListFilter) ([]*User, int, error) {
	conditions := []string{"true"}
	arguments := []any{}

	if filter.Search != "" {
		arguments = append(arguments, "%"+strings.ToLower(filter.Search)+"%")
		conditions = append(conditions, fmt.Sprintf(
			"(lower(username) LIKE $%d OR lower(email) LIKE $%d)", len(arguments), len(arguments)))
	}
	whereClause := strings.Join(conditions, " AND ")

	var total int
	if err := repository.pool.QueryRow(ctx,
		"SELECT count(*) FROM users WHERE "+whereClause, arguments...,
	).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count users: %w", err)
	}

	arguments = append(arguments, filter.PageSize, (filter.Page-1)*filter.PageSize)
	rows, err := repository.pool.Query(ctx, fmt.Sprintf(
		"SELECT %s FROM users WHERE %s ORDER BY created_at DESC LIMIT $%d OFFSET $%d",
		userColumns, whereClause, len(arguments)-1, len(arguments)), arguments...)
	if err != nil {
		return nil, 0, fmt.Errorf("list users: %w", err)
	}
	defer rows.Close()

	users := []*User{}
	for rows.Next() {
		user := &User{}
		if err := rows.Scan(&user.ID, &user.Username, &user.Email, &user.PasswordHash,
			&user.AvatarURL, &user.Role, &user.IsBanned, &user.CreatedAt); err != nil {
			return nil, 0, fmt.Errorf("scan user: %w", err)
		}
		users = append(users, user)
	}
	return users, total, rows.Err()
}

// UpdateRole promotes/demotes an account. role must already be validated
// by the caller ("reader" or "admin" — the DB CHECK constraint is the
// final backstop).
func (repository *UserRepository) UpdateRole(ctx context.Context, userID, role string) error {
	commandTag, err := repository.pool.Exec(ctx,
		"UPDATE users SET role = $1, updated_at = now() WHERE id = $2", role, userID)
	if err != nil {
		return fmt.Errorf("update user role: %w", err)
	}
	if commandTag.RowsAffected() == 0 {
		return ErrUserNotFound
	}
	return nil
}

// SetBanned is the only moderation action available today — there's no
// user-generated content (comments/reviews) yet to moderate directly.
func (repository *UserRepository) SetBanned(ctx context.Context, userID string, banned bool) error {
	commandTag, err := repository.pool.Exec(ctx,
		"UPDATE users SET is_banned = $1, updated_at = now() WHERE id = $2", banned, userID)
	if err != nil {
		return fmt.Errorf("update user banned status: %w", err)
	}
	if commandTag.RowsAffected() == 0 {
		return ErrUserNotFound
	}
	return nil
}

func (repository *UserRepository) FindByID(ctx context.Context, userID string) (*User, error) {
	user := &User{}
	err := repository.pool.QueryRow(ctx,
		`SELECT `+userColumns+` FROM users WHERE id = $1`,
		userID,
	).Scan(&user.ID, &user.Username, &user.Email, &user.PasswordHash, &user.AvatarURL, &user.Role, &user.IsBanned, &user.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrUserNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("find user by id: %w", err)
	}
	return user, nil
}

const pgerrcodeUniqueViolation = "23505"
