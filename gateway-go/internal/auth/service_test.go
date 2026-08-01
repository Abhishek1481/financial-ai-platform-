package auth

import (
	"context"
	"errors"
	"testing"
	"time"
)

func newTestService() *Service {
	repo := NewMemoryUserRepository()
	tokens := NewTokenService("test-secret", time.Hour)
	return NewService(repo, tokens)
}

func TestService_RegisterCreatesUserRole(t *testing.T) {
	svc := newTestService()

	user, err := svc.Register(context.Background(), "a@example.com", "correct-horse-battery")
	if err != nil {
		t.Fatalf("Register() error: %v", err)
	}
	if user.Role != RoleUser {
		t.Errorf("Role = %q, want %q", user.Role, RoleUser)
	}
	if user.PasswordHash == "correct-horse-battery" {
		t.Error("Register() stored the plaintext password")
	}
	if user.ID == "" {
		t.Error("Register() did not assign an ID")
	}
}

func TestService_RegisterDuplicateEmailFails(t *testing.T) {
	svc := newTestService()
	ctx := context.Background()

	if _, err := svc.Register(ctx, "a@example.com", "correct-horse-battery"); err != nil {
		t.Fatalf("first Register() error: %v", err)
	}
	_, err := svc.Register(ctx, "a@example.com", "another-password")
	if !errors.Is(err, ErrUserExists) {
		t.Errorf("second Register() error = %v, want %v", err, ErrUserExists)
	}
}

func TestService_RegisterRejectsInvalidEmail(t *testing.T) {
	svc := newTestService()
	_, err := svc.Register(context.Background(), "not-an-email", "correct-horse-battery")
	if !errors.Is(err, ErrInvalidEmail) {
		t.Errorf("error = %v, want %v", err, ErrInvalidEmail)
	}
}

func TestService_RegisterRejectsWeakPassword(t *testing.T) {
	svc := newTestService()
	_, err := svc.Register(context.Background(), "a@example.com", "short")
	if !errors.Is(err, ErrWeakPassword) {
		t.Errorf("error = %v, want %v", err, ErrWeakPassword)
	}
}

func TestService_LoginSucceedsWithCorrectCredentials(t *testing.T) {
	svc := newTestService()
	ctx := context.Background()
	if _, err := svc.Register(ctx, "a@example.com", "correct-horse-battery"); err != nil {
		t.Fatalf("Register() error: %v", err)
	}

	token, expiresAt, err := svc.Login(ctx, "a@example.com", "correct-horse-battery")
	if err != nil {
		t.Fatalf("Login() error: %v", err)
	}
	if token == "" {
		t.Error("Login() returned empty token")
	}
	if !expiresAt.After(time.Now()) {
		t.Errorf("expiresAt = %v, want in the future", expiresAt)
	}
}

func TestService_LoginFailsWithWrongPassword(t *testing.T) {
	svc := newTestService()
	ctx := context.Background()
	if _, err := svc.Register(ctx, "a@example.com", "correct-horse-battery"); err != nil {
		t.Fatalf("Register() error: %v", err)
	}

	_, _, err := svc.Login(ctx, "a@example.com", "wrong-password")
	if !errors.Is(err, ErrInvalidCredentials) {
		t.Errorf("error = %v, want %v", err, ErrInvalidCredentials)
	}
}

func TestService_LoginFailsForUnknownUser_SameErrorAsWrongPassword(t *testing.T) {
	svc := newTestService()

	_, _, err := svc.Login(context.Background(), "nobody@example.com", "whatever")
	if !errors.Is(err, ErrInvalidCredentials) {
		t.Errorf("error = %v, want %v (must not leak whether the account exists)", err, ErrInvalidCredentials)
	}
}

func TestService_SeedAdminIsIdempotent(t *testing.T) {
	svc := newTestService()
	ctx := context.Background()

	if err := svc.SeedAdmin(ctx, "admin@example.com", "admin-password-123"); err != nil {
		t.Fatalf("first SeedAdmin() error: %v", err)
	}
	if err := svc.SeedAdmin(ctx, "admin@example.com", "admin-password-123"); err != nil {
		t.Fatalf("second SeedAdmin() error: %v (must be idempotent)", err)
	}

	token, _, err := svc.Login(ctx, "admin@example.com", "admin-password-123")
	if err != nil {
		t.Fatalf("Login() as seeded admin: %v", err)
	}
	if token == "" {
		t.Error("Login() as seeded admin returned empty token")
	}
}
