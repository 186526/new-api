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
	// The subscription page of the admin console renders both allowances: the
	// API/Studio budget the API keys consume and the Vibe Code budget the Vibe
	// plan keys consume. The page is server-rendered, so its RSC payload is the
	// only place a session cookie can read both values from.
	defaultMistralAdminBaseURL = "https://admin.mistral.ai"
	mistralSubscriptionPath    = "/subscription"
	mistralSubscriptionReferer = "/subscription"

	// The Vibe app reports the plan allowance through its own endpoint. It stays
	// as the fallback for accounts whose plan has no billing page.
	defaultMistralVibeBaseURL = "https://chat.mistral.ai"
	mistralVibeUsagePath      = "/api/code-trpc/apiKey.getUsage"
	mistralVibeUsageInput     = `{"0":{"json":null,"meta":{"values":["undefined"],"v":1}}}`

	mistralQuotaTimeout   = 30 * time.Second
	mistralQuotaBodyLimit = 4 << 20

	MistralBudgetScopeAPI  = "api"
	MistralBudgetScopeVibe = "vibe"

	// Both hosts sit behind a Cloudflare managed challenge that rejects
	// requests without browser-like headers, even when the session cookie is
	// valid. The refresh loop keeps the request rate low so the session is not
	// treated as automated traffic.
	mistralBrowserUserAgent      = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/156.0.0.0 Safari/537.36"
	mistralBrowserAcceptLanguage = "zh-CN,zh;q=0.9,en-GB;q=0.8,en;q=0.7"
	mistralBrowserClientHint     = `"Not:A-Brand";v="8", "Chromium";v="156", "Google Chrome";v="156"`
)

// MistralQuotaAccount is one Mistral subscription. Mistral exposes the monthly
// allowances only to the signed-in console, so the credential that reads them is
// the browser session cookie; the two API keys identify which allowance a
// channel draws from, because a Studio key consumes the API budget and a Vibe
// plan key consumes the Vibe Code budget.
type MistralQuotaAccount struct {
	Channel string `json:"channel"`
	Cookie  string `json:"cookie"`
	// VibeKey is the Vibe Code plan key, StudioKey the key issued by the API
	// console. Either may be empty when the account only tracks one allowance.
	VibeKey   string `json:"vibeKey"`
	StudioKey string `json:"studioKey"`
	// The console reports the plan amounts, so these are fallbacks for accounts
	// whose page omits them, and stay the source of truth when no page data is
	// available.
	VibeLimitUSD float64 `json:"monthlyLimitUsd"`
	APILimitUSD  float64 `json:"apiLimitUsd"`
}

// BudgetScopeForKey reports which allowance a channel key consumes, or an empty
// string when the key is not one of the configured ones.
func (a MistralQuotaAccount) BudgetScopeForKey(apiKey string) string {
	key := strings.TrimSpace(apiKey)
	if key == "" {
		return ""
	}
	if a.StudioKey != "" && key == a.StudioKey {
		return MistralBudgetScopeAPI
	}
	if a.VibeKey != "" && key == a.VibeKey {
		return MistralBudgetScopeVibe
	}
	return ""
}

// MistralBudget is one monthly allowance: the console reports the used
// percentage and the plan amount, which together give the remaining dollars.
type MistralBudget struct {
	Scope           string
	UsagePercentage float64
	LimitUSD        float64
	ResetAt         time.Time
}

// MistralQuota holds the allowances the console reported for one account. A
// plan without a billing page has no API budget, so either side may be nil.
type MistralQuota struct {
	Channel     string
	API         *MistralBudget
	Vibe        *MistralBudget
	PaygEnabled bool
	FetchedAt   time.Time
}

func (q *MistralQuota) Budget(scope string) *MistralBudget {
	if q == nil {
		return nil
	}
	switch scope {
	case MistralBudgetScopeAPI:
		return q.API
	case MistralBudgetScopeVibe:
		return q.Vibe
	default:
		return nil
	}
}

// MistralQuotaAuthError reports an expired or rejected console session. The
// refresh loop treats it as an expected condition that needs a new cookie.
type MistralQuotaAuthError struct {
	Status int
}

func (e *MistralQuotaAuthError) Error() string {
	return fmt.Sprintf("mistral console session rejected with status %d", e.Status)
}

// LoadMistralQuotaAccounts reads the configured accounts. The file source is
// preferred so a rotated Secret reaches the process without a restart; when
// neither source is configured the lookup is disabled.
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
		if cookie == "" {
			return nil, nil
		}
		channel := strings.TrimSpace(os.Getenv("MISTRAL_VIBE_QUOTA_CHANNEL"))
		if channel == "" {
			channel = "mistral-vibe"
		}
		return []MistralQuotaAccount{{
			Channel:   channel,
			Cookie:    cookie,
			VibeKey:   strings.TrimSpace(os.Getenv("MISTRAL_VIBE_QUOTA_VIBE_KEY")),
			StudioKey: strings.TrimSpace(os.Getenv("MISTRAL_VIBE_QUOTA_STUDIO_KEY")),
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
		if account.Channel == "" || account.Cookie == "" {
			return nil, errors.New("Mistral quota account requires channel and cookie")
		}
		if account.VibeLimitUSD < 0 || account.APILimitUSD < 0 {
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

// MistralQuotaClient reads the monthly allowances of a signed-in account.
type MistralQuotaClient struct {
	AdminBaseURL string
	VibeBaseURL  string
	HTTPClient   *http.Client
}

func NewMistralQuotaClient() *MistralQuotaClient {
	return &MistralQuotaClient{
		AdminBaseURL: mistralEnvOrDefault("MISTRAL_QUOTA_ADMIN_BASE_URL", defaultMistralAdminBaseURL),
		VibeBaseURL:  mistralEnvOrDefault("MISTRAL_VIBE_QUOTA_BASE_URL", defaultMistralVibeBaseURL),
		HTTPClient:   newMistralHTTPClient(),
	}
}

func FetchMistralQuota(ctx context.Context, account MistralQuotaAccount) (*MistralQuota, error) {
	return NewMistralQuotaClient().Fetch(ctx, account)
}

func (c *MistralQuotaClient) Fetch(ctx context.Context, account MistralQuotaAccount) (*MistralQuota, error) {
	if c == nil {
		return nil, errors.New("Mistral quota client is nil")
	}
	channel := strings.TrimSpace(account.Channel)
	cookie := normalizeMistralCookie(account.Cookie)
	if channel == "" || cookie == "" {
		return nil, errors.New("Mistral quota account requires channel and cookie")
	}

	quota := &MistralQuota{Channel: channel, FetchedAt: time.Now()}
	api, vibe, payg, pageErr := c.fetchSubscription(ctx, cookie)
	if pageErr == nil {
		quota.API, quota.Vibe, quota.PaygEnabled = api, vibe, payg
	} else {
		var authErr *MistralQuotaAuthError
		if errors.As(pageErr, &authErr) {
			return nil, pageErr
		}
		// The Vibe endpoint still answers when the console page has no billing
		// section, and it is the only source for a plan-only account.
		vibeQuota, vibePayg, err := c.fetchVibeUsage(ctx, cookie)
		if err != nil {
			return nil, pageErr
		}
		quota.Vibe, quota.PaygEnabled = vibeQuota, vibePayg
	}
	if quota.Vibe == nil && quota.API == nil {
		return nil, errors.New("Mistral console reported no monthly allowance")
	}
	applyMistralAccountLimits(account, quota)
	return quota, nil
}

// fetchSubscription reads both allowances from the server-rendered
// subscription page. The page reports the Vibe allowance too when the plan has
// one, so the Vibe endpoint is only needed as a fallback.
func (c *MistralQuotaClient) fetchSubscription(ctx context.Context, cookie string) (*MistralBudget, *MistralBudget, bool, error) {
	baseURL := strings.TrimRight(c.adminBaseURL(), "/")
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+mistralSubscriptionPath, nil)
	if err != nil {
		return nil, nil, false, fmt.Errorf("build Mistral subscription request: %w", err)
	}
	c.setBrowserHeaders(request, cookie, baseURL, mistralSubscriptionReferer)
	request.Header.Set("Accept", "text/html")

	response, err := c.httpClient().Do(request)
	if err != nil {
		return nil, nil, false, fmt.Errorf("request Mistral subscription page: %w", err)
	}
	defer response.Body.Close()

	body, err := io.ReadAll(io.LimitReader(response.Body, mistralQuotaBodyLimit))
	if err != nil {
		return nil, nil, false, fmt.Errorf("read Mistral subscription page: %w", err)
	}
	if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden ||
		(response.StatusCode >= http.StatusMultipleChoices && response.StatusCode < http.StatusBadRequest) {
		return nil, nil, false, &MistralQuotaAuthError{Status: response.StatusCode}
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return nil, nil, false, fmt.Errorf("Mistral subscription page returned status %d", response.StatusCode)
	}
	return parseMistralSubscription(body)
}

// fetchVibeUsage reads the Vibe allowance from the Vibe app.
func (c *MistralQuotaClient) fetchVibeUsage(ctx context.Context, cookie string) (*MistralBudget, bool, error) {
	baseURL := strings.TrimRight(c.vibeBaseURL(), "/")
	query := url.Values{}
	query.Set("batch", "1")
	query.Set("input", mistralVibeUsageInput)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet,
		baseURL+mistralVibeUsagePath+"?"+query.Encode(), nil)
	if err != nil {
		return nil, false, fmt.Errorf("build Mistral Vibe request: %w", err)
	}
	c.setBrowserHeaders(request, cookie, baseURL, "/code/extensions?focus=key")
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
	if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden ||
		(response.StatusCode >= http.StatusMultipleChoices && response.StatusCode < http.StatusBadRequest) {
		return nil, false, &MistralQuotaAuthError{Status: response.StatusCode}
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return nil, false, fmt.Errorf("Mistral Vibe usage returned status %d: %s", response.StatusCode, truncateForLog(body))
	}
	return parseMistralVibeUsage(body)
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
// the console did not report one, and keeps a percentage inside its range.
func applyMistralAccountLimits(account MistralQuotaAccount, quota *MistralQuota) {
	if budget := quota.API; budget != nil && budget.LimitUSD <= 0 {
		budget.LimitUSD = account.APILimitUSD
	}
	if budget := quota.Vibe; budget != nil && budget.LimitUSD <= 0 {
		budget.LimitUSD = account.VibeLimitUSD
	}
	for _, budget := range []*MistralBudget{quota.API, quota.Vibe} {
		if budget == nil {
			continue
		}
		if budget.UsagePercentage < 0 {
			budget.UsagePercentage = 0
		}
		if budget.UsagePercentage > 100 {
			budget.UsagePercentage = 100
		}
	}
}

// parseMistralSubscription pulls both allowances out of the RSC payload the
// subscription page embeds. The payload is JSON embedded in a script string, so
// the escaped quotes are unescaped first and each "budget" object is decoded on
// its own instead of parsing the whole page.
func parseMistralSubscription(body []byte) (*MistralBudget, *MistralBudget, bool, error) {
	text := strings.ReplaceAll(string(body), `\"`, `"`)
	payg := mistralPaygEnabled(text)
	const marker = `"budget":{`
	for offset := 0; ; {
		index := strings.Index(text[offset:], marker)
		if index < 0 {
			break
		}
		start := offset + index + len(marker) - 1
		var raw map[string]any
		decoder := json.NewDecoder(strings.NewReader(text[start:]))
		if err := decoder.Decode(&raw); err == nil {
			api := newMistralBudget(MistralBudgetScopeAPI, raw["api_budget"])
			vibe := newMistralBudget(MistralBudgetScopeVibe, raw["vibe_budget"])
			if api != nil || vibe != nil {
				return api, vibe, payg, nil
			}
		}
		offset = start
	}
	return nil, nil, payg, errors.New("Mistral subscription page does not contain a monthly allowance")
}

func newMistralBudget(scope string, value any) *MistralBudget {
	object, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	percentage, ok := mistralNumber(object["usage_percentage"])
	if !ok || percentage < 0 {
		return nil
	}
	limit, _ := mistralNumber(object["initial_budget"])
	return &MistralBudget{
		Scope:           scope,
		UsagePercentage: percentage,
		LimitUSD:        limit,
		ResetAt:         mistralResetTime(object["reset_at"]),
	}
}

func mistralPaygEnabled(text string) bool {
	return strings.Contains(text, `"vibe_payg_enabled":true`)
}

func parseMistralVibeUsage(body []byte) (*MistralBudget, bool, error) {
	var raw any
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, false, fmt.Errorf("decode Mistral Vibe usage: %w", err)
	}
	payload := findMistralVibeUsageObject(raw)
	if payload == nil {
		return nil, false, errors.New("Mistral Vibe usage response does not contain a usage percentage")
	}
	percentage, ok := mistralNumber(mistralField(payload, "usagePercentage", "usage_percentage"))
	if !ok || percentage < 0 {
		return nil, false, errors.New("Mistral Vibe usage percentage is missing or invalid")
	}
	payg, _ := mistralField(payload, "paygEnabled", "payg_enabled").(bool)
	return &MistralBudget{
		Scope:           MistralBudgetScopeVibe,
		UsagePercentage: percentage,
		ResetAt:         mistralResetTime(mistralField(payload, "resetAt", "reset_at")),
	}, payg, nil
}

func findMistralVibeUsageObject(raw any) map[string]any {
	switch value := raw.(type) {
	case []any:
		for _, item := range value {
			if payload := findMistralVibeUsageObject(item); payload != nil {
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
		for _, key := range []string{"result", "data", "json"} {
			if nested, exists := value[key]; exists {
				if payload := findMistralVibeUsageObject(nested); payload != nil {
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

func normalizeMistralCookie(raw string) string {
	raw = strings.TrimSpace(raw)
	raw = strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(raw, "Cookie:"), "cookie:"))
	parts := strings.Split(raw, ";")
	cleaned := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			cleaned = append(cleaned, part)
		}
	}
	return strings.Join(cleaned, "; ")
}

func mistralEnvOrDefault(name string, fallback string) string {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	return value
}
