package request

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/pkg/cachex"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/service/websearch/abstract"
	"github.com/QuantumNous/new-api/setting/websearch_setting"

	"github.com/google/uuid"
	"github.com/samber/hot"
)

const (
	antigravityGenerateContentPath = "/v1internal:generateContent"
	antigravityDefaultSearchModel  = "gemini-3.5-flash-lite"
	antigravityEnvelopeUserAgent   = "antigravity"
	antigravityRequestTypeSearch   = "web_search"

	// antigravityRedirectLimit bounds how many redirect links are resolved at
	// once, so one search with many citations cannot open one connection per
	// citation against the grounding service.
	antigravityRedirectLimit = 8

	redirectCacheNamespace = "websearch:redirect:v1"
	redirectCacheCapacity  = 5000
	// defaultRedirectCacheTTLSeconds mirrors the setting's own default, so a
	// zero setting keeps the built-in lifetime rather than making the cache
	// permanent.
	defaultRedirectCacheTTLSeconds = 86400
)

func init() {
	Register(&antigravityRequester{})
}

// antigravityRequester sends one search through the Antigravity generateContent
// endpoint. It publishes no contract of its own: the grounding document is the
// answer, and every client-facing shape is rendered from it afterwards.
type antigravityRequester struct{}

func (*antigravityRequester) Name() string { return websearch_setting.ProviderAntigravity }

func (*antigravityRequester) Native() []abstract.Contract { return nil }

func (r *antigravityRequester) Search(ctx context.Context, channel abstract.ChannelContext, query Query) (Upstream, error) {
	// The credential is fetched before Forward because only its refresh knows the
	// project and the access token the request must carry; a failure here never
	// touched the search endpoint, so another candidate may still be tried.
	credential, err := service.EnsureAntigravityCredential(
		ctx,
		channel.ChannelID,
		channel.Key,
		channel.BaseURL,
		channel.Settings,
	)
	if err != nil {
		return Upstream{}, notSent(fmt.Errorf("websearch: antigravity credential: %w", err))
	}

	model := antigravitySearchModel(channel)
	instruction := antigravitySearchInstruction(query.Text)
	payload, err := antigravityEnvelope(model, credential.ProjectID, instruction)
	if err != nil {
		return Upstream{}, notSent(err)
	}

	endpoint := strings.TrimRight(credential.BaseURL, "/") + antigravityGenerateContentPath
	build := func(requestCtx context.Context) (*http.Request, error) {
		req, err := http.NewRequestWithContext(requestCtx, http.MethodPost, endpoint, bytes.NewReader(payload))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+credential.AccessToken)
		req.Header.Set("User-Agent", antigravityEnvelopeUserAgent)
		return req, nil
	}

	// Forward is the one place the cache, the concurrent-request merge and the
	// 408/429/5xx retry live, so a search is cached and deduplicated like every
	// other provider's. The envelope carries a random requestId, so the cache key
	// is the model and the instruction rather than the body.
	upstream, err := Forward(ctx, channel, build, ForwardOptions{
		CacheKey:        model + "\x00" + instruction,
		Search:          true,
		CacheTTLSeconds: channel.Settings.WebSearchCacheTTLSeconds,
	})
	if err != nil {
		return Upstream{}, err
	}
	upstream.Family = r.Name()

	// A refusal is the upstream's own document, which the renderer hands to the
	// client unread; only a success is unwrapped from the envelope.
	if upstream.StatusCode < 200 || upstream.StatusCode >= 300 {
		return upstream, nil
	}
	unwrapped, err := unwrapAntigravityResponse(upstream.Body)
	if err != nil {
		return Upstream{}, sentError{err}
	}
	upstream.Body = unwrapped
	return upstream, nil
}

// Fetch refuses the call because Antigravity publishes no fetch endpoint; its
// channel fetches through the in-process extractor instead.
func (*antigravityRequester) Fetch(context.Context, abstract.ChannelContext, Query) (Upstream, error) {
	return Upstream{}, notSent(abstract.ErrUnsupportedChannel)
}

// antigravitySearchModel resolves the model a search is sent to: the channel
// setting, then the global setting, then the upstream default.
func antigravitySearchModel(channel abstract.ChannelContext) string {
	if model := strings.TrimSpace(channel.Settings.AntigravityWebSearchModel); model != "" {
		return model
	}
	if model := websearch_setting.AntigravityModel(); model != "" {
		return model
	}
	return antigravityDefaultSearchModel
}

// antigravitySearchInstruction turns a query into the text the model is asked.
// The endpoint has no search action: it is generateContent with a googleSearch
// tool, and whether anything is searched is the model's own decision. A bare
// keyword such as "linux" gets answered from the model's own knowledge, which
// leaves no groundingMetadata and therefore no result.
//
// The instruction asks for the URLs and dates rather than for an answer or for
// the tool's whole output. Asking for the answer returns a summary the caller
// cannot use as results; asking for the whole output runs into the upstream
// copyright filter, which answers finishReason=RECITATION with no body for
// queries whose pages it recognises. URLs and dates are not prose, so they pass
// the filter, and they are what a caller cannot recompute. Measured on the live
// upstream: six out of six queries answered, nine URLs each, every one finishing
// with STOP.
func antigravitySearchInstruction(query string) string {
	return "Call the googleSearch tool with query \"" + query +
		"\". Then output one line per result: URL | publication time | page title. No other text."
}

// antigravityEnvelope builds the Google request body the Antigravity endpoint
// expects. The service package keeps its own envelope helper unexported and this
// call carries a different requestType, so the shape is written here.
func antigravityEnvelope(model, projectID, query string) ([]byte, error) {
	envelope := map[string]any{
		"model":       model,
		"userAgent":   antigravityEnvelopeUserAgent,
		"requestType": antigravityRequestTypeSearch,
		"requestId":   "agent-" + uuid.NewString(),
		"request": map[string]any{
			"contents": []any{
				map[string]any{
					"role":  "user",
					"parts": []any{map[string]any{"text": query}},
				},
			},
			"tools": []any{map[string]any{"googleSearch": map[string]any{}}},
		},
	}
	if projectID != "" {
		envelope["project"] = projectID
	}
	return common.Marshal(envelope)
}

// unwrapAntigravityResponse accepts both shapes the upstream answers with: the
// bare Gemini response and the envelope that wraps it under "response".
func unwrapAntigravityResponse(raw []byte) ([]byte, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return nil, errors.New("websearch: antigravity answered an empty body")
	}
	var envelope struct {
		Response map[string]any `json:"response"`
		Error    map[string]any `json:"error"`
	}
	if err := common.Unmarshal(trimmed, &envelope); err != nil {
		return nil, fmt.Errorf("websearch: decode antigravity response: %w", err)
	}
	if len(envelope.Error) > 0 {
		return nil, fmt.Errorf("websearch: antigravity upstream error: %v", envelope.Error["message"])
	}
	if len(envelope.Response) > 0 {
		return common.Marshal(envelope.Response)
	}
	return trimmed, nil
}

// ResolveSearchRedirects replaces the grounding redirect links of one parsed
// answer with the pages they point at. It runs after the parser because only the
// parsed results name which links are redirects. A link that cannot be followed
// takes its result out of the answer: the client cannot open a vertexaisearch
// short link, so returning one would hand it an address that answers 404.
func ResolveSearchRedirects(ctx context.Context, channel abstract.ChannelContext, search *abstract.Search) {
	if search == nil || len(search.Results) == 0 {
		return
	}
	client := channelHTTPClient(channel)

	var targets []int
	for index := range search.Results {
		if isGroundingRedirect(search.Results[index].URL) {
			targets = append(targets, index)
		}
	}
	if len(targets) == 0 {
		return
	}

	var (
		wait     sync.WaitGroup
		slots    = make(chan struct{}, antigravityRedirectLimit)
		mu       sync.Mutex
		resolved = make(map[int]string, len(targets))
	)
	for _, index := range targets {
		index := index
		wait.Add(1)
		go func() {
			defer wait.Done()
			select {
			case slots <- struct{}{}:
			case <-ctx.Done():
				return
			}
			defer func() { <-slots }()

			location := resolveRedirect(ctx, client, search.Results[index].URL)
			if location == "" {
				return
			}
			mu.Lock()
			resolved[index] = location
			mu.Unlock()
		}()
	}
	wait.Wait()

	// A short link that could not be followed is dropped rather than returned:
	// the client cannot open a vertexaisearch link, so a result carrying one is
	// worse than an answer that says fewer pages were found. Results that never
	// needed resolving are untouched.
	kept := search.Results[:0]
	for index, result := range search.Results {
		if !isGroundingRedirect(result.URL) {
			kept = append(kept, result)
			continue
		}
		location, found := resolved[index]
		if !found {
			continue
		}
		result.URL = location
		// The site name was read off the redirect service before the link was
		// followed. Replacing the URL without it would leave the caller with the
		// page's address but the redirect service's name.
		if host := hostOfURL(location); host != "" {
			result.SiteName = host
			result.Known.SiteName = true
		}
		kept = append(kept, result)
	}
	search.Results = kept
}

// resolveRedirect answers a short link through the cache first. Only a resolved
// Location is written: a failed lookup repeats on the next search, because the
// failure may be transient and a negative entry would pin the short link for the
// whole TTL.
func resolveRedirect(ctx context.Context, client *http.Client, target string) string {
	ttl := redirectCacheTTL()
	key := redirectCacheKey(target)
	if ttl > 0 {
		if location, found := lookupRedirect(key); found {
			return location
		}
	}

	location := resolveRedirectUncached(ctx, client, target)
	if location == "" {
		return ""
	}
	if ttl > 0 {
		storeRedirect(key, location, ttl)
	}
	return location
}

// resolveRedirectUncached follows the short link exactly one hop. The redirect
// itself is the answer, so the body is never read and the client's own redirect
// policy is left untouched by copying it.
func resolveRedirectUncached(ctx context.Context, client *http.Client, target string) string {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return ""
	}
	req.Header.Set("User-Agent", antigravityEnvelopeUserAgent)
	noRedirect := *client
	noRedirect.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	resp, err := noRedirect.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))

	if resp.StatusCode < 300 || resp.StatusCode >= 400 {
		return ""
	}
	location := strings.TrimSpace(resp.Header.Get("Location"))
	if location == "" {
		return ""
	}
	parsed, err := url.Parse(location)
	if err != nil {
		return ""
	}
	if !parsed.IsAbs() {
		// A relative Location still names the page, resolved against the link
		// that redirected.
		parsed = req.URL.ResolveReference(parsed)
	}
	if parsed.Host == "" {
		return ""
	}
	return parsed.String()
}

// isGroundingRedirect reports whether a citation points at the grounding
// service's redirect link rather than the page itself.
func isGroundingRedirect(target string) bool {
	parsed, err := url.Parse(target)
	if err != nil {
		return false
	}
	return strings.Contains(parsed.Host, "vertexaisearch") || strings.Contains(parsed.Path, "grounding-api-redirect")
}

// hostOfURL is the site name of a resolved citation. The parser's own site name
// comes from the same reading and lives in the abstract package, which the
// request package is not allowed to reach into for an unexported helper.
func hostOfURL(target string) string {
	parsed, err := url.Parse(strings.TrimSpace(target))
	if err != nil {
		return ""
	}
	return parsed.Hostname()
}

var (
	redirectCacheOnce sync.Once
	redirectCache     *cachex.HybridCache[string]
)

func getRedirectCache() *cachex.HybridCache[string] {
	redirectCacheOnce.Do(func() {
		redirectCache = cachex.NewHybridCache[string](cachex.HybridCacheConfig[string]{
			Namespace:  cachex.Namespace(redirectCacheNamespace),
			Redis:      common.RDB,
			RedisCodec: cachex.StringCodec{},
			RedisEnabled: func() bool {
				return common.RedisEnabled && common.RDB != nil
			},
			Memory: func() *hot.HotCache[string, string] {
				return hot.NewHotCache[string, string](hot.LRU, redirectCacheCapacity).
					WithTTL(time.Duration(effectiveRedirectCacheTTL()) * time.Second).
					WithJanitor().
					Build()
			},
		})
	})
	return redirectCache
}

// redirectCacheTTL resolves the cache lifetime. A negative setting disables the
// cache; a zero setting keeps the built-in lifetime.
func redirectCacheTTL() time.Duration {
	seconds := websearch_setting.RedirectCacheTTL()
	if seconds < 0 {
		return 0
	}
	if seconds == 0 {
		seconds = defaultRedirectCacheTTLSeconds
	}
	return time.Duration(seconds) * time.Second
}

// effectiveRedirectCacheTTL keeps the memory cache's default lifetime in step
// with the configured one, which is the lifetime every entry is written with.
func effectiveRedirectCacheTTL() int {
	seconds := websearch_setting.RedirectCacheTTL()
	if seconds <= 0 {
		seconds = defaultRedirectCacheTTLSeconds
	}
	return seconds
}

// redirectCacheKey hashes the short link because the URL itself can carry a
// long signed query string, while the digest is a fixed-size key.
func redirectCacheKey(target string) string {
	sum := sha256.Sum256([]byte(target))
	return hex.EncodeToString(sum[:])
}

func lookupRedirect(key string) (string, bool) {
	location, found, err := getRedirectCache().Get(key)
	if err != nil {
		logger.LogWarn(context.Background(), "websearch: redirect cache read failed: %v", err)
		return "", false
	}
	return location, found
}

func storeRedirect(key, location string, ttl time.Duration) {
	if err := getRedirectCache().SetWithTTL(key, location, ttl); err != nil {
		logger.LogWarn(context.Background(), "websearch: redirect cache write failed: %v", err)
	}
}
