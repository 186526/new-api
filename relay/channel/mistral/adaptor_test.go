package mistral

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"

	"github.com/gin-gonic/gin"
)

func TestSetupRequestHeaderUsesVibeUserAgentForPlanKeys(t *testing.T) {
	header := setupHeaderForAPIKey(t, "mstrl_test_key_123")
	if got := header.Get("User-Agent"); got != mistralVibeUserAgent {
		t.Errorf("User-Agent = %q, want %q", got, mistralVibeUserAgent)
	}
	if got := header.Get("Authorization"); got != "Bearer mstrl_test_key_123" {
		t.Errorf("Authorization = %q, want the bearer key", got)
	}
}

func TestSetupRequestHeaderKeepsDefaultUserAgentForAPIKeys(t *testing.T) {
	header := setupHeaderForAPIKey(t, "regularMistralKey")
	if got := header.Get("User-Agent"); got != "" {
		t.Errorf("User-Agent = %q, want it unset for a non-plan key", got)
	}
}

func setupHeaderForAPIKey(t *testing.T, apiKey string) http.Header {
	t.Helper()
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)

	header := http.Header{}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ApiKey: apiKey}}
	if err := (&Adaptor{}).SetupRequestHeader(c, &header, info); err != nil {
		t.Fatalf("SetupRequestHeader() error: %v", err)
	}
	return header
}

func TestConvertClaudeRequestRefusesWithATypedError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ApiKey: "mstrl_test_key_123"}}

	var converted any
	var err error
	func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				t.Fatalf("ConvertClaudeRequest panicked: %v", recovered)
			}
		}()
		converted, err = (&Adaptor{}).ConvertClaudeRequest(c, info, &dto.ClaudeRequest{Model: "mistral-large-latest"})
	}()

	if err == nil {
		t.Fatalf("ConvertClaudeRequest() error = nil, want a conversion error")
	}
	if converted != nil {
		t.Errorf("ConvertClaudeRequest() request = %v, want nil", converted)
	}

	var apiError *types.NewAPIError
	if !errors.As(err, &apiError) {
		t.Fatalf("ConvertClaudeRequest() error type = %T, want *types.NewAPIError", err)
	}
	if code := apiError.GetErrorCode(); code != types.ErrorCodeConvertRequestFailed {
		t.Errorf("error code = %q, want %q", code, types.ErrorCodeConvertRequestFailed)
	}
	if apiError.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", apiError.StatusCode, http.StatusBadRequest)
	}
	if !types.IsSkipRetryError(apiError) {
		t.Errorf("skip retry = false, want true: another attempt cannot make a Claude-format request acceptable to a Mistral channel")
	}
}
