package controller

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
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
