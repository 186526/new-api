package controller

import (
	"testing"
	"time"

	"github.com/QuantumNous/new-api/model"
)

func TestCommandCodeCreditsURLUsesHostRoot(t *testing.T) {
	cases := []struct {
		name    string
		baseURL string
		want    string
		wantErr bool
	}{
		{
			name:    "provider prefix is dropped",
			baseURL: "https://api.commandcode.ai/provider/v1",
			want:    "https://api.commandcode.ai/alpha/billing/credits",
		},
		{
			name:    "trailing slash is ignored",
			baseURL: "https://api.commandcode.ai/provider/v1/",
			want:    "https://api.commandcode.ai/alpha/billing/credits",
		},
		{
			name:    "port and scheme survive",
			baseURL: "http://api.commandcode.ai:8080/provider/v1",
			want:    "http://api.commandcode.ai:8080/alpha/billing/credits",
		},
		{
			name:    "missing host is rejected",
			baseURL: "/provider/v1",
			wantErr: true,
		},
		{
			name:    "empty value is rejected",
			baseURL: "  ",
			wantErr: true,
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got, err := commandCodeCreditsURL(testCase.baseURL)
			if testCase.wantErr {
				if err == nil {
					t.Fatalf("expected an error for %q, got %q", testCase.baseURL, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error for %q: %v", testCase.baseURL, err)
			}
			if got != testCase.want {
				t.Fatalf("URL for %q = %q, want %q", testCase.baseURL, got, testCase.want)
			}
		})
	}
}

func TestCommandCodeCreditsRemainingSumsEveryPool(t *testing.T) {
	credits := commandCodeCredits{}
	credits.Credits.MonthlyCredits = 61.5
	credits.Credits.PurchasedCredits = 20
	credits.Credits.FreeCredits = 1.25

	if got, want := credits.remainingUSD(), 82.75; got != want {
		t.Fatalf("remainingUSD = %v, want %v", got, want)
	}
}

func TestCommandCodeCreditsWindowConvertsMillisecondReset(t *testing.T) {
	window := commandCodeCreditsWindow(commandCodeWindowFiveHour, commandCodeCreditWindow{
		Used:    1.5,
		Cap:     14,
		ResetAt: 1793303693727,
	})
	if window.Name != commandCodeWindowFiveHour {
		t.Fatalf("window name = %q, want %q", window.Name, commandCodeWindowFiveHour)
	}
	if window.UsedUSD != 1.5 || window.CapUSD != 14 {
		t.Fatalf("window amounts = %v/%v, want 1.5/14", window.UsedUSD, window.CapUSD)
	}
	want := time.UnixMilli(1793303693727)
	if !window.ResetAt.Equal(want) {
		t.Fatalf("reset = %v, want %v", window.ResetAt, want)
	}
}

func TestCommandCodeCreditsWindowWithoutResetStaysZero(t *testing.T) {
	window := commandCodeCreditsWindow(commandCodeWindowWeekly, commandCodeCreditWindow{Used: 2, Cap: 35})
	if !window.ResetAt.IsZero() {
		t.Fatalf("reset = %v, want the zero time", window.ResetAt)
	}
}

func TestCommandCodeCredentialsListsEveryKey(t *testing.T) {
	channel := &model.Channel{Key: "first-key\n\n  second-key  "}

	keys := commandCodeCredentials(channel)
	if len(keys) != 2 || keys[0] != "first-key" || keys[1] != "second-key" {
		t.Fatalf("credentials = %q, want [first-key second-key]", keys)
	}
}

func TestCommandCodeCredentialsIgnoresBlankChannel(t *testing.T) {
	channel := &model.Channel{Key: "  \n "}

	if keys := commandCodeCredentials(channel); len(keys) != 0 {
		t.Fatalf("credentials = %q, want none", keys)
	}
}

func TestAggregateCommandCodeCreditsAddsAccounts(t *testing.T) {
	first := commandCodeCredits{}
	first.Credits.MonthlyCredits = 40
	first.WindowLimits.FiveHour = commandCodeCreditWindow{Used: 1, Cap: 14, ResetAt: 2000}
	first.WindowLimits.Weekly = commandCodeCreditWindow{Used: 4, Cap: 35, ResetAt: 5000}
	second := commandCodeCredits{}
	second.Credits.MonthlyCredits = 20
	second.Credits.PurchasedCredits = 5
	second.WindowLimits.FiveHour = commandCodeCreditWindow{Used: 2, Cap: 14, ResetAt: 1000}
	second.WindowLimits.Weekly = commandCodeCreditWindow{Used: 6, Cap: 35, ResetAt: 3000}

	total, windows := aggregateCommandCodeCredits([]commandCodeCredits{first, second})
	if total != 65 {
		t.Fatalf("total = %v, want 65", total)
	}
	if windows[0].Name != commandCodeWindowFiveHour || windows[0].UsedUSD != 3 || windows[0].CapUSD != 28 {
		t.Fatalf("five hour window = %+v, want used 3 of 28", windows[0])
	}
	if !windows[0].ResetAt.Equal(time.UnixMilli(1000)) {
		t.Fatalf("five hour reset = %v, want the earliest reset", windows[0].ResetAt)
	}
	if windows[1].UsedUSD != 10 || windows[1].CapUSD != 70 {
		t.Fatalf("weekly window = %+v, want used 10 of 70", windows[1])
	}
}

func TestAggregateCommandCodeCreditsLeavesUnknownWindowEmpty(t *testing.T) {
	account := commandCodeCredits{}
	account.Credits.MonthlyCredits = 10

	_, windows := aggregateCommandCodeCredits([]commandCodeCredits{account})
	if windows[0].CapUSD != 0 || windows[1].CapUSD != 0 {
		t.Fatalf("windows = %+v, want no caps when the upstream reports none", windows)
	}
}
