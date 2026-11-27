package abstract

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/websearch_setting"
)

const (
	antigravitySnippetMaxRune = 300
	antigravityTitleMaxRune   = 120
)

// antigravityParser reads the Gemini answer an Antigravity search produces. The
// grounding metadata is the citation list; the model's own answer is what the
// caller reads when the model chose not to search at all, because then it is the
// only answer the upstream produced.
type antigravityParser struct{}

func init() {
	RegisterParser(websearch_setting.ProviderAntigravity, antigravityParser{})
}

type antigravitySearchBody struct {
	Candidates []struct {
		GroundingMetadata *antigravityGroundingMetadata `json:"groundingMetadata"`
		Content           *antigravityContent           `json:"content"`
	} `json:"candidates"`
}

type antigravityContent struct {
	Parts []struct {
		Text string `json:"text"`
	} `json:"parts"`
}

type antigravityGroundingMetadata struct {
	GroundingChunks []struct {
		Web *struct {
			URI    string `json:"uri"`
			Title  string `json:"title"`
			Domain string `json:"domain"`
		} `json:"web"`
	} `json:"groundingChunks"`
	GroundingSupports []struct {
		Segment *struct {
			Text string `json:"text"`
		} `json:"segment"`
		GroundingChunkIndices []int `json:"groundingChunkIndices"`
	} `json:"groundingSupports"`
}

func (antigravityParser) ParseSearch(body []byte) ([]Result, error) {
	var payload antigravitySearchBody
	if err := common.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("websearch: decode antigravity search body: %w", err)
	}

	var metadata *antigravityGroundingMetadata
	answer := ""
	for _, candidate := range payload.Candidates {
		if metadata == nil && candidate.GroundingMetadata != nil {
			metadata = candidate.GroundingMetadata
		}
		if answer == "" && candidate.Content != nil {
			answer = antigravityAnswerText(candidate.Content)
		}
	}

	// The URL lines are what the request asks for, because a hit's title and
	// snippet are read off the page afterwards rather than repeated by the model.
	if raw, ok := parseAntigravityRawSearch(answer); ok {
		return antigravityRawResults(raw), nil
	}

	if metadata != nil && len(metadata.GroundingChunks) > 0 {
		if results := antigravityCitationResults(metadata); len(results) > 0 {
			return results, nil
		}
	}

	// The model answered from its own knowledge instead of searching. That answer
	// is what the upstream produced, so it is reported rather than dropped: an
	// empty list would tell the caller nothing was found when something was.
	if answer != "" {
		return []Result{antigravityAnswerResult(answer)}, nil
	}
	return nil, ErrEmptyResults
}

// ParseFetch refuses the call because Antigravity publishes no fetch endpoint;
// its channel fetches through the in-process extractor instead.
func (antigravityParser) ParseFetch([]byte) (Page, error) {
	return Page{}, ErrUnsupportedContentType
}

// antigravityRawResults turns the model's lines into results. The snippet stays
// empty on purpose: the model is not asked to repeat page prose, so the caller
// reads it from the page when it needs it.
func antigravityRawResults(raw []antigravityRawResult) []Result {
	results := make([]Result, 0, len(raw))
	for _, hit := range raw {
		host := antigravityHostOf(hit.URL)
		results = append(results, Result{
			Title:    hit.Title,
			URL:      hit.URL,
			Date:     hit.PublicationTime,
			SiteName: host,
			// The upstream names no per-page score, so the field keeps its
			// "unknown" value.
			Authority: -1,
			Known: KnownFields{
				Date:     hit.PublicationTime != "",
				SiteName: host != "",
			},
		})
	}
	return results
}

// antigravityAnswerText joins the text parts of one candidate, skipping the
// thought parts a thinking model emits alongside its answer.
func antigravityAnswerText(content *antigravityContent) string {
	parts := make([]string, 0, len(content.Parts))
	for _, part := range content.Parts {
		if text := strings.TrimSpace(part.Text); text != "" {
			parts = append(parts, text)
		}
	}
	return strings.TrimSpace(strings.Join(parts, "\n"))
}

// antigravityAnswerResult wraps the model's own answer as the single result.
// There is no cited page, so the URL stays empty: naming one would claim a page
// exists that the upstream never named.
func antigravityAnswerResult(answer string) Result {
	snippet := clipRunes(collapseAnswerWhitespace(answer), antigravitySnippetMaxRune)
	return Result{
		Title:     answerTitle(snippet, ""),
		Snippet:   snippet,
		Authority: -1,
		// No URL, no site name and no score: the upstream sent none of them.
	}
}

// antigravityCitationResults turns the grounding chunks into one result each,
// with the segment text that cited the chunk as its snippet.
func antigravityCitationResults(metadata *antigravityGroundingMetadata) []Result {
	// A segment cites one or more chunks, so its text has to be gathered per
	// chunk before the per-chunk snippet can be joined.
	snippets := make(map[int][]string, len(metadata.GroundingChunks))
	for _, support := range metadata.GroundingSupports {
		if support.Segment == nil {
			continue
		}
		text := strings.TrimSpace(support.Segment.Text)
		if text == "" {
			continue
		}
		for _, index := range support.GroundingChunkIndices {
			if index < 0 || index >= len(metadata.GroundingChunks) {
				continue
			}
			snippets[index] = append(snippets[index], text)
		}
	}

	results := make([]Result, 0, len(metadata.GroundingChunks))
	seen := make(map[string]struct{}, len(metadata.GroundingChunks))
	for index, chunk := range metadata.GroundingChunks {
		if chunk.Web == nil {
			continue
		}
		target := strings.TrimSpace(chunk.Web.URI)
		if target == "" {
			continue
		}
		if _, duplicated := seen[target]; duplicated {
			continue
		}
		seen[target] = struct{}{}

		siteName := antigravitySiteName(chunk.Web.Domain, chunk.Web.Title, target)
		snippet := clipRunes(joinAnswerSegments(snippets[index]), antigravitySnippetMaxRune)
		results = append(results, Result{
			Title:    answerTitle(snippet, siteName),
			URL:      target,
			Snippet:  snippet,
			SiteName: siteName,
			// The upstream names no per-page score, so the field keeps its
			// "unknown" value.
			Authority: -1,
			Known: KnownFields{
				SiteName: siteName != "",
				// Snippet has no flag in KnownFields, so the citation text is
				// published without one. The grounding document carries no
				// summary, body or score of its own, so those stay false.
			},
		})
	}
	return results
}

// antigravitySiteName prefers the domain the upstream names, then the title it
// sends, and falls back to the citation's own host. The upstream writes a bare
// host into both of the first two, never a page title.
func antigravitySiteName(domain, title, target string) string {
	if name := strings.TrimSpace(domain); name != "" {
		return name
	}
	if name := strings.TrimSpace(title); name != "" {
		return name
	}
	parsed, err := url.Parse(target)
	if err != nil {
		return ""
	}
	return parsed.Hostname()
}

// joinAnswerSegments glues the segments one page was cited for. Consecutive
// segments overlap, so a segment that repeats the tail of the text already kept
// contributes only what it adds; without this the first sentence appears twice
// and the title ends up being a copy of the snippet's prefix.
func joinAnswerSegments(segments []string) string {
	var joined strings.Builder
	for _, segment := range segments {
		text := strings.TrimSpace(segment)
		if text == "" {
			continue
		}
		previous := joined.String()
		switch {
		case previous == "":
			joined.WriteString(text)
		case strings.HasPrefix(text, previous):
			joined.Reset()
			joined.WriteString(text)
		case strings.HasSuffix(previous, text):
			// The whole segment is already covered by what came before.
		default:
			// A short overlap is the common case: keep only the new tail.
			joined.WriteString(" ")
			joined.WriteString(trimAnswerOverlap(previous, text))
		}
	}
	return joined.String()
}

// trimAnswerOverlap drops the longest prefix of next that the end of previous
// already contains.
func trimAnswerOverlap(previous, next string) string {
	limit := len(next)
	if len(previous) < limit {
		limit = len(previous)
	}
	for length := limit; length > 0; length-- {
		if strings.HasSuffix(previous, next[:length]) {
			return strings.TrimSpace(next[length:])
		}
	}
	return next
}

var answerEmphasisPattern = regexp.MustCompile("[*`_]+")

// answerTitle is the first sentence of a cited or self-answered text, with the
// markdown emphasis the upstream puts around names removed, because a caller
// shows the title as plain text. A page cited without a segment has no text to
// take a title from, so the site name stands in.
func answerTitle(snippet, fallback string) string {
	plain := answerEmphasisPattern.ReplaceAllString(snippet, "")
	if strings.TrimSpace(plain) == "" {
		return fallback
	}
	if stop := strings.IndexAny(plain, ".!?。！？"); stop > 0 && stop < antigravityTitleMaxRune {
		return strings.TrimSpace(plain[:stop])
	}
	return clipRunes(plain, antigravityTitleMaxRune)
}

// collapseAnswerWhitespace folds the runs a self-answered text arrives with.
func collapseAnswerWhitespace(text string) string {
	return strings.Join(strings.Fields(text), " ")
}

func clipRunes(text string, limit int) string {
	runes := []rune(strings.TrimSpace(text))
	if len(runes) <= limit {
		return string(runes)
	}
	return strings.TrimSpace(string(runes[:limit]))
}
