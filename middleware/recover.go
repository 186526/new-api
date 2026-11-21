package middleware

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"runtime/debug"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
)

func RelayPanicRecover() gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if err := recover(); err != nil {
				common.SysLog(common.LogText("panic detected: %v", err))
				common.SysLog(common.LogText("stacktrace from panic: %s", string(debug.Stack())))
				c.JSON(http.StatusInternalServerError, gin.H{
					"error": gin.H{
						"message": fmt.Sprintf("Panic detected, error: %v. Please submit a issue here: https://github.com/Calcium-Ion/new-api", err),
						"type":    "new_api_panic",
					},
				})
				c.Abort()
			}
		}()
		c.Next()
	}
}

// PanicRecover is the server-wide panic recovery. It does not use gin's
// CustomRecoveryWithWriter: that handler formats httputil.DumpRequest into its
// writer, and the request line it dumps keeps the query, so a payment callback
// that panicked wrote its signature to the log file. Only the Authorization
// header was masked, and a callback signs in a header or the query, not in
// Authorization.
//
// So this handler writes the panic, a redacted request target and the stack
// trace itself, through common.SysLog so log rotation still reaches the current
// file. Nothing it writes comes from a request dump, which is why no header and
// no query value can reach the log through this path. The shape of gin's
// handler is kept otherwise: a broken connection logs the panic without
// answering, and every other panic answers 500.
func PanicRecover() gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			recovered := recover()
			if recovered == nil {
				return
			}
			common.SysLog(common.LogText("panic detected: %v", recovered))
			common.SysLog(common.LogText("panic request method=%s target=%q client_ip=%s", c.Request.Method, redactRequestTarget(c.Request), c.ClientIP()))
			common.SysLog(common.LogText("stacktrace from panic: %s", string(debug.Stack())))
			if brokenConnection(recovered) {
				// The client is gone, so there is no answer to write; gin
				// records the failure on the context instead.
				var failure error
				if err, ok := recovered.(error); ok {
					failure = err
				} else {
					failure = fmt.Errorf("%v", recovered)
				}
				_ = c.Error(failure)
				c.Abort()
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{
				"error": gin.H{
					"message": fmt.Sprintf("Panic detected, error: %v. Please submit a issue here: https://github.com/Calcium-Ion/new-api", recovered),
					"type":    "new_api_panic",
				},
			})
			c.Abort()
		}()
		c.Next()
	}
}

// brokenConnection reports whether a panic value is a client that gave up
// waiting, which gin's recovery reads the same way.
func brokenConnection(recovered any) bool {
	operationError, ok := recovered.(*net.OpError)
	if !ok {
		return false
	}
	var syscallError *os.SyscallError
	if !errors.As(operationError, &syscallError) {
		return false
	}
	message := strings.ToLower(syscallError.Error())
	return strings.Contains(message, "broken pipe") ||
		strings.Contains(message, "connection reset by peer")
}
