package middleware

import (
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/Abhishek1481/financial-ai-platform/gateway-go/internal/reqid"
)

// RequestIDHeader is echoed on every response so a caller (or a load
// balancer/proxy in front of gateway-go) can supply its own correlation ID
// and have it threaded through, rather than always getting one gateway-go
// invented.
const RequestIDHeader = "X-Request-ID"

// RequestID stamps every request with a correlation ID — reused from the
// incoming header if the caller supplied one, minted otherwise — and
// stores it on the request context so internal/mlclient's client
// interceptor can forward it to ml-service as gRPC metadata. Must run
// before Logging (see server.go's middleware chain) so request logs can
// include it too.
func RequestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.GetHeader(RequestIDHeader)
		if id == "" {
			id = uuid.NewString()
		}
		c.Writer.Header().Set(RequestIDHeader, id)
		c.Request = c.Request.WithContext(reqid.WithRequestID(c.Request.Context(), id))
		c.Next()
	}
}
