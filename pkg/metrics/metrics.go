// Package metrics exposes New API application metrics for Prometheus. The
// endpoint is opt-in: it starts only when METRICS_ADDR is set, so a deployment
// without the variable keeps the existing single listener.
package metrics

import (
	"context"
	"errors"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/QuantumNous/new-api/common"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	registry = prometheus.NewRegistry()
	enabled  atomic.Bool
)

func init() {
	registry.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		relayRequestsTotal, relayDurationSeconds, relayTtftSeconds,
		upstreamAttemptsTotal, upstreamDurationSeconds, userRequestsTotal,
		tokensTotal, promptCacheTokensTotal, quotaUsedTotal, costCnyTotal, refundCnyTotal,
		userTokensTotal, userCostCnyTotal,
		channelBalanceUSD, upstreamCreditsUsedUSD, upstreamCreditsCapUSD,
		upstreamCreditsResetTimestamp, upstreamCreditsRefreshTimestamp,
		upstreamQuotaRemainingRatio, upstreamQuotaResetTimestamp, upstreamQuotaRefreshTimestamp,
		newPriceCollector(), newCacheCollector(), redisCacheCollector{},
	)
}

// StartServer serves /metrics on METRICS_ADDR and returns nil when the variable
// is unset.
func StartServer() *http.Server {
	addr := strings.TrimSpace(os.Getenv("METRICS_ADDR"))
	if addr == "" {
		return nil
	}
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.HandlerFor(registry, promhttp.HandlerOpts{EnableOpenMetrics: true}))
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	srv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	enabled.Store(true)
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			common.SysError(common.LogText("metrics server failed: %s", err.Error()))
		}
	}()
	common.SysLog(common.LogText("metrics endpoint listening on %s", addr))
	return srv
}

// Enabled reports whether the metrics endpoint is running. Callers consult it
// before resolving a display name for a label, so a deployment without
// METRICS_ADDR pays nothing for it.
func Enabled() bool { return enabled.Load() }

// labelName is the value a panel shows for a user or channel: the display name
// when the caller resolved one, otherwise the numeric ID as text.
func labelName(name string, id int) string {
	if name != "" {
		return name
	}
	if id <= 0 {
		return ""
	}
	return strconv.Itoa(id)
}

// Shutdown stops the metrics listener. A nil server means the endpoint was
// never enabled.
func Shutdown(ctx context.Context, srv *http.Server) {
	if srv == nil {
		return
	}
	if err := srv.Shutdown(ctx); err != nil {
		common.SysError(common.LogText("metrics server forced to shutdown: %s", err.Error()))
	}
}
