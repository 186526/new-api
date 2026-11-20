package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

var imageLimitRejectionsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
	Name: "newapi_image_limit_rejections_total",
	Help: "Requests rejected by a channel image limit before reaching the upstream.",
}, []string{"channel", "model"})

// ObserveImageLimitRejection counts one request refused for carrying too many
// images. model is the model the client asked for.
func ObserveImageLimitRejection(channelName string, channelID int, model string) {
	imageLimitRejectionsTotal.WithLabelValues(
		labelName(channelName, channelID),
		model,
	).Inc()
}

// ImageLimitRejectionsForTest returns the rejection count recorded under one
// channel and model label, so a caller can assert the metric it publishes.
func ImageLimitRejectionsForTest(channel, model string) float64 {
	return testutil.ToFloat64(imageLimitRejectionsTotal.WithLabelValues(channel, model))
}
