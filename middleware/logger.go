package middleware

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
)

const RouteTagKey = "route_tag"

func RouteTag(tag string) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Set(RouteTagKey, tag)
		c.Next()
	}
}

func SetUpLogger(server *gin.Engine) {
	server.Use(redactTaskArtifactAccessQuery())
	server.Use(gin.LoggerWithConfig(gin.LoggerConfig{
		Output: common.LogWriter,
		Formatter: func(param gin.LogFormatterParams) string {
			var requestID string
			if param.Keys != nil {
				requestID, _ = param.Keys[common.RequestIdKey].(string)
			}
			tag, _ := param.Keys[RouteTagKey].(string)
			if tag == "" {
				tag = "web"
			}
			path := param.Path
			// OAuth callbacks carry one-time codes and state in the query string.
			// Redact the log value only; the handler still needs the original query.
			if strings.HasPrefix(path, "/api/oauth/") || strings.HasPrefix(path, "/oauth/") {
				path, _, _ = strings.Cut(path, "?")
			} else {
				path = redactRequestTarget(param.Request)
			}
			return fmt.Sprintf("[GIN] %s | %s | %s | %3d | %13v | %15s | %7s %s\n",
				param.TimeStamp.Format("2006/01/02 - 15:04:05"),
				tag,
				requestID,
				param.StatusCode,
				param.Latency,
				param.ClientIP,
				param.Method,
				path,
			)
		},
	}))
}

// redactRequestTarget returns the request target a log line may write.
//
// The decision reads the decoded path: a "?" anywhere in it separates a query,
// whichever field of the URL it arrived in. gin hands the formatter a decoded
// path, so a path parameter holding an escaped "?" arrives with the query
// inside the path while RawQuery is empty; reading the escaped path for the
// decision would re-escape that "?" and still print the "=" after it.
//
// Two triggers drop the query whole instead of masking key by key:
//
//   - the path is a payment callback this tree serves (IsPaymentCallbackPath),
//     so a gateway that signs under a name the key list does not hold still
//     loses it, and a custom notify_url cannot move the callback off the rule
//     because the rule reads the route, not the query;
//   - the query carries a key named like a payment signature
//     (IsPaymentCallbackQueryText), which covers a signed redirect to a page
//     this tree does not route as a callback.
//
// Every other query keeps its readable values and masks the credential-looking
// ones, which is what the upstream debug URLs need.
func redactRequestTarget(request *http.Request) string {
	if request == nil || request.URL == nil {
		return ""
	}
	target := request.URL.Path
	if request.URL.RawQuery != "" {
		target += "?" + request.URL.RawQuery
	}
	path, query, hasQuery := strings.Cut(target, "?")
	if hasQuery && (relaycommon.IsPaymentCallbackPath(path) || relaycommon.IsPaymentCallbackQueryText(query)) {
		return path
	}
	if request.URL.RawQuery != "" {
		return request.URL.EscapedPath() + relaycommon.SanitizeURLForLog("?"+request.URL.Query().Encode())
	}
	if hasQuery {
		return path + relaycommon.SanitizeURLForLog("?"+query)
	}
	return target
}
