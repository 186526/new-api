package middleware

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// BenchmarkRedactRequestTarget pins the cost of the log-path redaction on a
// request line an unauthenticated client chooses. The decoded-path guard walks
// the segments of a path looking for "=", so a path that is one long
// "="-bearing segment is the input that makes a per-candidate backward scan
// quadratic; the second case is the same shape split into many segments, where
// a whole-remainder predicate call per candidate is the cost. Both stay inside
// a request line the edge allows (~8 KiB in the first case, ~8 KiB in the
// second), so a regression shows up as microseconds here rather than seconds.
func BenchmarkRedactRequestTarget(b *testing.B) {
	for _, tc := range []struct{ name, path string }{
		{"one equals segment 8000 bytes", "/" + strings.Repeat("=", 8000)},
		{"many equals segments 2000 times", "/" + strings.Repeat("a=b/", 2000)},
	} {
		b.Run(tc.name, func(b *testing.B) {
			request := &http.Request{Method: http.MethodGet, URL: &url.URL{Path: tc.path}}
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				redactRequestTarget(request)
			}
		})
	}
}

func TestSetUpLoggerMasksQueryCredentials(t *testing.T) {
	previousMode, previousWriter := gin.Mode(), gin.DefaultWriter
	gin.SetMode(gin.TestMode)
	t.Cleanup(func() {
		gin.SetMode(previousMode)
		gin.DefaultWriter = previousWriter
	})

	for _, tc := range []struct{ target, loggedPath string }{
		{"/v1/models?%6bey=first&key=second&API-Key=third&session_token=fourth&alt=sse",
			"/v1/models?API-Key=%2A%2A%2Amasked%2A%2A%2A&alt=sse&key=%2A%2A%2Amasked%2A%2A%2A&session_token=%2A%2A%2Amasked%2A%2A%2A"},
		{"/v1/models%23test%25?key=#secret&alt=#sse",
			"/v1/models%23test%25?alt=%23sse&key=%2A%2A%2Amasked%2A%2A%2A"},
		{"//v1%2Fmodels?key=secret", "//v1%2Fmodels?key=%2A%2A%2Amasked%2A%2A%2A"},
		{"/v1/models?key=bad%ZZ&%ZZ=bad&token=bad;other=true&alt=sse", "/v1/models?alt=sse"},
		{"/v1/models?key=bad%ZZ", "/v1/models?"},
		{"/v1/models?alt=sse&api-version=2024-02-01", "/v1/models?alt=sse&api-version=2024-02-01"},
		{"/v1/models", "/v1/models"},
		{"/%61pi/oauth/test?code=secret&state=secret", "/api/oauth/test"},
	} {
		t.Run(tc.target, func(t *testing.T) {
			var output bytes.Buffer
			gin.DefaultWriter = &output
			router := gin.New()
			SetUpLogger(router)
			request := httptest.NewRequest(http.MethodGet, tc.target, nil)
			originalURL := *request.URL
			router.NoRoute(func(c *gin.Context) {
				assert.Equal(t, originalURL, *c.Request.URL)
				assert.Equal(t, tc.target, c.Request.RequestURI)
				c.Status(http.StatusUnauthorized)
			})
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)

			require.Equal(t, http.StatusUnauthorized, response.Code)
			_, loggedPath, found := strings.Cut(strings.TrimSuffix(output.String(), "\n"), "GET ")
			require.True(t, found)
			assert.Equal(t, tc.loggedPath, loggedPath)
			assert.Equal(t, originalURL, *request.URL)
			assert.Equal(t, tc.target, request.RequestURI)
		})
	}
}

// TestAccessLogDropsASignatureDecodedIntoAPaymentCallbackPath pins the form
// where an escaped query lands in the path: gin hands the formatter a decoded
// path, so "sign%3D..." arrives with "=" and "&" decoded while RawQuery stays
// empty. The route segment is no longer the last one, which is what made both
// guards read the target as an ordinary path and write the signature verbatim.
func TestAccessLogDropsASignatureDecodedIntoAPaymentCallbackPath(t *testing.T) {
	const (
		canary     = "PATHSECRETSIGNATUREVALUE"
		merchantID = "path-epay-id"
	)
	for _, route := range []string{"/api/user/epay/notify", "/api/subscription/epay/notify"} {
		// The second suffix is the combination: the signature is decoded into the
		// path and the request also carries a query of its own. The "?" then
		// splits the target before the signature, so the first guard still sees a
		// route ending in "sign=" and the second one has to catch it.
		for _, suffix := range []string{"", "?page=2"} {
			t.Run(route+suffix, func(t *testing.T) {
				gin.SetMode(gin.TestMode)
				previousWriter := gin.DefaultWriter
				var output bytes.Buffer
				gin.DefaultWriter = &output
				t.Cleanup(func() { gin.DefaultWriter = previousWriter })

				router := gin.New()
				SetUpLogger(router)
				router.POST(route, func(c *gin.Context) { c.Status(http.StatusOK) })

				request := httptest.NewRequest(http.MethodPost,
					route+"/sign%3D"+canary+"%26pid%3D"+merchantID+suffix, nil)
				require.Equal(t, route+"/sign="+canary+"&pid="+merchantID, request.URL.Path,
					"the escaped query is decoded into the path, which is the case under test")
				require.Equal(t, strings.TrimPrefix(suffix, "?"), request.URL.RawQuery)
				response := httptest.NewRecorder()
				router.ServeHTTP(response, request)

				require.Equal(t, http.StatusNotFound, response.Code)
				logged := output.String()
				require.Contains(t, logged, "POST "+route, "the route stays in the line")
				assert.NotContains(t, logged, canary, "the signature may not reach the log")
				assert.NotContains(t, logged, merchantID)
				assert.NotContains(t, logged, "sign=")
			})
		}
	}
}

// TestAccessLogDropsASignatureBehindADecodedEqualsInAnEarlierSegment pins the
// case a single split at the first "=" misses: when an earlier segment already
// carries a decoded "=", splitting there leaves a route half without the
// callback segment and a query half whose first key is that earlier name, so
// both payment predicates read the target as ordinary and the signature reaches
// the log verbatim. The helper has to look at every "="-bearing segment, not
// only the first one.
func TestAccessLogDropsASignatureBehindADecodedEqualsInAnEarlierSegment(t *testing.T) {
	const canary = "EARLIEREQUALSIGNATUREVALUE"
	for _, tc := range []struct{ name, raw, loggedPath string }{
		{
			"an equals-bearing segment before the callback segment",
			"/api/user/epay/x%3Dy/notify/sign%3D" + canary,
			"/api/user/epay/x=y/notify",
		},
		{
			"an equals-bearing segment before a signature key",
			"/files/a%3Db/sign%3D" + canary,
			"/files/a=b",
		},
		{
			"an equals-bearing segment before a signature word",
			"/dashboard/x%3Dy/signature%3D" + canary,
			"/dashboard/x=y",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			previousWriter := gin.DefaultWriter
			var output bytes.Buffer
			gin.DefaultWriter = &output
			t.Cleanup(func() { gin.DefaultWriter = previousWriter })

			router := gin.New()
			SetUpLogger(router)
			router.POST("/api/user/epay/notify", func(c *gin.Context) { c.Status(http.StatusOK) })
			router.NoRoute(func(c *gin.Context) { c.Status(http.StatusNotFound) })
			request := httptest.NewRequest(http.MethodPost, tc.raw, nil)
			require.NotContains(t, request.URL.Path, "%3D",
				"the escaped \"=\" is decoded into the path, which is the case under test")
			require.Empty(t, request.URL.RawQuery)
			router.ServeHTTP(httptest.NewRecorder(), request)

			logged := strings.TrimSuffix(output.String(), "\n")
			_, loggedPath, found := strings.Cut(logged, "POST ")
			require.True(t, found)
			assert.Equal(t, tc.loggedPath, loggedPath, "the query is dropped and the readable path stays")
			assert.NotContains(t, logged, canary, "the signature may not reach the log")
		})
	}
}

// TestAccessLogDropsASignatureDecodedIntoAPathOnlyForAPaymentCallback pins what
// the decoded-path guard must not change. The normal query form still drops the
// query whole, a non-payment page keeps its readable query and masks only the
// credential-looking value, and a path that merely carries "=" without a payment
// key keeps its target, because nothing there looks like a callback or a
// signature.
func TestAccessLogDropsASignatureDecodedIntoAPathOnlyForAPaymentCallback(t *testing.T) {
	for _, tc := range []struct {
		name       string
		target     string
		loggedPath string
	}{
		{
			"a normal callback query still drops the query whole",
			"/api/user/epay/notify?sign=PATHSECRETSIGNATUREVALUE&pid=x",
			"/api/user/epay/notify",
		},
		{
			"a non-payment query masks only the credential-looking value",
			"/usage-logs?token=PATHSECRETSIGNATUREVALUE&page=2",
			"/usage-logs?page=2&token=%2A%2A%2Amasked%2A%2A%2A",
		},
		{
			"an equals outside a payment segment keeps the target",
			"/files/a=b",
			"/files/a=b",
		},
		{
			"a decoded signature in the root segment keeps only the root",
			"/sign=PATHSECRETSIGNATUREVALUE%26pid%3Dx",
			"/",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			previousWriter := gin.DefaultWriter
			var output bytes.Buffer
			gin.DefaultWriter = &output
			t.Cleanup(func() { gin.DefaultWriter = previousWriter })

			router := gin.New()
			SetUpLogger(router)
			router.NoRoute(func(c *gin.Context) { c.Status(http.StatusNotFound) })
			router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, tc.target, nil))

			logged := strings.TrimSuffix(output.String(), "\n")
			_, loggedPath, found := strings.Cut(logged, "GET ")
			require.True(t, found)
			assert.Equal(t, tc.loggedPath, loggedPath)
			if tc.name != "an equals outside a payment segment keeps the target" {
				assert.NotContains(t, logged, "PATHSECRETSIGNATUREVALUE")
			}
		})
	}
}

// TestAccessLogBoundsTheDecodedPathCandidateScan pins the cost ceiling: the
// guard runs in the formatter for every request, so a path built from many
// "="-bearing segments must not hand the remaining path to the predicates once
// per segment. Past paymentSplitSegmentLimit segments the split returns its
// first candidate, which drops the query from the log instead of leaking one.
func TestAccessLogBoundsTheDecodedPathCandidateScan(t *testing.T) {
	gin.SetMode(gin.TestMode)
	previousWriter := gin.DefaultWriter
	var output bytes.Buffer
	gin.DefaultWriter = &output
	t.Cleanup(func() { gin.DefaultWriter = previousWriter })

	// One more "=" segment than the limit allows, with the signature in the last
	// one, which the bounded scan never reaches.
	path := "/files" + strings.Repeat("/a=b", paymentSplitSegmentLimit+1) + "/sign=" + "BOUNDEDSCANSIGNATUREVALUE"
	router := gin.New()
	SetUpLogger(router)
	router.NoRoute(func(c *gin.Context) { c.Status(http.StatusNotFound) })
	router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, path, nil))

	logged := strings.TrimSuffix(output.String(), "\n")
	_, loggedPath, found := strings.Cut(logged, "POST ")
	require.True(t, found)
	// The bound returns the first candidate, which is the route before the first
	// "="-bearing segment. Without the bound the walk would reach the trailing
	// "sign=" segment and log the whole path instead, so this pins the ceiling.
	assert.Equal(t, "/files", loggedPath)
	assert.NotContains(t, logged, "BOUNDEDSCANSIGNATUREVALUE", "the bounded scan must not log what it stopped before")
}

func TestAccessAndPanicLogsFollowLogRotation(t *testing.T) {
	previousMode, previousWriter, previousErrorWriter := gin.Mode(), gin.DefaultWriter, gin.DefaultErrorWriter
	previousLogDir := *common.LogDir
	gin.SetMode(gin.TestMode)
	*common.LogDir = t.TempDir()
	t.Cleanup(func() {
		gin.SetMode(previousMode)
		gin.DefaultWriter, gin.DefaultErrorWriter = previousWriter, previousErrorWriter
		*common.LogDir = previousLogDir
	})

	logger.SetupLogger()
	router := gin.New()
	router.Use(PanicRecover())
	SetUpLogger(router)
	router.GET("/ok/:step", func(c *gin.Context) { c.Status(http.StatusNoContent) })
	router.GET("/panic", func(c *gin.Context) { panic("rotation-panic-marker") })

	router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/ok/before-rotation", nil))
	content, err := os.ReadFile(logger.GetCurrentLogPath())
	require.NoError(t, err)
	assert.Contains(t, string(content), "/ok/before-rotation")

	// Rotation closes the file the middleware saw at startup. The new file may
	// reuse the old name when both calls land in the same second; the checks
	// below hold either way because only the new descriptor stays open.
	logger.SetupLogger()
	router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/ok/after-rotation", nil))
	panicResponse := httptest.NewRecorder()
	router.ServeHTTP(panicResponse, httptest.NewRequest(http.MethodGet, "/panic", nil))
	require.Equal(t, http.StatusInternalServerError, panicResponse.Code)

	content, err = os.ReadFile(logger.GetCurrentLogPath())
	require.NoError(t, err)
	assert.Contains(t, string(content), "/ok/after-rotation")
	// PanicRecover writes the panic, its target and its stack through
	// common.SysLog rather than letting gin dump the request, so the log has the
	// [SYS] line and a source frame and none of gin's [Recovery] output.
	assert.Contains(t, string(content), "panic detected: rotation-panic-marker")
	assert.Contains(t, string(content), "stacktrace from panic:")
	assert.Contains(t, string(content), "middleware/logger_test.go")
	assert.NotContains(t, string(content), "[Recovery]")
}
