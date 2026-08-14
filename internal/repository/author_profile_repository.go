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

var ErrAuthorProfileNotFound = errors.New("author profile not found")
var ErrAuthorProfileExists = errors.New("author profile already exists")

// AuthorProfile is the additive fact that a user account is also an
// author — see the migration's comment on why this is a separate
// table keyed by user_id, not a role value.
type AuthorProfile struct {
	UserID    string
	PenName   string
	Bio       string
	CreatedAt time.Time
}

type AuthorProfileRepository struct {
	pool *pgxpool.Pool
}

func NewAuthorProfileRepository(pool *pgxpool.Pool) *AuthorProfileRepository {
	return &AuthorProfileRepository{pool: pool}
}

// Exists is the cheap check issueTokens calls on every sign-in to
// decide the JWT's is_author claim.
func (repository *AuthorProfileRepository) Exists(ctx context.Context, userID string) (bool, error) {
	var exists bool
	err := repository.pool.QueryRow(ctx,
		"SELECT EXISTS(SELECT 1 FROM author_profiles WHERE user_id = $1)", userID,
	).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("check author profile exists: %w", err)
	}
	return exists, nil
}

func (repository *AuthorProfileRepository) GetByUserID(ctx context.Context, userID string) (*AuthorProfile, error) {
	profile := &AuthorProfile{}
	err := repository.pool.QueryRow(ctx,
		"SELECT user_id, pen_name, bio, created_at FROM author_profiles WHERE user_id = $1", userID,
	).Scan(&profile.UserID, &profile.PenName, &profile.Bio, &profile.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrAuthorProfileNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get author profile: %w", err)
	}
	return profile, nil
}

// Create is the "become an author" write — 409-equivalent
// (ErrAuthorProfileExists) if the user already has a profile, since
// user_id is the primary key.
func (repository *AuthorProfileRepository) Create(ctx context.Context, userID, penName string) (*AuthorProfile, error) {
	profile := &AuthorProfile{}
	err := repository.pool.QueryRow(ctx, `
		INSERT INTO author_profiles (user_id, pen_name)
		VALUES ($1, $2) RETURNING user_id, pen_name, bio, created_at`,
		userID, penName,
	).Scan(&profile.UserID, &profile.PenName, &profile.Bio, &profile.CreatedAt)
	if err != nil {
		var postgresError *pgconn.PgError
		if errors.As(err, &postgresError) && postgresError.Code == pgerrcodeUniqueViolation {
			return nil, ErrAuthorProfileExists
		}
		return nil, fmt.Errorf("create author profile: %w", err)
	}
	return profile, nil
}
