package metrics

import (
	"sync/atomic"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"

	"github.com/prometheus/client_golang/prometheus"
)

var (
	modelPriceDesc = prometheus.NewDesc(
		"newapi_model_price_usd_per_million",
		"Effective token price in USD per million tokens.",
		[]string{"model", "kind"}, nil,
	)
	modelFixedPriceDesc = prometheus.NewDesc(
		"newapi_model_fixed_price_usd",
		"Configured fixed price in USD per request.",
		[]string{"model"}, nil,
	)
	groupRatioDesc = prometheus.NewDesc(
		"newapi_group_ratio",
		"Configured billing multiplier per group.",
		[]string{"group"}, nil,
	)
	quotaPerUnitDesc = prometheus.NewDesc(
		"newapi_quota_per_unit",
		"Quota units per USD.",
		nil, nil,
	)
	usdCnyRateDesc = prometheus.NewDesc(
		"newapi_usd_cny_rate",
		"USD to CNY exchange rate used for display and billing.",
		nil, nil,
	)

	priceModels atomic.Pointer[[]string]
)

// SetPriceModels limits the price gauges to the models this deployment offers.
// The built-in ratio table covers thousands of models that are not routable
// here, and exporting all of them would add tens of thousands of unused series.
func SetPriceModels(models []string) {
	normalized := append([]string(nil), models...)
	priceModels.Store(&normalized)
}

// priceCollector reports the configured price table at scrape time, so a price
// change shows up without a restart.
type priceCollector struct{}

func newPriceCollector() *priceCollector {
	return &priceCollector{}
}

func (*priceCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- modelPriceDesc
	ch <- modelFixedPriceDesc
	ch <- groupRatioDesc
	ch <- quotaPerUnitDesc
	ch <- usdCnyRateDesc
}

func (*priceCollector) Collect(ch chan<- prometheus.Metric) {
	ch <- prometheus.MustNewConstMetric(quotaPerUnitDesc, prometheus.GaugeValue, common.QuotaPerUnit)
	ch <- prometheus.MustNewConstMetric(usdCnyRateDesc, prometheus.GaugeValue, operation_setting.USDExchangeRate)
	for group, ratio := range ratio_setting.GetGroupRatioCopy() {
		ch <- prometheus.MustNewConstMetric(groupRatioDesc, prometheus.GaugeValue, ratio, group)
	}

	models := priceModels.Load()
	if models == nil || common.QuotaPerUnit <= 0 {
		return
	}
	for _, model := range *models {
		if price, ok := ratio_setting.GetModelPrice(model, false); ok && price > 0 {
			ch <- prometheus.MustNewConstMetric(modelFixedPriceDesc, prometheus.GaugeValue, price, model)
			continue
		}
		ratio, ok, _ := ratio_setting.GetModelRatio(model)
		if !ok || ratio <= 0 {
			continue
		}
		prompt := ratio / common.QuotaPerUnit * 1_000_000
		cacheRatio, _ := ratio_setting.GetCacheRatio(model)
		createRatio, _ := ratio_setting.GetCreateCacheRatio(model)
		ch <- prometheus.MustNewConstMetric(modelPriceDesc, prometheus.GaugeValue, prompt, model, "prompt")
		ch <- prometheus.MustNewConstMetric(modelPriceDesc, prometheus.GaugeValue, prompt*ratio_setting.GetCompletionRatio(model), model, "completion")
		ch <- prometheus.MustNewConstMetric(modelPriceDesc, prometheus.GaugeValue, prompt*cacheRatio, model, "cache_read")
		ch <- prometheus.MustNewConstMetric(modelPriceDesc, prometheus.GaugeValue, prompt*createRatio, model, "cache_write")
	}
}
