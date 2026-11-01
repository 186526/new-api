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

func TestFetchMistralVibeQuota(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/code-trpc/apiKey.getUsage" {
			t.Errorf("path = %q, want /api/code-trpc/apiKey.getUsage", r.URL.Path)
		}
		if got := r.URL.Query().Get("batch"); got != "1" {
			t.Errorf("batch = %q, want 1", got)
		}
		if got := r.Header.Get("Cookie"); got != "ory_session_test=abc" {
			t.Errorf("cookie = %q, want the configured session cookie", got)
		}
		if got := r.Header.Get("User-Agent"); got != mistralVibeBrowserUserAgent {
			t.Errorf("user agent = %q, want the browser string", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"result":{"data":{"json":{"usagePercentage":25.5,"paygEnabled":true,"resetAt":"2026-12-01T00:00:00Z"}}}}]`))
	}))
	defer server.Close()

	client := &MistralVibeClient{BaseURL: server.URL, HTTPClient: newMistralVibeHTTPClient()}
	quota, err := client.Fetch(context.Background(), MistralVibeAccount{Channel: "vibe-pro", Cookie: "ory_session_test=abc"})
	if err != nil {
		t.Fatalf("Fetch() error: %v", err)
	}
	if quota.Channel != "vibe-pro" {
		t.Errorf("Channel = %q, want vibe-pro", quota.Channel)
	}
	if quota.UsagePercentage != 25.5 {
		t.Errorf("UsagePercentage = %v, want 25.5", quota.UsagePercentage)
	}
	if !quota.PaygEnabled {
		t.Error("PaygEnabled = false, want true")
	}
	wantReset := time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)
	if !quota.ResetAt.Equal(wantReset) {
		t.Errorf("ResetAt = %v, want %v", quota.ResetAt, wantReset)
	}
}

func TestFetchMistralVibeQuotaRejectsExpiredSession(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "Just a moment...", http.StatusForbidden)
	}))
	defer server.Close()

	client := &MistralVibeClient{BaseURL: server.URL, HTTPClient: newMistralVibeHTTPClient()}
	_, err := client.Fetch(context.Background(), MistralVibeAccount{Channel: "vibe", Cookie: "ory_session_test=abc"})
	if err == nil {
		t.Fatal("Fetch() error = nil, want a rejected-session error")
	}
	var authErr *MistralVibeAuthError
	if !errors.As(err, &authErr) || authErr.Status != http.StatusForbidden {
		t.Fatalf("Fetch() error = %v, want MistralVibeAuthError with status 403", err)
	}
}

func TestFetchMistralVibeQuotaRejectsRedirect(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://auth.mistral.ai/self-service/login/browser", http.StatusFound)
	}))
	defer server.Close()

	client := &MistralVibeClient{BaseURL: server.URL, HTTPClient: newMistralVibeHTTPClient()}
	_, err := client.Fetch(context.Background(), MistralVibeAccount{Channel: "vibe", Cookie: "ory_session_test=abc"})
	var authErr *MistralVibeAuthError
	if !errors.As(err, &authErr) || authErr.Status != http.StatusFound {
		t.Fatalf("Fetch() error = %v, want MistralVibeAuthError with status 302", err)
	}
}

func TestParseMistralVibeUsageAcceptsKnownShapes(t *testing.T) {
	for _, body := range []string{
		`{"usagePercentage":12,"resetAt":"2026-12-01T00:00:00Z"}`,
		`{"usage_percentage":12,"reset_at":"2026-12-01T00:00:00Z"}`,
		`{"result":{"data":{"json":{"usagePercentage":12,"resetAt":"2026-12-01T00:00:00Z"}}}}`,
		`[{"result":{"data":{"json":{"usagePercentage":12,"resetAt":"2026-12-01T00:00:00Z"}}}}]`,
	} {
		payload, err := parseMistralVibeUsage([]byte(body))
		if err != nil {
			t.Fatalf("parseMistralVibeUsage(%s) error: %v", body, err)
		}
		percentage, ok := mistralVibeNumber(mistralVibeField(payload, "usagePercentage", "usage_percentage"))
		if !ok || percentage != 12 {
			t.Errorf("usage percentage = %v, want 12", percentage)
		}
	}
}

func TestParseMistralVibeUsageRejectsMissingPercentage(t *testing.T) {
	if _, err := parseMistralVibeUsage([]byte(`{"result":{"data":{"json":null}}}`)); err == nil {
		t.Fatal("parseMistralVibeUsage() error = nil, want an error")
	}
}

func TestNewMistralVibeQuotaRejectsOutOfRangePercentage(t *testing.T) {
	if _, err := newMistralVibeQuota("vibe", map[string]any{"usagePercentage": 101.0}); err == nil {
		t.Fatal("newMistralVibeQuota() error = nil, want an out-of-range error")
	}
	if _, err := newMistralVibeQuota("vibe", map[string]any{"usagePercentage": -1.0}); err == nil {
		t.Fatal("newMistralVibeQuota() error = nil, want a negative error")
	}
}

func TestLoadMistralVibeAccountsFromFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "accounts.json")
	if err := os.WriteFile(path, []byte(`[
		{"channel":"vibe-pro","cookie":"ory_session_a=1; csrftoken=a"},
		{"channel":"vibe-team","cookie":"ory_session_b=2; csrftoken=b"}
	]`), 0o600); err != nil {
		t.Fatalf("write accounts file: %v", err)
	}
	t.Setenv("MISTRAL_VIBE_QUOTA_ACCOUNTS", "")
	t.Setenv("MISTRAL_VIBE_QUOTA_COOKIE", "")
	t.Setenv("MISTRAL_VIBE_QUOTA_ACCOUNTS_FILE", path)

	accounts, err := LoadMistralVibeAccounts()
	if err != nil {
		t.Fatalf("LoadMistralVibeAccounts() error: %v", err)
	}
	if len(accounts) != 2 {
		t.Fatalf("len(accounts) = %d, want 2", len(accounts))
	}
	if accounts[0].Channel != "vibe-pro" || accounts[1].Channel != "vibe-team" {
		t.Errorf("accounts = %#v, want vibe-pro and vibe-team", accounts)
	}
}

func TestLoadMistralVibeAccountsMissingFileDisablesLookup(t *testing.T) {
	t.Setenv("MISTRAL_VIBE_QUOTA_ACCOUNTS", "")
	t.Setenv("MISTRAL_VIBE_QUOTA_COOKIE", "")
	t.Setenv("MISTRAL_VIBE_QUOTA_ACCOUNTS_FILE", filepath.Join(t.TempDir(), "absent.json"))

	accounts, err := LoadMistralVibeAccounts()
	if err != nil {
		t.Fatalf("LoadMistralVibeAccounts() error: %v", err)
	}
	if len(accounts) != 0 {
		t.Fatalf("len(accounts) = %d, want 0", len(accounts))
	}
}

func TestNormalizeMistralVibeCookie(t *testing.T) {
	got := normalizeMistralVibeCookie(" Cookie: ory_session_a=1 ; ; csrftoken=b ")
	if got != "ory_session_a=1; csrftoken=b" {
		t.Errorf("normalizeMistralVibeCookie() = %q, want the trimmed cookie pair list", got)
	}
	if strings.Contains(got, "Cookie:") {
		t.Error("normalizeMistralVibeCookie() kept the Cookie prefix")
	}
}
