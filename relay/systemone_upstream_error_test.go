package relay

import (
	"compress/gzip"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
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

func TestSystemOneFailureDecodesUpstreamResponse(t *testing.T) {
	const validation = `{"detail":[{"loc":["body","questions","q","criteria"],"msg":"Field required","type":"missing"}]}`
	for _, tc := range []struct {
		name, encoding, body, mapping, retryAfter string
		status, wantStatus                        int
		wantDocument, wantMessage                 string
		skipRetry                                 bool
	}{
		{name: "validation", encoding: "gzip", body: validation, status: 422, wantStatus: 422, wantDocument: validation, skipRetry: true},
		{name: "rate limit", encoding: "x-gzip", body: `{"detail":"slow down"}`, retryAfter: "7", status: 429, wantStatus: 429, wantDocument: `{"detail":"slow down"}`, skipRetry: true},
		{name: "server fault", encoding: "gzip", body: `{"error":{"message":"temporarily unavailable"}}`, mapping: `{"503":502}`, status: 503, wantStatus: 502, wantMessage: "temporarily unavailable"},
		{name: "broken gzip", encoding: "gzip", body: "invalid", mapping: `{"503":502}`, status: 503, wantStatus: 502, wantMessage: "bad response status code 503"},
		{name: "unsupported encoding", encoding: "br", body: validation, status: 422, wantStatus: 422, wantDocument: string(systemOneDetailBytes(422, http.StatusText(422))), skipRetry: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Encoding", tc.encoding)
				if tc.retryAfter != "" {
					w.Header().Set("Retry-After", tc.retryAfter)
				}
				w.WriteHeader(tc.status)
				if (tc.encoding == "gzip" || tc.encoding == "x-gzip") && tc.name != "broken gzip" {
					compressed := gzip.NewWriter(w)
					_, _ = compressed.Write([]byte(tc.body))
					_ = compressed.Close()
					return
				}
				_, _ = w.Write([]byte(tc.body))
			}))
			defer upstream.Close()
			req := httptest.NewRequest(http.MethodPost, "/v1/systemone", nil)
			outbound, err := http.NewRequest(http.MethodPost, upstream.URL, nil)
			require.NoError(t, err)
			outbound.Header.Set("Accept-Encoding", tc.encoding)
			response, err := upstream.Client().Do(outbound)
			require.NoError(t, err)
			defer response.Body.Close()
			require.Equal(t, tc.encoding, response.Header.Get("Content-Encoding"))
			writer := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(writer)
			ctx.Request = req
			apiError := systemOneFailure(ctx, response, tc.mapping)
			require.NotNil(t, apiError)
			assert.Equal(t, tc.wantStatus, apiError.StatusCode)
			assert.Equal(t, tc.skipRetry, types.IsSkipRetryError(apiError))
			assert.Equal(t, tc.retryAfter, writer.Header().Get("Retry-After"))
			if tc.wantDocument != "" {
				assert.Equal(t, tc.wantDocument, string(apiError.Metadata))
			} else {
				assert.Contains(t, apiError.Error(), tc.wantMessage)
			}
		})
	}
}
