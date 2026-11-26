package relay

import (
	"net/http"
	"testing"

	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSystemOneUpstreamErrorKeepsTheValidationArray(t *testing.T) {
	body := []byte(`{"detail":[{"loc":["body","questions","q","criteria"],"msg":"Field required","type":"missing"}]}`)
	apiError := systemOneUpstreamError(http.StatusUnprocessableEntity, body, "")
	require.NotNil(t, apiError)
	assert.Equal(t, http.StatusUnprocessableEntity, apiError.StatusCode)
	assert.JSONEq(t, string(body), string(apiError.Metadata))
}

func TestSystemOneUpstreamErrorRebuildsAForeignDocument(t *testing.T) {
	apiError := systemOneUpstreamError(http.StatusBadRequest, []byte(`{"error":{"message":"bad model"}}`), "")
	require.NotNil(t, apiError)
	assert.Equal(t, http.StatusBadRequest, apiError.StatusCode)
	assert.JSONEq(t,
		`{"detail":{"error_type":"invalid_request_error","message":"bad model"}}`,
		string(apiError.Metadata))
}

func TestSystemOneUpstreamErrorLeavesServerFaultsToTheRetryLoop(t *testing.T) {
	assert.Nil(t, systemOneUpstreamError(http.StatusInternalServerError, []byte("upstream exploded"), ""))
	assert.Nil(t, systemOneUpstreamError(http.StatusBadGateway, []byte("gateway"), ""))
}

func TestSystemOneUpstreamErrorSkipsRetry(t *testing.T) {
	apiError := systemOneUpstreamError(http.StatusTooManyRequests, []byte(`{"detail":"slow down"}`), "7")
	require.NotNil(t, apiError)
	assert.True(t, types.IsSkipRetryError(apiError))
	// The retry delay is reported on the response header, which the handler
	// writes from the upstream's own Retry-After.
	assert.Contains(t, apiError.Error(), "slow down")
}
