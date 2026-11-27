package abstract

import (
	"errors"
	"net/http"
)

// Contract names one document a client can ask for. The same client request
// always names exactly one, and every provider either speaks it already or is
// rendered into it.
type Contract string

const (
	ContractToolsSearch    Contract = "tools/search"
	ContractToolsFetch     Contract = "tools/fetch"
	ContractAlphaSearch    Contract = "alpha/search"
	ContractMoonshotSearch Contract = "moonshot/search"
	ContractMoonshotFetch  Contract = "moonshot/fetch"
)

// AllContracts lists every contract this package knows, in the order a new
// provider should be wired for them.
func AllContracts() []Contract {
	return []Contract{
		ContractToolsSearch,
		ContractToolsFetch,
		ContractAlphaSearch,
		ContractMoonshotSearch,
		ContractMoonshotFetch,
	}
}

// IsSearch reports whether a contract answers a search rather than a fetch, so
// a caller can pick the search or the fetch half of a provider.
func (c Contract) IsSearch() bool {
	switch c {
	case ContractToolsSearch, ContractAlphaSearch, ContractMoonshotSearch:
		return true
	default:
		return false
	}
}

// KnownFields records which members the provider published. A renderer needs
// the difference between "the upstream sent null" and "the value is false or
// zero", because the client reads a null as unknown and a zero as a value.
type KnownFields struct {
	Summary     bool
	Content     bool
	SiteName    bool
	SiteIcon    bool
	Date        bool
	Authority   bool
	RerankScore bool
}

// Result is one search hit after abstraction. Every upstream field the clients
// read has a place here; Known says which of them the upstream actually sent.
type Result struct {
	Title       string
	URL         string
	Snippet     string
	Summary     string
	Content     string
	SiteName    string
	SiteIcon    string
	Date        string
	Authority   int // -1 when the provider names none
	RerankScore float64
	Known       KnownFields
}

// Page is one fetched document: the title and the body already extracted into
// text.
type Page struct {
	Title   string
	Content string
}

// Search is one answer after abstraction. Raw is the upstream body exactly as
// it arrived and RawContract names the contract it already speaks; a renderer
// whose contract equals RawContract emits Raw instead of re-serializing, which
// is how a client keeps reading the bytes its own upstream sent.
type Search struct {
	Results     []Result
	Raw         []byte
	RawContract Contract
	StatusCode  int
	Header      http.Header
}

// Fetch mirrors Search for a fetched page.
type Fetch struct {
	Page        Page
	Raw         []byte
	RawContract Contract
	StatusCode  int
	Header      http.Header
}

var (
	// ErrEmptyResults is returned when a search completed without a hit. A
	// caller refunds the request rather than charging for nothing, which is the
	// same rule the DeepSeek API applies.
	ErrEmptyResults = errors.New("websearch: search returned no result")
	// ErrEmptyContent is returned when a fetch produced no body.
	ErrEmptyContent = errors.New("websearch: fetch returned no content")
	// ErrUnsupportedChannel marks a request no candidate can serve. The relay
	// loop may retry such a request on another channel.
	ErrUnsupportedChannel = errors.New("websearch: channel does not support this endpoint")
	// ErrUnsupportedContentType marks a fetch whose target answered with a body
	// this server cannot turn into text.
	ErrUnsupportedContentType = errors.New("websearch: unsupported content type")
)
