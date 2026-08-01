package auth

import "time"

// User is the auth domain's view of an account — deliberately smaller than
// whatever a future "profile" or "billing" table might hold. Auth only
// needs enough to authenticate and authorize.
type User struct {
	ID           string
	Email        string
	PasswordHash string
	Role         Role
	CreatedAt    time.Time
}
