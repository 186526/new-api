package service

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// subscriptionPage reproduces the escaped RSC payload of the console page:
// both allowances sit in one "budget" object inside a script string.
const subscriptionPage = `<html><body><script>self.__next_f.push([1,"\"budget\":{\"usage_percentage\":60.06123824,\"initial_budget\":10,\"currency\":\"USD\",\"reset_at\":\"2026-12-01T00:00:00Z\",\"api_budget\":{\"usage_percentage\":60.06123824,\"initial_budget\":10,\"currency\":\"USD\",\"reset_at\":\"2026-12-01T00:00:00Z\"},\"vibe_budget\":{\"usage_percentage\":100,\"initial_budget\":150,\"currency\":\"USD\",\"reset_at\":\"2026-12-01T00:00:00Z\"}},\"vibe_payg_enabled\":false"])</script></body></html>`

func TestFetchMistralQuotaReadsBothAllowances(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/subscription" {
			t.Errorf("path = %q, want /subscription", r.URL.Path)
		}
		if got := r.Header.Get("Cookie"); got != "ory_session_test=abc" {
			t.Errorf("cookie = %q, want the configured session cookie", got)
		}
		if got := r.Header.Get("User-Agent"); got != mistralBrowserUserAgent {
			t.Errorf("user agent = %q, want the browser string", got)
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(subscriptionPage))
	}))
	defer server.Close()

	client := &MistralQuotaClient{AdminBaseURL: server.URL, VibeBaseURL: server.URL, HTTPClient: newMistralHTTPClient()}
	quota, err := client.Fetch(context.Background(), MistralQuotaAccount{Channel: "studio", Cookie: "ory_session_test=abc"})
	if err != nil {
		t.Fatalf("Fetch() error: %v", err)
	}
	if quota.Channel != "studio" {
		t.Errorf("Channel = %q, want studio", quota.Channel)
	}
	if quota.API == nil || quota.Vibe == nil {
		t.Fatalf("budgets = %#v, want both allowances", quota)
	}
	if quota.API.UsagePercentage != 60.06123824 {
		t.Errorf("API percentage = %v, want 60.06123824", quota.API.UsagePercentage)
	}
	if quota.API.LimitUSD != 10 {
		t.Errorf("API limit = %v, want 10", quota.API.LimitUSD)
	}
	if quota.Vibe.UsagePercentage != 100 || quota.Vibe.LimitUSD != 150 {
		t.Errorf("Vibe budget = %#v, want 100 percent of 150", quota.Vibe)
	}
	wantReset := time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)
	if !quota.API.ResetAt.Equal(wantReset) {
		t.Errorf("ResetAt = %v, want %v", quota.API.ResetAt, wantReset)
	}
}

func TestFetchMistralQuotaRejectsExpiredSession(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "Just a moment...", http.StatusForbidden)
	}))
	defer server.Close()

	client := &MistralQuotaClient{AdminBaseURL: server.URL, VibeBaseURL: server.URL, HTTPClient: newMistralHTTPClient()}
	_, err := client.Fetch(context.Background(), MistralQuotaAccount{Channel: "vibe", Cookie: "ory_session_test=abc"})
	if err == nil {
		t.Fatal("Fetch() error = nil, want a rejected-session error")
	}
	var authErr *MistralQuotaAuthError
	if !errors.As(err, &authErr) || authErr.Status != http.StatusForbidden {
		t.Fatalf("Fetch() error = %v, want MistralQuotaAuthError with status 403", err)
	}
}

func TestFetchMistralQuotaRejectsRedirect(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://auth.mistral.ai/self-service/login/browser", http.StatusFound)
	}))
	defer server.Close()

	client := &MistralQuotaClient{AdminBaseURL: server.URL, VibeBaseURL: server.URL, HTTPClient: newMistralHTTPClient()}
	_, err := client.Fetch(context.Background(), MistralQuotaAccount{Channel: "vibe", Cookie: "ory_session_test=abc"})
	var authErr *MistralQuotaAuthError
	if !errors.As(err, &authErr) || authErr.Status != http.StatusFound {
		t.Fatalf("Fetch() error = %v, want MistralQuotaAuthError with status 302", err)
	}
}

func TestFetchMistralQuotaFallsBackToTheVibeEndpoint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/subscription":
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte(`<html><body>no billing section</body></html>`))
		case "/api/code-trpc/apiKey.getUsage":
			_, _ = w.Write([]byte(`[{"result":{"data":{"json":{"usagePercentage":25.5,"paygEnabled":true,"resetAt":"2026-12-01T00:00:00Z"}}}}]`))
		default:
			t.Errorf("unexpected path %q", r.URL.Path)
		}
	}))
	defer server.Close()

	client := &MistralQuotaClient{AdminBaseURL: server.URL, VibeBaseURL: server.URL, HTTPClient: newMistralHTTPClient()}
	quota, err := client.Fetch(context.Background(), MistralQuotaAccount{
		Channel: "vibe", Cookie: "ory_session_test=abc", VibeLimitUSD: 150,
	})
	if err != nil {
		t.Fatalf("Fetch() error: %v", err)
	}
	if quota.API != nil {
		t.Errorf("API budget = %#v, want nil", quota.API)
	}
	if quota.Vibe == nil || quota.Vibe.UsagePercentage != 25.5 {
		t.Fatalf("Vibe budget = %#v, want 25.5%%", quota.Vibe)
	}
	if quota.Vibe.LimitUSD != 150 {
		t.Errorf("Vibe limit = %v, want the configured monthlyLimitUsd fallback", quota.Vibe.LimitUSD)
	}
	if !quota.PaygEnabled {
		t.Error("PaygEnabled = false, want true")
	}
}

func TestParseMistralVibeUsageAcceptsKnownShapes(t *testing.T) {
	for _, body := range []string{
		`{"usagePercentage":12,"resetAt":"2026-12-01T00:00:00Z"}`,
		`{"usage_percentage":12,"reset_at":"2026-12-01T00:00:00Z"}`,
		`{"result":{"data":{"json":{"usagePercentage":12,"resetAt":"2026-12-01T00:00:00Z"}}}}`,
		`[{"result":{"data":{"json":{"usagePercentage":12,"resetAt":"2026-12-01T00:00:00Z"}}}}]`,
	} {
		budget, _, err := parseMistralVibeUsage([]byte(body))
		if err != nil {
			t.Fatalf("parseMistralVibeUsage(%s) error: %v", body, err)
		}
		if budget.UsagePercentage != 12 {
			t.Errorf("usage percentage = %v, want 12", budget.UsagePercentage)
		}
	}
}

func TestParseMistralVibeUsageRejectsMissingPercentage(t *testing.T) {
	if _, _, err := parseMistralVibeUsage([]byte(`{"result":{"data":{"json":null}}}`)); err == nil {
		t.Fatal("parseMistralVibeUsage() error = nil, want an error")
	}
}

func TestParseMistralSubscriptionNeedsABudgetObject(t *testing.T) {
	if _, _, _, err := parseMistralSubscription([]byte(`<html>nothing</html>`)); err == nil {
		t.Fatal("parseMistralSubscription() error = nil, want an error")
	}
}

func TestBudgetScopeForKey(t *testing.T) {
	account := MistralQuotaAccount{VibeKey: "mstrl_vibe", StudioKey: "mstrl_studio"}
	if got := account.BudgetScopeForKey("mstrl_studio"); got != MistralBudgetScopeAPI {
		t.Errorf("studio key scope = %q, want api", got)
	}
	if got := account.BudgetScopeForKey("mstrl_vibe"); got != MistralBudgetScopeVibe {
		t.Errorf("vibe key scope = %q, want vibe", got)
	}
	if got := account.BudgetScopeForKey("mstrl_unknown"); got != "" {
		t.Errorf("unknown key scope = %q, want empty", got)
	}
}

func TestLoadMistralQuotaAccountsFromFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "accounts.json")
	if err := os.WriteFile(path, []byte(`[
		{"channel":"vibe-pro","cookie":"ory_session_a=1; csrftoken=a","vibeKey":"mstrl_v","studioKey":"mstrl_s","monthlyLimitUsd":150,"apiLimitUsd":15},
		{"channel":"vibe-team","cookie":"ory_session_b=2; csrftoken=b"}
	]`), 0o600); err != nil {
		t.Fatalf("write accounts file: %v", err)
	}
	t.Setenv("MISTRAL_VIBE_QUOTA_ACCOUNTS", "")
	t.Setenv("MISTRAL_VIBE_QUOTA_COOKIE", "")
	t.Setenv("MISTRAL_VIBE_QUOTA_ACCOUNTS_FILE", path)

	accounts, err := LoadMistralQuotaAccounts()
	if err != nil {
		t.Fatalf("LoadMistralQuotaAccounts() error: %v", err)
	}
	if len(accounts) != 2 {
		t.Fatalf("len(accounts) = %d, want 2", len(accounts))
	}
	if accounts[0].Channel != "vibe-pro" || accounts[1].Channel != "vibe-team" {
		t.Errorf("accounts = %#v, want vibe-pro and vibe-team", accounts)
	}
	if accounts[0].VibeKey != "mstrl_v" || accounts[0].StudioKey != "mstrl_s" {
		t.Errorf("keys = %q/%q, want the configured pair", accounts[0].VibeKey, accounts[0].StudioKey)
	}
	if accounts[0].VibeLimitUSD != 150 || accounts[0].APILimitUSD != 15 {
		t.Errorf("limits = %v/%v, want 150/15", accounts[0].VibeLimitUSD, accounts[0].APILimitUSD)
	}
}

func TestLoadMistralQuotaAccountsMissingFileDisablesLookup(t *testing.T) {
	t.Setenv("MISTRAL_VIBE_QUOTA_ACCOUNTS", "")
	t.Setenv("MISTRAL_VIBE_QUOTA_COOKIE", "")
	t.Setenv("MISTRAL_VIBE_QUOTA_ACCOUNTS_FILE", filepath.Join(t.TempDir(), "absent.json"))

	accounts, err := LoadMistralQuotaAccounts()
	if err != nil {
		t.Fatalf("LoadMistralQuotaAccounts() error: %v", err)
	}
	if len(accounts) != 0 {
		t.Fatalf("len(accounts) = %d, want 0", len(accounts))
	}
}

func TestNormalizeMistralCookie(t *testing.T) {
	got := normalizeMistralCookie(" Cookie: ory_session_a=1 ; ; csrftoken=b ")
	if got != "ory_session_a=1; csrftoken=b" {
		t.Errorf("normalizeMistralCookie() = %q, want the trimmed cookie pair list", got)
	}
	if strings.Contains(got, "Cookie:") {
		t.Error("normalizeMistralCookie() kept the Cookie prefix")
	}
}
