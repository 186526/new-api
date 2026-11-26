package constant

import "strings"

// IsSystemOnePath reports whether a request path belongs to the System One
// contract, the legacy alias included.
func IsSystemOnePath(path string) bool {
	return strings.HasPrefix(path, SystemOneContractPathPrefix) || strings.HasPrefix(path, SystemOneUpstreamPath)
}
