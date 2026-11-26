package common

import (
	"strings"

	relayconstant "github.com/QuantumNous/new-api/relay/constant"
)

// SystemOneUpstreamRequestPath answers the path to forward a System One request
// to. A request that arrived under the contract prefix is sent under the path
// the upstream serves, with its query string kept; the legacy alias and every
// other path are returned unchanged.
func SystemOneUpstreamRequestPath(requestURLPath string) string {
	if !strings.HasPrefix(requestURLPath, relayconstant.SystemOneContractPathPrefix) {
		return requestURLPath
	}
	if index := strings.IndexByte(requestURLPath, '?'); index >= 0 {
		return relayconstant.SystemOneUpstreamPath + requestURLPath[index:]
	}
	return relayconstant.SystemOneUpstreamPath
}
