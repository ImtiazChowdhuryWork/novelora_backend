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
	CreatedAt    time.Time
}

type UserRepository struct {
	pool *pgxpool.Pool
}

func NewUserRepository(pool *pgxpool.Pool) *UserRepository {
	return &UserRepository{pool: pool}
}

const userColumns = `id, username, email, password_hash, created_at`

// Create inserts a new user and returns the stored row. Duplicate
// username/email surface as ErrUsernameTaken / ErrEmailTaken.
func (repository *UserRepository) Create(ctx context.Context, username, email, passwordHash string) (*User, error) {
	user := &User{}
	err := repository.pool.QueryRow(ctx,
		`INSERT INTO users (username, email, password_hash)
		 VALUES ($1, $2, $3)
		 RETURNING `+userColumns,
		username, email, passwordHash,
	).Scan(&user.ID, &user.Username, &user.Email, &user.PasswordHash, &user.CreatedAt)
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
	).Scan(&user.ID, &user.Username, &user.Email, &user.PasswordHash, &user.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrUserNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("find user by email: %w", err)
	}
	return user, nil
}

func (repository *UserRepository) FindByID(ctx context.Context, userID string) (*User, error) {
	user := &User{}
	err := repository.pool.QueryRow(ctx,
		`SELECT `+userColumns+` FROM users WHERE id = $1`,
		userID,
	).Scan(&user.ID, &user.Username, &user.Email, &user.PasswordHash, &user.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrUserNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("find user by id: %w", err)
	}
	return user, nil
}

const pgerrcodeUniqueViolation = "23505"
