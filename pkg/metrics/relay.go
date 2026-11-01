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
		Help: "Finished relay requests by channel, model, group, stream mode and outcome.",
	}, []string{"channel", "model", "group", "stream", "outcome"})

	relayDurationSeconds = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "newapi_request_duration_seconds",
		Help:    "Relay request duration from request start to the final response, by the channel that served it.",
		Buckets: []float64{0.1, 0.25, 0.5, 1, 2, 5, 10, 30, 60, 120, 300},
	}, []string{"channel", "model", "stream"})

	relayTtftSeconds = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "newapi_ttft_seconds",
		Help:    "Time from request start to the first streamed token, by the channel that served it.",
		Buckets: []float64{0.05, 0.1, 0.25, 0.5, 1, 2, 5, 10, 20, 30, 60},
	}, []string{"channel", "model"})

	upstreamAttemptsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "newapi_upstream_attempts_total",
		Help: "Upstream channel attempts, counting every retry.",
	}, []string{"model", "channel", "outcome"})

	upstreamDurationSeconds = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "newapi_upstream_duration_seconds",
		Help:    "Duration of one upstream channel attempt.",
		Buckets: []float64{0.1, 0.25, 0.5, 1, 2, 5, 10, 30, 60, 120, 300},
	}, []string{"channel", "model", "outcome"})

	relayOutputTokensTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "newapi_relay_output_tokens_total",
		Help: "Completion tokens of finished relay requests with a reported usage, by channel, model and stream mode.",
	}, []string{"channel", "model", "stream"})

	relayGenerationSecondsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "newapi_relay_generation_seconds_total",
		Help: "Time spent streaming a finished relay request after its first token, by channel, model and stream mode.",
	}, []string{"channel", "model", "stream"})

	relayRequestTps = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "newapi_relay_request_tps",
		Help:    "Output tokens per generation second of one finished relay request, by channel, model and stream mode.",
		Buckets: []float64{1, 2, 5, 10, 20, 30, 50, 80, 120, 200, 300, 500, 800},
	}, []string{"channel", "model", "stream"})

	userRequestsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "newapi_user_requests_total",
		Help: "Finished relay requests by user and outcome.",
	}, []string{"user", "outcome"})
)

// RelayResult is one finished relay request, sampled once at the request
// boundary: a retried request is still one result.
type RelayResult struct {
	UserID      int
	Username    string
	ChannelID   int
	ChannelName string
	Model       string
	Group       string
	Stream      bool
	Outcome     string
	Latency     time.Duration
	Ttft        time.Duration
	HasTtft     bool
	// OutputTokens is the completion token count the settlement recorded. It is
	// zero when the upstream reported no usage, and such a request is left out
	// of the token throughput.
	OutputTokens int64
}

func ObserveRelay(result RelayResult) {
	if result.Model == "" {
		return
	}
	model := result.Model
	channel := labelName(result.ChannelName, result.ChannelID)
	stream := strconv.FormatBool(result.Stream)
	relayRequestsTotal.WithLabelValues(channel, model, cmp.Or(result.Group, "default"), stream, result.Outcome).Inc()
	relayDurationSeconds.WithLabelValues(channel, model, stream).Observe(result.Latency.Seconds())
	if result.HasTtft {
		relayTtftSeconds.WithLabelValues(channel, model).Observe(result.Ttft.Seconds())
	}
	if result.OutputTokens > 0 {
		relayOutputTokensTotal.WithLabelValues(channel, model, stream).Add(float64(result.OutputTokens))
		generation := result.Latency
		if result.HasTtft {
			generation = result.Latency - result.Ttft
		}
		if generation > 0 {
			relayGenerationSecondsTotal.WithLabelValues(channel, model, stream).Add(generation.Seconds())
			relayRequestTps.WithLabelValues(channel, model, stream).Observe(float64(result.OutputTokens) / generation.Seconds())
		}
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
	channel := labelName(attempt.ChannelName, attempt.ChannelID)
	upstreamAttemptsTotal.WithLabelValues(attempt.Model, channel, attempt.Outcome).Inc()
	upstreamDurationSeconds.WithLabelValues(channel, attempt.Model, attempt.Outcome).Observe(attempt.Duration.Seconds())
}
