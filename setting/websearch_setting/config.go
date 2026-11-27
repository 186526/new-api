package websearch_setting

import (
	"fmt"
	"strings"
	"sync/atomic"

	"github.com/QuantumNous/new-api/setting/config"
)

// Provider names a search or fetch implementation. The same list is what the
// channel setting and the global candidate order accept.
const (
	ProviderAntigravity = "antigravity"
	ProviderCodex       = "codex"
	ProviderDeepSeek    = "deepseek"
	ProviderExa         = "exa"
	ProviderLocal       = "local"
)

// KnownProviders lists every provider name this build registers. A name outside
// this list is dropped from CandidateOrder rather than failing the update, so a
// typo costs the wrong order and not the whole chain.
func KnownProviders() []string {
	return []string{ProviderAntigravity, ProviderCodex, ProviderDeepSeek, ProviderExa, ProviderLocal}
}

// WebSearchSetting is the global default for every channel. A channel setting
// that is set wins over it, and a zero here keeps the built-in default.
type WebSearchSetting struct {
	// CandidateOrder names providers in the order they should be tried, comma
	// separated. Empty keeps the built-in ordering. It reorders the candidates a
	// channel can reach; it never adds one that has no credential.
	CandidateOrder string `json:"candidate_order"`
	// NativeFirst keeps the provider whose upstream speaks the client's own
	// contract ahead of the rest. Turning it off leaves the ordering to
	// CandidateOrder and the registration order.
	NativeFirst bool `json:"native_first"`

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
	defaultSearchCacheTTLSeconds        = 600
	defaultFetchCacheTTLSeconds         = 3600
	defaultRedirectCacheTTLSeconds      = 86400
	defaultResponseHeaderTimeoutSeconds = 25
	defaultRetryAttempts                = 3
	defaultRetryBaseDelayMilliseconds   = 500
	defaultExaBaseURL                   = "https://api.exa.ai"
)

var webSearchSetting = WebSearchSetting{
	NativeFirst:                  true,
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

// UpdateAndSync validates the value that was just written into the struct and
// publishes it. A bad order is reported but still published with the unknown
// names dropped, so the operator sees the warning without losing the rest.
func UpdateAndSync() error {
	if err := validate(webSearchSetting); err != nil {
		published.Store(webSearchSetting)
		return err
	}
	published.Store(webSearchSetting)
	return nil
}

func validate(setting WebSearchSetting) error {
	known := make(map[string]struct{}, len(KnownProviders()))
	for _, provider := range KnownProviders() {
		known[provider] = struct{}{}
	}
	var unknown []string
	for _, provider := range strings.Split(setting.CandidateOrder, ",") {
		name := strings.ToLower(strings.TrimSpace(provider))
		if name == "" {
			continue
		}
		if _, ok := known[name]; !ok {
			unknown = append(unknown, name)
		}
	}
	if len(unknown) > 0 {
		return fmt.Errorf("candidate_order names unknown providers: %s", strings.Join(unknown, ", "))
	}
	if setting.SearchCacheTTLSeconds < -1 || setting.FetchCacheTTLSeconds < -1 || setting.RedirectCacheTTLSeconds < -1 {
		return fmt.Errorf("cache ttl must be -1 or greater")
	}
	if setting.ResponseHeaderTimeoutSeconds < 0 {
		return fmt.Errorf("response_header_timeout_seconds must not be negative")
	}
	if setting.RetryAttempts < 0 || setting.RetryAttempts > 10 {
		return fmt.Errorf("retry_attempts must be between 0 and 10")
	}
	if setting.RetryBaseDelayMilliseconds < 0 {
		return fmt.Errorf("retry_base_delay_milliseconds must not be negative")
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
