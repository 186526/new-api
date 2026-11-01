package mistral

import (
	"net/http"
	"net/http/httptest"
	"testing"

	relaycommon "github.com/QuantumNous/new-api/relay/common"

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
