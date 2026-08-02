package auth

import (
	"context"
	"strings"
	"sync"
)

// MemoryUserRepository is a UserRepository backed by an in-process map.
//
// This is a deliberate, temporary stand-in — there is no Postgres
// connection wired up yet (that lands alongside Docker Compose in Phase
// 16). Every account created here is lost on restart and never shared
// across replicas, which is fine for local development and for the tests
// in this package, and would not be fine in any deployed environment.
// UserRepository is the seam a PostgresUserRepository slots into later
// without any caller (Service, handlers, middleware) changing.
type MemoryUserRepository struct {
	mu    sync.RWMutex
	byID  map[string]User
	byEml map[string]string // lowercased email -> user ID
}

func NewMemoryUserRepository() *MemoryUserRepository {
	return &MemoryUserRepository{
		byID:  make(map[string]User),
		byEml: make(map[string]string),
	}
}

func (r *MemoryUserRepository) Create(ctx context.Context, user User) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	key := normalizeEmail(user.Email)
	if _, exists := r.byEml[key]; exists {
		return ErrUserExists
	}
	r.byID[user.ID] = user
	r.byEml[key] = user.ID
	return nil
}

func (r *MemoryUserRepository) FindByEmail(ctx context.Context, email string) (User, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	id, ok := r.byEml[normalizeEmail(email)]
	if !ok {
		return User{}, ErrUserNotFound
	}
	return r.byID[id], nil
}

func (r *MemoryUserRepository) FindByID(ctx context.Context, id string) (User, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	user, ok := r.byID[id]
	if !ok {
		return User{}, ErrUserNotFound
	}
	return user, nil
}

func (r *MemoryUserRepository) ListAll(ctx context.Context) ([]User, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	users := make([]User, 0, len(r.byID))
	for _, user := range r.byID {
		users = append(users, user)
	}
	return users, nil
}

func normalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}
