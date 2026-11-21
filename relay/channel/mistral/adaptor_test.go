package mistral

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
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

// The channel test in controller/channel-test.go converts the request it built
// in memory over a context whose request carries no body. An empty stored body
// must not turn into "unexpected end of JSON input" here.
func TestConvertOpenAIRequestReadsTheParsedRequestWhenTheStoredBodyIsEmpty(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ApiKey: "mstrl_test_key_123"}}
	request := &dto.GeneralOpenAIRequest{
		Model:    "deepseek-v4-1-flash-1017",
		Messages: []dto.Message{{Role: "user", Content: "hi"}},
	}

	converted, err := (&Adaptor{}).ConvertOpenAIRequest(c, info, request)
	if err != nil {
		t.Fatalf("ConvertOpenAIRequest() error = %v, want the converted parsed request", err)
	}

	body, ok := converted.([]byte)
	if !ok {
		t.Fatalf("ConvertOpenAIRequest() = %T, want []byte", converted)
	}
	var decoded map[string]json.RawMessage
	if err := common.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("unmarshal converted body %s: %v", body, err)
	}
	var model string
	if err := common.Unmarshal(decoded["model"], &model); err != nil {
		t.Fatalf("unmarshal converted model %s: %v", decoded["model"], err)
	}
	if model != "deepseek-v4-1-flash-1017" {
		t.Errorf("model = %q, want %q", model, "deepseek-v4-1-flash-1017")
	}
	if !strings.Contains(string(body), `"content":"hi"`) {
		t.Errorf("converted body = %s, want the parsed request's message", body)
	}
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
