package repository

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrRefreshTokenNotFound = errors.New("refresh token not found")

// RefreshToken is a row in the refresh_tokens table. Only a SHA-256
// hash of the client-held token is stored.
type RefreshToken struct {
	ID        string
	UserID    string
	TokenHash string
	ExpiresAt time.Time
	RevokedAt *time.Time
}

type RefreshTokenRepository struct {
	pool *pgxpool.Pool
}

func NewRefreshTokenRepository(pool *pgxpool.Pool) *RefreshTokenRepository {
	return &RefreshTokenRepository{pool: pool}
}

func (repository *RefreshTokenRepository) Store(ctx context.Context, userID, tokenHash string, expiresAt time.Time) error {
	_, err := repository.pool.Exec(ctx,
		`INSERT INTO refresh_tokens (user_id, token_hash, expires_at) VALUES ($1, $2, $3)`,
		userID, tokenHash, expiresAt,
	)
	if err != nil {
		return fmt.Errorf("insert refresh token: %w", err)
	}
	return nil
}

// FindActive returns the token only if it is unrevoked and unexpired.
func (repository *RefreshTokenRepository) FindActive(ctx context.Context, tokenHash string) (*RefreshToken, error) {
	refreshToken := &RefreshToken{}
	err := repository.pool.QueryRow(ctx,
		`SELECT id, user_id, token_hash, expires_at, revoked_at
		 FROM refresh_tokens
		 WHERE token_hash = $1 AND revoked_at IS NULL AND expires_at > now()`,
		tokenHash,
	).Scan(&refreshToken.ID, &refreshToken.UserID, &refreshToken.TokenHash,
		&refreshToken.ExpiresAt, &refreshToken.RevokedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrRefreshTokenNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("find refresh token: %w", err)
	}
	return refreshToken, nil
}

// Revoke marks the token as revoked. Revoking a token that is already
// revoked or unknown is not an error (logout stays idempotent).
func (repository *RefreshTokenRepository) Revoke(ctx context.Context, tokenHash string) error {
	_, err := repository.pool.Exec(ctx,
		`UPDATE refresh_tokens SET revoked_at = now()
		 WHERE token_hash = $1 AND revoked_at IS NULL`,
		tokenHash,
	)
	if err != nil {
		return fmt.Errorf("revoke refresh token: %w", err)
	}
	return nil
}
