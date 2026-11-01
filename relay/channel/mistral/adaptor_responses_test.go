package mistral

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"

	"github.com/gin-gonic/gin"
)

func TestConvertOpenAIResponsesRequestProducesMistralChatRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}

	converted, err := (&Adaptor{}).ConvertOpenAIResponsesRequest(c, info, dto.OpenAIResponsesRequest{
		Model:        "deepseek-v4.1-flash",
		Instructions: json.RawMessage(`"answer briefly"`),
		Input:        []byte(`[{"role":"user","content":[{"type":"input_text","text":"hello"}]}]`),
	})
	if err != nil {
		t.Fatalf("ConvertOpenAIResponsesRequest() error: %v", err)
	}
	chatRequest, ok := converted.(*dto.GeneralOpenAIRequest)
	if !ok {
		t.Fatalf("converted request has type %T, want *dto.GeneralOpenAIRequest", converted)
	}
	if chatRequest.Model != "deepseek-v4.1-flash" {
		t.Errorf("model = %q, want %q", chatRequest.Model, "deepseek-v4.1-flash")
	}
	for i, message := range chatRequest.Messages {
		switch message.Role {
		case "system", "user", "assistant", "tool":
		default:
			t.Errorf("message %d role = %q, which Mistral rejects", i, message.Role)
		}
	}
}

func TestDoResponseRoutesResponsesFormatThroughChatConversion(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       http.NoBody,
	}
	info := &relaycommon.RelayInfo{
		RelayFormat: types.RelayFormatOpenAIResponses,
		ChannelMeta: &relaycommon.ChannelMeta{},
	}

	_, apiErr := (&Adaptor{}).DoResponse(c, resp, info)

	if apiErr != nil && apiErr.Error() == "not implemented" {
		t.Fatalf("DoResponse() still reports the Responses format as not implemented")
	}
}
