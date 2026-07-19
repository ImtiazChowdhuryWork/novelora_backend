package repository

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

type DeviceTokenRepository struct {
	pool *pgxpool.Pool
}

func NewDeviceTokenRepository(pool *pgxpool.Pool) *DeviceTokenRepository {
	return &DeviceTokenRepository{pool: pool}
}

// Upsert registers or re-associates a device token. Tokens are unique
// per device (Firebase reissues a new one on reinstall/logout), so a
// re-registration under a different account simply reassigns it.
func (repository *DeviceTokenRepository) Upsert(ctx context.Context, userID, token, platform string) error {
	_, err := repository.pool.Exec(ctx, `
		INSERT INTO device_tokens (user_id, token, platform)
		VALUES ($1, $2, $3)
		ON CONFLICT (token) DO UPDATE
		SET user_id = $1, platform = $3, updated_at = now()`,
		userID, token, platform)
	if err != nil {
		return fmt.Errorf("upsert device token: %w", err)
	}
	return nil
}

// ListAllTokens returns every registered device token. Novelora has no
// per-novel "following" list yet, so a new chapter is broadcast to
// every device — narrow this once readers can follow specific novels.
func (repository *DeviceTokenRepository) ListAllTokens(ctx context.Context) ([]string, error) {
	rows, err := repository.pool.Query(ctx, "SELECT token FROM device_tokens")
	if err != nil {
		return nil, fmt.Errorf("list device tokens: %w", err)
	}
	defer rows.Close()

	tokens := []string{}
	for rows.Next() {
		var token string
		if err := rows.Scan(&token); err != nil {
			return nil, fmt.Errorf("scan device token: %w", err)
		}
		tokens = append(tokens, token)
	}
	return tokens, rows.Err()
}
