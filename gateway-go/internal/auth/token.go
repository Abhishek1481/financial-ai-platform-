package auth

import (
	"errors"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const issuer = "financial-ai-platform/gateway-go"

var (
	ErrInvalidToken = errors.New("auth: invalid or expired token")
)

// Claims is what actually rides inside the JWT. Embedding
// jwt.RegisteredClaims gets exp/iat/sub/iss for free instead of hand-rolling
// them; UserID/Email/Role are this platform's own claims.
type Claims struct {
	jwt.RegisteredClaims
	UserID string `json:"user_id"`
	Email  string `json:"email"`
	Role   Role   `json:"role"`
}

// TokenService issues and verifies JWTs with a single shared secret
// (HS256). Symmetric signing is the right choice here specifically because
// gateway-go is both the only issuer and the only verifier — no other
// service ever checks a user-facing token directly (ml-service is reached
// over gRPC, authenticated at the transport/network level, not per-user
// JWTs). If a second service ever needed to verify tokens without sharing
// this secret, that would be the point to move to RS256 and distribute a
// public key instead.
type TokenService struct {
	secret []byte
	ttl    time.Duration
}

func NewTokenService(secret string, ttl time.Duration) *TokenService {
	return &TokenService{secret: []byte(secret), ttl: ttl}
}

// Generate issues a signed token for user, returning the token string and
// its expiry.
func (s *TokenService) Generate(user User) (string, time.Time, error) {
	now := time.Now()
	expiresAt := now.Add(s.ttl)

	claims := Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    issuer,
			Subject:   user.ID,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(expiresAt),
		},
		UserID: user.ID,
		Email:  user.Email,
		Role:   user.Role,
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString(s.secret)
	if err != nil {
		return "", time.Time{}, err
	}
	return signed, expiresAt, nil
}

// Parse validates a token's signature and expiry and returns its claims.
// Any failure — bad signature, expired, malformed, wrong algorithm — comes
// back as ErrInvalidToken; callers on the request path don't need to
// distinguish why a token was rejected, only that it was.
func (s *TokenService) Parse(tokenString string) (*Claims, error) {
	claims := &Claims{}
	token, err := jwt.ParseWithClaims(tokenString, claims, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, ErrInvalidToken
		}
		return s.secret, nil
	})
	if err != nil || !token.Valid {
		return nil, ErrInvalidToken
	}
	return claims, nil
}
