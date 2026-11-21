package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	// The console bills Vibe and Studio from one monthly allowance and answers it
	// as structured JSON over trpc, so a session cookie reads the percentage and
	// the plan amount from one request without parsing a page. It is the fallback
	// for an entry that carries a cookie, which is also where a key the billing
	// endpoint refuses falls through.
	defaultMistralAdminBaseURL = "https://admin.mistral.ai"
	mistralConsoleBudgetPath   = "/api/trpc/billing.budget"
	mistralConsoleReferer      = "/subscription"

	// The Vibe app reports the same percentage of the merged allowance without
	// the plan amount, so it is the last source for a session the console
	// endpoint refuses.
	defaultMistralVibeBaseURL = "https://chat.mistral.ai"
	mistralVibeUsagePath      = "/api/code-trpc/apiKey.getUsage"
	mistralVibeUsageInput     = `{"0":{"json":null,"meta":{"values":["undefined"],"v":1}}}`

	// The public billing endpoint answers an API key with the merged allowance of
	// the plan that key is billed on, so it is the first source whenever the
	// account has a key. A key of a free plan reports that plan's 10 USD, which is
	// why a channel's own key is preferred over a key written into the file.
	defaultMistralAPIBaseURL = "https://api.mistral.ai"
	mistralBillingBudgetPath = "/v1/billing/budget"

	mistralQuotaTimeout   = 30 * time.Second
	mistralQuotaBodyLimit = 4 << 20

	// MistralBudgetScopeVibe labels the merged monthly allowance. The label used
	// to name the Vibe Code budget alone; Mistral now reports one "Included Vibe &
	// Studio usage" figure, so one label describes it. The value stays "vibe"
	// because the dashboard panels and the quota alert rule group by it, and a new
	// value would leave every existing Mistral row without a sample.
	MistralBudgetScopeVibe = "vibe"

	// The console answers session cookies only with browser-like headers. No
	// pause separates the accounts of one refresh, so the refresh interval is the
	// only brake on how often the console is asked.
	mistralBrowserUserAgent      = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/156.0.0.0 Safari/537.36"
	mistralBrowserAcceptLanguage = "zh-CN,zh;q=0.9,en-GB;q=0.8,en;q=0.7"
	mistralBrowserClientHint     = `"Not:A-Brand";v="8", "Chromium";v="156", "Google Chrome";v="156"`
)

// MistralQuotaAccount is one Mistral subscription. The public billing endpoint
// reports the merged monthly allowance to an API key, and Mistral no longer
// renders that allowance to a console session, so a key is the credential that
// reads it and the browser session cookie is the fallback for an entry that
// carries one, which is also where a key the billing endpoint refuses falls
// through. The two configured keys belong to the account entry, while
// ChannelKey is filled in when the channel is known, so a rotated channel key
// decides the allowance of the channel that carries it.
type MistralQuotaAccount struct {
	Channel string `json:"channel"`
	Cookie  string `json:"cookie"`
	// ChannelKey is the key the channel itself calls the upstream with. It is
	// filled in by the balance path and the quota refresh and never read from
	// the accounts file, so a configured key cannot override a live channel.
	ChannelKey string `json:"-"`
	// VibeKey is the Vibe Code plan key, StudioKey the key issued by the API
	// console. Either may be empty when the account only tracks one.
	VibeKey   string `json:"vibeKey"`
	StudioKey string `json:"studioKey"`
	// MonthlyLimitUSD is the plan amount of the merged monthly allowance. The key
	// endpoint reports the amount itself; the Vibe app does not, so an account
	// read through it needs the amount here.
	MonthlyLimitUSD float64 `json:"monthlyLimitUsd"`
}

// MistralBudget is the merged monthly allowance: the source reports the used
// percentage and the plan amount, which together give the remaining dollars.
type MistralBudget struct {
	UsagePercentage float64
	LimitUSD        float64
	ResetAt         time.Time
}

// MistralQuota holds the allowance one source reported for an account.
type MistralQuota struct {
	Channel     string
	Allowance   *MistralBudget
	PaygEnabled bool
	FetchedAt   time.Time
}

var errMistralAccountWithoutCredential = errors.New("Mistral quota account has neither a console session nor an API key")

// MistralQuotaAuthError reports a rejected credential. The refresh loop treats
// it as an expected condition that needs a new cookie or key, and the fetch chain
// treats it as a source that is unavailable rather than as an allowance of zero.
type MistralQuotaAuthError struct {
	Status int
	// Credential names which credential was rejected, so a caller can tell a dead
	// cookie from a key the channel no longer carries.
	Credential string
}

func (e *MistralQuotaAuthError) Error() string {
	if e.Credential == "" {
		return fmt.Sprintf("mistral console session rejected with status %d", e.Status)
	}
	return fmt.Sprintf("mistral %s rejected with status %d", e.Credential, e.Status)
}

const (
	mistralCredentialSession = "console session"
	mistralCredentialKey     = "API key"
)

// LoadMistralQuotaAccounts reads the configured accounts. The file source is
// preferred so a rotated Secret reaches the process without a restart; when
// neither source is configured the lookup is disabled. An account entry names a
// channel and at least one credential: a key reads the allowance from the public
// billing endpoint, and a cookie is the fallback reading of the console.
func LoadMistralQuotaAccounts() ([]MistralQuotaAccount, error) {
	raw := strings.TrimSpace(os.Getenv("MISTRAL_VIBE_QUOTA_ACCOUNTS"))
	path := strings.TrimSpace(os.Getenv("MISTRAL_VIBE_QUOTA_ACCOUNTS_FILE"))
	if path != "" {
		content, err := os.ReadFile(path)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil, nil
			}
			return nil, fmt.Errorf("read Mistral quota accounts file: %w", err)
		}
		raw = strings.TrimSpace(string(content))
	}
	if raw == "" {
		cookie := strings.TrimSpace(os.Getenv("MISTRAL_VIBE_QUOTA_COOKIE"))
		vibeKey := strings.TrimSpace(os.Getenv("MISTRAL_VIBE_QUOTA_VIBE_KEY"))
		studioKey := strings.TrimSpace(os.Getenv("MISTRAL_VIBE_QUOTA_STUDIO_KEY"))
		if cookie == "" && vibeKey == "" && studioKey == "" {
			return nil, nil
		}
		channel := strings.TrimSpace(os.Getenv("MISTRAL_VIBE_QUOTA_CHANNEL"))
		if channel == "" {
			channel = "mistral-vibe"
		}
		return []MistralQuotaAccount{{
			Channel:   channel,
			Cookie:    cookie,
			VibeKey:   vibeKey,
			StudioKey: studioKey,
		}}, nil
	}

	var accounts []MistralQuotaAccount
	if err := json.Unmarshal([]byte(raw), &accounts); err != nil {
		return nil, fmt.Errorf("parse Mistral quota accounts: %w", err)
	}
	seen := make(map[string]struct{}, len(accounts))
	valid := accounts[:0]
	for _, account := range accounts {
		account.Channel = strings.TrimSpace(account.Channel)
		account.Cookie = strings.TrimSpace(account.Cookie)
		account.VibeKey = strings.TrimSpace(account.VibeKey)
		account.StudioKey = strings.TrimSpace(account.StudioKey)
		if account.Channel == "" {
			return nil, errors.New("Mistral quota account requires a channel")
		}
		if account.Cookie == "" && account.VibeKey == "" && account.StudioKey == "" {
			return nil, fmt.Errorf(
				"Mistral quota account channel %q has neither a console session nor an API key", account.Channel)
		}
		if account.MonthlyLimitUSD < 0 {
			return nil, fmt.Errorf("Mistral quota account channel %q has a negative limit", account.Channel)
		}
		if _, exists := seen[account.Channel]; exists {
			return nil, fmt.Errorf("duplicate Mistral quota account channel %q", account.Channel)
		}
		seen[account.Channel] = struct{}{}
		valid = append(valid, account)
	}
	return valid, nil
}

// MistralQuotaClient reads the monthly allowance of an account: from the public
// billing endpoint with an API key, and from the console trpc endpoint or the
// Vibe app with a session cookie for an entry that carries one, which is also
// where a key the billing endpoint refuses falls through.
type MistralQuotaClient struct {
	AdminBaseURL string
	VibeBaseURL  string
	APIBaseURL   string
	HTTPClient   *http.Client
}

func NewMistralQuotaClient() *MistralQuotaClient {
	return &MistralQuotaClient{
		AdminBaseURL: mistralEnvOrDefault("MISTRAL_QUOTA_ADMIN_BASE_URL", defaultMistralAdminBaseURL),
		VibeBaseURL:  mistralEnvOrDefault("MISTRAL_VIBE_QUOTA_BASE_URL", defaultMistralVibeBaseURL),
		APIBaseURL:   mistralEnvOrDefault("MISTRAL_QUOTA_API_BASE_URL", defaultMistralAPIBaseURL),
		HTTPClient:   newMistralHTTPClient(),
	}
}

func FetchMistralQuota(ctx context.Context, account MistralQuotaAccount) (*MistralQuota, error) {
	return NewMistralQuotaClient().Fetch(ctx, account)
}

// APIKey answers the credential the public billing endpoint is read with: the
// key of the channel when the caller resolved one, and the configured Studio or
// Vibe key of the account entry otherwise. The channel key comes first because a
// rotated channel key is the key the account is actually billed against, while
// the key written into the accounts file may belong to another key of the same
// account or to a free plan.
func (account MistralQuotaAccount) APIKey() string {
	for _, key := range []string{account.ChannelKey, account.StudioKey, account.VibeKey} {
		if trimmed := strings.TrimSpace(key); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func (c *MistralQuotaClient) Fetch(ctx context.Context, account MistralQuotaAccount) (*MistralQuota, error) {
	if c == nil {
		return nil, errors.New("Mistral quota client is nil")
	}
	channel := strings.TrimSpace(account.Channel)
	if channel == "" {
		return nil, errors.New("Mistral quota account requires a channel")
	}
	account.Cookie = normalizeMistralCookie(account.Cookie)

	quota := &MistralQuota{Channel: channel, FetchedAt: time.Now()}
	var firstErr error
	for _, fetch := range c.allowanceSources(account) {
		allowance, payg, err := fetch(ctx, account)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if allowance == nil {
			continue
		}
		quota.Allowance, quota.PaygEnabled = allowance, payg
		applyMistralAccountLimits(account, quota)
		return quota, nil
	}
	if firstErr != nil {
		return nil, firstErr
	}
	return nil, errMistralAccountWithoutCredential
}

// allowanceSources lists the endpoints the allowance is read from, in order. The
// public billing endpoint answers the merged allowance of the plan the key is
// billed on, and it is the first source whenever the account has a key: the
// console renders that allowance to nobody now, because a dead session is still
// answered with 200 and the login page, so the cookie cannot be trusted to
// report it. The console trpc endpoint, which carries the plan amount, and the
// Vibe app, which carries the percentage alone, stay as the fallbacks for an
// entry that carries a cookie, which is also where a key the billing endpoint
// refuses falls through. A source that answers 401 or 403 for the key, or a
// login redirect for the cookie, falls through to the next source instead of
// being reported as the allowance.
func (c *MistralQuotaClient) allowanceSources(account MistralQuotaAccount) []func(context.Context, MistralQuotaAccount) (*MistralBudget, bool, error) {
	var sources []func(context.Context, MistralQuotaAccount) (*MistralBudget, bool, error)
	if account.APIKey() != "" {
		sources = append(sources, c.fetchKeyBudget)
	}
	if account.Cookie != "" {
		sources = append(sources, c.fetchConsoleBudget, c.fetchVibeUsage)
	}
	return sources
}

// fetchConsoleBudget reads the merged allowance from the console's trpc budget
// endpoint. One answer carries the percentage and the plan amount.
func (c *MistralQuotaClient) fetchConsoleBudget(ctx context.Context, account MistralQuotaAccount) (*MistralBudget, bool, error) {
	baseURL := strings.TrimRight(c.adminBaseURL(), "/")
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+mistralConsoleBudgetPath, nil)
	if err != nil {
		return nil, false, fmt.Errorf("build Mistral console budget request: %w", err)
	}
	c.setBrowserHeaders(request, account.Cookie, baseURL, mistralConsoleReferer)
	request.Header.Set("Accept", "application/json")

	response, err := c.httpClient().Do(request)
	if err != nil {
		return nil, false, fmt.Errorf("request Mistral console budget: %w", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, mistralQuotaBodyLimit))
	if err != nil {
		return nil, false, fmt.Errorf("read Mistral console budget: %w", err)
	}
	if err := mistralConsoleStatusError(response.StatusCode, "budget"); err != nil {
		return nil, false, err
	}
	return parseMistralAllowance(body)
}

// fetchVibeUsage reads the merged allowance from the Vibe app, which reports the
// percentage alone.
func (c *MistralQuotaClient) fetchVibeUsage(ctx context.Context, account MistralQuotaAccount) (*MistralBudget, bool, error) {
	baseURL := strings.TrimRight(c.vibeBaseURL(), "/")
	query := url.Values{}
	query.Set("batch", "1")
	query.Set("input", mistralVibeUsageInput)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet,
		baseURL+mistralVibeUsagePath+"?"+query.Encode(), nil)
	if err != nil {
		return nil, false, fmt.Errorf("build Mistral Vibe request: %w", err)
	}
	c.setBrowserHeaders(request, account.Cookie, baseURL, "/code/extensions?focus=key")
	request.Header.Set("Accept", "application/json")
	request.Header.Set("trpc-accept", "application/json")

	response, err := c.httpClient().Do(request)
	if err != nil {
		return nil, false, fmt.Errorf("request Mistral Vibe usage: %w", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, mistralQuotaBodyLimit))
	if err != nil {
		return nil, false, fmt.Errorf("read Mistral Vibe usage: %w", err)
	}
	if err := mistralConsoleStatusError(response.StatusCode, "Vibe usage"); err != nil {
		return nil, false, err
	}
	return parseMistralAllowance(body)
}

// fetchKeyBudget reads the allowance the public billing endpoint reports to an
// API key. The answer names the plan the key is billed on, so its own amount is
// kept; a configured plan amount fills in only an answer that reports none.
func (c *MistralQuotaClient) fetchKeyBudget(ctx context.Context, account MistralQuotaAccount) (*MistralBudget, bool, error) {
	apiKey := account.APIKey()
	if apiKey == "" {
		return nil, false, errMistralAccountWithoutCredential
	}
	baseURL := strings.TrimRight(c.billingBaseURL(), "/")
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+mistralBillingBudgetPath, nil)
	if err != nil {
		return nil, false, fmt.Errorf("build Mistral billing request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+apiKey)
	request.Header.Set("Accept", "application/json")

	response, err := c.httpClient().Do(request)
	if err != nil {
		return nil, false, fmt.Errorf("request Mistral billing allowance: %w", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, mistralQuotaBodyLimit))
	if err != nil {
		return nil, false, fmt.Errorf("read Mistral billing allowance: %w", err)
	}
	if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden {
		return nil, false, &MistralQuotaAuthError{Status: response.StatusCode, Credential: mistralCredentialKey}
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return nil, false, fmt.Errorf("Mistral billing allowance returned status %d", response.StatusCode)
	}
	return parseMistralBillingBudget(body)
}

// mistralConsoleStatusError turns the status of a console answer into the error
// the refresh loop acts on. An expired session redirects to the login page or
// answers 401/403, and both mean the cookie needs replacing: the caller falls
// through to the next source and reports this error if no source answers.
func mistralConsoleStatusError(status int, what string) error {
	switch {
	case status == http.StatusUnauthorized || status == http.StatusForbidden,
		status >= http.StatusMultipleChoices && status < http.StatusBadRequest:
		return &MistralQuotaAuthError{Status: status, Credential: mistralCredentialSession}
	case status < http.StatusOK || status >= http.StatusMultipleChoices:
		return fmt.Errorf("Mistral console %s returned status %d", what, status)
	}
	return nil
}

func (c *MistralQuotaClient) setBrowserHeaders(request *http.Request, cookie, baseURL, refererPath string) {
	request.Header.Set("Cookie", cookie)
	request.Header.Set("User-Agent", mistralBrowserUserAgent)
	request.Header.Set("Accept-Language", mistralBrowserAcceptLanguage)
	request.Header.Set("Referer", baseURL+refererPath)
	request.Header.Set("sec-ch-ua", mistralBrowserClientHint)
	request.Header.Set("sec-ch-ua-mobile", "?0")
	request.Header.Set("sec-ch-ua-platform", `"Linux"`)
}

func (c *MistralQuotaClient) adminBaseURL() string {
	if strings.TrimSpace(c.AdminBaseURL) == "" {
		return defaultMistralAdminBaseURL
	}
	return c.AdminBaseURL
}

func (c *MistralQuotaClient) vibeBaseURL() string {
	if strings.TrimSpace(c.VibeBaseURL) == "" {
		return defaultMistralVibeBaseURL
	}
	return c.VibeBaseURL
}

func (c *MistralQuotaClient) billingBaseURL() string {
	if strings.TrimSpace(c.APIBaseURL) == "" {
		return mistralEnvOrDefault("MISTRAL_QUOTA_API_BASE_URL", defaultMistralAPIBaseURL)
	}
	return c.APIBaseURL
}

func (c *MistralQuotaClient) httpClient() *http.Client {
	if c.HTTPClient != nil {
		return c.HTTPClient
	}
	return newMistralHTTPClient()
}

func newMistralHTTPClient() *http.Client {
	return &http.Client{
		Timeout: mistralQuotaTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// applyMistralAccountLimits fills the plan amount from the account entry when
// the source reported no amount, and keeps a percentage inside its range. A
// reported amount is never replaced: the console reports the plan the account is
// on, and the configured amount is only the fallback for the source that reports
// the percentage alone.
func applyMistralAccountLimits(account MistralQuotaAccount, quota *MistralQuota) {
	budget := quota.Allowance
	if budget == nil {
		return
	}
	if budget.LimitUSD <= 0 {
		budget.LimitUSD = account.MonthlyLimitUSD
	}
	if budget.UsagePercentage < 0 {
		budget.UsagePercentage = 0
	}
	if budget.UsagePercentage > 100 {
		budget.UsagePercentage = 100
	}
}

// parseMistralAllowance reads the merged allowance out of a trpc answer. The
// console reports the plan amount next to the percentage; the Vibe app reports
// the percentage alone.
func parseMistralAllowance(body []byte) (*MistralBudget, bool, error) {
	var raw any
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, false, fmt.Errorf("decode Mistral allowance: %w", err)
	}
	payload := findMistralAllowanceObject(raw)
	if payload == nil {
		return nil, false, errors.New("Mistral answer does not contain a usage percentage")
	}
	allowance := newMistralBudgetFromObject(payload)
	if allowance == nil {
		return nil, false, errors.New("Mistral usage percentage is missing or invalid")
	}
	payg, _ := mistralField(payload, "payg_enabled", "paygEnabled").(bool)
	return allowance, payg, nil
}

// findMistralAllowanceObject walks a trpc answer to the object that names the
// usage percentage. The endpoint answers one object either at the top level or
// wrapped in the trpc envelope, and a batch answer wraps it in an array.
func findMistralAllowanceObject(raw any) map[string]any {
	switch value := raw.(type) {
	case []any:
		for _, item := range value {
			if payload := findMistralAllowanceObject(item); payload != nil {
				return payload
			}
		}
	case map[string]any:
		if _, exists := value["usagePercentage"]; exists {
			return value
		}
		if _, exists := value["usage_percentage"]; exists {
			return value
		}
		for _, key := range []string{"result", "data", "json", "budget", "vibe_budget"} {
			if nested, exists := value[key]; exists {
				if payload := findMistralAllowanceObject(nested); payload != nil {
					return payload
				}
			}
		}
	}
	return nil
}

func mistralField(payload map[string]any, names ...string) any {
	for _, name := range names {
		if value, exists := payload[name]; exists {
			return value
		}
	}
	return nil
}

func mistralNumber(value any) (float64, bool) {
	switch number := value.(type) {
	case float64:
		return number, true
	case float32:
		return float64(number), true
	case int:
		return float64(number), true
	case int64:
		return float64(number), true
	case json.Number:
		parsed, err := number.Float64()
		return parsed, err == nil
	case string:
		parsed, err := strconv.ParseFloat(strings.TrimSpace(number), 64)
		return parsed, err == nil
	default:
		return 0, false
	}
}

func mistralResetTime(value any) time.Time {
	switch reset := value.(type) {
	case string:
		text := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(reset), "$D"))
		if text == "" {
			return time.Time{}
		}
		for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
			if parsed, err := time.Parse(layout, text); err == nil {
				return parsed
			}
		}
	case float64:
		if reset > 0 {
			if reset > 1e12 {
				return time.UnixMilli(int64(reset))
			}
			return time.Unix(int64(reset), 0)
		}
	}
	return time.Time{}
}

// parseMistralBillingBudget reads the merged allowance out of the public billing
// endpoint's answer. The endpoint still reports a budget per plan side; the
// merged allowance is the Vibe one, which is the side the console renders, and
// the API side is read only when the answer carries nothing else.
func parseMistralBillingBudget(body []byte) (*MistralBudget, bool, error) {
	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, false, fmt.Errorf("decode Mistral billing allowance: %w", err)
	}
	side := mistralObject(raw["vibe_budget"])
	if _, ok := mistralNumber(side["usage_percentage"]); !ok {
		side = mistralObject(raw["api_budget"])
	}
	allowance := newMistralBudgetFromObject(side)
	if allowance == nil {
		return nil, false, errors.New("Mistral billing endpoint reported no monthly allowance")
	}
	payg, _ := mistralField(side, "payg_enabled", "paygEnabled").(bool)
	return allowance, payg, nil
}

// newMistralBudgetFromObject reads one allowance out of a decoded object.
func newMistralBudgetFromObject(payload map[string]any) *MistralBudget {
	percentage, ok := mistralNumber(mistralField(payload, "usage_percentage", "usagePercentage"))
	if !ok || percentage < 0 {
		return nil
	}
	limit, _ := mistralNumber(mistralField(payload, "initial_budget", "initialBudget"))
	return &MistralBudget{
		UsagePercentage: percentage,
		LimitUSD:        limit,
		ResetAt:         mistralResetTime(mistralField(payload, "reset_at", "resetAt")),
	}
}

func mistralObject(value any) map[string]any {
	object, _ := value.(map[string]any)
	return object
}

func normalizeMistralCookie(raw string) string {
	raw = strings.TrimSpace(raw)
	raw = strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(raw, "Cookie:"), "cookie:"))
	parts := strings.Split(raw, ";")
	cleaned := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			cleaned = append(cleaned, unquoteMistralCookieValue(part))
		}
	}
	return strings.Join(cleaned, "; ")
}

// unquoteMistralCookieValue drops one pair of quotes around the value of a
// name=value pair, and leaves the value untouched when it carries none. A session
// value copied from the console's storage is quoted, and sending the quotes back
// makes the session unreadable: the same account answers the login redirect with
// them and the allowance without them.
func unquoteMistralCookieValue(pair string) string {
	name, value, found := strings.Cut(pair, "=")
	if !found {
		return pair
	}
	value = strings.TrimSpace(value)
	if len(value) >= 2 && strings.HasPrefix(value, `"`) && strings.HasSuffix(value, `"`) {
		value = value[1 : len(value)-1]
	}
	return name + "=" + value
}

func mistralEnvOrDefault(name string, fallback string) string {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	return value
}
