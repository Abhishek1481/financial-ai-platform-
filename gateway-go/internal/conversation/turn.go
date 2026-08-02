package conversation

import "time"

type Role string

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
)

// Turn is one message in a conversation — gateway-go's own representation,
// independent of the proto ConversationTurn RAGHandlers converts it to/from
// at the mlclient boundary, same reasoning as auth.User vs. the JWT claims
// it's encoded into.
type Turn struct {
	Role      Role
	Content   string
	CreatedAt time.Time
}
