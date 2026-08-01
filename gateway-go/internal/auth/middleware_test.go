package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func init() {
	gin.SetMode(gin.TestMode)
}

func newAuthenticatedContext(t *testing.T, tokens *TokenService, user User) (*gin.Context, *httptest.ResponseRecorder, string) {
	t.Helper()
	tokenString, _, err := tokens.Generate(user)
	if err != nil {
		t.Fatalf("Generate() error: %v", err)
	}
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/protected", nil)
	c.Request.Header.Set("Authorization", "Bearer "+tokenString)
	return c, rec, tokenString
}

func TestMiddleware_Authenticate_MissingHeaderIs401(t *testing.T) {
	tokens := NewTokenService("test-secret", time.Hour)
	mw := NewMiddleware(tokens)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/protected", nil)

	mw.Authenticate()(c)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
	if !c.IsAborted() {
		t.Error("expected the chain to be aborted")
	}
}

func TestMiddleware_Authenticate_MalformedHeaderIs401(t *testing.T) {
	tokens := NewTokenService("test-secret", time.Hour)
	mw := NewMiddleware(tokens)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/protected", nil)
	c.Request.Header.Set("Authorization", "not-a-bearer-token")

	mw.Authenticate()(c)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestMiddleware_Authenticate_InvalidTokenIs401(t *testing.T) {
	tokens := NewTokenService("test-secret", time.Hour)
	mw := NewMiddleware(tokens)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/protected", nil)
	c.Request.Header.Set("Authorization", "Bearer garbage-token")

	mw.Authenticate()(c)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestMiddleware_Authenticate_ValidTokenSetsClaims(t *testing.T) {
	tokens := NewTokenService("test-secret", time.Hour)
	mw := NewMiddleware(tokens)
	user := User{ID: "user-1", Email: "a@example.com", Role: RoleUser}

	c, rec, _ := newAuthenticatedContext(t, tokens, user)

	mw.Authenticate()(c)

	if rec.Code != 200 && c.IsAborted() {
		t.Fatalf("Authenticate aborted a valid request with status %d", rec.Code)
	}
	claims, ok := CurrentClaims(c)
	if !ok {
		t.Fatal("CurrentClaims() ok = false after successful Authenticate")
	}
	if claims.UserID != user.ID || claims.Role != user.Role {
		t.Errorf("claims = %+v, want matching %+v", claims, user)
	}
}

func TestMiddleware_RequireRole_AllowsMatchingRole(t *testing.T) {
	tokens := NewTokenService("test-secret", time.Hour)
	mw := NewMiddleware(tokens)
	c, rec, _ := newAuthenticatedContext(t, tokens, User{ID: "admin-1", Role: RoleAdmin})
	mw.Authenticate()(c)

	mw.RequireRole(RoleAdmin)(c)

	if c.IsAborted() {
		t.Errorf("RequireRole(admin) aborted an admin request, status = %d", rec.Code)
	}
}

func TestMiddleware_RequireRole_RejectsMismatchedRole(t *testing.T) {
	tokens := NewTokenService("test-secret", time.Hour)
	mw := NewMiddleware(tokens)
	c, rec, _ := newAuthenticatedContext(t, tokens, User{ID: "user-1", Role: RoleUser})
	mw.Authenticate()(c)

	mw.RequireRole(RoleAdmin)(c)

	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusForbidden)
	}
	if !c.IsAborted() {
		t.Error("expected the chain to be aborted")
	}
}

func TestMiddleware_RequireRole_WithoutAuthenticateIs500(t *testing.T) {
	tokens := NewTokenService("test-secret", time.Hour)
	mw := NewMiddleware(tokens)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/protected", nil)

	mw.RequireRole(RoleAdmin)(c)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d (RequireRole used without Authenticate is a wiring bug)",
			rec.Code, http.StatusInternalServerError)
	}
}
