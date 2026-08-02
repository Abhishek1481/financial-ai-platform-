package auth

import (
	"context"
	"errors"
	"fmt"
	"net/mail"
	"time"

	"github.com/google/uuid"
)

var (
	ErrInvalidCredentials = errors.New("auth: invalid credentials")
	ErrInvalidEmail       = errors.New("auth: invalid email address")
	ErrWeakPassword       = errors.New("auth: password must be at least 8 characters")
)

const minPasswordLength = 8

// Service is the auth domain's business logic, sitting between HTTP
// handlers and the UserRepository — handlers translate transport
// (JSON/HTTP) to these calls and back; Service never imports gin or knows
// an HTTP request exists, which is what makes it unit-testable without
// spinning up a server and reusable if a non-HTTP entrypoint (a CLI admin
// tool, say) ever needs auth logic.
type Service struct {
	repo   UserRepository
	tokens *TokenService
}

func NewService(repo UserRepository, tokens *TokenService) *Service {
	return &Service{repo: repo, tokens: tokens}
}

// Register creates a new user with role "user". There is no path to
// self-register as admin — the only admin account in this phase is the one
// seeded at startup (see SeedAdmin) from GATEWAY_ADMIN_EMAIL/PASSWORD.
// Granting admin via the API is Phase 15's problem (admin dashboard), once
// there's an existing admin identity to authorize the grant.
func (s *Service) Register(ctx context.Context, email, password string) (User, error) {
	return s.createUser(ctx, email, password, RoleUser)
}

// SeedAdmin ensures exactly one admin account exists, identified by email.
// Idempotent: calling it again with the same email is a no-op, so it's
// safe to run unconditionally on every process start.
func (s *Service) SeedAdmin(ctx context.Context, email, password string) error {
	_, err := s.repo.FindByEmail(ctx, email)
	if err == nil {
		return nil // already seeded
	}
	if !errors.Is(err, ErrUserNotFound) {
		return err
	}

	_, err = s.createUser(ctx, email, password, RoleAdmin)
	return err
}

func (s *Service) createUser(ctx context.Context, email, password string, role Role) (User, error) {
	if _, err := mail.ParseAddress(email); err != nil {
		return User{}, ErrInvalidEmail
	}
	if len(password) < minPasswordLength {
		return User{}, ErrWeakPassword
	}

	hash, err := HashPassword(password)
	if err != nil {
		return User{}, fmt.Errorf("auth: hash password: %w", err)
	}

	user := User{
		ID:           uuid.NewString(),
		Email:        email,
		PasswordHash: hash,
		Role:         role,
		CreatedAt:    time.Now(),
	}
	if err := s.repo.Create(ctx, user); err != nil {
		return User{}, err
	}
	return user, nil
}

// Login verifies credentials and issues a token. On any failure — no such
// user, wrong password — it returns ErrInvalidCredentials for both cases
// rather than distinguishing them: telling a client "no such user" instead
// of "wrong password" hands an attacker a working email-enumeration oracle
// for free.
func (s *Service) Login(ctx context.Context, email, password string) (string, time.Time, error) {
	user, err := s.repo.FindByEmail(ctx, email)
	if err != nil {
		return "", time.Time{}, ErrInvalidCredentials
	}
	if err := VerifyPassword(user.PasswordHash, password); err != nil {
		return "", time.Time{}, ErrInvalidCredentials
	}
	return s.tokens.Generate(user)
}

// ListUsers powers the admin dashboard's user listing (Phase 15) — an
// admin-only capability, gated by RBAC at the handler layer
// (RequireRole), not here: Service methods trust their caller the same
// way DocumentRepository/JobRepository do, since the HTTP transport layer
// is where "is this caller allowed to call this" is decided.
func (s *Service) ListUsers(ctx context.Context) ([]User, error) {
	return s.repo.ListAll(ctx)
}
