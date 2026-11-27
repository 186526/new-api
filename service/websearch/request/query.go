package request

import (
	"context"
	"net/http"

	"github.com/QuantumNous/new-api/service/websearch/abstract"
)

// Query is one request on its way to a provider.
type Query struct {
	Contract abstract.Contract
	// Text and URL are what a provider that does not speak Contract reads. A
	// native provider ignores them and forwards Raw instead.
	Text        string
	URL         string
	Count       int
	NeedContent bool
	// Model is the mapped upstream model used only to rebuild a request.
	Model string
	// Raw is the client's own body. A provider whose Native() covers Contract
	// forwards it verbatim; any other provider ignores it.
	Raw []byte
	// Endpoint and Prepare are injected by the relay layer: only a channel's
	// adaptor knows the URL it routes to and the auth it signs with. Without
	// them, only providers with a standalone endpoint can send the request.
	Endpoint string
	Prepare  func(*http.Request) error
}

// Upstream is one raw answer. Family names the parser that reads Body; it is the
// requester's Name(), not the contract, because the wire shape is the provider's
// own.
type Upstream struct {
	Family     string
	StatusCode int
	Header     http.Header
	Body       []byte
	Cached     bool
}

// Requester sends one contract to one provider. A requester that has no upstream
// for a contract returns an error whose WasSent is false, so the caller may try
// the next candidate without risking a double charge.
type Requester interface {
	Name() string
	// Native lists the contracts this requester's upstream publishes itself.
	Native() []abstract.Contract
	Search(ctx context.Context, channel abstract.ChannelContext, query Query) (Upstream, error)
	Fetch(ctx context.Context, channel abstract.ChannelContext, query Query) (Upstream, error)
}
