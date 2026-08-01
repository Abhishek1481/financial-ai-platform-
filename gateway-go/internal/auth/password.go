package auth

import "golang.org/x/crypto/bcrypt"

// bcrypt's default cost (10) is deliberately left as-is rather than tuned
// up: cost is a throughput/security tradeoff that should be chosen against
// real hardware and login-latency budgets, not guessed at in a skeleton
// phase with no production traffic to measure.
const bcryptCost = bcrypt.DefaultCost

func HashPassword(plain string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(plain), bcryptCost)
	if err != nil {
		return "", err
	}
	return string(hash), nil
}

// VerifyPassword returns nil if plain matches hash, and a non-nil error
// (bcrypt.ErrMismatchedHashAndPassword, typically) otherwise.
func VerifyPassword(hash, plain string) error {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(plain))
}
