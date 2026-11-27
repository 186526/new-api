package abstract

import (
	"regexp"
	"strings"

	"github.com/QuantumNous/new-api/setting/websearch_setting"
)

// localParser fronts the in-process fetcher. There is no local search, and the
// fetcher already produces the Page it fetched, so this parser only has to lift
// the title out of the body the caller passes back.
type localParser struct{}

func init() {
	RegisterParser(websearch_setting.ProviderLocal, localParser{})
}

// localTitlePattern reads the first <title> element. The fetcher's HTML pass
// already knows the title; this pattern exists so a caller that hands back the
// raw body still gets one.
var localTitlePattern = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)

// ParseSearch refuses the call because the in-process fetcher has no search
// half.
func (localParser) ParseSearch([]byte) ([]Result, error) {
	return nil, ErrUnsupportedChannel
}

func (localParser) ParseFetch(body []byte) (Page, error) {
	content := string(body)
	return Page{Title: localTitle(content), Content: content}, nil
}

func localTitle(content string) string {
	match := localTitlePattern.FindStringSubmatch(content)
	if len(match) < 2 {
		return ""
	}
	return strings.Join(strings.Fields(match[1]), " ")
}
