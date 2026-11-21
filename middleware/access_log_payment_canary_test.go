package middleware

import (
	"bytes"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"syscall"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAccessLogDropsAQueryOfAPaymentCallbackRoute pins the route-based trigger:
// a callback whose signature key is not on any list still loses its whole query
// because the access log recognises the route, not the parameter name. The
// parameter names below are deliberately ones no list holds, which is the case
// the M60 review found: an Epay-compatible gateway that signs under
// `epay_signature` or `merchant_token` used to land on the ordinary branch.
func TestAccessLogDropsAQueryOfAPaymentCallbackRoute(t *testing.T) {
	for _, tc := range []struct{ name, target, loggedPath string }{
		{
			"epay notify",
			"/api/user/epay/notify?epay_signature=SUPERSECRETSIGNATUREVALUE&trade_status=TRADE_SUCCESS",
			"/api/user/epay/notify",
		},
		{
			"/subscription notify",
			"/api/subscription/epay/notify?epay_signature=SUPERSECRETSIGNATUREVALUE&trade_status=TRADE_SUCCESS",
			"/api/subscription/epay/notify",
		},
		{
			"subscription return",
			"/api/subscription/epay/return?epay_signature=SUPERSECRETSIGNATUREVALUE&out_trade_no=ORDER1",
			"/api/subscription/epay/return",
		},
		{
			"webhook behind a custom notify_url prefix",
			"/gateway/payments/api/creem/webhook?merchant_token=SUPERSECRETSIGNATUREVALUE",
			"/gateway/payments/api/creem/webhook",
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
			router.GET("/*rest", func(c *gin.Context) {
				assert.Equal(t, "SUPERSECRETSIGNATUREVALUE", c.Query("epay_signature")+c.Query("merchant_token"),
					"the handler still reads the query the log dropped")
				c.Status(http.StatusOK)
			})
			request := httptest.NewRequest(http.MethodGet, tc.target, nil)
			router.ServeHTTP(httptest.NewRecorder(), request)

			logged := output.String()
			require.Contains(t, logged, "GET "+tc.loggedPath, "the path stays in the line")
			assert.NotContains(t, logged, "SUPERSECRETSIGNATUREVALUE", "no part of the query may reach the log")
			assert.NotContains(t, logged, "epay_signature", "the whole query is dropped, not masked key by key")
			assert.NotContains(t, logged, "merchant_token")
		})
	}
}

// TestAccessLogDropsAQueryThePathParameterSwallowed pins the third review
// finding: gin hands the formatter a decoded path, so a route parameter holding
// an escaped "?" arrives with the query inside param.Path while RawQuery is
// empty. The old branch was entered only when RawQuery was non-empty, so this
// target reached the log verbatim and latent; the rule now reads the "?" in the
// target and drops what follows, because what follows is what a callback
// signature travels in.
func TestAccessLogDropsAQueryThePathParameterSwallowed(t *testing.T) {
	gin.SetMode(gin.TestMode)
	previousWriter := gin.DefaultWriter
	var output bytes.Buffer
	gin.DefaultWriter = &output
	t.Cleanup(func() { gin.DefaultWriter = previousWriter })

	router := gin.New()
	SetUpLogger(router)
	// NoRoute keeps the request unmodified: the test needs the same URL the
	// formatter reads, which a route parameter would rewrite.
	router.NoRoute(func(c *gin.Context) { c.Status(http.StatusNotFound) })
	request := httptest.NewRequest(http.MethodGet, "/files/a%3Fsign=SUPERSECRETSIGNATUREVALUE", nil)
	require.Empty(t, request.URL.RawQuery, "the query is in the path, which is the case under test")
	router.ServeHTTP(httptest.NewRecorder(), request)

	assert.NotContains(t, output.String(), "SUPERSECRETSIGNATUREVALUE")
	assert.NotContains(t, output.String(), "sign=")
}

// TestAccessLogDropsASwallowedQueryBesideARealOne covers the combination: the
// path parameter swallowed a "?" and the request also carried a query of its
// own. Reading the escaped path for the decision would re-escape the swallowed
// "?" and print the signature after it, so this target has to be judged on the
// decoded path.
func TestAccessLogDropsASwallowedQueryBesideARealOne(t *testing.T) {
	gin.SetMode(gin.TestMode)
	previousWriter := gin.DefaultWriter
	var output bytes.Buffer
	gin.DefaultWriter = &output
	t.Cleanup(func() { gin.DefaultWriter = previousWriter })

	router := gin.New()
	SetUpLogger(router)
	router.NoRoute(func(c *gin.Context) { c.Status(http.StatusNotFound) })
	request := httptest.NewRequest(http.MethodGet, "/files/a%3Fsign=SUPERSECRETSIGNATUREVALUE?page=2", nil)
	require.Equal(t, "page=2", request.URL.RawQuery, "a real query sits beside the swallowed one")
	router.ServeHTTP(httptest.NewRecorder(), request)

	assert.NotContains(t, output.String(), "SUPERSECRETSIGNATUREVALUE")
}

// TestAccessLogMasksARouteLessPaymentSignature keeps the key-based net covered:
// a signed redirect to a page this tree does not route as a callback still
// carries a signature key, and the mask has to catch the names it cannot fully
// enumerate.
func TestAccessLogMasksARouteLessPaymentSignature(t *testing.T) {
	for _, key := range []string{"sign", "pid", "signature", "epay_sign", "hmac", "digest", "provider_sign"} {
		t.Run(key, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			previousWriter := gin.DefaultWriter
			var output bytes.Buffer
			gin.DefaultWriter = &output
			t.Cleanup(func() { gin.DefaultWriter = previousWriter })

			router := gin.New()
			SetUpLogger(router)
			router.GET("/usage-logs", func(c *gin.Context) { c.Status(http.StatusOK) })
			router.ServeHTTP(httptest.NewRecorder(),
				httptest.NewRequest(http.MethodGet, "/usage-logs?"+key+"=SUPERSECRETSIGNATUREVALUE&page=2", nil))

			logged := output.String()
			assert.NotContains(t, logged, "SUPERSECRETSIGNATUREVALUE")
			// A route-less signature drops the query whole rather than masking
			// one key, because the gateway chooses the keys beside it.
			assert.Contains(t, logged, "GET /usage-logs")
		})
	}
}

// TestAccessLogKeepsAnOrdinaryQuery pins the counter-case: redaction must not
// swallow the query of a page or an upstream-facing route, which is what makes
// the log useful at all.
func TestAccessLogKeepsAnOrdinaryQuery(t *testing.T) {
	gin.SetMode(gin.TestMode)
	previousWriter := gin.DefaultWriter
	var output bytes.Buffer
	gin.DefaultWriter = &output
	t.Cleanup(func() { gin.DefaultWriter = previousWriter })

	router := gin.New()
	SetUpLogger(router)
	router.GET("/v1/models", func(c *gin.Context) { c.Status(http.StatusOK) })
	router.ServeHTTP(httptest.NewRecorder(),
		httptest.NewRequest(http.MethodGet, "/v1/models?alt=sse&api-version=2024-02-01&beta=true", nil))

	assert.Contains(t, output.String(), "/v1/models?alt=sse")
	assert.Contains(t, output.String(), "api-version=2024-02-01")
	assert.Contains(t, output.String(), "beta=true")
}

// TestPanicRecoveryNeverWritesTheCallbackQuery pins the second review finding.
// gin's CustomRecoveryWithWriter formats httputil.DumpRequest, request line and
// query included, into its writer. It writes that dump on a broken pipe in
// every mode, and a payment gateway that gives up while the handler still works
// is exactly a broken pipe. The recovery has to keep the panic and its stack in
// the log while writing neither query value.
func TestPanicRecoveryNeverWritesTheCallbackQuery(t *testing.T) {
	for _, tc := range []struct {
		name    string
		panicOn any
	}{
		{"ordinary panic", "callback-panic-marker"},
		{"client gave up mid-handler", brokenPipePanic()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gin.SetMode(gin.ReleaseMode)
			previousMode := gin.Mode()
			previousWriter, previousErrorWriter := gin.DefaultWriter, gin.DefaultErrorWriter
			var output bytes.Buffer
			gin.DefaultWriter, gin.DefaultErrorWriter = &output, &output
			t.Cleanup(func() {
				gin.SetMode(previousMode)
				gin.DefaultWriter, gin.DefaultErrorWriter = previousWriter, previousErrorWriter
			})

			router := gin.New()
			router.Use(PanicRecover())
			SetUpLogger(router)
			router.GET("/api/user/epay/notify", func(c *gin.Context) { panic(tc.panicOn) })
			request := httptest.NewRequest(http.MethodGet,
				"/api/user/epay/notify?epay_signature=SUPERSECRETSIGNATUREVALUE&pid=canary-epay-id", nil)
			router.ServeHTTP(httptest.NewRecorder(), request)

			logged := output.String()
			assert.Contains(t, logged, "stacktrace from panic:", "the stack trace still reaches the log")
			assert.Contains(t, logged, "access_log_payment_canary_test.go")
			assert.NotContains(t, logged, "SUPERSECRETSIGNATUREVALUE")
			assert.NotContains(t, logged, "canary-epay-id")
			assert.NotContains(t, logged, "epay_signature=")
			assert.NotContains(t, logged, "[Recovery]", "gin's request dump is not written at all")
		})
	}
}

// brokenPipePanic builds the panic value gin's recovery reads as a client that
// gave up: a *net.OpError wrapping a *os.SyscallError whose text names the
// condition.
func brokenPipePanic() error {
	return &net.OpError{
		Op:  "write",
		Net: "tcp",
		Err: &os.SyscallError{Syscall: "write", Err: syscall.EPIPE},
	}
}

// TestPanicRecoveryKeepsStackTraceAndTarget pins what the replacement still has
// to say: a panic on an ordinary route names the route and keeps the trace, so
// the redaction did not trade away the diagnosis.
func TestPanicRecoveryKeepsStackTraceAndTarget(t *testing.T) {
	gin.SetMode(gin.TestMode)
	previousWriter, previousErrorWriter := gin.DefaultWriter, gin.DefaultErrorWriter
	var output bytes.Buffer
	gin.DefaultWriter, gin.DefaultErrorWriter = &output, &output
	t.Cleanup(func() {
		gin.DefaultWriter, gin.DefaultErrorWriter = previousWriter, previousErrorWriter
	})

	router := gin.New()
	router.Use(PanicRecover())
	router.GET("/panic/marker", func(c *gin.Context) { panic("ordinary-panic-marker") })
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/panic/marker?alt=sse", nil))

	require.Equal(t, http.StatusInternalServerError, response.Code)
	logged := output.String()
	assert.Contains(t, logged, "ordinary-panic-marker")
	assert.Contains(t, logged, "stacktrace from panic:")
	assert.Contains(t, logged, `panic request method=GET target="/panic/marker?alt=sse"`)
}

func TestPanicRecoveryAnswers500AndAborts(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(PanicRecover())
	router.GET("/panic", func(c *gin.Context) { panic("answer-marker") })
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/panic", nil))

	require.Equal(t, http.StatusInternalServerError, response.Code)
	assert.Contains(t, response.Body.String(), "new_api_panic")
	// The body is JSON with the marker, and the request did not continue past
	// the recovery.
	assert.True(t, strings.Contains(response.Body.String(), "answer-marker"))
}
