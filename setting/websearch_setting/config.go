package websearch_setting

import (
	"fmt"
	"math"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/QuantumNous/new-api/pkg/toolcatalog"
	"github.com/QuantumNous/new-api/setting/config"
)

const (
	ProviderAntigravity = toolcatalog.Antigravity
	ProviderCodex       = toolcatalog.Codex
	ProviderDeepSeek    = toolcatalog.DeepSeek
	ProviderExa         = toolcatalog.Exa
	ProviderLocal       = toolcatalog.Local
	ProviderMoonshot    = toolcatalog.Moonshot
	ProviderPerplexity  = toolcatalog.Perplexity
	ProviderZhipu       = toolcatalog.Zhipu
	ProviderBaiduV2     = toolcatalog.BaiduV2
)

func KnownProviders() []string {
	return toolcatalog.IDs()
}

// WebSearchSetting is the global default for every channel. A channel setting
// that is set wins over it, and a zero here keeps the built-in default.
type WebSearchSetting struct {
	// CandidateOrder names providers in the order they should be tried, comma
	// separated. Empty keeps the built-in ordering. It reorders the candidates a
	// channel can reach; it never adds one that has no credential.
	CandidateOrder string `json:"candidate_order"`
	GroupPolicies  string `json:"group_policies"`
	groupPolicies  map[string]map[string]groupPolicy
	// NativeFirst keeps the provider whose upstream speaks the client's own
	// contract ahead of the rest. Turning it off leaves the ordering to
	// CandidateOrder and the registration order.
	NativeFirst            bool `json:"native_first"`
	BuiltinFallbackEnabled bool `json:"builtin_fallback_enabled"`
	FallbackEnabled        bool `json:"fallback_enabled"`
	LocalFetchMaxBytes     int  `json:"local_fetch_max_bytes"`

	SearchCacheTTLSeconds        int `json:"search_cache_ttl_seconds"`
	FetchCacheTTLSeconds         int `json:"fetch_cache_ttl_seconds"`
	RedirectCacheTTLSeconds      int `json:"redirect_cache_ttl_seconds"`
	ResponseHeaderTimeoutSeconds int `json:"response_header_timeout_seconds"`
	RetryAttempts                int `json:"retry_attempts"`
	RetryBaseDelayMilliseconds   int `json:"retry_base_delay_milliseconds"`

	ExaBaseURL string `json:"exa_base_url"`
	// ExaAPIKey lets Exa serve every channel, not only the one that binds it.
	// The option API never returns this value; the operator page reads
	// ExaKeyConfigured instead.
	ExaAPIKey string `json:"exa_api_key"`
	// AntigravitySearchModel is the model an Antigravity search is sent to. An
	// empty value keeps the built-in default.
	AntigravitySearchModel string `json:"antigravity_search_model"`
}

// Defaults for the numeric fields. Zero in the setting means "keep this".
const (
	DefaultSearchResultCount            = 5
	defaultSearchCacheTTLSeconds        = 600
	defaultLocalFetchMaxBytes           = 2 << 20
	maxLocalFetchMaxBytes               = 8 << 20
	defaultFetchCacheTTLSeconds         = 3600
	defaultRedirectCacheTTLSeconds      = 86400
	defaultResponseHeaderTimeoutSeconds = 25
	defaultRetryAttempts                = 3
	defaultRetryBaseDelayMilliseconds   = 500
	defaultExaBaseURL                   = "https://api.exa.ai"
)

var webSearchSetting = WebSearchSetting{
	GroupPolicies:                "{}",
	NativeFirst:                  true,
	LocalFetchMaxBytes:           defaultLocalFetchMaxBytes,
	SearchCacheTTLSeconds:        defaultSearchCacheTTLSeconds,
	FetchCacheTTLSeconds:         defaultFetchCacheTTLSeconds,
	RedirectCacheTTLSeconds:      defaultRedirectCacheTTLSeconds,
	ResponseHeaderTimeoutSeconds: defaultResponseHeaderTimeoutSeconds,
	RetryAttempts:                defaultRetryAttempts,
	RetryBaseDelayMilliseconds:   defaultRetryBaseDelayMilliseconds,
	ExaBaseURL:                   defaultExaBaseURL,
}

// published holds the value every reader sees, so a reader never has to take a
// lock while the config manager updates the struct in place.
var published atomic.Value

func init() {
	config.GlobalConfig.Register("websearch_setting", &webSearchSetting)
	published.Store(webSearchSetting)
}

// Get returns the current settings. The copy is what a caller iterates over for
// the rest of one request, so a concurrent update cannot change it midway.
func Get() WebSearchSetting {
	value, ok := published.Load().(WebSearchSetting)
	if !ok {
		return webSearchSetting
	}
	return value
}

// ProviderOrder splits CandidateOrder into provider names, dropping duplicates
// and names this build does not know. The result keeps the order the operator
// wrote.
func ProviderOrder() []string {
	return parseProviderOrder(Get().CandidateOrder)
}

// GlobalProviders lists the providers that can serve every channel because the
// global settings hold their credential. Exa is the only one today: the others
// need a channel whose own upstream is the provider.
func GlobalProviders() []string {
	if strings.TrimSpace(Get().ExaAPIKey) == "" {
		return nil
	}
	return []string{ProviderExa}
}

const optionPrefix = "websearch_setting."

func IsOption(key string) bool {
	return strings.HasPrefix(key, optionPrefix)
}

// UpdateAndSync publishes a validated snapshot from the staged settings.
func UpdateAndSync() error {
	next := webSearchSetting
	if next.LocalFetchMaxBytes == 0 {
		next.LocalFetchMaxBytes = defaultLocalFetchMaxBytes
	}
	if next.GroupPolicies == "" {
		next.GroupPolicies = "{}"
	}
	previous := Get()
	next.groupPolicies = previous.groupPolicies
	if next.GroupPolicies != previous.GroupPolicies {
		policies, err := parseGroupPolicies(next.GroupPolicies)
		if err != nil {
			webSearchSetting = previous
			return err
		}
		next.groupPolicies = policies
	}
	options, err := config.ConfigToMap(&next)
	if err == nil {
		for field, value := range options {
			if field == "group_policies" {
				continue
			}
			if err = ValidateOption(optionPrefix+field, value); err != nil {
				break
			}
		}
	}
	if err != nil {
		webSearchSetting = previous
		return err
	}
	webSearchSetting = next
	published.Store(next)
	return nil
}

func ValidateOption(key, value string) error {
	field := strings.TrimPrefix(key, optionPrefix)
	switch field {
	case "group_policies":
		return ValidateGroupPolicies(value)
	case "candidate_order":
		return validateProviderOrder(value)
	case "native_first", "builtin_fallback_enabled", "fallback_enabled":
		if _, err := strconv.ParseBool(value); err != nil {
			return fmt.Errorf("%s must be a boolean", field)
		}
	case "local_fetch_max_bytes":
		return validateOptionInteger(field, value, 1, maxLocalFetchMaxBytes)
	case "search_cache_ttl_seconds", "fetch_cache_ttl_seconds", "redirect_cache_ttl_seconds":
		return validateOptionInteger(field, value, -1, math.MaxInt64/int64(time.Second))
	case "response_header_timeout_seconds":
		return validateOptionInteger(field, value, 0, math.MaxInt64/int64(time.Second))
	case "retry_attempts":
		return validateOptionInteger(field, value, 0, 5)
	case "retry_base_delay_milliseconds":
		return validateOptionInteger(field, value, 0, math.MaxInt64/int64(time.Millisecond)/16)
	case "exa_base_url":
		if endpoint := strings.TrimSpace(value); endpoint != "" {
			parsed, err := url.Parse(endpoint)
			if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Hostname() == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" {
				return fmt.Errorf("%s must be an absolute HTTP or HTTPS base URL without credentials, query, or fragment", field)
			}
		}
	case "exa_api_key", "antigravity_search_model":
		if strings.ContainsAny(value, "\r\n\x00") {
			return fmt.Errorf("%s must not contain line breaks or NUL bytes", field)
		}
	default:
		return fmt.Errorf("unknown web search option %q", key)
	}
	return nil
}

func validateOptionInteger(field, raw string, min, max int64) error {
	value, err := strconv.ParseInt(raw, 10, strconv.IntSize)
	if err != nil || value < min || value > max {
		return fmt.Errorf("%s must be an integer between %d and %d", field, min, max)
	}
	return nil
}

func validateProviderOrder(raw string) error {
	seen := make(map[string]struct{})
	for _, provider := range strings.Split(raw, ",") {
		name := strings.ToLower(strings.TrimSpace(provider))
		if name == "" {
			continue
		}
		if !ChannelProviderIsKnown(name) {
			return fmt.Errorf("candidate_order names unknown provider %q", name)
		}
		if _, exists := seen[name]; exists {
			return fmt.Errorf("candidate_order names duplicate provider %q", name)
		}
		seen[name] = struct{}{}
	}
	return nil
}

// parseProviderOrder is the shared splitter, so ProviderOrder and validate
// cannot disagree about what a name is.
func parseProviderOrder(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	known := make(map[string]struct{}, len(KnownProviders()))
	for _, provider := range KnownProviders() {
		known[provider] = struct{}{}
	}
	seen := make(map[string]struct{}, len(KnownProviders()))
	order := make([]string, 0, len(KnownProviders()))
	for _, provider := range strings.Split(raw, ",") {
		name := strings.ToLower(strings.TrimSpace(provider))
		if name == "" {
			continue
		}
		if _, ok := known[name]; !ok {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		order = append(order, name)
	}
	return order
}

// SearchCacheTTL resolves how long a search answer lives when the channel sets
// nothing. A negative global value disables the cache.
func SearchCacheTTL() int {
	return Get().SearchCacheTTLSeconds
}

func FetchCacheTTL() int {
	return Get().FetchCacheTTLSeconds
}

func RedirectCacheTTL() int {
	return Get().RedirectCacheTTLSeconds
}

func ResponseHeaderTimeout() int {
	return Get().ResponseHeaderTimeoutSeconds
}

func RetryAttempts() int {
	return Get().RetryAttempts
}

func RetryBaseDelayMilliseconds() int {
	return Get().RetryBaseDelayMilliseconds
}

// ExaEndpoint is the base URL an Exa request is sent to.
func ExaEndpoint() string {
	endpoint := strings.TrimSpace(Get().ExaBaseURL)
	if endpoint == "" {
		return defaultExaBaseURL
	}
	return strings.TrimRight(endpoint, "/")
}

// AntigravityModel is the model an Antigravity search is sent to.
func AntigravityModel() string {
	return strings.TrimSpace(Get().AntigravitySearchModel)
}

// ChannelProviderIsKnown reports whether a channel setting names a provider this
// build can dispatch to. The channel setting validation uses it so the accepted
// names and the registered names cannot drift apart.
func ChannelProviderIsKnown(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "":
		return true
	}
	for _, provider := range KnownProviders() {
		if provider == strings.ToLower(strings.TrimSpace(name)) {
			return true
		}
	}
	return false
}
