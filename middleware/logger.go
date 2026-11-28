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
			if protectedPath, ok := param.Keys[requestRedactionLogPath].(string); ok {
				path = protectedPath
			} else if strings.HasPrefix(path, "/api/oauth/") || strings.HasPrefix(path, "/oauth/") {
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
// Both triggers run twice: once on the target as gin hands it over, and once on
// the target split at the "=" of a query that arrived escaped inside a path
// parameter and was decoded into the path, where there is no "?" to cut at and
// the route is no longer the last segment.
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
	// An escaped query in a path parameter is decoded into the path, so neither
	// guard above sees it: there is no "?" to cut the target at, and the route
	// segment is no longer the last one. Judge the decoded form instead, so the
	// route still reads as the callback and the rest still reads as the query.
	if routePath, _, found := splitQueryDecodedIntoPath(path); found {
		return routePath
	}
	if request.URL.RawQuery != "" {
		return request.URL.EscapedPath() + relaycommon.SanitizeURLForLog("?"+request.URL.Query().Encode())
	}
	if hasQuery {
		return path + relaycommon.SanitizeURLForLog("?"+query)
	}
	return target
}

// paymentSplitSegmentLimit caps how many "="-bearing segments the decoded-path
// split tests before it falls back. The guard runs in the gin formatter for
// every request, so its cost is chosen by an unauthenticated client, and a cap
// keeps a crafted path from turning the walk into a long candidate list. A real
// callback URL has a handful of segments, so the limit only binds on a crafted
// path. Reaching it returns the first candidate instead of the path: that route
// half ends before the first "="-bearing segment, so it holds no literal "=" and
// cannot print a value that arrived after one.
const paymentSplitSegmentLimit = 64

// splitQueryDecodedIntoPath splits a path that carries a decoded query in one of
// its segments: "sign%3DSECRET%26pid%3DID" reaches the log as
// "sign=SECRET&pid=ID", so the endpoint sits before the "=" and the query text
// starts at the beginning of the segment that holds it. Splitting there keeps
// the key name in the query text, so the caller judges both halves with the same
// payment predicates it applies to a real query instead of growing a second key
// list.
//
// Every "="-bearing segment is tried, not only the first: an earlier segment may
// carry a decoded "=" of its own, and splitting there would leave a route half
// without the callback segment while the query half starts with that earlier
// name, so neither predicate would fire. The first split either predicate
// accepts is the one returned. The segments are walked forward once, and each
// candidate hands the key-name predicate only its own segment, so the per
// candidate cost is bounded by that segment's length: a long "=" segment costs
// one pass, and an "&"-dense one costs its own part count rather than the
// remainder's.
//
// At most paymentSplitSegmentLimit segments are tested; past that the first
// candidate is returned (see that constant for why). A double-encoded "="
// ("%253D") stays a known limit: it decodes once to "%3D", so the path holds no
// literal "=", found is false, and the value keeps its percent-encoding. It is
// not plaintext in the log, but it is recoverable by decoding the line. found is
// false when no segment is accepted, and path is then returned unchanged with an
// empty query half.
func splitQueryDecodedIntoPath(path string) (routePath, queryText string, found bool) {
	var firstCandidateRoutePath string
	tested := 0
	for segmentStart := 0; segmentStart < len(path); {
		segmentEnd := strings.IndexByte(path[segmentStart:], '/')
		if segmentEnd < 0 {
			segmentEnd = len(path) - segmentStart
		}
		if strings.IndexByte(path[segmentStart:segmentStart+segmentEnd], '=') >= 0 {
			// The segment holding this "=" is where that decoded query starts. The
			// key name sits in that segment, so the query text keeps it.
			routePath = strings.TrimSuffix(path[:segmentStart], "/")
			if routePath == "" {
				// The query took the whole first segment, so no route precedes it. "/"
				// says the request reached no route, which is what the log should show.
				routePath = "/"
			}
			// The decoded query starts at this segment, and its key name is in the
			// same segment because the key precedes the "=". Bounding the predicate
			// input here keeps the per-candidate cost proportional to this segment
			// instead of the whole remainder: "&" separates parts only inside a
			// segment, so nothing the predicate needs lies past it.
			queryText = path[segmentStart : segmentStart+segmentEnd]
			if tested == 0 {
				firstCandidateRoutePath = routePath
			}
			if relaycommon.IsPaymentCallbackPath(routePath) || relaycommon.IsPaymentCallbackQueryText(queryText) {
				return routePath, queryText, true
			}
			tested++
			if tested == paymentSplitSegmentLimit {
				return firstCandidateRoutePath, "", true
			}
		}
		segmentStart += segmentEnd + 1
	}
	return path, "", false
}
