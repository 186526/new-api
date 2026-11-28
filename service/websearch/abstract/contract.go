package abstract

import (
	"errors"
	"net/http"

	"github.com/QuantumNous/new-api/pkg/toolcatalog"
)

// Contract names the response format the client requested.
type Contract = toolcatalog.Contract

const (
	ContractToolsSearch    Contract = toolcatalog.ToolsSearch
	ContractToolsFetch     Contract = toolcatalog.ToolsFetch
	ContractAlphaSearch    Contract = toolcatalog.AlphaSearch
	ContractMoonshotSearch Contract = toolcatalog.MoonshotSearch
	ContractMoonshotFetch  Contract = toolcatalog.MoonshotFetch
)

func AllContracts() []Contract {
	return toolcatalog.Contracts()
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
