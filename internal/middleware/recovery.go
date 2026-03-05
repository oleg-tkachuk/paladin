package middleware

import (
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"os"
	"runtime/debug"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/oleg-tkachuk/paladin/internal/utils"
	"go.uber.org/zap"
)

// ZapRecovery returns a middleware that recovers from any panics and writes a 500 if there was one.
// It logs the panic and the stack trace using zap.
func ZapRecovery(logger *zap.Logger, stack bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if err := recover(); err != nil {
				// Check for a broken connection, as it is not a "normal"
				// panic conditions.
				var brokenPipe bool
				if ne, ok := err.(*net.OpError); ok {
					if se, ok := ne.Err.(*os.SyscallError); ok {
						if strings.Contains(strings.ToLower(se.Error()), "broken pipe") || strings.Contains(strings.ToLower(se.Error()), "connection reset by peer") {
							brokenPipe = true
						}
					}
				}

				rid := utils.RequestIDFromContext(c.Request.Context(), "")
				tenant := utils.TenantIDFromContext(c.Request.Context(), "")

				httpRequest, _ := httputil.DumpRequest(c.Request, false)
				if brokenPipe {
					logger.Error(c.Request.URL.Path,
						zap.Any("error", err),
						zap.String("request_id", rid),
						zap.String("tenant_id", tenant),
						zap.String("request", string(httpRequest)),
					)
					// If the connection is dead, we can't write a response to it.
					c.Error(err.(error)) // nolint: errcheck
					c.Abort()
					return
				}

				if stack {
					logger.Error("[Recovery from panic]",
						zap.Any("error", err),
						zap.String("request_id", rid),
						zap.String("tenant_id", tenant),
						zap.String("request", string(httpRequest)),
						zap.String("stack", string(debug.Stack())),
					)
				} else {
					logger.Error("[Recovery from panic]",
						zap.Any("error", err),
						zap.String("request_id", rid),
						zap.String("tenant_id", tenant),
						zap.String("request", string(httpRequest)),
					)
				}

				c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
					"error": gin.H{
						"code":    "internal",
						"message": fmt.Sprintf("internal server error: panic recovered [%s]", rid),
					},
				})
			}
		}()
		c.Next()
	}
}
