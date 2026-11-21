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

// consoleBudgetAnswer is the merged allowance as the console trpc endpoint
// reports it: one "Included Vibe & Studio usage" figure with the plan amount.
const consoleBudgetAnswer = `{"result":{"data":{"json":{"usage_percentage":65.79,"initial_budget":150,"currency":"USD","reset_at":"2026-12-01T00:00:00Z","payg_enabled":false}}}}`

// vibeUsageAnswer is the same percentage as the Vibe app reports it, without a
// plan amount.
const vibeUsageAnswer = `[{"result":{"data":{"json":{"usagePercentage":65.79,"paygEnabled":false,"resetAt":"2026-12-01T00:00:00Z"}}}}]`

// keyBudgetAnswer is what the public billing endpoint answers to any key of an
// Education plan account: the free plan's 10 USD, fully used, split into the two
// sides the endpoint still reports. It is wrong for the plan and must never
// replace the console answer.
const keyBudgetAnswer = `{"vibe_budget":{"usage_percentage":100.0,"initial_budget":10.0,"currency":"USD","reset_at":"2026-12-01T00:00:00Z","payg_enabled":false},"api_budget":{"usage_percentage":100.0,"initial_budget":10.0,"currency":"USD","reset_at":"2026-12-01T00:00:00Z","payg_enabled":false}}`

// newMistralSourceServer serves one answer per path, and records the order the
// paths were asked for so a test can require the chain.
func newMistralSourceServer(t *testing.T, answers map[string]string) (*httptest.Server, *[]string) {
	t.Helper()
	var asked []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked = append(asked, r.URL.Path)
		answer, ok := answers[r.URL.Path]
		if !ok {
			http.Error(w, "unexpected path", http.StatusInternalServerError)
			return
		}
		_, _ = w.Write([]byte(answer))
	}))
	t.Cleanup(server.Close)
	return server, &asked
}

func newMistralTestClient(serverURL string) *MistralQuotaClient {
	return &MistralQuotaClient{
		AdminBaseURL: serverURL,
		VibeBaseURL:  serverURL,
		APIBaseURL:   serverURL,
		HTTPClient:   newMistralHTTPClient(),
	}
}

// TestFetchMistralQuotaPrefersTheConsoleBudget requires the merged allowance to
// come from the console trpc endpoint when the account has a session, and the
// public billing endpoint never to be asked: that endpoint reported 10 USD on
// this plan, so reading it would replace the real plan amount.
func TestFetchMistralQuotaPrefersTheConsoleBudget(t *testing.T) {
	server, asked := newMistralSourceServer(t, map[string]string{
		mistralConsoleBudgetPath: consoleBudgetAnswer,
		mistralVibeUsagePath:     vibeUsageAnswer,
		mistralBillingBudgetPath: keyBudgetAnswer,
	})

	quota, err := newMistralTestClient(server.URL).Fetch(context.Background(), MistralQuotaAccount{
		Channel: "Mistral (pro)", Cookie: "ory_session_test=abc",
		VibeKey: "mstrl_vibe", StudioKey: "mstrl_studio", MonthlyLimitUSD: 150,
	})
	if err != nil {
		t.Fatalf("Fetch() error: %v", err)
	}
	if quota.Allowance == nil {
		t.Fatalf("Allowance = nil, want the console answer")
	}
	if quota.Allowance.UsagePercentage != 65.79 {
		t.Errorf("usage percentage = %v, want 65.79", quota.Allowance.UsagePercentage)
	}
	if quota.Allowance.LimitUSD != 150 {
		t.Errorf("plan amount = %v, want the console's 150", quota.Allowance.LimitUSD)
	}
	wantReset := time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)
	if !quota.Allowance.ResetAt.Equal(wantReset) {
		t.Errorf("ResetAt = %v, want %v", quota.Allowance.ResetAt, wantReset)
	}
	if want := []string{mistralConsoleBudgetPath}; !equalPaths(*asked, want) {
		t.Errorf("paths asked = %v, want %v", *asked, want)
	}
}

// TestFetchMistralQuotaFallsThroughARejectedConsoleSession requires a console
// answer that redirects to the login page to fall through to the next source
// instead of being reported as the allowance: an expired cookie answers 302 with
// no body, and a refresh that trusts it reports a zero balance.
func TestFetchMistralQuotaFallsThroughARejectedConsoleSession(t *testing.T) {
	var asked []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked = append(asked, r.URL.Path)
		switch r.URL.Path {
		case mistralConsoleBudgetPath:
			http.Redirect(w, r, "https://auth.mistral.ai/self-service/login/browser", http.StatusFound)
		case mistralVibeUsagePath:
			_, _ = w.Write([]byte(vibeUsageAnswer))
		default:
			http.Error(w, "the key budget endpoint must not answer an account with a session", http.StatusInternalServerError)
		}
	}))
	defer server.Close()

	quota, err := newMistralTestClient(server.URL).Fetch(context.Background(), MistralQuotaAccount{
		Channel: "Mistral (pro)", Cookie: "ory_session_test=abc", MonthlyLimitUSD: 150,
	})
	if err != nil {
		t.Fatalf("Fetch() error: %v", err)
	}
	if quota.Allowance == nil || quota.Allowance.UsagePercentage != 65.79 {
		t.Fatalf("Allowance = %#v, want the Vibe app's 65.79%%", quota.Allowance)
	}
	if quota.Allowance.LimitUSD != 150 {
		t.Errorf("plan amount = %v, want the configured 150 because the Vibe app reports no amount", quota.Allowance.LimitUSD)
	}
	if want := []string{mistralConsoleBudgetPath, mistralVibeUsagePath}; !equalPaths(asked, want) {
		t.Errorf("paths asked = %v, want %v", asked, want)
	}
}

// TestFetchMistralQuotaReportsARejectedSessionWhenNoSourceAnswers requires both
// console sources being refused to surface the auth error, so the refresh backs
// off and names the cookie instead of publishing a zero balance.
func TestFetchMistralQuotaReportsARejectedSessionWhenNoSourceAnswers(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://auth.mistral.ai/self-service/login/browser", http.StatusFound)
	}))
	defer server.Close()

	_, err := newMistralTestClient(server.URL).Fetch(context.Background(), MistralQuotaAccount{
		Channel: "Mistral (pro)", Cookie: "ory_session_test=abc", MonthlyLimitUSD: 150,
	})
	var authErr *MistralQuotaAuthError
	if !errors.As(err, &authErr) || authErr.Status != http.StatusFound {
		t.Fatalf("Fetch() error = %v, want MistralQuotaAuthError with status 302", err)
	}
}

// TestFetchMistralQuotaReadsTheKeyBudgetOnlyWithoutACookie requires the public
// billing endpoint to stay reachable for an account that has no console session
// at all, and to be reported without asking either console source.
func TestFetchMistralQuotaReadsTheKeyBudgetOnlyWithoutACookie(t *testing.T) {
	server, asked := newMistralSourceServer(t, map[string]string{
		mistralConsoleBudgetPath: consoleBudgetAnswer,
		mistralVibeUsagePath:     vibeUsageAnswer,
		mistralBillingBudgetPath: keyBudgetAnswer,
	})

	quota, err := newMistralTestClient(server.URL).Fetch(context.Background(), MistralQuotaAccount{
		Channel: "Mistral (key only)", StudioKey: "mstrl_studio", MonthlyLimitUSD: 150,
	})
	if err != nil {
		t.Fatalf("Fetch() error: %v", err)
	}
	if quota.Allowance == nil {
		t.Fatal("Allowance = nil, want the key budget answer")
	}
	if quota.Allowance.LimitUSD != 150 {
		t.Errorf("plan amount = %v, want the configured 150 to win over the endpoint's 10", quota.Allowance.LimitUSD)
	}
	if want := []string{mistralBillingBudgetPath}; !equalPaths(*asked, want) {
		t.Errorf("paths asked = %v, want %v", *asked, want)
	}
}

// TestFetchMistralQuotaSendsTheSessionWithoutQuotes requires the quotes a
// console session is copied with to be dropped. The same account answers the
// login redirect with the quotes and the allowance without them, so sending them
// back fails the refresh on a cookie that is still valid.
func TestFetchMistralQuotaSendsTheSessionWithoutQuotes(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie := r.Header.Get("Cookie")
		if strings.Contains(cookie, `"`) {
			http.Redirect(w, r, "https://auth.mistral.ai/self-service/login/browser", http.StatusFound)
			return
		}
		if !strings.Contains(cookie, "ory_session_test=abc123") {
			t.Errorf("cookie = %q, want the unquoted session value", cookie)
		}
		_, _ = w.Write([]byte(consoleBudgetAnswer))
	}))
	defer server.Close()

	quota, err := newMistralTestClient(server.URL).Fetch(context.Background(), MistralQuotaAccount{
		Channel: "Mistral (pro)",
		Cookie:  `Cookie: csrftoken=vX1DZwu0Bsw1ZJBoHuhAAbsoG91EVQvD; ory_session_test="abc123"`,
	})
	if err != nil {
		t.Fatalf("Fetch() error: %v", err)
	}
	if quota.Allowance == nil || quota.Allowance.UsagePercentage != 65.79 {
		t.Fatalf("Allowance = %#v, want the console answer", quota.Allowance)
	}
}

func equalPaths(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for index := range got {
		if got[index] != want[index] {
			return false
		}
	}
	return true
}

func TestParseMistralAllowanceAcceptsKnownShapes(t *testing.T) {
	cases := []struct {
		name       string
		body       string
		percentage float64
		limit      float64
	}{
		{
			name:       "the console trpc envelope",
			body:       consoleBudgetAnswer,
			percentage: 65.79,
			limit:      150,
		},
		{
			name:       "the console envelope carrying a budget object",
			body:       `{"result":{"data":{"json":{"budget":{"usage_percentage":65.79,"initial_budget":150,"reset_at":"$D2026-12-01T00:00:00.000Z"}}}}}`,
			percentage: 65.79,
			limit:      150,
		},
		{
			name:       "the Vibe app batch answer",
			body:       vibeUsageAnswer,
			percentage: 65.79,
		},
		{
			name:       "a bare object",
			body:       `{"usage_percentage":12,"initial_budget":15}`,
			percentage: 12,
			limit:      15,
		},
		{
			name:       "a numeric string",
			body:       `{"usage_percentage":"12.5","initial_budget":"15"}`,
			percentage: 12.5,
			limit:      15,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			allowance, _, err := parseMistralAllowance([]byte(testCase.body))
			if err != nil {
				t.Fatalf("parseMistralAllowance() error: %v", err)
			}
			if allowance.UsagePercentage != testCase.percentage {
				t.Errorf("usage percentage = %v, want %v", allowance.UsagePercentage, testCase.percentage)
			}
			if allowance.LimitUSD != testCase.limit {
				t.Errorf("plan amount = %v, want %v", allowance.LimitUSD, testCase.limit)
			}
		})
	}
}

func TestParseMistralAllowanceRejectsMissingPercentage(t *testing.T) {
	for _, body := range []string{
		`{"result":{"data":{"json":null}}}`,
		`<html>nothing</html>`,
		`{}`,
	} {
		if _, _, err := parseMistralAllowance([]byte(body)); err == nil {
			t.Fatalf("parseMistralAllowance(%s) error = nil, want an error", body)
		}
	}
}

// TestParseMistralBillingBudgetPrefersTheVibeSide requires the merged allowance
// of the public endpoint to be the side the console renders, and the API side to
// be read only when the answer carries nothing else.
func TestParseMistralBillingBudgetPrefersTheVibeSide(t *testing.T) {
	allowance, payg, err := parseMistralBillingBudget([]byte(keyBudgetAnswer))
	if err != nil {
		t.Fatalf("parseMistralBillingBudget() error: %v", err)
	}
	if allowance.UsagePercentage != 100 || allowance.LimitUSD != 10 {
		t.Errorf("allowance = %#v, want the Vibe side's 100%% of 10", allowance)
	}
	if payg {
		t.Error("PaygEnabled = true, want false")
	}

	allowance, _, err = parseMistralBillingBudget([]byte(`{"api_budget":{"usage_percentage":40,"initial_budget":15}}`))
	if err != nil {
		t.Fatalf("parseMistralBillingBudget() error: %v", err)
	}
	if allowance.UsagePercentage != 40 || allowance.LimitUSD != 15 {
		t.Errorf("allowance = %#v, want the API side as the only one reported", allowance)
	}

	if _, _, err := parseMistralBillingBudget([]byte(`{}`)); err == nil {
		t.Fatal("parseMistralBillingBudget() error = nil, want an error when no side is reported")
	}
}

func TestLoadMistralQuotaAccountsFromFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "accounts.json")
	if err := os.WriteFile(path, []byte(`[
		{"channel":"vibe-pro","cookie":"ory_session_a=1; csrftoken=a","vibeKey":"mstrl_v","studioKey":"mstrl_s","monthlyLimitUsd":150},
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
	if accounts[0].MonthlyLimitUSD != 150 {
		t.Errorf("monthly limit = %v, want 150", accounts[0].MonthlyLimitUSD)
	}
}

// TestLoadMistralQuotaAccountsRequiresASession requires the configuration to keep
// naming a session cookie. An account that could be configured with a key alone
// would read the public billing endpoint, which reported the free plan's 10 USD
// on every plan measured, and would report nothing left without any error.
func TestLoadMistralQuotaAccountsRequiresASession(t *testing.T) {
	path := filepath.Join(t.TempDir(), "accounts.json")
	if err := os.WriteFile(path, []byte(`[{"channel":"key-only","vibeKey":"mstrl_v"}]`), 0o600); err != nil {
		t.Fatalf("write accounts file: %v", err)
	}
	t.Setenv("MISTRAL_VIBE_QUOTA_ACCOUNTS", "")
	t.Setenv("MISTRAL_VIBE_QUOTA_COOKIE", "")
	t.Setenv("MISTRAL_VIBE_QUOTA_ACCOUNTS_FILE", path)

	if _, err := LoadMistralQuotaAccounts(); err == nil {
		t.Fatal("LoadMistralQuotaAccounts() error = nil, want an account without a cookie to be rejected")
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

// TestNormalizeMistralCookieDropsTheQuotesOfAValue requires one pair of quotes
// around a value to be removed and nothing else of the value to be touched: the
// session value is quoted where it is copied from, and sending the quotes back
// makes the session unreadable.
func TestNormalizeMistralCookieDropsTheQuotesOfAValue(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{
			name: "a quoted session value",
			raw:  `csrftoken=vX1DZwu0Bsw1ZJBoHuhAAbsoG91EVQvD; ory_session_test="abc123"`,
			want: `csrftoken=vX1DZwu0Bsw1ZJBoHuhAAbsoG91EVQvD; ory_session_test=abc123`,
		},
		{
			name: "a quote inside the value stays",
			raw:  `ory_session_test="ab"cd"`,
			want: `ory_session_test=ab"cd`,
		},
		{
			name: "an unquoted value is untouched",
			raw:  `ory_session_test=abc123`,
			want: `ory_session_test=abc123`,
		},
		{
			name: "a lone quote is untouched",
			raw:  `ory_session_test=abc"`,
			want: `ory_session_test=abc"`,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := normalizeMistralCookie(testCase.raw); got != testCase.want {
				t.Errorf("normalizeMistralCookie(%q) = %q, want %q", testCase.raw, got, testCase.want)
			}
		})
	}
}

// TestApplyMistralAccountLimitsKeepsAReportedAmount requires a plan amount the
// console reported to survive, and the configured amount to fill in only a
// missing one: the Vibe app reports the percentage alone.
func TestApplyMistralAccountLimitsKeepsAReportedAmount(t *testing.T) {
	reported := &MistralBudget{UsagePercentage: 65.79, LimitUSD: 150}
	quota := &MistralQuota{Allowance: reported}
	applyMistralAccountLimits(MistralQuotaAccount{MonthlyLimitUSD: 10}, quota)
	if reported.LimitUSD != 150 {
		t.Errorf("plan amount = %v, want the console's 150 to survive a configured 10", reported.LimitUSD)
	}

	missing := &MistralBudget{UsagePercentage: 65.79}
	quota = &MistralQuota{Allowance: missing}
	applyMistralAccountLimits(MistralQuotaAccount{MonthlyLimitUSD: 150}, quota)
	if missing.LimitUSD != 150 {
		t.Errorf("plan amount = %v, want the configured 150", missing.LimitUSD)
	}

	outOfRange := &MistralBudget{UsagePercentage: 140, LimitUSD: 150}
	quota = &MistralQuota{Allowance: outOfRange}
	applyMistralAccountLimits(MistralQuotaAccount{}, quota)
	if outOfRange.UsagePercentage != 100 {
		t.Errorf("usage percentage = %v, want it held at 100", outOfRange.UsagePercentage)
	}
}
