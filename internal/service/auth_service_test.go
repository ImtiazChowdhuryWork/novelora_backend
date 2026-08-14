package service_test

import (
	"context"
	"errors"
	"testing"

	"github.com/ImtiazChowdhuryWork/novelora_backend/internal/repository"
	"github.com/ImtiazChowdhuryWork/novelora_backend/internal/service"
	"github.com/ImtiazChowdhuryWork/novelora_backend/internal/testsupport"
)

func TestAuthService_BecomeAuthor(t *testing.T) {
	pool := testsupport.NewPool(t)
	services := testsupport.NewServices(t, pool)
	ctx := context.Background()

	// NewTestAuthor already calls BecomeAuthor's equivalent (creates the
	// profile directly) — here we want a plain reader, not yet an
	// author, to exercise BecomeAuthor itself.
	user, err := services.Users.Create(ctx, "become_author_reader", "become_author_reader@novelora.test", "unused-test-hash")
	if err != nil {
		t.Fatalf("create test reader: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, "DELETE FROM users WHERE id = $1", user.ID)
	})

	result, err := services.Auth.BecomeAuthor(ctx, user.ID, "  Test Pen Name  ")
	if err != nil {
		t.Fatalf("BecomeAuthor() error = %v", err)
	}
	if result.AuthorProfile == nil {
		t.Fatal("BecomeAuthor() result.AuthorProfile is nil, want a profile")
	}
	if got, want := result.AuthorProfile.PenName, "Test Pen Name"; got != want {
		t.Errorf("pen name = %q, want %q (trimmed)", got, want)
	}
	if result.User.Role != "reader" {
		t.Errorf("user.Role = %q, want unchanged %q — becoming an author must not touch role", result.User.Role, "reader")
	}
	if result.AccessToken == "" {
		t.Error("BecomeAuthor() did not return a fresh access token")
	}

	// Calling it again for the same user is a conflict, not a silent
	// overwrite of the pen name.
	_, err = services.Auth.BecomeAuthor(ctx, user.ID, "Second Attempt")
	if !errors.Is(err, repository.ErrAuthorProfileExists) {
		t.Errorf("second BecomeAuthor() error = %v, want %v", err, repository.ErrAuthorProfileExists)
	}

	// An empty pen name is rejected before anything is written.
	emptyUser, err := services.Users.Create(ctx, "become_author_empty", "become_author_empty@novelora.test", "unused-test-hash")
	if err != nil {
		t.Fatalf("create second test reader: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, "DELETE FROM users WHERE id = $1", emptyUser.ID)
	})
	_, err = services.Auth.BecomeAuthor(ctx, emptyUser.ID, "   ")
	var validationError *service.ValidationError
	if !errors.As(err, &validationError) {
		t.Errorf("BecomeAuthor(empty pen name) error = %v, want a *service.ValidationError", err)
	}
}
