package dify

import (
	"encoding/base64"
	"net/http"
	"testing"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestConvertOpenAIRequestRefusesAnOversizedImage drives the refusal through the
// adaptor rather than the converter, so the typed error the client reads is
// pinned: the mapping in ConvertOpenAIRequest is what turns an uploadRejection
// into 400 invalid_request with skip-retry. Without it the handler's own
// wrapping answers 500 convert_request_failed, and every other test in this
// package stays green, which is why this case exists.
func TestConvertOpenAIRequestRefusesAnOversizedImage(t *testing.T) {
	previous := constant.MaxFileDownloadMB
	constant.MaxFileDownloadMB = 1
	t.Cleanup(func() { constant.MaxFileDownloadMB = previous })

	c := newDifyTestContext("req-adaptor-oversized")
	request := imageRequest("data:image/png;base64," + base64.StdEncoding.EncodeToString(make([]byte, (1<<20)+3)))

	_, err := (&Adaptor{}).ConvertOpenAIRequest(c, newDifyRelayInfo("https://dify.example"), &request)

	require.Error(t, err)
	newAPIErr, ok := err.(*types.NewAPIError)
	require.True(t, ok, "the adaptor must return a typed error, got %T", err)
	assert.Equal(t, http.StatusBadRequest, newAPIErr.StatusCode)
	assert.Equal(t, types.ErrorCodeInvalidRequest, newAPIErr.GetErrorCode())
	assert.True(t, types.IsSkipRetryError(newAPIErr), "a refusal of the client's own request must not be retried")
}

// TestConvertOpenAIRequestRefusesAForeignUploadHost pins the same mapping for
// the URL check, which fails before any payload work.
func TestConvertOpenAIRequestRefusesAForeignUploadHost(t *testing.T) {
	c := newDifyTestContext("req-adaptor-host")
	request := imageRequest("data:image/png;base64," + testPNGBase64)

	_, err := (&Adaptor{}).ConvertOpenAIRequest(c, newDifyRelayInfo("file:///tmp/dify"), &request)

	require.Error(t, err)
	newAPIErr, ok := err.(*types.NewAPIError)
	require.True(t, ok, "the adaptor must return a typed error, got %T", err)
	assert.Equal(t, http.StatusBadRequest, newAPIErr.StatusCode)
	assert.Equal(t, types.ErrorCodeInvalidRequest, newAPIErr.GetErrorCode())
	assert.True(t, types.IsSkipRetryError(newAPIErr))
}
