package abstract

import (
	"fmt"
	"sort"
	"sync"
)

// Parser turns one provider's upstream body into the abstract objects. It is
// keyed by the provider family the requester reports, because the family
// decides the wire shape, not the contract the client asked for.
type Parser interface {
	// ParseSearch turns one search body into results.
	ParseSearch(body []byte) ([]Result, error)
	// ParseFetch turns one fetch body into a page.
	ParseFetch(body []byte) (Page, error)
}

var (
	parserMu       sync.RWMutex
	parserRegistry = make(map[string]Parser)
)

// RegisterParser binds a provider family to its parser. It runs from an init in
// the family's own file, so a build that omits a family also omits its parser.
func RegisterParser(family string, parser Parser) {
	parserMu.Lock()
	defer parserMu.Unlock()
	parserRegistry[family] = parser
}

// ParserFor returns the parser of the given provider family.
func ParserFor(family string) (Parser, error) {
	parserMu.RLock()
	defer parserMu.RUnlock()
	parser, ok := parserRegistry[family]
	if !ok {
		return nil, fmt.Errorf("websearch: no parser for provider family %q", family)
	}
	return parser, nil
}

// sortCandidates orders candidates by the given less function. It is a thin
// wrapper so the call site reads as an ordering rule rather than as a sort
// implementation.
func sortCandidates(candidates []Candidate, less func(a, b Candidate) bool) {
	sort.SliceStable(candidates, func(i, j int) bool {
		return less(candidates[i], candidates[j])
	})
}
