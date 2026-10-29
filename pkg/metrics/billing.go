package metrics

import (
	"cmp"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"

	"github.com/prometheus/client_golang/prometheus"
)

var (
	tokensTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "newapi_tokens_total",
		Help: "Settled tokens by model, channel and type.",
	}, []string{"model", "channel", "type"})

	promptCacheTokensTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "newapi_prompt_cache_tokens_total",
		Help: "Prompt tokens read from or written to the upstream prompt cache.",
	}, []string{"model", "type"})

	quotaUsedTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "newapi_quota_used_total",
		Help: "Quota charged by settled requests, in database quota units.",
	}, []string{"model", "group"})

	costCnyTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "newapi_cost_cny_total",
		Help: "Charged amount in CNY, converted at the exchange rate in effect when charged.",
	}, []string{"model", "group"})

	refundCnyTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "newapi_refund_cny_total",
		Help: "Refunded task amount in CNY, converted at the exchange rate in effect when refunded.",
	}, []string{"model", "group"})

	userTokensTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "newapi_user_tokens_total",
		Help: "Settled tokens by user and type.",
	}, []string{"user", "type"})

	userCostCnyTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "newapi_user_cost_cny_total",
		Help: "Charged amount by user in CNY, converted at the exchange rate in effect when charged.",
	}, []string{"user"})
)

// Settlement is one charge as written to the usage log. PromptTokens already
// includes cache reads, so CacheReadTokens and CacheWriteTokens are reported
// separately instead of being added to the token total.
type Settlement struct {
	UserID           int
	Username         string
	Model            string
	ChannelID        int
	ChannelName      string
	Group            string
	PromptTokens     int
	CompletionTokens int
	CacheReadTokens  int
	CacheWriteTokens int
	Quota            int
}

func ObserveSettlement(settlement Settlement) {
	if settlement.Model == "" {
		return
	}
	model := settlement.Model
	group := cmp.Or(settlement.Group, "default")
	channel := labelName(settlement.ChannelName, settlement.ChannelID)
	cost := quotaToCNY(settlement.Quota)

	if settlement.PromptTokens > 0 {
		tokensTotal.WithLabelValues(model, channel, "prompt").Add(float64(settlement.PromptTokens))
	}
	if settlement.CompletionTokens > 0 {
		tokensTotal.WithLabelValues(model, channel, "completion").Add(float64(settlement.CompletionTokens))
	}
	if settlement.CacheReadTokens > 0 {
		promptCacheTokensTotal.WithLabelValues(model, "read").Add(float64(settlement.CacheReadTokens))
	}
	if settlement.CacheWriteTokens > 0 {
		promptCacheTokensTotal.WithLabelValues(model, "write").Add(float64(settlement.CacheWriteTokens))
	}
	if settlement.Quota > 0 {
		quotaUsedTotal.WithLabelValues(model, group).Add(float64(settlement.Quota))
	}
	if cost > 0 {
		costCnyTotal.WithLabelValues(model, group).Add(cost)
	}
	if settlement.UserID <= 0 && settlement.Username == "" {
		return
	}
	user := labelName(settlement.Username, settlement.UserID)
	if settlement.PromptTokens > 0 {
		userTokensTotal.WithLabelValues(user, "prompt").Add(float64(settlement.PromptTokens))
	}
	if settlement.CompletionTokens > 0 {
		userTokensTotal.WithLabelValues(user, "completion").Add(float64(settlement.CompletionTokens))
	}
	if cost > 0 {
		userCostCnyTotal.WithLabelValues(user).Add(cost)
	}
}

// TaskBilling is one asynchronous task charge adjustment. A refund is recorded
// on its own counter because a Prometheus counter cannot be decreased.
type TaskBilling struct {
	UserID   int
	Username string
	Model    string
	Group    string
	Quota    int
	Refund   bool
}

func ObserveTaskBilling(billing TaskBilling) {
	if billing.Model == "" || billing.Quota <= 0 {
		return
	}
	cost := quotaToCNY(billing.Quota)
	if cost <= 0 {
		return
	}
	group := cmp.Or(billing.Group, "default")
	if billing.Refund {
		refundCnyTotal.WithLabelValues(billing.Model, group).Add(cost)
		return
	}
	costCnyTotal.WithLabelValues(billing.Model, group).Add(cost)
	if billing.UserID > 0 || billing.Username != "" {
		userCostCnyTotal.WithLabelValues(labelName(billing.Username, billing.UserID)).Add(cost)
	}
}

// quotaToCNY converts stored quota into CNY. QuotaPerUnit is the number of
// quota units per USD; USDExchangeRate is the displayed USD/CNY rate.
func quotaToCNY(quota int) float64 {
	if quota <= 0 || common.QuotaPerUnit <= 0 {
		return 0
	}
	return float64(quota) / common.QuotaPerUnit * operation_setting.USDExchangeRate
}
