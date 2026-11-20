package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
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
