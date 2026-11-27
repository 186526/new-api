package request

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/service/websearch/abstract"
	"github.com/QuantumNous/new-api/setting/websearch_setting"

	"golang.org/x/net/html"
)

const (
	localFetchTimeout    = 20 * time.Second
	localFetchBodyLimit  = 200 << 10 // 200 KB
	localExtractMaxChars = 400_000
)

// droppedElements carry navigation, scripting or chrome rather than the body the
// caller asked for, so they never reach the extracted text.
var droppedElements = map[string]bool{
	"script":   true,
	"style":    true,
	"noscript": true,
	"nav":      true,
	"header":   true,
	"footer":   true,
	"aside":    true,
	"form":     true,
	"iframe":   true,
	"svg":      true,
	"template": true,
}

var whitespaceRun = regexp.MustCompile(`[ \t\f\v]+`)

var blankLineRun = regexp.MustCompile(`\n{3,}`)

// localRequester reads a page in-process. It serves fetches only: it has no
// upstream to ask for a search, so Search refuses instead of faking one.
//
// It caches nothing of its own. The cache that every provider shares lives in
// Forward, which needs an *http.Request to key on, and this requester never
// builds one; the caller above it (websearch.go) is where a locally fetched page
// is cached, together with every other provider's answer.
type localRequester struct {
	// client overrides the SSRF-protected client, which a test needs because the
	// package-level one is built at startup and is nil before InitHttpClient.
	client *http.Client
}

func init() {
	Register(localRequester{})
}

func (localRequester) Name() string { return websearch_setting.ProviderLocal }

// Native reports nothing: a local fetch answers no client document itself, the
// local parser lifts the body out of what this requester extracted.
func (localRequester) Native() []abstract.Contract { return nil }

// Search refuses because the in-process fetcher has no search half.
func (localRequester) Search(context.Context, abstract.ChannelContext, Query) (Upstream, error) {
	return Upstream{}, notSent(abstract.ErrUnsupportedChannel)
}

// Fetch reads the target itself and returns the extracted text as its body, so
// the local parser can hand it to the caller as a Page.Content.
func (r localRequester) Fetch(ctx context.Context, _ abstract.ChannelContext, query Query) (Upstream, error) {
	text, err := r.extract(ctx, query.URL)
	if err != nil {
		return Upstream{}, err
	}
	return Upstream{
		Family:     websearch_setting.ProviderLocal,
		StatusCode: http.StatusOK,
		Body:       []byte(text),
	}, nil
}

func (r localRequester) httpClient() *http.Client {
	if r.client != nil {
		return r.client
	}
	// The target is arbitrary, client-supplied input, so the dialer must refuse
	// loopback and private addresses; without this a fetch would reach whatever
	// this server can.
	if client := service.GetSSRFProtectedHTTPClient(); client != nil {
		return client
	}
	return &http.Client{Timeout: localFetchTimeout}
}

func (r localRequester) extract(ctx context.Context, target string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(target))
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return "", notSent(fmt.Errorf("websearch: invalid fetch url: %q", target))
	}

	callCtx, cancel := context.WithTimeout(ctx, localFetchTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(callCtx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return "", notSent(err)
	}
	req.Header.Set("Accept", "text/markdown, text/html;q=0.9")
	req.Header.Set("User-Agent", "new-api websearch/1.0")

	resp, err := r.httpClient().Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("websearch: fetch %s answered %d", parsed.String(), resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, localFetchBodyLimit))
	if err != nil {
		return "", err
	}

	switch mediaType := common.MediaTypeOf(resp.Header.Get("Content-Type")); {
	case mediaType == "text/html" || mediaType == "application/xhtml+xml" || mediaType == "":
		return extractLocalHTML(body, parsed)
	case mediaType == "text/plain" || mediaType == "text/markdown":
		text := capLocalText(string(body))
		if strings.TrimSpace(text) == "" {
			return "", abstract.ErrEmptyContent
		}
		return text, nil
	default:
		return "", fmt.Errorf("%w: %s", abstract.ErrUnsupportedContentType, mediaType)
	}
}

// extractLocalHTML turns a page into markdown. The extractor is deliberately
// small: it drops the elements that never hold an article body, picks the
// densest container the page marks as one, and renders the text of what remains.
func extractLocalHTML(body []byte, pageURL *url.URL) (string, error) {
	doc, err := html.Parse(bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("websearch: parse html: %w", err)
	}

	root := findLocalContentRoot(doc)
	if root == nil {
		return "", abstract.ErrEmptyContent
	}

	var out strings.Builder
	renderLocalNode(&out, root, pageURL)
	content := capLocalText(normalizeLocalMarkdown(out.String()))
	if content == "" {
		return "", abstract.ErrEmptyContent
	}
	return content, nil
}

// findLocalContentRoot prefers the semantic containers a page marks itself, and
// falls back to the div holding the most text, which is where most pages that
// mark nothing put the article.
func findLocalContentRoot(doc *html.Node) *html.Node {
	for _, tag := range []string{"article", "main"} {
		if node := findFirstLocalNode(doc, tag); node != nil {
			return node
		}
	}
	if node := findLocalRoleMain(doc); node != nil {
		return node
	}

	var best *html.Node
	bestDensity := 0
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if node.Type == html.ElementNode && node.Data == "div" {
			if density := localContentDensity(node); density > bestDensity {
				best, bestDensity = node, density
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(doc)
	if best != nil && bestDensity > 0 {
		return best
	}

	if node := findFirstLocalNode(doc, "body"); node != nil {
		return node
	}
	return doc
}

func findLocalRoleMain(doc *html.Node) *html.Node {
	var found *html.Node
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if found != nil {
			return
		}
		if node.Type == html.ElementNode {
			for _, attr := range node.Attr {
				if attr.Key == "role" && strings.EqualFold(strings.TrimSpace(attr.Val), "main") {
					found = node
					return
				}
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(doc)
	return found
}

// localContentDensity counts the characters a container contributes, ignoring
// the elements that never carry the body. Link text counts for less because a
// list of links is what a navigation block looks like.
func localContentDensity(node *html.Node) int {
	density := 0
	var walk func(*html.Node, bool)
	walk = func(current *html.Node, inLink bool) {
		if current.Type == html.ElementNode && droppedElements[current.Data] {
			return
		}
		link := inLink || current.Data == "a"
		if current.Type == html.TextNode {
			length := len(strings.TrimSpace(current.Data))
			if link {
				length /= 2
			}
			density += length
		}
		for child := current.FirstChild; child != nil; child = child.NextSibling {
			walk(child, link)
		}
	}
	walk(node, false)
	return density
}

func renderLocalNode(out *strings.Builder, node *html.Node, base *url.URL) {
	walkLocalMarkdown(out, node, base)
}

// walkLocalMarkdown writes one node and its children as markdown. Block elements
// end with a blank line so the next block starts on its own paragraph.
func walkLocalMarkdown(out *strings.Builder, node *html.Node, base *url.URL) {
	if node.Type == html.ElementNode && droppedElements[node.Data] {
		return
	}

	switch node.Type {
	case html.TextNode:
		out.WriteString(collapseLocalText(node.Data))
		return
	case html.CommentNode, html.DoctypeNode:
		return
	}

	writeChildNodes := func() {
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walkLocalMarkdown(out, child, base)
		}
	}

	switch node.Data {
	case "h1", "h2", "h3", "h4", "h5", "h6":
		level := int(node.Data[1] - '0')
		ensureLocalBlankLine(out)
		out.WriteString(strings.Repeat("#", level) + " ")
		out.WriteString(strings.TrimSpace(localNodeText(node)))
		ensureLocalBlankLine(out)
	case "p":
		ensureLocalBlankLine(out)
		writeChildNodes()
		ensureLocalBlankLine(out)
	case "br":
		out.WriteString("\n")
	case "hr":
		ensureLocalBlankLine(out)
		out.WriteString("---")
		ensureLocalBlankLine(out)
	case "li":
		out.WriteString("\n- ")
		writeChildNodes()
	case "ul", "ol", "menu":
		ensureLocalBlankLine(out)
		writeChildNodes()
		ensureLocalBlankLine(out)
	case "blockquote":
		ensureLocalBlankLine(out)
		var inner strings.Builder
		writeLocalChildren(&inner, node, base)
		for _, line := range strings.Split(strings.TrimSpace(inner.String()), "\n") {
			out.WriteString("> " + line + "\n")
		}
		ensureLocalBlankLine(out)
	case "pre":
		ensureLocalBlankLine(out)
		out.WriteString("```\n")
		out.WriteString(strings.TrimRight(localNodeText(node), "\n"))
		out.WriteString("\n```")
		ensureLocalBlankLine(out)
	case "a":
		href := localAttrValue(node, "href")
		label := strings.TrimSpace(localNodeText(node))
		switch {
		case label == "":
			return
		case href == "" || strings.HasPrefix(href, "#") || strings.HasPrefix(href, "javascript:"):
			out.WriteString(label)
		default:
			out.WriteString("[" + label + "](" + resolveLocalHref(href, base) + ")")
		}
	case "img":
		alt := strings.TrimSpace(localAttrValue(node, "alt"))
		if alt == "" {
			return
		}
		out.WriteString("![")
		out.WriteString(alt)
		out.WriteString("]")
	default:
		writeChildNodes()
	}
}

func writeLocalChildren(out *strings.Builder, node *html.Node, base *url.URL) {
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		walkLocalMarkdown(out, child, base)
	}
}

// ensureLocalBlankLine keeps blocks one blank line apart without stacking
// separators.
func ensureLocalBlankLine(out *strings.Builder) {
	text := out.String()
	if text == "" {
		return
	}
	if !strings.HasSuffix(text, "\n\n") {
		if strings.HasSuffix(text, "\n") {
			out.WriteString("\n")
		} else {
			out.WriteString("\n\n")
		}
	}
}

func findFirstLocalNode(node *html.Node, tag string) *html.Node {
	if node.Type == html.ElementNode && node.Data == tag {
		return node
	}
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		if found := findFirstLocalNode(child, tag); found != nil {
			return found
		}
	}
	return nil
}

// localNodeText joins the text of a subtree, skipping the elements the extractor
// drops so a heading never reads a script body.
func localNodeText(node *html.Node) string {
	if node == nil {
		return ""
	}
	var out strings.Builder
	var walk func(*html.Node)
	walk = func(current *html.Node) {
		if current.Type == html.ElementNode && droppedElements[current.Data] {
			return
		}
		if current.Type == html.TextNode {
			out.WriteString(collapseLocalText(current.Data))
		}
		for child := current.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(node)
	return collapseLocalSpace(out.String())
}

func localAttrValue(node *html.Node, key string) string {
	for _, attr := range node.Attr {
		if attr.Key == key {
			return attr.Val
		}
	}
	return ""
}

func resolveLocalHref(href string, base *url.URL) string {
	parsed, err := url.Parse(href)
	if err != nil || parsed.IsAbs() || base == nil {
		return href
	}
	return base.ResolveReference(parsed).String()
}

// collapseLocalText keeps inline whitespace meaningful but folds runs of spaces,
// which is what HTML itself does with them.
func collapseLocalText(text string) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	text = strings.ReplaceAll(text, "\n", " ")
	return whitespaceRun.ReplaceAllString(text, " ")
}

func collapseLocalSpace(text string) string {
	return strings.Join(strings.Fields(text), " ")
}

func normalizeLocalMarkdown(text string) string {
	lines := strings.Split(text, "\n")
	for index, line := range lines {
		lines[index] = strings.TrimRight(line, " ")
	}
	joined := strings.Join(lines, "\n")
	joined = blankLineRun.ReplaceAllString(joined, "\n\n")
	return strings.TrimSpace(joined)
}

// capLocalText bounds one page so a huge document cannot fill the caller's
// context.
func capLocalText(text string) string {
	if len(text) <= localExtractMaxChars {
		return text
	}
	return text[:localExtractMaxChars]
}
