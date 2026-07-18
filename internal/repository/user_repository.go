package repository

import (
	"context"
	"errors"
	"fmt"
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
	CreatedAt    time.Time
}

type UserRepository struct {
	pool *pgxpool.Pool
}

func NewUserRepository(pool *pgxpool.Pool) *UserRepository {
	return &UserRepository{pool: pool}
}

const userColumns = `id, username, email, password_hash, coalesce(avatar_url, ''), created_at`

// Create inserts a new user and returns the stored row. Duplicate
// username/email surface as ErrUsernameTaken / ErrEmailTaken.
func (repository *UserRepository) Create(ctx context.Context, username, email, passwordHash string) (*User, error) {
	user := &User{}
	err := repository.pool.QueryRow(ctx,
		`INSERT INTO users (username, email, password_hash)
		 VALUES ($1, $2, $3)
		 RETURNING `+userColumns,
		username, email, passwordHash,
	).Scan(&user.ID, &user.Username, &user.Email, &user.PasswordHash, &user.AvatarURL, &user.CreatedAt)
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
	).Scan(&user.ID, &user.Username, &user.Email, &user.PasswordHash, &user.AvatarURL, &user.CreatedAt)
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
	).Scan(&user.ID, &user.Username, &user.Email, &user.PasswordHash, &user.AvatarURL, &user.CreatedAt)
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
	).Scan(&user.ID, &user.Username, &user.Email, &user.PasswordHash, &user.AvatarURL, &user.CreatedAt)
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

func (repository *UserRepository) FindByID(ctx context.Context, userID string) (*User, error) {
	user := &User{}
	err := repository.pool.QueryRow(ctx,
		`SELECT `+userColumns+` FROM users WHERE id = $1`,
		userID,
	).Scan(&user.ID, &user.Username, &user.Email, &user.PasswordHash, &user.AvatarURL, &user.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrUserNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("find user by id: %w", err)
	}
	return user, nil
}

const pgerrcodeUniqueViolation = "23505"
