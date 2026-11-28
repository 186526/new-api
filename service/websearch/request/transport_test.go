package request

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/service/redaction"
	"github.com/QuantumNous/new-api/service/websearch/abstract"
	"github.com/QuantumNous/new-api/setting/websearch_setting"
)

func TestForwardProtectedRequestsDoNotReuseSharedCache(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		_, _ = writer.Write([]byte(`{"results":[]}`))
	}))
	defer server.Close()
	build := func(ctx context.Context) (*http.Request, error) {
		return http.NewRequestWithContext(ctx, http.MethodPost, server.URL, nil)
	}
	policy, err := redaction.Compile(redaction.DefaultConfig(), 10, false)
	if err != nil {
		t.Fatal(err)
	}
	session, err := policy.NewSession(nil)
	if err != nil {
		t.Fatal(err)
	}
	protected := session.ProtectedContext(t.Context())
	for _, example := range []struct {
		ctx    context.Context
		cached bool
	}{{t.Context(), false}, {protected, false}, {protected, false}, {t.Context(), true}} {
		answer, err := Forward(example.ctx, abstract.ChannelContext{}, build, ForwardOptions{CacheTTLSeconds: 60, TimeoutSeconds: 2})
		if err != nil || answer.Cached != example.cached {
			t.Fatalf("cached = %v, want %v, error = %v", answer.Cached, example.cached, err)
		}
	}
	if calls.Load() != 3 {
		t.Fatalf("upstream attempts = %d, want 3", calls.Load())
	}
}

func TestForwardCacheSeparatesTenantsAndBodies(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		_, _ = writer.Write([]byte(`{"results":[]}`))
	}))
	defer server.Close()
	for _, example := range []struct {
		tenant int
		body   string
		cached bool
	}{
		{1, `{"q":"first"}`, false},
		{1, `{"q":"first"}`, true},
		{2, `{"q":"first"}`, false},
		{2, `{"q":"second"}`, false},
		{2, `{"q":"second"}`, true},
	} {
		ctx := abstract.WithCacheTenant(t.Context(), example.tenant)
		build := func(ctx context.Context) (*http.Request, error) {
			return http.NewRequestWithContext(ctx, http.MethodPost, server.URL, strings.NewReader(example.body))
		}
		answer, err := Forward(ctx, abstract.ChannelContext{}, build, ForwardOptions{CacheTTLSeconds: 60, TimeoutSeconds: 2})
		if err != nil || answer.Cached != example.cached {
			t.Fatalf("tenant %d body %q: cached = %v, want %v, error = %v", example.tenant, example.body, answer.Cached, example.cached, err)
		}
	}
	if calls.Load() != 3 {
		t.Fatalf("upstream attempts = %d, want 3", calls.Load())
	}
}

func TestForwardDoesNotRetryAnIncompleteUpstreamResponse(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Length", "10")
		_, _ = w.Write([]byte("short"))
	}))
	defer server.Close()

	_, err := Forward(t.Context(), abstract.ChannelContext{}, func(ctx context.Context) (*http.Request, error) {
		return http.NewRequestWithContext(ctx, http.MethodPost, server.URL, nil)
	}, ForwardOptions{CacheTTLSeconds: -1, TimeoutSeconds: 2})
	if !WasSent(err) || calls.Load() != 1 {
		t.Fatalf("incomplete response: error = %v, attempts = %d; want a sent error and one attempt", err, calls.Load())
	}
}

func TestForwardDoesNotRetryWhenTheUpstreamClosesBeforeHeaders(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		conn, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		_ = conn.Close()
	}))
	defer server.Close()

	_, err := Forward(t.Context(), abstract.ChannelContext{}, func(ctx context.Context) (*http.Request, error) {
		return http.NewRequestWithContext(ctx, http.MethodPost, server.URL, nil)
	}, ForwardOptions{CacheTTLSeconds: -1, TimeoutSeconds: 2})
	if !WasSent(err) || calls.Load() != 1 {
		t.Fatalf("upstream closed before headers: error = %v, attempts = %d; want a sent error and one attempt", err, calls.Load())
	}
}

func TestForwardDoesNotRetryAnUpstreamRefusal(t *testing.T) {
	const refusal = `{"error":"upstream refusal"}`
	for _, status := range []int{
		http.StatusRequestTimeout, http.StatusTooManyRequests, http.StatusInternalServerError,
		http.StatusServiceUnavailable, http.StatusGatewayTimeout, http.StatusBadRequest,
		http.StatusTemporaryRedirect, http.StatusSeeOther,
	} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				if calls.Add(1) > 1 || req.URL.Path != "/initial" {
					_, _ = w.Write([]byte(`{"results":[]}`))
					return
				}
				w.Header().Set("Location", "/next")
				w.Header().Set("Retry-After", "1")
				w.WriteHeader(status)
				_, _ = w.Write([]byte(refusal))
			}))
			defer server.Close()

			answer, err := Forward(t.Context(), abstract.ChannelContext{}, func(ctx context.Context) (*http.Request, error) {
				return http.NewRequestWithContext(ctx, http.MethodPost, server.URL+"/initial", nil)
			}, ForwardOptions{CacheTTLSeconds: -1, TimeoutSeconds: 2})
			if err != nil || calls.Load() != 1 || answer.StatusCode != status || string(answer.Body) != refusal || answer.Header.Get("Retry-After") != "1" {
				t.Fatalf("refusal: status = %d, body = %q, error = %v, attempts = %d; want original %d answer and one attempt", answer.StatusCode, answer.Body, err, calls.Load(), status)
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
