package metrics

import (
	"cmp"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

var (
	relayRequestsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "newapi_requests_total",
		Help: "Finished relay requests by model, group, stream mode and outcome.",
	}, []string{"model", "group", "stream", "outcome"})

	relayDurationSeconds = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "newapi_request_duration_seconds",
		Help:    "Relay request duration from request start to the final response.",
		Buckets: []float64{0.1, 0.25, 0.5, 1, 2, 5, 10, 30, 60, 120, 300},
	}, []string{"model", "stream"})

	relayTtftSeconds = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "newapi_ttft_seconds",
		Help:    "Time from request start to the first streamed token.",
		Buckets: []float64{0.05, 0.1, 0.25, 0.5, 1, 2, 5, 10, 20, 30, 60},
	}, []string{"model"})

	upstreamAttemptsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "newapi_upstream_attempts_total",
		Help: "Upstream channel attempts, counting every retry.",
	}, []string{"model", "channel", "outcome"})

	upstreamDurationSeconds = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "newapi_upstream_duration_seconds",
		Help:    "Duration of one upstream channel attempt.",
		Buckets: []float64{0.1, 0.25, 0.5, 1, 2, 5, 10, 30, 60, 120, 300},
	}, []string{"model", "outcome"})

	userRequestsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "newapi_user_requests_total",
		Help: "Finished relay requests by user and outcome.",
	}, []string{"user", "outcome"})
)

// RelayResult is one finished relay request, sampled once at the request
// boundary: a retried request is still one result.
type RelayResult struct {
	UserID   int
	Username string
	Model    string
	Group    string
	Stream   bool
	Outcome  string
	Latency  time.Duration
	Ttft     time.Duration
	HasTtft  bool
}

func ObserveRelay(result RelayResult) {
	if result.Model == "" {
		return
	}
	model := result.Model
	stream := strconv.FormatBool(result.Stream)
	relayRequestsTotal.WithLabelValues(model, cmp.Or(result.Group, "default"), stream, result.Outcome).Inc()
	relayDurationSeconds.WithLabelValues(model, stream).Observe(result.Latency.Seconds())
	if result.HasTtft {
		relayTtftSeconds.WithLabelValues(model).Observe(result.Ttft.Seconds())
	}
	if result.UserID > 0 || result.Username != "" {
		userRequestsTotal.WithLabelValues(labelName(result.Username, result.UserID), result.Outcome).Inc()
	}
}

// UpstreamAttempt is one call to one upstream channel, retries included.
type UpstreamAttempt struct {
	Model       string
	ChannelID   int
	ChannelName string
	Outcome     string
	Duration    time.Duration
}

func ObserveUpstreamAttempt(attempt UpstreamAttempt) {
	if attempt.Model == "" {
		return
	}
	upstreamAttemptsTotal.WithLabelValues(attempt.Model, labelName(attempt.ChannelName, attempt.ChannelID), attempt.Outcome).Inc()
	upstreamDurationSeconds.WithLabelValues(attempt.Model, attempt.Outcome).Observe(attempt.Duration.Seconds())
}
