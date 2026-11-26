package constant

// The TypeSafe System One contract is served under its own path prefix, so the
// shapes it requires never reach the shared OpenAI endpoints.
const (
	// SystemOneContractPathPrefix is the namespace a TypeSafe client points its
	// base_url at: /systemone/v1/models and /systemone/v1/systemone.
	SystemOneContractPathPrefix = "/systemone/v1"
	// SystemOneUpstreamPath is the path the TypeSafe upstream answers. Both the
	// contract prefix and the legacy alias reach it, because the upstream serves
	// no other.
	SystemOneUpstreamPath = "/v1/systemone"
)
