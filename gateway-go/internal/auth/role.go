package auth

// Role is a closed set (not a bare string) so a typo like "admni" fails at
// compile time in code that constructs one, and IsValid() catches it for
// values coming from outside the process (JWT claims, request bodies).
type Role string

const (
	RoleUser  Role = "user"
	RoleAdmin Role = "admin"
)

func (r Role) IsValid() bool {
	switch r {
	case RoleUser, RoleAdmin:
		return true
	default:
		return false
	}
}
