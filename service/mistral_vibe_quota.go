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
	defaultMistralVibeBaseURL = "https://chat.mistral.ai"
	mistralVibeUsagePath      = "/api/code-trpc/apiKey.getUsage"
	mistralVibeQuotaTimeout   = 30 * time.Second
	mistralVibeQuotaBodyLimit = 1 << 20
	mistralVibeUsageInput     = `{"0":{"json":null,"meta":{"values":["undefined"],"v":1}}}`
	mistralVibeRefererPath    = "/code/extensions?focus=key"

	// chat.mistral.ai sits behind a Cloudflare managed challenge that rejects
	// requests without browser-like headers, even when the session cookie is
	// valid. The refresh loop keeps the request rate low so the session is not
	// treated as automated traffic.
	mistralVibeBrowserUserAgent      = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/156.0.0.0 Safari/537.36"
	mistralVibeBrowserAcceptLanguage = "zh-CN,zh;q=0.9,en-GB;q=0.8,en;q=0.7"
	mistralVibeBrowserClientHint     = `"Not:A-Brand";v="8", "Chromium";v="156", "Google Chrome";v="156"`
)

// MistralVibeAccount is one Vibe subscription. Vibe exposes its monthly
// allowance only to the signed-in web app, so the credential is the browser
// session cookie rather than the API key used for model calls.
type MistralVibeAccount struct {
	Channel string `json:"channel"`
	Cookie  string `json:"cookie"`
	// MonthlyLimitUSD is the size of the Vibe Code allowance this account
	// consumes. The web app reports only the used percentage, so the channel
	// balance needs the plan amount to turn that percentage into dollars.
	// Zero means the amount is unknown and the balance stays untouched.
	MonthlyLimitUSD float64 `json:"monthlyLimitUsd"`
}

// MistralVibeQuota is the Vibe Code monthly allowance reported by the web app.
type MistralVibeQuota struct {
	Channel         string
	UsagePercentage float64
	ResetAt         time.Time
	PaygEnabled     bool
	FetchedAt       time.Time
}

// MistralVibeAuthError reports an expired or rejected web session. The refresh
// loop treats it as an expected condition that needs a new cookie.
type MistralVibeAuthError struct {
	Status int
}

func (e *MistralVibeAuthError) Error() string {
	return fmt.Sprintf("mistral vibe session rejected with status %d", e.Status)
}

// LoadMistralVibeAccounts reads the configured accounts. The file source is
// preferred so a rotated Secret reaches the process without a restart; when
// neither source is configured the lookup is disabled.
func LoadMistralVibeAccounts() ([]MistralVibeAccount, error) {
	raw := strings.TrimSpace(os.Getenv("MISTRAL_VIBE_QUOTA_ACCOUNTS"))
	path := strings.TrimSpace(os.Getenv("MISTRAL_VIBE_QUOTA_ACCOUNTS_FILE"))
	if path != "" {
		content, err := os.ReadFile(path)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil, nil
			}
			return nil, fmt.Errorf("read Mistral Vibe accounts file: %w", err)
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
		return []MistralVibeAccount{{Channel: channel, Cookie: cookie}}, nil
	}

	var accounts []MistralVibeAccount
	if err := json.Unmarshal([]byte(raw), &accounts); err != nil {
		return nil, fmt.Errorf("parse Mistral Vibe accounts: %w", err)
	}
	seen := make(map[string]struct{}, len(accounts))
	valid := accounts[:0]
	for _, account := range accounts {
		account.Channel = strings.TrimSpace(account.Channel)
		account.Cookie = strings.TrimSpace(account.Cookie)
		if account.Channel == "" || account.Cookie == "" {
			return nil, errors.New("Mistral Vibe account requires channel and cookie")
		}
		if account.MonthlyLimitUSD < 0 {
			return nil, fmt.Errorf("Mistral Vibe account channel %q has a negative monthlyLimitUsd", account.Channel)
		}
		if _, exists := seen[account.Channel]; exists {
			return nil, fmt.Errorf("duplicate Mistral Vibe account channel %q", account.Channel)
		}
		seen[account.Channel] = struct{}{}
		valid = append(valid, account)
	}
	return valid, nil
}

// MistralVibeClient queries the Vibe web app for its monthly allowance.
type MistralVibeClient struct {
	BaseURL    string
	HTTPClient *http.Client
}

func NewMistralVibeClient() *MistralVibeClient {
	return &MistralVibeClient{
		BaseURL:    mistralVibeEnvOrDefault("MISTRAL_VIBE_QUOTA_BASE_URL", defaultMistralVibeBaseURL),
		HTTPClient: newMistralVibeHTTPClient(),
	}
}

func FetchMistralVibeQuota(ctx context.Context, account MistralVibeAccount) (*MistralVibeQuota, error) {
	return NewMistralVibeClient().Fetch(ctx, account)
}

func (c *MistralVibeClient) Fetch(ctx context.Context, account MistralVibeAccount) (*MistralVibeQuota, error) {
	if c == nil {
		return nil, errors.New("Mistral Vibe client is nil")
	}
	channel := strings.TrimSpace(account.Channel)
	cookie := normalizeMistralVibeCookie(account.Cookie)
	if channel == "" || cookie == "" {
		return nil, errors.New("Mistral Vibe account requires channel and cookie")
	}
	payload, err := c.fetchUsage(ctx, cookie)
	if err != nil {
		return nil, err
	}
	return newMistralVibeQuota(channel, payload)
}

func (c *MistralVibeClient) fetchUsage(ctx context.Context, cookie string) (map[string]any, error) {
	query := url.Values{}
	query.Set("batch", "1")
	query.Set("input", mistralVibeUsageInput)
	requestURL := strings.TrimRight(c.BaseURL, "/") + mistralVibeUsagePath + "?" + query.Encode()

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
	if err != nil {
		return nil, fmt.Errorf("build Mistral Vibe request: %w", err)
	}
	baseURL := strings.TrimRight(c.BaseURL, "/")
	request.Header.Set("Cookie", cookie)
	request.Header.Set("Accept", "application/json")
	request.Header.Set("trpc-accept", "application/json")
	request.Header.Set("User-Agent", mistralVibeBrowserUserAgent)
	request.Header.Set("Accept-Language", mistralVibeBrowserAcceptLanguage)
	request.Header.Set("Referer", baseURL+mistralVibeRefererPath)
	request.Header.Set("sec-ch-ua", mistralVibeBrowserClientHint)
	request.Header.Set("sec-ch-ua-mobile", "?0")
	request.Header.Set("sec-ch-ua-platform", `"Linux"`)

	response, err := c.httpClient().Do(request)
	if err != nil {
		return nil, fmt.Errorf("request Mistral Vibe usage: %w", err)
	}
	defer response.Body.Close()

	body, err := io.ReadAll(io.LimitReader(response.Body, mistralVibeQuotaBodyLimit))
	if err != nil {
		return nil, fmt.Errorf("read Mistral Vibe usage: %w", err)
	}
	if response.StatusCode >= http.StatusMultipleChoices && response.StatusCode < http.StatusBadRequest {
		return nil, &MistralVibeAuthError{Status: response.StatusCode}
	}
	if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden {
		return nil, &MistralVibeAuthError{Status: response.StatusCode}
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("Mistral Vibe usage returned status %d: %s", response.StatusCode, truncateForLog(body))
	}
	return parseMistralVibeUsage(body)
}

func (c *MistralVibeClient) httpClient() *http.Client {
	if c.HTTPClient != nil {
		return c.HTTPClient
	}
	return newMistralVibeHTTPClient()
}

func newMistralVibeHTTPClient() *http.Client {
	return &http.Client{
		Timeout: mistralVibeQuotaTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func newMistralVibeQuota(channel string, payload map[string]any) (*MistralVibeQuota, error) {
	percentage, ok := mistralVibeNumber(mistralVibeField(payload, "usagePercentage", "usage_percentage"))
	if !ok || percentage < 0 {
		return nil, errors.New("Mistral Vibe usage percentage is missing or invalid")
	}
	if percentage > 100 {
		return nil, fmt.Errorf("Mistral Vibe usage percentage exceeds 100: %v", percentage)
	}
	paygEnabled, _ := mistralVibeField(payload, "paygEnabled", "payg_enabled").(bool)
	return &MistralVibeQuota{
		Channel:         channel,
		UsagePercentage: percentage,
		ResetAt:         mistralVibeResetTime(mistralVibeField(payload, "resetAt", "reset_at")),
		PaygEnabled:     paygEnabled,
		FetchedAt:       time.Now(),
	}, nil
}

func parseMistralVibeUsage(body []byte) (map[string]any, error) {
	var raw any
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("decode Mistral Vibe usage: %w", err)
	}
	if payload := findMistralVibeUsageObject(raw); payload != nil {
		return payload, nil
	}
	return nil, errors.New("Mistral Vibe usage response does not contain a usage percentage")
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

func mistralVibeField(payload map[string]any, names ...string) any {
	for _, name := range names {
		if value, exists := payload[name]; exists {
			return value
		}
	}
	return nil
}

func mistralVibeNumber(value any) (float64, bool) {
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

func mistralVibeResetTime(value any) time.Time {
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

func normalizeMistralVibeCookie(raw string) string {
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

func mistralVibeEnvOrDefault(name string, fallback string) string {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	return value
}
