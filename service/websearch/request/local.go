package request

import (
	"bytes"
	"context"
	"errors"
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
	localFetchTimeout = 20 * time.Second
	// localFetchBodyLimit bounds one download. At 200 KB the limit cut the page
	// before its body: github.com/186526/new-api is 443 KB and
	// kubernetes.io/docs/concepts/overview 508 KB, so both extracted to nothing,
	// while the same bytes parsed locally yielded 15.7 KB and 10.8 KB.
	localFetchBodyLimit  = 2 << 20 // 2 MB
	localExtractMaxChars = 400_000
	// localExtractMinChars is the least text a chosen container must hold. Below
	// it the container is a headline or a teaser rather than the page, and the
	// whole body is rendered instead.
	localExtractMinChars = 400
	// localHeadingMinChars is the least text a heading must hold to count as one.
	// A page ships empty headings in nav wrappers for its own layout.
	localHeadingMinChars = 2
)

// droppedElements carry navigation, scripting or chrome rather than the body the
// caller asked for, so they never reach the extracted text.
//
// header and footer are the page's own banner only when they sit outside an
// article: an article's header carries its title and byline, and dropping it
// left a listing page with no text at all.
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
	"dialog":   true,
	"menu":     true,
	"button":   true,
}

// localChromeNameParts are the words a class or id uses to name a block that
// holds something other than the article. The list is Readability.js's negative
// class list, which readeck's go-readability carries as
// internal/re2go/class-weight.re; keeping the same words means a page that
// library drops is dropped here too.
//
// A part matches only between separators (-, _, space, or the ends of the
// token), so "ad" matches "ad-slot" and "sidebar-ad" but not "author" or "load".
var localChromeNameParts = []string{
	"ad", "banner", "combx", "comment", "com", "contact", "footer", "gdpr",
	"masthead", "meta", "outbrain", "promo", "related", "share", "shoutbox",
	"sidebar", "skyscraper", "sponsor", "shopping", "tags", "widget",
	"nav", "menu", "breadcrumb", "pagination", "cookie", "consent", "newsletter",
	"subscribe", "popup", "modal", "toolbar", "skip", "search", "social",
	"copyright", "recommend",
}

// localChromeExactNames are names that must match the whole token. They are too
// short or too common to match as a part without hitting content: "hid" is
// inside "hidden" but also inside nothing useful, and "com" would match
// "comment" twice.
var localChromeExactNames = map[string]bool{
	"hid": true, "hidden": true, "d-none": true,
}

// localPositiveNameParts are the words that name an article body. Readability.js
// scores them +25 against the negative list's -25, which is what keeps a page
// that wraps its article in a div named "content" from being pruned.
var localPositiveNameParts = []string{
	"article", "body", "content", "entry", "hentry", "main", "page", "post",
	"text", "blog", "story",
}

// localNameTokens splits a class or id into the tokens a name check reads. An
// attribute value holds several classes separated by spaces, and a class itself
// uses - and _ between words.
func localNameTokens(value string) []string {
	var tokens []string
	for _, field := range strings.Fields(strings.ToLower(value)) {
		for _, token := range strings.FieldsFunc(field, func(r rune) bool {
			return r == '-' || r == '_'
		}) {
			if token != "" {
				tokens = append(tokens, token)
			}
		}
	}
	return tokens
}

// localChromeWeight scores a node's class and id the way Readability.js does:
// -25 for a negative name, +25 for a positive one, summed over both attributes.
func localChromeWeight(node *html.Node) int {
	weight := 0
	for _, attr := range node.Attr {
		if attr.Key != "class" && attr.Key != "id" {
			continue
		}
		for _, token := range localNameTokens(attr.Val) {
			if localChromeExactNames[token] {
				weight -= 25
				continue
			}
			for _, part := range localChromeNameParts {
				if token == part {
					weight -= 25
					break
				}
			}
			for _, part := range localPositiveNameParts {
				if token == part || strings.HasPrefix(token, part) {
					weight += 25
					break
				}
			}
		}
	}
	return weight
}

// localDroppedElement reports whether a node is chrome rather than body text.
// A header or footer inside an article is the article's own, so it is kept: the
// page-level one is the banner that localInsideArticle rejects.
func localDroppedElement(node *html.Node) bool {
	if !droppedElements[node.Data] {
		return false
	}
	if node.Data == "header" || node.Data == "footer" {
		return !localInsideArticle(node)
	}
	return true
}

// localChromeNamed reports whether a node's class or id names a block that holds
// something other than the article. The name is read as whole words: the parts
// are compared to tokens, so "author" and "load" do not match "ad".
func localChromeNamed(node *html.Node) bool {
	return localChromeWeight(node) < 0
}

// pruneLocalChrome removes the blocks Readability.js removes in
// cleanConditionally: a container whose text is mostly links is a navigation
// list, and a container that names itself chrome without one positive name is
// chrome. A block holding most of the page is kept regardless, because a page
// may wrap its whole article in a div whose name says otherwise.
func pruneLocalChrome(root *html.Node) {
	total := localContentDensity(root)
	if total <= 0 {
		return
	}
	var drop []*html.Node
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			if child.Type == html.ElementNode && localPrunableChrome(child, total) {
				drop = append(drop, child)
				continue
			}
			walk(child)
		}
	}
	walk(root)
	// The nodes are collected before any is detached: removing during the walk
	// would clear the sibling pointer the loop is about to read.
	for _, node := range drop {
		if node.Parent != nil {
			node.Parent.RemoveChild(node)
		}
	}
}

// localPrunableChrome reports whether a node is a chrome block to remove.
//
// Readability.js decides this while it already holds the article candidate, so a
// rule as broad as "mostly links" is safe there. Pruning the whole page instead
// hits a listing page, whose every entry is a title link with no prose around it,
// and every bare <a>. Three conditions keep those: the node must be a container,
// it must hold no heading (a listing entry has its title as one), and it must be
// small next to the page. What is left is navigation, breadcrumbs, a link footer
// and a sticky sub-nav, which is what a reader-facing extractor should drop.
func localPrunableChrome(node *html.Node, pageDensity int) bool {
	if !localChromeContainer(node) {
		return false
	}
	// The page's data lives in its tables: a table of releases or of prices is
	// named by the page as a widget and is still the content. A block inside a
	// code sample is part of the listing, not chrome.
	if localInsideDataTable(node) || localInsideCode(node) {
		return false
	}
	// A block that holds most of the page is the page: a wrapper named "widget"
	// around the whole article must not empty the answer.
	if localContentDensity(node)*10 >= pageDensity {
		return false
	}
	if localHoldsHeading(node) {
		return false
	}
	linkDensity := localLinkDensity(node)
	// A block the page names as chrome and that is mostly links is dropped.
	if localChromeWeight(node) < 0 && linkDensity > 0.5 {
		return true
	}
	// A block that is almost nothing but links is a navigation strip even when
	// the page gave it no telling name: linux.org's secondary section links are
	// div.p-sectionLinks-inner, which scores zero against both word lists.
	return linkDensity > 0.85
}

// localChromeContainer reports whether a node is a block that can hold a
// navigation list, a share bar or a comment section. An inline element is never
// one, so a bare <a> is not pruned for being all link text.
func localChromeContainer(node *html.Node) bool {
	switch node.Data {
	case "div", "section", "aside", "ul", "ol", "menu", "header", "footer",
		"form", "widget", "table":
		return true
	}
	return false
}

// localLinkDensity is the share of a block's text that sits inside links.
func localLinkDensity(node *html.Node) float64 {
	total, linked := localTextAndLinkDensity(node, false)
	if total == 0 {
		return 0
	}
	return float64(linked) / float64(total)
}

func localTextAndLinkDensity(node *html.Node, inLink bool) (int, int) {
	total, linked := 0, 0
	var walk func(*html.Node, bool)
	walk = func(current *html.Node, linkedNow bool) {
		if current.Type == html.ElementNode && localDroppedElement(current) {
			return
		}
		if localNodeHidden(current) {
			return
		}
		link := linkedNow || current.Data == "a"
		if current.Type == html.TextNode {
			length := len(strings.TrimSpace(current.Data))
			total += length
			if link {
				linked += length
			}
		}
		for child := current.FirstChild; child != nil; child = child.NextSibling {
			walk(child, link)
		}
	}
	walk(node, inLink)
	return total, linked
}

// localInsideDataTable reports whether a node sits within a table that holds data
// rather than layout.
func localInsideDataTable(node *html.Node) bool {
	for parent := node.Parent; parent != nil; parent = parent.Parent {
		if parent.Type == html.ElementNode && parent.Data == "table" {
			return true
		}
	}
	return false
}

// localInsideCode reports whether a node sits within a code sample, which is
// three levels up at most: a fenced listing nests a div or two.
func localInsideCode(node *html.Node) bool {
	depth := 0
	for parent := node.Parent; parent != nil && depth < 3; parent = parent.Parent {
		if parent.Type == html.ElementNode && (parent.Data == "code" || parent.Data == "pre") {
			return true
		}
		depth++
	}
	return false
}

// localInsideArticle reports whether a node sits within an <article>. A listing
// page puts each entry's title and date in that entry's header, so the element
// is content there rather than the site banner.
func localInsideArticle(node *html.Node) bool {
	for parent := node.Parent; parent != nil; parent = parent.Parent {
		if parent.Type == html.ElementNode && parent.Data == "article" {
			return true
		}
	}
	return false
}

var whitespaceRun = regexp.MustCompile(`[ \t\f\v]+`)

var blankLineRun = regexp.MustCompile(`\n{3,}`)

// localFetchStatusError reports a target that answered with a non-2xx status.
// It is a typed value so the relay layer can answer with that status instead of
// folding a bad URL and an unreachable host into the same gateway error.
type localFetchStatusError struct {
	URL        string
	StatusCode int
}

func (e localFetchStatusError) Error() string {
	return fmt.Sprintf("websearch: fetch %s answered %d", e.URL, e.StatusCode)
}

// FetchAnswerStatus reports the status a fetched target answered with, which a
// caller relays to its own client so a missing page is not reported as a gateway
// fault. It reports false for every other failure, including a host that could
// not be reached.
func FetchAnswerStatus(err error) (int, bool) {
	var answered localFetchStatusError
	if errors.As(err, &answered) {
		return answered.StatusCode, true
	}
	return 0, false
}

// localNodeHidden reports whether a page keeps this subtree out of sight. A page
// ships its own text for readers who cannot see the rendered result: a browser
// upgrade notice, a login dialog and a cookie bar all sit in the markup with
// display:none until a script shows them. Reading them as content puts a
// warning about the visitor's browser into an answer about the page.
func localNodeHidden(node *html.Node) bool {
	if node.Type != html.ElementNode {
		return false
	}
	for _, attr := range node.Attr {
		switch attr.Key {
		case "hidden":
			return true
		case "aria-hidden":
			if strings.EqualFold(strings.TrimSpace(attr.Val), "true") {
				return true
			}
		case "style":
			// A style attribute holds declarations, so the value is compared with
			// its spaces and case removed rather than matched as written.
			normalized := strings.ToLower(strings.NewReplacer(" ", "", "\t", "", "\n", "").Replace(attr.Val))
			// visibility:hidden reads the same way to a visitor as display:none:
			// the subtree keeps its space on the page but nothing in it is shown.
			if strings.Contains(normalized, "display:none") || strings.Contains(normalized, "visibility:hidden") {
				return true
			}
		}
	}
	return false
}

// localRequester reads a page in-process. It serves fetches only: it has no
// upstream to ask for a search, so Search refuses instead of faking one.
//
// It caches nothing of its own: a locally fetched page goes through the same
// cache and concurrency merge as every other provider's answer, which lives in
// Forward, keyed on the request this requester builds in Fetch.
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
//
// The page goes through the same cache and concurrency merge as an upstream
// answer. This requester builds no *http.Request that Forward could key on, so
// it keys on the provider and the URL instead; without this every repeated
// fetch re-read the page.
func (r localRequester) Fetch(ctx context.Context, channel abstract.ChannelContext, query Query) (Upstream, error) {
	key := CacheKeyFor(websearch_setting.ProviderLocal, strings.TrimSpace(query.URL))
	entry, cached, err := forwardThrough(key, CacheTTL(false, channel.Settings.WebSearchFetchCacheTTLSeconds), func() (forwardEntry, error) {
		text, err := r.extract(ctx, query.URL)
		if err != nil {
			return forwardEntry{}, err
		}
		return forwardEntry{
			StatusCode:  http.StatusOK,
			ContentType: "text/markdown; charset=utf-8",
			Body:        []byte(text),
		}, nil
	})
	if err != nil {
		return Upstream{}, err
	}
	return Upstream{
		Family:     websearch_setting.ProviderLocal,
		StatusCode: entry.StatusCode,
		Body:       entry.Body,
		Cached:     cached,
	}, nil
}

// FetchPageText reads one page for a caller that is not a fetch contract, which
// today is the search path filling result snippets. It goes through Fetch rather
// than extract so the page shares the fetch cache: a snippet request and a later
// client fetch of the same page read one entry.
func FetchPageText(ctx context.Context, channel abstract.ChannelContext, target string) (string, error) {
	upstream, err := localRequester{}.Fetch(ctx, channel, Query{
		Contract: abstract.ContractMoonshotFetch,
		URL:      target,
	})
	if err != nil {
		return "", err
	}
	return string(upstream.Body), nil
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
		// The status is carried as a typed value rather than folded into the
		// message: a caller tells "this page does not exist" from "this host
		// cannot be reached" only by the status, and both used to read as one
		// gateway error.
		return "", localFetchStatusError{URL: parsed.String(), StatusCode: resp.StatusCode}
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
// densest container the page marks as one, removes the named chrome blocks
// inside it, and renders the text of what remains.
func extractLocalHTML(body []byte, pageURL *url.URL) (string, error) {
	doc, err := html.Parse(bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("websearch: parse html: %w", err)
	}

	root := findLocalContentRoot(doc)
	if root == nil {
		return "", abstract.ErrEmptyContent
	}
	// Chrome is pruned only inside a container the page marked as its article.
	// The fallback is the whole body, where a listing page's own entries look
	// like navigation and pruning them would drop the answer.
	if root.Data != "body" && root.Data != "html" {
		pruneLocalChrome(root)
	}

	var out strings.Builder
	renderLocalNode(&out, root, pageURL)
	content := capLocalText(renderLocalMarkdown(out.String()))
	if content == "" {
		return "", abstract.ErrEmptyContent
	}
	return content, nil
}

// findLocalContentRoot picks the container holding the most text. A page marks
// several: www.debian.org has six <article> elements, all of them one-line news
// headings, so taking the first one returns a headline instead of the page.
//
// The body is what a page whose containers all come up short falls back to, so
// it is never a candidate here: its density always exceeds any child's, and
// choosing it by density would make the fallback unreachable.
func findLocalContentRoot(doc *html.Node) *html.Node {
	var best *html.Node
	bestDensity := 0
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if node.Type == html.ElementNode && localContentContainer(node) {
			if density := localContentDensity(node); density > bestDensity {
				best, bestDensity = node, density
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(doc)

	if best != nil && bestDensity >= localExtractMinChars {
		return best
	}
	if node := findFirstLocalNode(doc, "body"); node != nil {
		return node
	}
	return doc
}

// localContentContainer reports whether a node is one of the containers a page
// uses to mark its article. A div is included because most pages that mark
// nothing still wrap the article in one, and the density threshold below is what
// rejects a div that only holds navigation.
func localContentContainer(node *html.Node) bool {
	switch node.Data {
	case "article", "main", "div":
		return true
	}
	for _, attr := range node.Attr {
		if attr.Key == "role" && strings.EqualFold(strings.TrimSpace(attr.Val), "main") {
			return true
		}
	}
	return false
}

// localContentDensity counts the characters a container contributes, ignoring
// the elements that never carry the body. Link text counts for less because a
// list of links is what a navigation block looks like.
func localContentDensity(node *html.Node) int {
	density := 0
	var walk func(*html.Node, bool)
	walk = func(current *html.Node, inLink bool) {
		if current.Type == html.ElementNode && localDroppedElement(current) {
			return
		}
		if localNodeHidden(current) {
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
	walkLocalMarkdown(out, node, base, "")
}

// walkLocalMarkdown writes one node and its children as markdown. Block elements
// end with a blank line so the next block starts on its own paragraph.
//
// link carries the target of an anchor the subtree sits in. A listing entry links
// its whole card, so the anchor wraps a heading instead of holding a phrase; the
// blocks then render as blocks and each one keeps the link.
func walkLocalMarkdown(out *strings.Builder, node *html.Node, base *url.URL, link string) {
	if node.Type == html.ElementNode && localDroppedElement(node) {
		return
	}
	if localNodeHidden(node) {
		return
	}

	switch node.Type {
	case html.TextNode:
		out.WriteString(localLinkText(collapseLocalText(node.Data), link))
		return
	case html.CommentNode, html.DoctypeNode:
		return
	}

	writeChildNodes := func() {
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walkLocalMarkdown(out, child, base, link)
		}
	}
	writeChildNodesWithLink := func(target string) {
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walkLocalMarkdown(out, child, base, target)
		}
	}

	switch node.Data {
	case "h1", "h2", "h3", "h4", "h5", "h6":
		level := int(node.Data[1] - '0')
		ensureLocalBlankLine(out)
		out.WriteString(strings.Repeat("#", level) + " ")
		out.WriteString(localLinkText(strings.TrimSpace(localNodeText(node)), link))
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
		writeLocalListItem(out, node, base, link)
	case "ul", "ol", "menu":
		ensureLocalBlankLine(out)
		writeChildNodes()
		ensureLocalBlankLine(out)
	case "blockquote":
		ensureLocalBlankLine(out)
		var inner strings.Builder
		writeLocalChildren(&inner, node, base, link)
		for _, line := range strings.Split(strings.TrimSpace(inner.String()), "\n") {
			out.WriteString("> " + line + "\n")
		}
		ensureLocalBlankLine(out)
	case "pre":
		ensureLocalBlankLine(out)
		out.WriteString("```\n")
		// The text is taken verbatim: localNodeText folds whitespace, which put a
		// REPL transcript's every line on one line. The indentation and the line
		// breaks are the code.
		out.WriteString(strings.Trim(localRawText(node), "\n"))
		out.WriteString("\n```")
		ensureLocalBlankLine(out)
	case "a":
		href := localAttrValue(node, "href")
		target := link
		if resolved := resolveLocalHref(href, base); href != "" && !strings.HasPrefix(href, "#") && !strings.HasPrefix(href, "javascript:") {
			target = resolved
		}
		label := strings.TrimSpace(localNodeText(node))
		switch {
		case label == "":
			return
		// A listing entry links its whole card, so the anchor holds a heading and
		// a date rather than a phrase. Writing that as one link label glued the
		// title and the date together; the blocks are rendered as blocks instead,
		// and each block keeps the link.
		case localHoldsBlockElement(node):
			writeChildNodesWithLink(target)
		case target == "":
			out.WriteString(label)
		default:
			out.WriteString(localLinkText(label, target))
		}
	case "img":
		alt := strings.TrimSpace(localAttrValue(node, "alt"))
		if alt == "" {
			return
		}
		out.WriteString("![")
		out.WriteString(alt)
		out.WriteString("]")
	case "table":
		ensureLocalBlankLine(out)
		// A table is a layout device on many pages, not a data grid. Rendering the
		// whole of news.ycombinator.com as one turned its front page into a single
		// `| --- |` row holding every story, so a layout table is walked as blocks.
		if localIsLayoutTable(node) {
			writeChildNodes()
		} else {
			writeLocalTable(out, node)
		}
		ensureLocalBlankLine(out)
	case "thead", "tbody", "tfoot", "caption", "colgroup", "col":
		// A data table writes its own rows and never walks into these; reaching
		// this branch means the table is a layout table, where they are only
		// groupings to descend through. Swallowing them emptied news.ycombinator.com,
		// whose whole page sits inside one tbody.
		writeChildNodes()
	case "tr", "td", "th":
		// A row or cell reaches this branch only inside a layout table, where each
		// one is a block of the page rather than a column of a grid.
		ensureLocalBlankLine(out)
		writeChildNodes()
		ensureLocalBlankLine(out)
	case "div", "section", "article", "main", "header", "footer", "aside",
		"figure", "figcaption", "dl", "dt", "dd", "details", "summary", "address",
		"fieldset", "legend", "nav":
		// A container separates what it holds into blocks. Without this a listing
		// entry rendered as one line: "标题 10月 27, 2026 阅读时长: 3 分钟分类".
		ensureLocalBlankLine(out)
		writeChildNodes()
		ensureLocalBlankLine(out)
	default:
		writeChildNodes()
	}
}

func writeLocalChildren(out *strings.Builder, node *html.Node, base *url.URL, link string) {
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		walkLocalMarkdown(out, child, base, link)
	}
}

// localLinkText writes text as a markdown link when it has a target. Text that
// is only whitespace is written as is: an anchor around a card holds the spaces
// between its children, and linking those produced an empty "[](...)".
func localLinkText(text, target string) string {
	label := strings.TrimSpace(text)
	if label == "" {
		// A text node holding only whitespace is the separation between two
		// inline elements; dropping it glued "10月 27, 2026" to "阅读时长".
		return " "
	}
	if target == "" {
		// Text outside a link keeps the spaces HTML puts between inline nodes.
		return text
	}
	// The spaces around a label are kept: two inline nodes that HTML separates
	// with whitespace would otherwise be written as "Nov11".
	leading := ""
	if strings.HasPrefix(text, " ") {
		leading = " "
	}
	trailing := ""
	if strings.HasSuffix(text, " ") {
		trailing = " "
	}
	return leading + "[" + label + "](" + target + ")" + trailing
}

// writeLocalListItem writes one list item as a bullet. The content is rendered
// into a scratch buffer first: a list item commonly wraps its text in a
// paragraph, and writing "- " before the children let the paragraph's own blank
// line push the text onto the next one, which markdown reads as an empty bullet
// followed by a paragraph.
func writeLocalListItem(out *strings.Builder, node *html.Node, base *url.URL, link string) {
	var inner strings.Builder
	writeLocalChildren(&inner, node, base, link)
	text := strings.TrimSpace(inner.String())
	if text == "" {
		return
	}
	ensureLocalBlankLine(out)
	for index, line := range strings.Split(text, "\n") {
		if index == 0 {
			out.WriteString("- " + strings.TrimSpace(line) + "\n")
			continue
		}
		out.WriteString(strings.TrimRight(line, " \t") + "\n")
	}
}

// localIsLayoutTable reports whether a table lays the page out rather than
// holding data.
//
// A grid balances its columns, so no single cell of a row holds most of the
// row's text. news.ycombinator.com nests a three-column table per story whose
// cells are a rank digit, an empty vote arrow and the title: judging by column
// count alone made every story a `| --- |` row. A table nesting another table is
// a layout wrapper too, since the outer one exists to place the inner one.
func localIsLayoutTable(node *html.Node) bool {
	// A table nested in a table is a wrapper placing the inner one, and a heading
	// row marks a real grid whatever its cells hold.
	if localHoldsElement(node, "th") {
		return false
	}
	rows := localTableRows(node)
	if len(rows) == 0 {
		return true
	}
	width := 0
	for _, row := range rows {
		if len(row) > width {
			width = len(row)
		}
	}
	// One column is never a grid: the table exists to stack blocks, which is how
	// news.ycombinator.com wraps its whole page.
	if width <= 1 {
		return true
	}
	for _, row := range rows {
		if localRowIsLayout(row) {
			return true
		}
	}
	return false
}

// localRowIsLayout reports a row whose text sits in one cell while the rest stay
// near-empty, which is a column of the page rather than one line of a grid.
func localRowIsLayout(row []string) bool {
	if len(row) <= 1 {
		return false
	}
	total, largest := 0, 0
	for _, cell := range row {
		size := len(strings.TrimSpace(cell))
		total += size
		if size > largest {
			largest = size
		}
	}
	// A row holding one short cell and one long one is a label and a value, which
	// is still a grid; only a row that empties every cell but one is a layout.
	nonEmpty := 0
	for _, cell := range row {
		if len(strings.TrimSpace(cell)) > 0 {
			nonEmpty++
		}
	}
	return nonEmpty <= 1 || largest*10 >= total*8
}

// localHoldsElement reports whether a subtree contains an element of the tag.
func localHoldsElement(node *html.Node, tag string) bool {
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		if child.Type == html.ElementNode {
			if child.Data == tag {
				return true
			}
			if localHoldsElement(child, tag) {
				return true
			}
		}
	}
	return false
}

// localRawText joins the text of a subtree unchanged, which is what a code block
// needs: localNodeText folds every newline and run of spaces into one space.
func localRawText(node *html.Node) string {
	var out strings.Builder
	var walk func(*html.Node)
	walk = func(current *html.Node) {
		if current.Type == html.ElementNode && localDroppedElement(current) {
			return
		}
		if localNodeHidden(current) {
			return
		}
		switch current.Type {
		case html.TextNode:
			out.WriteString(current.Data)
		case html.ElementNode:
			switch current.Data {
			case "br":
				out.WriteString("\n")
			case "p", "div", "li", "tr":
				out.WriteString("\n")
			}
		}
		for child := current.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(node)
	return out.String()
}

// writeLocalTable renders a table as markdown rows. www.kernel.org puts its
// whole release list inside one, and the default branch joined every cell into a
// single line, which lost both the columns and the row boundaries.
func writeLocalTable(out *strings.Builder, node *html.Node) {
	rows := localTableRows(node)
	if len(rows) == 0 {
		return
	}
	width := 0
	for _, row := range rows {
		if len(row) > width {
			width = len(row)
		}
	}
	for index, row := range rows {
		out.WriteString("|")
		for column := 0; column < width; column++ {
			cell := ""
			if column < len(row) {
				cell = row[column]
			}
			out.WriteString(" " + cell + " |")
		}
		out.WriteString("\n")
		// The separator after the first row is what makes a reader treat the
		// block as a table rather than as a paragraph of pipes.
		if index == 0 {
			out.WriteString("|")
			for column := 0; column < width; column++ {
				out.WriteString(" --- |")
			}
			out.WriteString("\n")
		}
	}
}

// localTableRows collects the cells of each row in document order, descending
// through the grouping elements a table may wrap its rows in.
func localTableRows(node *html.Node) [][]string {
	var rows [][]string
	var walk func(*html.Node)
	walk = func(current *html.Node) {
		for child := current.FirstChild; child != nil; child = child.NextSibling {
			if child.Type != html.ElementNode {
				continue
			}
			switch child.Data {
			case "tr":
				rows = append(rows, localTableCells(child))
			case "thead", "tbody", "tfoot":
				walk(child)
			}
		}
	}
	walk(node)
	return rows
}

func localTableCells(row *html.Node) []string {
	var cells []string
	for child := row.FirstChild; child != nil; child = child.NextSibling {
		if child.Type != html.ElementNode || (child.Data != "td" && child.Data != "th") {
			continue
		}
		cells = append(cells, strings.ReplaceAll(localNodeText(child), "|", "\\|"))
	}
	return cells
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
		if current.Type == html.ElementNode && localDroppedElement(current) {
			return
		}
		if localNodeHidden(current) {
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

// renderLocalMarkdown turns the renderer's raw output into markdown a reader
// shows as structure rather than as text. Three things made the raw output read
// badly: a line kept the indentation it had in the source, which markdown turns
// into a code block at four columns; runs of spaces between two nodes stayed in
// the line; and an empty block left three or more newlines. Fenced code blocks
// are passed through untouched, since their indentation is the code.
func renderLocalMarkdown(text string) string {
	lines := strings.Split(text, "\n")
	out := make([]string, 0, len(lines))
	inFence := false
	blanks := 0
	for _, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			inFence = !inFence
			out = append(out, strings.TrimRight(line, " \t"))
			blanks = 0
			continue
		}
		if inFence {
			out = append(out, strings.TrimRight(line, " \t"))
			continue
		}
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			blanks++
			if blanks > 1 {
				continue
			}
			out = append(out, "")
			continue
		}
		blanks = 0
		out = append(out, whitespaceRun.ReplaceAllString(trimmed, " "))
	}
	for len(out) > 0 && out[0] == "" {
		out = out[1:]
	}
	for len(out) > 0 && out[len(out)-1] == "" {
		out = out[:len(out)-1]
	}
	return strings.Join(out, "\n")
}

// capLocalText bounds one page so a huge document cannot fill the caller's
// context.
func capLocalText(text string) string {
	if len(text) <= localExtractMaxChars {
		return text
	}
	return text[:localExtractMaxChars]
}

// localHoldsHeading reports whether a node holds a heading of its own that says
// something. A heading is what a listing entry is: its title. Navigation, a
// share bar and a comment list have none — a page may ship an empty heading in a
// nav wrapper for its own layout, and that must not save the nav from being
// pruned.
func localHoldsHeading(node *html.Node) bool {
	if node.Type == html.ElementNode && len(node.Data) == 2 && node.Data[0] == 'h' &&
		node.Data[1] >= '1' && node.Data[1] <= '6' {
		return len(strings.TrimSpace(localNodeText(node))) >= localHeadingMinChars
	}
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		if localHoldsHeading(child) {
			return true
		}
	}
	return false
}

// localHoldsBlockElement reports whether a node holds an element that starts a
// block. An anchor holding one is a card around an entry rather than a phrase
// inside a sentence, so its children are rendered as blocks.
func localHoldsBlockElement(node *html.Node) bool {
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		if child.Type != html.ElementNode {
			continue
		}
		switch child.Data {
		case "div", "section", "article", "header", "footer", "p",
			"h1", "h2", "h3", "h4", "h5", "h6", "ul", "ol", "li", "table", "pre", "figure":
			return true
		}
	}
	return false
}
