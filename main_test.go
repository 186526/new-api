package main

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	prometheusmodel "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	appmetrics "github.com/QuantumNous/new-api/pkg/metrics"
)

const (
	// mistralQuotaGateTestKey is the key the entry carries and the channel carries,
	// so the allowance is read from the billing endpoint with it.
	mistralQuotaGateTestKey = "mstrl_gatekey"
	// mistralQuotaGateTestScope and mistralQuotaGateTestWindow are the scope and
	// window the refresh publishes the merged monthly allowance under.
	mistralQuotaGateTestScope  = "vibe"
	mistralQuotaGateTestWindow = "monthly"
)

// mistralQuotaGateRuns numbers the channel each run is seeded with. The metrics
// registry is process-wide and keeps every series it ever created, so a channel
// name reused by the sibling subtest or by a second run under -count would read as
// an allowance this run already published. A fresh name per run is what makes the
// publish the proof that this run's loop reached the endpoint.
var mistralQuotaGateRuns atomic.Int32

func mistralQuotaGateChannel(source string) string {
	return fmt.Sprintf("Mistral (gate, %s %d)", source, mistralQuotaGateRuns.Add(1))
}

// mistralQuotaGateAccounts is a key-only entry: it carries a channel and a key,
// and no console session. An entry shaped like this reads its allowance from the
// public billing endpoint with that key.
func mistralQuotaGateAccounts(channel string) string {
	return `[{"channel":"` + channel + `","vibeKey":"` + mistralQuotaGateTestKey +
		`","monthlyLimitUsd":150}]`
}

// TestMistralQuotaRefreshStartsForAKeyOnlyEntry requires an account that carries
// only a key to start the refresh and to publish its allowance, from both sources
// that can configure one. Neither subtest sets a console session.
//
// The two subtests do not prove the same thing. The single-account variables are the
// shape the old gate refused: its disjunction listed the two accounts sources and
// the cookie, so a channel and a key with no cookie started nothing. The accounts
// file is not that case — the old gate started on the path alone — so it covers the
// loader, which accepts a key-only entry, rather than the gate. The file source's
// gate case is TestMistralQuotaRefreshStaysOffForAnAccountsFileWithoutEntries.
func TestMistralQuotaRefreshStartsForAKeyOnlyEntry(t *testing.T) {
	sources := []struct {
		name  string
		apply func(t *testing.T, channel string)
	}{
		{
			name: "accounts file",
			apply: func(t *testing.T, channel string) {
				path := filepath.Join(t.TempDir(), "accounts.json")
				require.NoError(t, os.WriteFile(path, []byte(mistralQuotaGateAccounts(channel)), 0o600))
				t.Setenv("MISTRAL_VIBE_QUOTA_ACCOUNTS_FILE", path)
			},
		},
		{
			name: "single-account variables",
			apply: func(t *testing.T, channel string) {
				t.Setenv("MISTRAL_VIBE_QUOTA_CHANNEL", channel)
				t.Setenv("MISTRAL_VIBE_QUOTA_VIBE_KEY", mistralQuotaGateTestKey)
			},
		},
	}
	for _, source := range sources {
		t.Run(source.name, func(t *testing.T) {
			mistralQuotaGateTestEnv(t)
			channel := mistralQuotaGateChannel(source.name)
			seedMistralChannel(t, channel, mistralQuotaGateTestKey)
			asked := newMistralBillingServer(t, 25, 150)
			source.apply(t, channel)

			ctx, cancel := context.WithCancel(t.Context())
			started, stop := startMistralQuotaRefreshIfConfigured(ctx)
			t.Cleanup(cancel)
			// Registered after the interval and the database are replaced, so it runs
			// before they are restored (cleanups run last in, first out) and joins the
			// loop first: a tick still running would read the interval and write the
			// balance of a test that already ended.
			t.Cleanup(stop)
			require.True(t, started, "an entry that carries only a key must start the refresh")

			require.Equal(t, 0.75, waitForMistralRemainingRatio(t, channel),
				"the allowance the key answered must be published")
			requireMistralRefreshStops(t, stop, asked)
		})
	}
}

// requireMistralRefreshStops stops the loop and requires the endpoint to be left
// alone afterwards. The stop function cancels the loop and waits for it to return,
// so nothing is polling once it has been called. The caller waits for the publish
// before this, which is what tells that the loop had already read the allowance: a
// cancel landing before the first fetch of a tick is what used to read as a loop
// that kept polling.
func requireMistralRefreshStops(t *testing.T, stop func(), asked *atomic.Int32) {
	t.Helper()
	stop()
	require.GreaterOrEqual(t, asked.Load(), int32(1),
		"the refresh must have read the allowance before it is stopped")
	settled := asked.Load()
	require.Never(t, func() bool {
		return asked.Load() != settled
	}, 300*time.Millisecond, 25*time.Millisecond,
		"a stopped refresh must not keep polling the endpoint")
}

// TestMistralQuotaRefreshStaysOffForAnEmptyConfiguration requires no starts and no
// upstream request when no source is configured at all. An unconfigured deployment
// must not poll Mistral, and the old gate returned false here for the right reason:
// nothing is configured, not a missing cookie.
func TestMistralQuotaRefreshStaysOffForAnEmptyConfiguration(t *testing.T) {
	mistralQuotaGateTestEnv(t)
	asked := newMistralBillingServer(t, 25, 150)
	deleteMistralQuotaAccountsEnv(t)

	started, stop := startMistralQuotaRefreshIfConfigured(t.Context())
	t.Cleanup(stop)
	assert.False(t, started)
	assert.Zero(t, asked.Load(), "an unconfigured deployment must not reach Mistral")
}

// TestMistralQuotaRefreshStaysOffForAnUnloadableConfiguration requires no starts
// and no upstream request when the configured file cannot be parsed, and requires
// the failure to be logged. A file that does not load is a mistake to surface, not
// a reason to poll: the old gate looked only at whether the path was set and
// started anyway.
func TestMistralQuotaRefreshStaysOffForAnUnloadableConfiguration(t *testing.T) {
	mistralQuotaGateTestEnv(t)
	asked := newMistralBillingServer(t, 25, 150)
	path := filepath.Join(t.TempDir(), "accounts.json")
	require.NoError(t, os.WriteFile(path, []byte(`[{"channel":"Mistral (broken)",}]`), 0o600))
	t.Setenv("MISTRAL_VIBE_QUOTA_ACCOUNTS_FILE", path)
	t.Setenv("MISTRAL_VIBE_QUOTA_ACCOUNTS", "")
	captured := captureMainLog(t)

	started, stop := startMistralQuotaRefreshIfConfigured(t.Context())
	t.Cleanup(stop)
	assert.False(t, started)
	assert.Contains(t, captured.String(), "failed to load the Mistral quota accounts")
	assert.Zero(t, asked.Load(), "a file that does not load must not reach Mistral")
}

// TestMistralQuotaRefreshStaysOffForAnAccountsFileWithoutEntries is the file
// source's gate case. The old gate started on a non-empty path alone, so it
// started the loop for this file and polled with a loader that returned no entry.
// The gate now reads what the loader returned, so it starts nothing.
func TestMistralQuotaRefreshStaysOffForAnAccountsFileWithoutEntries(t *testing.T) {
	mistralQuotaGateTestEnv(t)
	asked := newMistralBillingServer(t, 25, 150)
	path := filepath.Join(t.TempDir(), "accounts.json")
	require.NoError(t, os.WriteFile(path, []byte(`[]`), 0o600))
	t.Setenv("MISTRAL_VIBE_QUOTA_ACCOUNTS_FILE", path)

	started, stop := startMistralQuotaRefreshIfConfigured(t.Context())
	t.Cleanup(stop)
	assert.False(t, started, "a file that holds no entry must not start the refresh")
	assert.Zero(t, asked.Load(), "a file that holds no entry must not reach Mistral")
}

// mistralQuotaGateTestEnv points every Mistral source at a server the caller
// passes in later and clears the ones that would otherwise leak from the host
// process, so each test decides what is configured.
func mistralQuotaGateTestEnv(t *testing.T) {
	t.Helper()
	for _, name := range []string{
		"MISTRAL_VIBE_QUOTA_ACCOUNTS",
		"MISTRAL_VIBE_QUOTA_ACCOUNTS_FILE",
		"MISTRAL_VIBE_QUOTA_COOKIE",
		"MISTRAL_VIBE_QUOTA_CHANNEL",
		"MISTRAL_VIBE_QUOTA_VIBE_KEY",
		"MISTRAL_VIBE_QUOTA_STUDIO_KEY",
	} {
		t.Setenv(name, "")
	}
	t.Setenv("MISTRAL_VIBE_QUOTA_FREQUENCY", "")
	// The gate starts the loop in a goroutine that reads these URLs through the
	// service package, so every test points them at its own server.
	t.Setenv("MISTRAL_QUOTA_API_BASE_URL", "")
	t.Setenv("MISTRAL_QUOTA_ADMIN_BASE_URL", "")
	t.Setenv("MISTRAL_VIBE_QUOTA_BASE_URL", "")
	previousInterval := common.RequestInterval
	common.RequestInterval = 0
	t.Cleanup(func() { common.RequestInterval = previousInterval })
}

// newMistralBillingServer answers the public billing endpoint with one merged
// allowance used at percentage percent of limit dollars, points every Mistral
// source at that answer, and counts the requests that reach it. The caller
// configures the entry, so a test can tell "did not start" from "started and
// published" by the counter rather than by the process-wide metric registry.
func newMistralBillingServer(t *testing.T, percentage float64, limit float64) *atomic.Int32 {
	t.Helper()
	var asked atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/billing/budget" {
			http.NotFound(w, r)
			return
		}
		asked.Add(1)
		_, _ = fmt.Fprintf(w,
			`{"vibe_budget":{"usage_percentage":%v,"initial_budget":%v,"currency":"USD","reset_at":"2026-12-01T00:00:00Z","payg_enabled":false}}`,
			percentage, limit)
	}))
	t.Cleanup(server.Close)
	t.Setenv("MISTRAL_QUOTA_API_BASE_URL", server.URL)
	t.Setenv("MISTRAL_QUOTA_ADMIN_BASE_URL", server.URL)
	t.Setenv("MISTRAL_VIBE_QUOTA_BASE_URL", server.URL)
	return &asked
}

// deleteMistralQuotaAccountsEnv empties every accounts source, so the loader
// returns no entries. The file path is read before the inline JSON and before the
// single-account variables, so all three have to be cleared.
func deleteMistralQuotaAccountsEnv(t *testing.T) {
	t.Helper()
	t.Setenv("MISTRAL_VIBE_QUOTA_ACCOUNTS", "")
	t.Setenv("MISTRAL_VIBE_QUOTA_ACCOUNTS_FILE", "")
	t.Setenv("MISTRAL_VIBE_QUOTA_COOKIE", "")
	t.Setenv("MISTRAL_VIBE_QUOTA_VIBE_KEY", "")
	t.Setenv("MISTRAL_VIBE_QUOTA_STUDIO_KEY", "")
}

// seedMistralChannel puts the channel the entry is matched to into an in-memory
// table, so the published series is labelled channel_configured="true" and the
// allowance is read with the key the channel carries.
func seedMistralChannel(t *testing.T, name string, key string) {
	t.Helper()
	originalDB := model.DB
	database, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Discard})
	require.NoError(t, err)
	sqlDB, err := database.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	require.NoError(t, database.AutoMigrate(&model.Channel{}))
	model.DB = database
	t.Cleanup(func() { model.DB = originalDB })

	require.NoError(t, model.DB.Create(&model.Channel{
		Type: 42, // constant.ChannelTypeMistral
		Key:  key,
		Name: name,
	}).Error)
}

// waitForMistralRemainingRatio reads the published series of the channel until it
// appears, because the refresh runs on its own goroutine. The channel name is
// unique to the run, so the series cannot be one an earlier run published; the
// value is the allowance the fetch of this run returned. The refresh loop sleeps
// between ticks, so the value is stable once it is written and the timeout only
// bounds how long the goroutine gets to reach the publish.
func waitForMistralRemainingRatio(t *testing.T, channel string) float64 {
	t.Helper()
	var observed float64
	require.Eventually(t, func() bool {
		observed = mistralRemainingRatio(t, channel)
		return observed > 0
	}, 10*time.Second, 20*time.Millisecond,
		"the refresh must publish newapi_upstream_quota_remaining_ratio for %s", channel)
	return observed
}

// mistralRemainingRatio reads one series of the allowance gauge from the registry
// the metrics endpoint serves, so the assertion is on what an operator sees. The
// registry keeps every series it ever created, so a missing series reads as zero,
// which is outside the fraction the refresh can publish.
func mistralRemainingRatio(t *testing.T, channel string) float64 {
	t.Helper()
	families, err := appmetrics.Registry().Gather()
	require.NoError(t, err)
	for _, family := range families {
		if family.GetName() != "newapi_upstream_quota_remaining_ratio" {
			continue
		}
		for _, metric := range family.GetMetric() {
			if metricHasLabels(metric, map[string]string{
				"channel": channel,
				"scope":   mistralQuotaGateTestScope,
				"window":  mistralQuotaGateTestWindow,
			}) {
				return metric.GetGauge().GetValue()
			}
		}
	}
	return 0
}

func metricHasLabels(metric *prometheusmodel.Metric, want map[string]string) bool {
	labels := make(map[string]string, len(metric.GetLabel()))
	for _, label := range metric.GetLabel() {
		labels[label.GetName()] = label.GetValue()
	}
	for name, value := range want {
		if labels[name] != value {
			return false
		}
	}
	return true
}

// captureMainLog redirects the server log to a buffer for the duration of the
// test, because common.SysError writes through gin's error writer and a test that
// reads a log line needs to own that writer.
func captureMainLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var output bytes.Buffer
	previousWriter, previousErrorWriter := gin.DefaultWriter, gin.DefaultErrorWriter
	gin.DefaultWriter, gin.DefaultErrorWriter = &output, &output
	t.Cleanup(func() {
		gin.DefaultWriter, gin.DefaultErrorWriter = previousWriter, previousErrorWriter
	})
	return &output
}
