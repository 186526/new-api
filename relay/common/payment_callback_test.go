package common

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestPaymentCallbackQueryNameCoversUnenumerableKeys pins the key-based net
// widening: the M60 review's counter-examples (`epay_sign`, `hash`) and the
// bare provider-prefixed names have to trigger the drop, while a plain
// upstream parameter must not.
func TestPaymentCallbackQueryNameCoversUnenumerableKeys(t *testing.T) {
	for _, key := range []string{
		"sign", "pid", "sign_type", "signature", "sig",
		"epay_sign", "hash", "hmac", "digest", "provider_sign", "HMAC", " Epay_Sign ",
		"merchantSign", "x-sign", "pay-hmac",
	} {
		assert.True(t, IsPaymentCallbackQueryName(key), key)
	}
	for _, key := range []string{
		"alt", "beta", "api-version", "model", "page", "client_version", "input",
		// Words that merely contain a signature word keep their value.
		"design", "signal", "assignee", "hashrate",
	} {
		assert.False(t, IsPaymentCallbackQueryName(key), key)
	}
}

// TestPaymentCallbackQueryTextReadsARawQuery covers the form a swallowed "?"
// leaves behind, where only the text is available.
func TestPaymentCallbackQueryTextReadsARawQuery(t *testing.T) {
	assert.True(t, IsPaymentCallbackQueryText("epay_signature=secret&trade_status=TRADE_SUCCESS"))
	assert.True(t, IsPaymentCallbackQueryText("provider_sign=secret"))
	assert.True(t, IsPaymentCallbackQueryText("sign%5Ftype=MD5&sign=x"))
	assert.False(t, IsPaymentCallbackQueryText("alt=sse&api-version=2024-02-01"))
	assert.False(t, IsPaymentCallbackQueryText(""))
}

// TestIsPaymentCallbackQueryNameIsTheOnlyNet pins that the key net is read from
// raw query text, which is the one form the access log always has.
func TestPaymentCallbackQueryTextReadsEveryGatewayName(t *testing.T) {
	assert.True(t, IsPaymentCallbackQueryText("gateway_sign=x&trade_status=ok"))
	assert.False(t, IsPaymentCallbackQueryText("design=wide&signal=on"))
}

// TestIsPaymentCallbackPathReadsTheRoute pins the route-based trigger, which is
// the one a gateway cannot move by renaming its signature parameter.
func TestIsPaymentCallbackPathReadsTheRoute(t *testing.T) {
	for _, path := range []string{
		"/api/user/epay/notify",
		"/api/subscription/epay/notify",
		"/api/subscription/epay/return",
		"/api/creem/webhook",
		"/api/waffo-pancake/webhook/prod",
		"/gateway/payments/api/creem/webhook",
		"/api/user/epay/notify/",
	} {
		assert.True(t, IsPaymentCallbackPath(path), path)
	}
	for _, path := range []string{
		"/v1/chat/completions",
		"/usage-logs",
		"/api/user/self",
		"/api/token/",
		"",
	} {
		assert.False(t, IsPaymentCallbackPath(path), path)
	}
}

// TestSanitizeURLForLogMasksOnlyThePaymentSignatureKeys pins the shared mask:
// the keys the relay itself sends stay readable, and the payment signature
// names, including the merchant id that travels beside the signature, do not.
func TestSanitizeURLForLogMasksOnlyThePaymentSignatureKeys(t *testing.T) {
	masked := SanitizeURLForLog("https://example.test/callback?sign=secret&pid=merchant&signature=secret&sig=secret&alt=sse&beta=true")
	assert.NotContains(t, masked, "secret")
	assert.NotContains(t, masked, "merchant")
	assert.Contains(t, masked, "alt=sse")
	assert.Contains(t, masked, "beta=true")

	kept := "https://example.test/v1/models?api-version=2024-02-01&client_version=1.2&batch=3"
	assert.Equal(t, kept, SanitizeURLForLog(kept))
}
