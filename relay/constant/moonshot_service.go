package constant

import "strings"

// The Moonshot Service namespace a Kimi Code client points
// [services.moonshot_search] and [services.moonshot_fetch] at. The client
// appends /search and /fetch to the base URL it is given, so both paths live
// under one prefix.
const (
	MoonshotServicePathPrefix = "/moonshot.service/v1"
	MoonshotSearchPath        = MoonshotServicePathPrefix + "/search"
	MoonshotFetchPath         = MoonshotServicePathPrefix + "/fetch"
)

// IsMoonshotServicePath reports whether the path belongs to the Moonshot
// Service namespace. It is matched by prefix so a trailing segment still
// routes to the namespace's handlers.
func IsMoonshotServicePath(path string) bool {
	return strings.HasPrefix(path, MoonshotServicePathPrefix)
}
