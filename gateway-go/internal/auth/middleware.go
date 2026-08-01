package auth

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// claimsContextKey is the gin.Context key Authenticate stores validated
// claims under. Unexported so only this package's own helpers can set or
// read it — a handler can't accidentally (or maliciously, if it ever took
// untrusted input for a key name) forge authentication state via c.Set.
const claimsContextKey = "auth.claims"

// Middleware wraps a TokenService with gin.HandlerFuncs. Kept separate
// from TokenService itself so the token-parsing logic stays framework-
// agnostic and unit-testable without gin in the picture at all (see
// token_test.go); this layer is just the thin HTTP-transport translation
// on top of it.
type Middleware struct {
	tokens *TokenService
}

func NewMiddleware(tokens *TokenService) *Middleware {
	return &Middleware{tokens: tokens}
}

// Authenticate requires a valid "Authorization: Bearer <token>" header,
// aborting with 401 if it's missing, malformed, or the token doesn't
// verify. On success it stores the token's claims on the context for
// downstream handlers and middleware (RequireRole, CurrentClaims) to read.
func (m *Middleware) Authenticate() gin.HandlerFunc {
	return func(c *gin.Context) {
		header := c.GetHeader("Authorization")
		token, ok := strings.CutPrefix(header, "Bearer ")
		if !ok || token == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"error": "missing or malformed Authorization header",
			})
			return
		}

		claims, err := m.tokens.Parse(token)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid or expired token"})
			return
		}

		c.Set(claimsContextKey, claims)
		c.Next()
	}
}

// RequireRole aborts with 403 unless the authenticated caller's role is
// one of allowed. Must run after Authenticate — it reads the claims
// Authenticate stores, and treats their absence as a bug in route wiring
// (500), not a client error, since a missing Authenticate call is a
// programmer error, not a bad request.
func (m *Middleware) RequireRole(allowed ...Role) gin.HandlerFunc {
	return func(c *gin.Context) {
		claims, ok := CurrentClaims(c)
		if !ok {
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
				"error": "RequireRole used without Authenticate",
			})
			return
		}

		for _, role := range allowed {
			if claims.Role == role {
				c.Next()
				return
			}
		}
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "insufficient role"})
	}
}

// CurrentClaims retrieves the authenticated caller's claims, set by
// Authenticate. Returns ok=false if called on a route Authenticate never
// ran on.
func CurrentClaims(c *gin.Context) (*Claims, bool) {
	value, exists := c.Get(claimsContextKey)
	if !exists {
		return nil, false
	}
	claims, ok := value.(*Claims)
	return claims, ok
}
