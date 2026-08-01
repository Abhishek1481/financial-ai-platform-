package handlers

import (
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/Abhishek1481/financial-ai-platform/gateway-go/internal/auth"
)

// AuthHandlers is the HTTP transport layer over auth.Service — it only
// translates JSON requests/responses to Service calls and maps domain
// errors to status codes. No business logic (password rules, credential
// checking, token issuance) lives here; all of that is in auth.Service,
// where it's testable without an HTTP server.
type AuthHandlers struct {
	service *auth.Service
}

func NewAuthHandlers(service *auth.Service) *AuthHandlers {
	return &AuthHandlers{service: service}
}

type registerRequest struct {
	Email    string `json:"email" binding:"required"`
	Password string `json:"password" binding:"required"`
}

type userResponse struct {
	ID    string `json:"id"`
	Email string `json:"email"`
	Role  string `json:"role"`
}

func (h *AuthHandlers) Register(c *gin.Context) {
	var req registerRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}

	user, err := h.service.Register(c.Request.Context(), req.Email, req.Password)
	if err != nil {
		writeAuthError(c, err)
		return
	}

	c.JSON(http.StatusCreated, userResponse{ID: user.ID, Email: user.Email, Role: string(user.Role)})
}

type loginRequest struct {
	Email    string `json:"email" binding:"required"`
	Password string `json:"password" binding:"required"`
}

type loginResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int64  `json:"expires_in"` // seconds
}

func (h *AuthHandlers) Login(c *gin.Context) {
	var req loginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}

	token, expiresAt, err := h.service.Login(c.Request.Context(), req.Email, req.Password)
	if err != nil {
		writeAuthError(c, err)
		return
	}

	c.JSON(http.StatusOK, loginResponse{
		AccessToken: token,
		TokenType:   "bearer",
		ExpiresIn:   int64(time.Until(expiresAt).Seconds()),
	})
}

// Me echoes the caller's own validated token claims. It deliberately does
// not re-query the repository: JWTs here are stateless (see
// TokenService's doc comment on the RS256-vs-HS256 tradeoff for the
// related issuer/verifier tradeoff) — the short TTL is what bounds how
// stale this can be, not a database round trip on every request.
func (h *AuthHandlers) Me(c *gin.Context) {
	claims, ok := auth.CurrentClaims(c)
	if !ok {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "no authenticated claims on request"})
		return
	}
	c.JSON(http.StatusOK, userResponse{ID: claims.UserID, Email: claims.Email, Role: string(claims.Role)})
}

func writeAuthError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, auth.ErrUserExists):
		c.JSON(http.StatusConflict, gin.H{"error": "an account with this email already exists"})
	case errors.Is(err, auth.ErrInvalidEmail):
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid email address"})
	case errors.Is(err, auth.ErrWeakPassword):
		c.JSON(http.StatusBadRequest, gin.H{"error": "password must be at least 8 characters"})
	case errors.Is(err, auth.ErrInvalidCredentials):
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid email or password"})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
	}
}
