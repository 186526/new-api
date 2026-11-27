package request

import (
	"fmt"
	"sync"
)

var (
	requesterMu sync.RWMutex
	requesters  = make(map[string]Requester)
)

// Register binds a provider name to its requester. It runs from an init in the
// provider's own file, so a build that omits a provider also omits its client.
func Register(requester Requester) {
	requesterMu.Lock()
	defer requesterMu.Unlock()
	requesters[requester.Name()] = requester
}

// For returns the requester of the given provider.
func For(provider string) (Requester, error) {
	requesterMu.RLock()
	defer requesterMu.RUnlock()
	requester, ok := requesters[provider]
	if !ok {
		return nil, fmt.Errorf("websearch: no requester for provider %q", provider)
	}
	return requester, nil
}
