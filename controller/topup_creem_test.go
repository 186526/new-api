package controller

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// creemHTTPTransport answers genCreemLink's checkout call without a network.
type creemHTTPTransport func(*http.Request) (*http.Response, error)

func (transport creemHTTPTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return transport(request)
}

// capturePaymentServerLog replaces gin's writers for the duration of a test, the
// way the other tests in this package read a server log line.
func capturePaymentServerLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var output bytes.Buffer
	previousWriter, previousErrorWriter := gin.DefaultWriter, gin.DefaultErrorWriter
	gin.DefaultWriter, gin.DefaultErrorWriter = &output, &output
	t.Cleanup(func() {
		gin.DefaultWriter, gin.DefaultErrorWriter = previousWriter, previousErrorWriter
	})
	return &output
}

// configureCreemCheckoutAnswer makes genCreemLink answer with status and body
// and keeps the operator key configured for the call.
func configureCreemCheckoutAnswer(t *testing.T, status int, body string) {
	t.Helper()
	previousTransport := http.DefaultTransport
	previousAPIKey := setting.CreemApiKey
	setting.CreemApiKey = "creem-test-key"
	t.Cleanup(func() {
		http.DefaultTransport = previousTransport
		setting.CreemApiKey = previousAPIKey
	})
	http.DefaultTransport = creemHTTPTransport(func(request *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: status,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(body)),
			Request:    request,
		}, nil
	})
}

// TestCreemCheckoutResponseSummaryKeepsTheOrderAndDropsTheBody pins
// controller/topup_creem.go's checkout response line. The body used to be
// written whole with body=%q; it carries the payable checkout URL and the
// customer data. The summary keeps the order id and the status code, which the
// operator follows an order with, and drops the body.
func TestCreemCheckoutResponseSummaryKeepsTheOrderAndDropsTheBody(t *testing.T) {
	captured := capturePaymentServerLog(t)
	referenceId := "ref_creem_canary_order"
	body := `{"id":"ch_canary_session","checkout_url":"https://checkout.creem.test/c/session-canary","customer":{"email":"buyer-canary@example.test"},"status":"open"}`
	configureCreemCheckoutAnswer(t, http.StatusCreated, body)

	link, err := genCreemLink(t.Context(), referenceId,
		&CreemProduct{ProductId: "prod_canary", Name: "Canary", Quota: 500, Price: 1},
		"buyer-canary@example.test", "canary")
	require.NoError(t, err)
	assert.Equal(t, "https://checkout.creem.test/c/session-canary", link, "the return value still carries the link")

	logged := captured.String()
	summaryLine := creemResponseSummaryLine(t, logged)
	assert.Contains(t, summaryLine, "trade_no="+referenceId, "the order id stays in the summary")
	assert.Contains(t, summaryLine, "status_code=201", "the status code stays in the summary")
	assert.NotContains(t, summaryLine, "session-canary", "the payable checkout url never reaches this line")
	assert.NotContains(t, summaryLine, "buyer-canary@example.test", "customer data never reaches this line")
	assert.NotContains(t, summaryLine, `body="`+body, "the raw body never reaches this line")
}

// creemResponseSummaryLine returns the checkout response line alone: the two
// lines around it (the request and the created link) are older and name fields
// of their own, so a check of the response summary has to read that line.
func creemResponseSummaryLine(t *testing.T, logged string) string {
	t.Helper()
	for _, line := range strings.Split(logged, "\n") {
		if strings.Contains(line, "Creem API response received") {
			return line
		}
	}
	require.FailNow(t, "the checkout response summary was not logged", logged)
	return ""
}

// TestCreemCheckoutRejectionSummaryNamesTheOrderAndStatus covers the non-2xx
// branch: the operator still reads which order failed and with which status,
// and the body that said why stays out of the log, because an upstream answer
// can echo the key it rejected.
func TestCreemCheckoutRejectionSummaryNamesTheOrderAndStatus(t *testing.T) {
	captured := capturePaymentServerLog(t)
	referenceId := "ref_creem_rejected_order"
	configureCreemCheckoutAnswer(t, http.StatusUnauthorized, `{"message":"invalid api key sk_live_canary_secret"}`)

	_, err := genCreemLink(t.Context(), referenceId,
		&CreemProduct{ProductId: "prod_canary", Name: "Canary", Quota: 500, Price: 1}, "", "canary")
	require.Error(t, err)

	logged := captured.String()
	assert.Contains(t, logged, "trade_no="+referenceId)
	assert.Contains(t, logged, "status_code=401")
	assert.NotContains(t, logged, "sk_live_canary_secret", "a rejection body does not reach the log either")
}

// TestCreemCheckoutSummaryIsTheSharedPaymentSummary pins that the line is built
// by the summary the other callbacks use, so one member list decides what a
// Creem answer may write, and a body that does not parse is reported by size.
func TestCreemCheckoutSummaryIsTheSharedPaymentSummary(t *testing.T) {
	unparsed := "not a json body"
	assert.Equal(t, fmt.Sprintf("body_bytes=%d unparsed=true", len(unparsed)), paymentLogSummary([]byte(unparsed)))
	assert.NotContains(t, paymentLogSummary([]byte(`{"secret":"x","signature":"y"}`)), "secret")
	assert.Equal(t, "status=paid", paymentLogSummary([]byte(`{"secret":"x","status":"paid"}`)))
}

// enableCreemWebhookForTest turns the Creem webhook on for the duration of a
// test, so the handler reaches the lines after the availability check.
func enableCreemWebhookForTest(t *testing.T) {
	t.Helper()
	confirmPaymentComplianceForTest(t)
	previousAPIKey, previousProducts, previousWebhookSecret := setting.CreemApiKey, setting.CreemProducts, setting.CreemWebhookSecret
	t.Cleanup(func() {
		setting.CreemApiKey, setting.CreemProducts, setting.CreemWebhookSecret = previousAPIKey, previousProducts, previousWebhookSecret
	})
	setting.CreemApiKey = "creem-test-key"
	setting.CreemProducts = `[{"productId":"prod_canary"}]`
	setting.CreemWebhookSecret = "creem-test-secret"
}

// creemWebhookRequest drives CreemWebhook for one request and returns what the
// handler wrote and what it logged.
func creemWebhookRequest(t *testing.T, method, target, body string) (*httptest.ResponseRecorder, string) {
	t.Helper()
	captured := capturePaymentServerLog(t)
	gin.SetMode(gin.TestMode)
	previousMode := gin.Mode()
	t.Cleanup(func() { gin.SetMode(previousMode) })

	router := gin.New()
	router.POST("/api/creem/webhook", CreemWebhook)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(method, target, strings.NewReader(body)))
	return recorder, captured.String()
}

// TestCreemWebhookLogsNoCallbackQueryWhenDisabled covers the unauthenticated
// rejection: with the webhook turned off, any caller reaches this line.
func TestCreemWebhookLogsNoCallbackQueryWhenDisabled(t *testing.T) {
	confirmPaymentComplianceForTest(t)
	previousAPIKey, previousProducts, previousWebhookSecret := setting.CreemApiKey, setting.CreemProducts, setting.CreemWebhookSecret
	t.Cleanup(func() {
		setting.CreemApiKey, setting.CreemProducts, setting.CreemWebhookSecret = previousAPIKey, previousProducts, previousWebhookSecret
	})
	setting.CreemWebhookSecret = "" // webhook disabled: the first branch

	recorder, logged := creemWebhookRequest(t, http.MethodPost,
		"/api/creem/webhook?sign=SUPERSECRETSIGNATUREVALUE&pid=canary-pid", `{}`)

	require.Equal(t, http.StatusForbidden, recorder.Code)
	assert.Contains(t, logged, `path="/api/creem/webhook"`, "the line still names the endpoint")
	assert.NotContains(t, logged, "SUPERSECRETSIGNATUREVALUE")
	assert.NotContains(t, logged, "canary-pid")
	assert.NotContains(t, logged, "sign=")
}

// TestCreemWebhookLogsNoCallbackQueryWhenTheBodyCannotBeRead covers the second
// line: a body the handler cannot read still names the endpoint, not the query.
func TestCreemWebhookLogsNoCallbackQueryWhenTheBodyCannotBeRead(t *testing.T) {
	enableCreemWebhookForTest(t)
	captured := capturePaymentServerLog(t)
	gin.SetMode(gin.TestMode)
	previousMode := gin.Mode()
	t.Cleanup(func() { gin.SetMode(previousMode) })

	request := httptest.NewRequest(http.MethodPost,
		"/api/creem/webhook?sign=SUPERSECRETSIGNATUREVALUE&pid=canary-pid", nil)
	request.Body = io.NopCloser(failingReader{})
	router := gin.New()
	router.POST("/api/creem/webhook", CreemWebhook)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusBadRequest, recorder.Code)
	logged := captured.String()
	assert.Contains(t, logged, "failed to read request body")
	assert.Contains(t, logged, `path="/api/creem/webhook"`)
	assert.NotContains(t, logged, "SUPERSECRETSIGNATUREVALUE")
	assert.NotContains(t, logged, "canary-pid")
}

// failingReader is a request body that cannot be read, so CreemWebhook reaches
// its read-error line.
type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

// TestCreemWebhookLogsNoCallbackQueryWhenTheSignatureVerifies covers the third
// line: a signed callback that passes verification still logs the endpoint
// alone.
func TestCreemWebhookLogsNoCallbackQueryWhenTheSignatureVerifies(t *testing.T) {
	enableCreemWebhookForTest(t)
	captured := capturePaymentServerLog(t)
	gin.SetMode(gin.TestMode)
	previousMode := gin.Mode()
	t.Cleanup(func() { gin.SetMode(previousMode) })

	body := `{"id":"evt_canary","eventType":"checkout.completed"}`
	signature := generateCreemSignature(body, setting.CreemWebhookSecret)
	request := httptest.NewRequest(http.MethodPost,
		"/api/creem/webhook?sign=SUPERSECRETSIGNATUREVALUE&pid=canary-pid", strings.NewReader(body))
	request.Header.Set(CreemSignatureHeader, signature)
	router := gin.New()
	router.POST("/api/creem/webhook", CreemWebhook)
	router.ServeHTTP(httptest.NewRecorder(), request)

	logged := captured.String()
	assert.Contains(t, logged, "Creem webhook signature verified")
	assert.NotContains(t, logged, "SUPERSECRETSIGNATUREVALUE")
	assert.NotContains(t, logged, "canary-pid")
	assert.NotContains(t, logged, "sign=")
}
