package response

import (
	"fmt"
	"sync"

	"github.com/QuantumNous/new-api/service/websearch/abstract"
)

// Renderer turns one abstract answer into the document a client contract reads.
// It is keyed by the contract because the contract decides the member names and
// their order, not the provider that answered.
type Renderer interface {
	// Contract names the document this renderer writes.
	Contract() abstract.Contract
	// ContentType is the media type of the bytes RenderSearch or RenderFetch
	// returns, so the caller writes a header the client can act on.
	ContentType() string
	RenderSearch(abstract.Search) ([]byte, error)
	RenderFetch(abstract.Fetch) ([]byte, error)
}

var (
	rendererMu       sync.RWMutex
	rendererRegistry = make(map[abstract.Contract]Renderer)
)

// RegisterRenderer binds a contract to its renderer. It runs from an init in the
// renderer's own file, so a build that omits a contract also omits its renderer.
func RegisterRenderer(renderer Renderer) {
	rendererMu.Lock()
	defer rendererMu.Unlock()
	rendererRegistry[renderer.Contract()] = renderer
}

// For returns the renderer of the given contract, or nil when this build
// registers none. A nil result means the request named a contract this server
// cannot write, which the caller reports as a client error.
func For(contract abstract.Contract) Renderer {
	rendererMu.RLock()
	defer rendererMu.RUnlock()
	return rendererRegistry[contract]
}

// forwardNativeAnswer returns the upstream bytes when this renderer's contract
// is the one they already speak. Re-serializing them would reorder members and
// drop what the abstraction does not model, so the client that reached its own
// upstream must keep reading the exact body it used to read.
func forwardNativeAnswer(raw []byte, rawContract, own abstract.Contract) ([]byte, bool) {
	if rawContract == own && len(raw) > 0 {
		return raw, true
	}
	return nil, false
}

// wrongAnswerHalf reports a renderer asked for the half of a contract it does
// not serve, which means the caller paired the request with the wrong renderer.
func wrongAnswerHalf(contract abstract.Contract, asked string) error {
	return fmt.Errorf("websearch: contract %s does not carry a %s answer", contract, asked)
}
