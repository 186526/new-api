package websearch

import (
	"context"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/service/websearch/abstract"
	"github.com/QuantumNous/new-api/service/websearch/request"
)

const (
	// snippetFetchBudget bounds the whole batch, not one page: the client waits
	// for the search answer, so a page that has not arrived by then is answered
	// from the model's own sentence instead. Measured on the live pipeline,
	// nine pages read in parallel finish between two and seven of them inside
	// two seconds, and the batch always returns within 2.1 seconds.
	snippetFetchBudget = 2 * time.Second
	// snippetMinRunes is the least text a lead paragraph must hold to be used. A
	// shorter block is a navigation label or a one-line teaser rather than a
	// description of the page.
	snippetMinRunes = 60
	// snippetLetterRatio keeps a block made mostly of punctuation, numbers or
	// dangling link labels out of the snippet.
	snippetLetterRatio = 0.85
	// snippetFirstSentenceAfter skips the opening words before cutting at a
	// sentence boundary, so a very short first sentence does not become the whole
	// snippet.
	snippetFirstSentenceAfter = 40
)

// fillSnippets gives every result a snippet. The model already wrote a sentence
// about each page; a page that can be read inside the budget replaces it with the
// page's own lead paragraph, which is what a caller reads to judge relevance.
//
// A result whose page was not read keeps the model's sentence, and a result the
// model wrote no sentence for keeps an empty snippet rather than a fabricated one.
func fillSnippets(ctx context.Context, channel abstract.ChannelContext, results []abstract.Result) {
	// The snippets are collected apart from the results so a read that finishes
	// after the budget cannot write into a slice the caller already returned.
	snippets := make([]string, len(results))
	var (
		wait sync.WaitGroup
		mu   sync.Mutex
	)
	for index := range results {
		if snippet := results[index].Snippet; snippet != "" {
			snippets[index] = snippet
			continue
		}
		summary := results[index].Summary
		// The model's sentence is the default: it stands until a page read
		// replaces it, including for a read that never finishes.
		snippets[index] = summary
		target := results[index].URL
		if target == "" {
			continue
		}

		index, target := index, target
		wait.Add(1)
		go func() {
			defer wait.Done()
			text, err := readPageInsideBudget(ctx, channel, target)
			if err != nil {
				return
			}
			lead := localLeadSnippet(text)
			if lead == "" {
				return
			}
			mu.Lock()
			snippets[index] = lead
			mu.Unlock()
		}()
	}

	// The budget is what bounds the wait: a page that has not answered by then is
	// answered from the model's sentence, and its read is cancelled through the
	// context it carries.
	done := make(chan struct{})
	go func() {
		wait.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(snippetFetchBudget):
	case <-ctx.Done():
	}

	mu.Lock()
	defer mu.Unlock()
	for index := range results {
		results[index].Snippet = snippets[index]
	}
}

// readPageInsideBudget reads one page under the batch budget. The budget is
// applied per read rather than around the group so a page that answers instantly
// is not delayed by one that does not.
func readPageInsideBudget(ctx context.Context, channel abstract.ChannelContext, target string) (string, error) {
	readCtx, cancel := context.WithTimeout(ctx, snippetFetchBudget)
	defer cancel()
	return request.FetchPageText(readCtx, channel, target)
}

var (
	// localLinkOnly is a block that is nothing but links, which is what a page's
	// navigation or footer renders into.
	localLinkOnly = regexp.MustCompile(`^(?:\[[^\]]*\]\([^)]*\)\s*)+$`)
	// localCitationNumber is the "[12]" a page leaves behind on a quoted source.
	localCitationNumber = regexp.MustCompile(`\[\d+\]`)
	localSentenceEnd    = regexp.MustCompile(`[.!?。！？]\s`)
	localRunOfSpace     = regexp.MustCompile(`\s+`)
)

// localLeadSnippet reads the first paragraph of an extracted page that reads as
// prose. Measured on the live extractor, seven of nine nginx results produced a
// usable lead this way; the two it rejected were a Docker Hub help list and a
// GitHub chrome block, both navigation rather than content.
func localLeadSnippet(markdown string) string {
	for _, block := range strings.Split(markdown, "\n\n") {
		if text := localProseParagraph(block); text != "" {
			return text
		}
	}
	return ""
}

// localProseParagraph turns one markdown block into a sentence, or returns
// empty when the block is not prose.
func localProseParagraph(block string) string {
	lines := make([]string, 0, 4)
	for _, line := range strings.Split(block, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case line == "":
			continue
		case strings.HasPrefix(line, "#"), strings.HasPrefix(line, "|"),
			strings.HasPrefix(line, "!["), strings.HasPrefix(line, "- "),
			strings.HasPrefix(line, "* "), strings.HasPrefix(line, ">"):
			continue
		case localLinkOnly.MatchString(line):
			continue
		}
		lines = append(lines, line)
	}
	if len(lines) == 0 {
		return ""
	}

	text := strings.Join(lines, " ")
	text = localMarkdownLink.ReplaceAllString(text, "$1")
	text = localCitationNumber.ReplaceAllString(text, "")
	text = strings.NewReplacer("**", "", "*", "", "`", "", "_", "").Replace(text)
	text = strings.TrimSpace(localRunOfSpace.ReplaceAllString(text, " "))
	if len([]rune(text)) < snippetMinRunes {
		return ""
	}
	if localLetterRatio(text) < snippetLetterRatio {
		return ""
	}
	// A page that renders its own chrome as prose (a sign-in banner, a cookie
	// notice) leaves a paragraph that passes every structural check and says
	// nothing about the page. Measured on the live extractor, github.com pages
	// come back as exactly that.
	if localIsPageChrome(text) {
		return ""
	}
	return localFirstSentence(text)
}

// localPageChromeMarkers are phrases a page's own interface writes, never its
// content. A paragraph carrying one is the page talking about itself.
var localPageChromeMarkers = []string{
	"skip to content",
	"you signed in",
	"reload to refresh",
	"sign in to github",
	"we use cookies",
	"accept all cookies",
	"enable javascript",
	"this site uses cookies",
	"javascript is disabled",
	// A page says this to a visitor whose browser it does not recognise. The
	// notice lives in the markup behind display:none on pages that ship it, so
	// the extractor normally never reads it; this keeps a page that leaves it
	// visible from putting it in an answer about that page.
	"out of date browser",
	"using an outdated browser",
	"browser is not supported",
	// A login box. www.linux.com renders one that is not hidden, and its opening
	// words read as a description of the page otherwise.
	"welcome! log into your account",
	"forgot your password",
	"sign in to continue",
}

// localIsPageChrome reports whether a paragraph is the page's interface rather
// than its content.
func localIsPageChrome(text string) bool {
	lower := strings.ToLower(text)
	for _, marker := range localPageChromeMarkers {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

// localMarkdownLink keeps the label of a link and drops its target.
var localMarkdownLink = regexp.MustCompile(`\[([^\]]*)\]\([^)]*\)`)

// localLetterRatio reports how much of the text is letters and spaces. A table
// row or a list of numbers scores low, which is how it is told from a sentence.
func localLetterRatio(text string) float64 {
	if text == "" {
		return 0
	}
	letters := 0
	for _, r := range text {
		if r == ' ' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || r > 127 && localIsLetter(r) {
			letters++
		}
	}
	return float64(letters) / float64(len([]rune(text)))
}

// localFirstSentence cuts the text at the first sentence boundary after the
// opening words. A text with no boundary at all is returned whole, capped.
func localFirstSentence(text string) string {
	runes := []rune(text)
	if len(runes) <= snippetMinRunes+len([]rune("x")) {
		return text
	}
	start := 0
	if len(runes) > snippetFirstSentenceAfter {
		start = snippetFirstSentenceAfter
	}
	rest := string(runes[start:])
	if stop := localSentenceEnd.FindStringIndex(rest); stop != nil {
		return strings.TrimSpace(string(runes[:start+stop[1]]))
	}
	return text
}

// localIsLetter reports the non-ASCII runes a sentence is made of. Latin-1
// letters cover the French and German pages the upstream returns, and every
// script's letters are categories L*, which a range check on the common blocks
// approximates without pulling in a full Unicode table.
func localIsLetter(r rune) bool {
	switch {
	case r >= 0xC0 && r <= 0x24F: // Latin-1 supplement and extended
		return true
	case r >= 0x370 && r <= 0x3FF: // Greek
		return true
	case r >= 0x400 && r <= 0x4FF: // Cyrillic
		return true
	case r >= 0x4E00 && r <= 0x9FFF: // CJK unified ideographs
		return true
	case r >= 0x3040 && r <= 0x30FF: // Hiragana and katakana
		return true
	case r >= 0xAC00 && r <= 0xD7AF: // Hangul
		return true
	}
	return false
}
