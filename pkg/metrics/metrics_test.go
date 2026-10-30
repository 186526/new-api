package metrics

import (
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// useConversion fixes the quota and exchange-rate settings for one test and
// restores them afterwards.
func useConversion(t *testing.T, quotaPerUnit, usdCny float64) {
	t.Helper()
	previousQuota, previousRate := common.QuotaPerUnit, operation_setting.USDExchangeRate
	common.QuotaPerUnit, operation_setting.USDExchangeRate = quotaPerUnit, usdCny
	t.Cleanup(func() {
		common.QuotaPerUnit, operation_setting.USDExchangeRate = previousQuota, previousRate
	})
}

func TestQuotaToCNY(t *testing.T) {
	useConversion(t, 500_000, 7.3)

	assert.Equal(t, 7.3, quotaToCNY(500_000))
	assert.Equal(t, 0.0, quotaToCNY(0))
	assert.Equal(t, 0.0, quotaToCNY(-1))

	useConversion(t, 0, 7.3)
	assert.Equal(t, 0.0, quotaToCNY(500_000))
}

func TestObserveSettlementRecordsTokensCacheAndCost(t *testing.T) {
	useConversion(t, 500_000, 7.3)

	ObserveSettlement(Settlement{
		UserID:           71,
		Username:         "alice",
		Model:            "test-settlement-model",
		ChannelID:        9,
		ChannelName:      "openai-main",
		Group:            "vip",
		PromptTokens:     1_000,
		CompletionTokens: 250,
		CacheReadTokens:  400,
		CacheWriteTokens: 100,
		Quota:            500_000,
	})

	assert.Equal(t, 1_000.0, testutil.ToFloat64(tokensTotal.WithLabelValues("test-settlement-model", "openai-main", "prompt")))
	assert.Equal(t, 250.0, testutil.ToFloat64(tokensTotal.WithLabelValues("test-settlement-model", "openai-main", "completion")))
	assert.Equal(t, 400.0, testutil.ToFloat64(promptCacheTokensTotal.WithLabelValues("test-settlement-model", "read")))
	assert.Equal(t, 100.0, testutil.ToFloat64(promptCacheTokensTotal.WithLabelValues("test-settlement-model", "write")))
	assert.Equal(t, 500_000.0, testutil.ToFloat64(quotaUsedTotal.WithLabelValues("test-settlement-model", "vip")))
	assert.Equal(t, 7.3, testutil.ToFloat64(costCnyTotal.WithLabelValues("test-settlement-model", "vip")))
	assert.Equal(t, 1_000.0, testutil.ToFloat64(userTokensTotal.WithLabelValues("alice", "prompt")))
	assert.Equal(t, 7.3, testutil.ToFloat64(userCostCnyTotal.WithLabelValues("alice")))

	ObserveSettlement(Settlement{Model: "test-fallback-model", ChannelID: 9, PromptTokens: 5})
	assert.Equal(t, 5.0, testutil.ToFloat64(tokensTotal.WithLabelValues("test-fallback-model", "9", "prompt")))
}

func TestObserveSettlementSkipsUserMetricsForAnonymousCharge(t *testing.T) {
	useConversion(t, 500_000, 7.3)

	ObserveSettlement(Settlement{Model: "test-anonymous-model", Quota: 500_000})

	assert.Equal(t, 7.3, testutil.ToFloat64(costCnyTotal.WithLabelValues("test-anonymous-model", "default")))

	series := testutil.CollectAndCount(userCostCnyTotal, "newapi_user_cost_cny_total")
	ObserveSettlement(Settlement{Model: "test-anonymous-model", Quota: 500_000})
	assert.Equal(t, series, testutil.CollectAndCount(userCostCnyTotal, "newapi_user_cost_cny_total"))
}

func TestObserveTaskBillingRoutesRefundToItsOwnCounter(t *testing.T) {
	useConversion(t, 500_000, 7.3)

	ObserveTaskBilling(TaskBilling{UserID: 72, Model: "test-task-model", Group: "vip", Quota: 500_000})
	ObserveTaskBilling(TaskBilling{UserID: 72, Model: "test-task-model", Group: "vip", Quota: 250_000, Refund: true})
	ObserveTaskBilling(TaskBilling{UserID: 72, Model: "test-task-model", Quota: -1})

	assert.Equal(t, 7.3, testutil.ToFloat64(costCnyTotal.WithLabelValues("test-task-model", "vip")))
	assert.Equal(t, 3.65, testutil.ToFloat64(refundCnyTotal.WithLabelValues("test-task-model", "vip")))
	assert.Equal(t, 7.3, testutil.ToFloat64(userCostCnyTotal.WithLabelValues("72")))
	assert.Equal(t, 0.0, testutil.ToFloat64(costCnyTotal.WithLabelValues("test-task-model", "default")))
}

func TestObserveRelayIgnoresEmptyModel(t *testing.T) {
	series := testutil.CollectAndCount(relayRequestsTotal, "newapi_requests_total")
	ObserveRelay(RelayResult{Outcome: "success"})
	assert.Equal(t, series, testutil.CollectAndCount(relayRequestsTotal, "newapi_requests_total"))
}

func TestObserveRelayAttributesTheServingChannel(t *testing.T) {
	ObserveRelay(RelayResult{
		ChannelID:   11,
		ChannelName: "upstream-a",
		Model:       "test-relay-channel",
		Stream:      true,
		Outcome:     "success",
		Latency:     2 * time.Second,
		Ttft:        time.Second,
		HasTtft:     true,
	})
	ObserveRelay(RelayResult{ChannelID: 12, Model: "test-relay-channel", Outcome: "failure"})

	assert.Equal(t, 1.0, testutil.ToFloat64(relayRequestsTotal.WithLabelValues("upstream-a", "test-relay-channel", "default", "true", "success")))
	assert.Equal(t, 1.0, testutil.ToFloat64(relayRequestsTotal.WithLabelValues("12", "test-relay-channel", "default", "false", "failure")))
	assert.Equal(t, []string{"upstream-a"}, collectLabelValues(t, relayTtftSeconds, "channel"))
}

func TestObserveUpstreamAttemptAttributesTheChannel(t *testing.T) {
	ObserveUpstreamAttempt(UpstreamAttempt{
		ChannelID:   21,
		ChannelName: "upstream-b",
		Model:       "test-attempt-channel",
		Outcome:     "failure",
		Duration:    3 * time.Second,
	})

	assert.Equal(t, 1.0, testutil.ToFloat64(upstreamAttemptsTotal.WithLabelValues("test-attempt-channel", "upstream-b", "failure")))
	assert.Equal(t, []string{"upstream-b"}, collectLabelValues(t, upstreamDurationSeconds, "channel"))
}

// collectLabelValues returns the values a collector reports for one label.
func collectLabelValues(t *testing.T, collector prometheus.Collector, label string) []string {
	t.Helper()

	metrics := make(chan prometheus.Metric, 64)
	collector.Collect(metrics)
	close(metrics)

	values := make([]string, 0, len(metrics))
	for metric := range metrics {
		written := &dto.Metric{}
		require.NoError(t, metric.Write(written))
		for _, pair := range written.GetLabel() {
			if pair.GetName() == label {
				values = append(values, pair.GetValue())
			}
		}
	}
	return values
}

func TestPriceCollectorReportsOnlyConfiguredModels(t *testing.T) {
	useConversion(t, 500_000, 7.3)
	ratio_setting.InitRatioSettings()
	SetPriceModels([]string{"gpt-4o"})
	t.Cleanup(func() { SetPriceModels(nil) })

	collector := newPriceCollector()

	assert.Equal(t, 4, testutil.CollectAndCount(collector, "newapi_model_price_usd_per_million"))
	assert.Equal(t, 0, testutil.CollectAndCount(collector, "newapi_model_fixed_price_usd"))
}

func TestParseRedisInfo(t *testing.T) {
	info := "# Stats\r\nkeyspace_hits:120\r\nkeyspace_misses:8\r\nused_memory:1048576\r\n" +
		"total_net_input_bytes:12.5k\r\nempty:\r\nnot_a_pair\r\n"

	values := parseRedisInfo(info)

	require.Len(t, values, 3)
	assert.Equal(t, int64(120), values["keyspace_hits"])
	assert.Equal(t, int64(8), values["keyspace_misses"])
	assert.Equal(t, int64(1048576), values["used_memory"])
}

func TestObserveChannelBalanceUSDUsesDisplayNameThenID(t *testing.T) {
	ObserveChannelBalanceUSD("test-balance-named", 7, 12.5)
	assert.Equal(t, 12.5, testutil.ToFloat64(channelBalanceUSD.WithLabelValues("test-balance-named")))

	ObserveChannelBalanceUSD("", 42, 3.25)
	assert.Equal(t, 3.25, testutil.ToFloat64(channelBalanceUSD.WithLabelValues("42")))
}

func TestObserveChannelBalanceUSDSkipsUnidentifiedChannel(t *testing.T) {
	before := testutil.CollectAndCount(channelBalanceUSD)

	ObserveChannelBalanceUSD("", 0, 9)

	assert.Equal(t, before, testutil.CollectAndCount(channelBalanceUSD))
}
