package auth

import (
	"testing"
	"time"
)

func TestTokenService_GenerateAndParseRoundTrip(t *testing.T) {
	svc := NewTokenService("test-secret", time.Hour)
	user := User{ID: "user-1", Email: "a@example.com", Role: RoleUser}

	tokenString, expiresAt, err := svc.Generate(user)
	if err != nil {
		t.Fatalf("Generate() error: %v", err)
	}
	if tokenString == "" {
		t.Fatal("Generate() returned empty token")
	}
	if !expiresAt.After(time.Now()) {
		t.Errorf("expiresAt = %v, want in the future", expiresAt)
	}

	claims, err := svc.Parse(tokenString)
	if err != nil {
		t.Fatalf("Parse() error: %v", err)
	}
	if claims.UserID != user.ID || claims.Email != user.Email || claims.Role != user.Role {
		t.Errorf("claims = %+v, want matching %+v", claims, user)
	}
}

func TestTokenService_ExpiredTokenIsRejected(t *testing.T) {
	svc := NewTokenService("test-secret", -time.Hour) // already expired
	tokenString, _, err := svc.Generate(User{ID: "user-1", Role: RoleUser})
	if err != nil {
		t.Fatalf("Generate() error: %v", err)
	}

	if _, err := svc.Parse(tokenString); err == nil {
		t.Fatal("Parse() of expired token: expected error, got nil")
	}
}

func TestTokenService_WrongSecretIsRejected(t *testing.T) {
	issuer := NewTokenService("secret-a", time.Hour)
	verifier := NewTokenService("secret-b", time.Hour)

	tokenString, _, err := issuer.Generate(User{ID: "user-1", Role: RoleUser})
	if err != nil {
		t.Fatalf("Generate() error: %v", err)
	}

	if _, err := verifier.Parse(tokenString); err == nil {
		t.Fatal("Parse() with wrong secret: expected error, got nil")
	}
}

func TestTokenService_MalformedTokenIsRejected(t *testing.T) {
	svc := NewTokenService("test-secret", time.Hour)

	if _, err := svc.Parse("not-a-jwt"); err == nil {
		t.Fatal("Parse() of malformed token: expected error, got nil")
	}
}
