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

// paidKeyBudgetAnswer is the answer the channel's own key gave on 2026-11-21:
// 1.238795712% of the merged 150 USD allowance used, which leaves about 148.14
// USD, next to an API side of 0.00018766% of 15 USD.
const paidKeyBudgetAnswer = `{"vibe_budget":{"usage_percentage":1.238795712,"initial_budget":150.0,"currency":"USD","reset_at":"2026-12-01T00:00:00Z","payg_enabled":false},"api_budget":{"usage_percentage":0.00018766,"initial_budget":15.0,"currency":"USD","reset_at":"2026-12-01T00:00:00Z","payg_enabled":false}}`

// newMistralSourceServer serves one answer per path, and records the order the
// paths were asked for so a test can require the chain. A path mapped to an empty
// answer is refused with 401, which is what a key the account no longer carries
// answers.
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
		if answer == "" {
			http.Error(w, `{"message":"Unauthorized"}`, http.StatusUnauthorized)
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

// TestFetchMistralQuotaReadsTheKeyBudgetFirst requires the merged allowance to
// come from the public billing endpoint when the account has a key, even with a
// console session present: the console answers a dead session with 200 and the
// login page, so it cannot be trusted to report the allowance, and the key
// endpoint is the only source that does.
func TestFetchMistralQuotaReadsTheKeyBudgetFirst(t *testing.T) {
	server, asked := newMistralSourceServer(t, map[string]string{
		mistralConsoleBudgetPath: consoleBudgetAnswer,
		mistralVibeUsagePath:     vibeUsageAnswer,
		mistralBillingBudgetPath: paidKeyBudgetAnswer,
	})

	quota, err := newMistralTestClient(server.URL).Fetch(context.Background(), MistralQuotaAccount{
		Channel: "Mistral (pro)", Cookie: "ory_session_test=abc",
		VibeKey: "mstrl_vibe", StudioKey: "mstrl_studio", MonthlyLimitUSD: 150,
	})
	if err != nil {
		t.Fatalf("Fetch() error: %v", err)
	}
	if quota.Allowance == nil {
		t.Fatalf("Allowance = nil, want the key budget answer")
	}
	if quota.Allowance.UsagePercentage != 1.238795712 {
		t.Errorf("usage percentage = %v, want the key endpoint's 1.238795712", quota.Allowance.UsagePercentage)
	}
	if quota.Allowance.LimitUSD != 150 {
		t.Errorf("plan amount = %v, want the 150 the key endpoint reports", quota.Allowance.LimitUSD)
	}
	wantReset := time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)
	if !quota.Allowance.ResetAt.Equal(wantReset) {
		t.Errorf("ResetAt = %v, want %v", quota.Allowance.ResetAt, wantReset)
	}
	if want := []string{mistralBillingBudgetPath}; !equalPaths(*asked, want) {
		t.Errorf("paths asked = %v, want %v", *asked, want)
	}
}

// TestFetchMistralQuotaReadsTheChannelKeyOverTheFileKey requires the key the
// channel calls the upstream with to decide the allowance of that channel. On
// 2026-11-21 the accounts file named a key that answered the free plan's 100% of
// 10 USD, while the key of channel 16 answered 1.238795712% of 150 USD, about
// 148.14 USD left; reading the file key reported nothing left and the balance
// sweep disabled the channel.
func TestFetchMistralQuotaReadsTheChannelKeyOverTheFileKey(t *testing.T) {
	server, asked := newMistralSourceServer(t, map[string]string{
		mistralConsoleBudgetPath: consoleBudgetAnswer,
		mistralVibeUsagePath:     vibeUsageAnswer,
		mistralBillingBudgetPath: paidKeyBudgetAnswer,
	})

	quota, err := newMistralTestClient(server.URL).Fetch(context.Background(), MistralQuotaAccount{
		Channel: "Mistral Vibe (xu.bo)", Cookie: "ory_session_test=stale",
		VibeKey: "mstrl_old", StudioKey: "", ChannelKey: "mstrl_channel", MonthlyLimitUSD: 150,
	})
	if err != nil {
		t.Fatalf("Fetch() error: %v", err)
	}
	if quota.Allowance == nil {
		t.Fatalf("Allowance = nil, want the channel key's answer")
	}
	remaining := quota.Allowance.LimitUSD * (1 - quota.Allowance.UsagePercentage/100)
	if want := 148.14; remaining < want-0.01 || remaining > want+0.01 {
		t.Errorf("remaining = %v USD, want about %v USD", remaining, want)
	}
	if want := []string{mistralBillingBudgetPath}; !equalPaths(*asked, want) {
		t.Errorf("paths asked = %v, want %v", *asked, want)
	}
}

// TestFetchMistralQuotaFallsThroughARejectedKey requires a key the endpoint
// refuses to fall through to the console sources instead of being reported as an
// allowance of zero: the key of a channel may have been rotated, and the cookie
// still reads the plan.
func TestFetchMistralQuotaFallsThroughARejectedKey(t *testing.T) {
	server, asked := newMistralSourceServer(t, map[string]string{
		mistralConsoleBudgetPath: consoleBudgetAnswer,
		mistralVibeUsagePath:     vibeUsageAnswer,
		mistralBillingBudgetPath: "",
	})

	quota, err := newMistralTestClient(server.URL).Fetch(context.Background(), MistralQuotaAccount{
		Channel: "Mistral (pro)", Cookie: "ory_session_test=abc", ChannelKey: "mstrl_rotated",
	})
	if err != nil {
		t.Fatalf("Fetch() error: %v", err)
	}
	if quota.Allowance == nil || quota.Allowance.UsagePercentage != 65.79 {
		t.Fatalf("Allowance = %#v, want the console's 65.79%%", quota.Allowance)
	}
	if want := []string{mistralBillingBudgetPath, mistralConsoleBudgetPath}; !equalPaths(*asked, want) {
		t.Errorf("paths asked = %v, want the key endpoint then the console", *asked)
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
			http.Error(w, "an account without a key must not reach the key budget endpoint", http.StatusInternalServerError)
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
	if authErr.Credential != mistralCredentialSession {
		t.Errorf("credential = %q, want the console session to be named", authErr.Credential)
	}
}

// TestFetchMistralQuotaReportsARejectedKeyWhenNoSourceAnswers requires a refused
// key with no console session to surface the auth error, so the refresh backs off
// instead of publishing a zero balance for a channel whose key was rotated.
func TestFetchMistralQuotaReportsARejectedKeyWhenNoSourceAnswers(t *testing.T) {
	server, _ := newMistralSourceServer(t, map[string]string{mistralBillingBudgetPath: ""})

	_, err := newMistralTestClient(server.URL).Fetch(context.Background(), MistralQuotaAccount{
		Channel: "Mistral (key only)", ChannelKey: "mstrl_rotated", MonthlyLimitUSD: 150,
	})
	var authErr *MistralQuotaAuthError
	if !errors.As(err, &authErr) || authErr.Status != http.StatusUnauthorized {
		t.Fatalf("Fetch() error = %v, want MistralQuotaAuthError with status 401", err)
	}
	if authErr.Credential != mistralCredentialKey {
		t.Errorf("credential = %q, want the API key to be named", authErr.Credential)
	}
}

// TestFetchMistralQuotaReadsTheKeyBudgetWithoutACookie requires the public
// billing endpoint to answer an account that has a key and no console session,
// without asking either console source.
func TestFetchMistralQuotaReadsTheKeyBudgetWithoutACookie(t *testing.T) {
	server, asked := newMistralSourceServer(t, map[string]string{
		mistralConsoleBudgetPath: consoleBudgetAnswer,
		mistralVibeUsagePath:     vibeUsageAnswer,
		mistralBillingBudgetPath: paidKeyBudgetAnswer,
	})

	quota, err := newMistralTestClient(server.URL).Fetch(context.Background(), MistralQuotaAccount{
		Channel: "Mistral (key only)", StudioKey: "mstrl_studio",
	})
	if err != nil {
		t.Fatalf("Fetch() error: %v", err)
	}
	if quota.Allowance == nil {
		t.Fatal("Allowance = nil, want the key budget answer")
	}
	if quota.Allowance.LimitUSD != 150 {
		t.Errorf("plan amount = %v, want the 150 the key endpoint reports", quota.Allowance.LimitUSD)
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
// of the public endpoint to be the Vibe side the console renders, and the API
// side to be read only when the answer carries no Vibe side. The top-level
// object holds no percentage, so its numbers can never become the allowance.
func TestParseMistralBillingBudgetPrefersTheVibeSide(t *testing.T) {
	allowance, payg, err := parseMistralBillingBudget([]byte(paidKeyBudgetAnswer))
	if err != nil {
		t.Fatalf("parseMistralBillingBudget() error: %v", err)
	}
	if allowance.UsagePercentage != 1.238795712 || allowance.LimitUSD != 150 {
		t.Errorf("allowance = %#v, want the Vibe side's 1.238795712%% of 150", allowance)
	}
	if payg {
		t.Error("PaygEnabled = true, want false")
	}

	allowance, _, err = parseMistralBillingBudget([]byte(`{"vibe_budget":{},"api_budget":{"usage_percentage":40,"initial_budget":15}}`))
	if err != nil {
		t.Fatalf("parseMistralBillingBudget() error: %v", err)
	}
	if allowance.UsagePercentage != 40 || allowance.LimitUSD != 15 {
		t.Errorf("allowance = %#v, want the API side as the only one reported", allowance)
	}

	if _, _, err := parseMistralBillingBudget([]byte(`{"usage_percentage":0,"initial_budget":0}`)); err == nil {
		t.Fatal("parseMistralBillingBudget() error = nil, want an error when neither side is reported")
	}
	if _, _, err := parseMistralBillingBudget([]byte(`{}`)); err == nil {
		t.Fatal("parseMistralBillingBudget() error = nil, want an error when no side is reported")
	}
}

// TestLoadMistralQuotaAccountsFromFile reads a file that mixes a session entry
// with a key-only entry: the key endpoint reads the allowance with a key, so an
// account that names one needs no cookie.
func TestLoadMistralQuotaAccountsFromFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "accounts.json")
	if err := os.WriteFile(path, []byte(`[
		{"channel":"vibe-pro","cookie":"ory_session_a=1; csrftoken=a","vibeKey":"mstrl_v","studioKey":"mstrl_s","monthlyLimitUsd":150},
		{"channel":"vibe-team","vibeKey":"mstrl_team"}
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
	if accounts[1].Cookie != "" || accounts[1].VibeKey != "mstrl_team" {
		t.Errorf("key-only entry = %#v, want the key and no cookie", accounts[1])
	}
}

// TestLoadMistralQuotaAccountsRequiresACredential requires an entry that names
// neither a cookie nor a key to be rejected: such an account has no credential
// that reads the allowance, so loading it would publish nothing in silence.
func TestLoadMistralQuotaAccountsRequiresACredential(t *testing.T) {
	path := filepath.Join(t.TempDir(), "accounts.json")
	if err := os.WriteFile(path, []byte(`[{"channel":"no-credential","monthlyLimitUsd":150}]`), 0o600); err != nil {
		t.Fatalf("write accounts file: %v", err)
	}
	t.Setenv("MISTRAL_VIBE_QUOTA_ACCOUNTS", "")
	t.Setenv("MISTRAL_VIBE_QUOTA_COOKIE", "")
	t.Setenv("MISTRAL_VIBE_QUOTA_ACCOUNTS_FILE", path)

	if _, err := LoadMistralQuotaAccounts(); err == nil {
		t.Fatal("LoadMistralQuotaAccounts() error = nil, want an entry without a credential to be rejected")
	} else if !strings.Contains(err.Error(), "neither a console session nor an API key") {
		t.Errorf("error = %v, want a message naming the missing credential", err)
	}
}

// TestLoadMistralQuotaAccountsFromEnvWithoutACookie requires the single-account
// environment source to accept a key alone, the same way the file does.
func TestLoadMistralQuotaAccountsFromEnvWithoutACookie(t *testing.T) {
	t.Setenv("MISTRAL_VIBE_QUOTA_ACCOUNTS", "")
	t.Setenv("MISTRAL_VIBE_QUOTA_ACCOUNTS_FILE", "")
	t.Setenv("MISTRAL_VIBE_QUOTA_COOKIE", "")
	t.Setenv("MISTRAL_VIBE_QUOTA_CHANNEL", "Mistral Vibe (xu.bo)")
	t.Setenv("MISTRAL_VIBE_QUOTA_VIBE_KEY", "mstrl_v")
	t.Setenv("MISTRAL_VIBE_QUOTA_STUDIO_KEY", "")

	accounts, err := LoadMistralQuotaAccounts()
	if err != nil {
		t.Fatalf("LoadMistralQuotaAccounts() error: %v", err)
	}
	if len(accounts) != 1 {
		t.Fatalf("len(accounts) = %d, want 1", len(accounts))
	}
	if accounts[0].Channel != "Mistral Vibe (xu.bo)" || accounts[0].VibeKey != "mstrl_v" {
		t.Errorf("account = %#v, want the configured channel and key", accounts[0])
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
