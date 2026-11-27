package request

import (
	"net/http"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/setting/websearch_setting"
)

// TestRetryableStatusClassifiesTheUpstreamAnswersARetryCanFix pins which status
// codes are worth a second request: the two the upstreams use for "ask again
// later", and every server error. A 4xx that names a fault in our own request
// is not retried, because the same bytes would be refused again.
func TestRetryableStatusClassifiesTheUpstreamAnswersARetryCanFix(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		want   bool
	}{
		{name: "request timeout is retried", status: http.StatusRequestTimeout, want: true},
		{name: "too many requests is retried", status: http.StatusTooManyRequests, want: true},
		{name: "internal server error is retried", status: http.StatusInternalServerError, want: true},
		{name: "service unavailable is retried", status: http.StatusServiceUnavailable, want: true},
		{name: "gateway timeout is retried", status: http.StatusGatewayTimeout, want: true},
		{name: "bad request is not retried", status: http.StatusBadRequest},
		{name: "unauthorized is not retried", status: http.StatusUnauthorized},
		{name: "forbidden is not retried", status: http.StatusForbidden},
		{name: "not found is not retried", status: http.StatusNotFound},
		{name: "unprocessable entity is not retried", status: http.StatusUnprocessableEntity},
		{name: "success is not retried", status: http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := retryableStatus(tc.status); got != tc.want {
				t.Errorf("retryableStatus(%d) = %v, want %v", tc.status, got, tc.want)
			}
		})
	}
}

// TestForwardCacheTTLResolvesTheThreeLevels requires the per-request option to
// win over the global setting, a negative value on either level to disable the
// cache, and a zero to leave the choice to the next level down.
func TestForwardCacheTTLResolvesTheThreeLevels(t *testing.T) {
	settings := websearch_setting.WebSearchSetting{
		SearchCacheTTLSeconds: 600,
		FetchCacheTTLSeconds:  3600,
	}
	for _, tc := range []struct {
		name    string
		options ForwardOptions
		search  int
		fetch   int
		want    time.Duration
	}{
		{
			name:    "search falls back to the search setting",
			options: ForwardOptions{Search: true},
			search:  settings.SearchCacheTTLSeconds, fetch: settings.FetchCacheTTLSeconds,
			want: 600 * time.Second,
		},
		{
			name:    "fetch falls back to the fetch setting",
			options: ForwardOptions{},
			search:  settings.SearchCacheTTLSeconds, fetch: settings.FetchCacheTTLSeconds,
			want: 3600 * time.Second,
		},
		{
			name:    "a positive option wins over the setting",
			options: ForwardOptions{Search: true, CacheTTLSeconds: 30},
			search:  settings.SearchCacheTTLSeconds, fetch: settings.FetchCacheTTLSeconds,
			want: 30 * time.Second,
		},
		{
			name:    "a negative option disables the cache",
			options: ForwardOptions{Search: true, CacheTTLSeconds: -1},
			search:  settings.SearchCacheTTLSeconds, fetch: settings.FetchCacheTTLSeconds,
			want: 0,
		},
		{
			name:    "a negative setting disables the cache",
			options: ForwardOptions{Search: true},
			search:  -1, fetch: settings.FetchCacheTTLSeconds,
			want: 0,
		},
		{
			name:    "an unset search setting keeps the built-in search default",
			options: ForwardOptions{Search: true},
			search:  0, fetch: settings.FetchCacheTTLSeconds,
			want: 600 * time.Second,
		},
		{
			name:    "an unset fetch setting keeps the built-in fetch default",
			options: ForwardOptions{},
			search:  settings.SearchCacheTTLSeconds, fetch: 0,
			want: 3600 * time.Second,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			global := websearch_setting.WebSearchSetting{
				SearchCacheTTLSeconds: tc.search,
				FetchCacheTTLSeconds:  tc.fetch,
			}
			if got := forwardCacheTTL(tc.options, global); got != tc.want {
				t.Errorf("forwardCacheTTL = %v, want %v", got, tc.want)
			}
		})
	}
}
