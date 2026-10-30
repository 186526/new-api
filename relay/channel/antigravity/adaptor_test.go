package antigravity

import (
	"io"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
)

func TestBuildRequestBodyWrapsGeminiRequest(t *testing.T) {
	geminiRequest := &dto.GeminiChatRequest{
		Contents: []dto.GeminiChatContent{
			{Role: "user", Parts: []dto.GeminiPart{{Text: "hello"}}},
		},
		SafetySettings: []dto.GeminiChatSafetySettings{{Category: "HARM_CATEGORY_HATE_SPEECH", Threshold: "BLOCK_NONE"}},
	}

	envelope, err := buildRequestBody("claude-sonnet-5-5-high", "project-123", geminiRequest)
	if err != nil {
		t.Fatalf("buildRequestBody returned error: %v", err)
	}

	if got := envelope["model"]; got != "claude-sonnet-5-5-high" {
		t.Errorf("model = %v, want claude-sonnet-5-5-high", got)
	}
	if got := envelope["project"]; got != "project-123" {
		t.Errorf("project = %v, want project-123", got)
	}
	if got := envelope["userAgent"]; got != envelopeUserAgent {
		t.Errorf("userAgent = %v, want %s", got, envelopeUserAgent)
	}
	if got := envelope["requestType"]; got != requestTypeAgent {
		t.Errorf("requestType = %v, want %s", got, requestTypeAgent)
	}
	requestID, _ := envelope["requestId"].(string)
	if !strings.HasPrefix(requestID, "agent-") {
		t.Errorf("requestId = %q, want agent- prefix", requestID)
	}

	inner, ok := envelope["request"].(map[string]any)
	if !ok {
		t.Fatalf("request is %T, want map[string]any", envelope["request"])
	}
	if _, exists := inner["model"]; exists {
		t.Error("inner request still carries model; upstream reads it from the envelope")
	}
	if _, exists := inner["safetySettings"]; exists {
		t.Error("inner request still carries safetySettings; upstream ignores it")
	}
	if sessionID, _ := inner["sessionId"].(string); !strings.HasPrefix(sessionID, "-") {
		t.Errorf("sessionId = %q, want a negative id", sessionID)
	}
}

func TestBuildRequestBodyMarksImageModels(t *testing.T) {
	envelope, err := buildRequestBody("gemini-3.1-flash-image", "project-123", &dto.GeminiChatRequest{})
	if err != nil {
		t.Fatalf("buildRequestBody returned error: %v", err)
	}
	if got := envelope["requestType"]; got != requestTypeImageGen {
		t.Errorf("requestType = %v, want %s", got, requestTypeImageGen)
	}
	requestID, _ := envelope["requestId"].(string)
	if !strings.HasPrefix(requestID, "image_gen/") {
		t.Errorf("requestId = %q, want image_gen/ prefix", requestID)
	}
}

const streamFixture = `data: {"response":{"candidates":[{"content":{"role":"model","parts":[{"text":"Hel"}]}}],"modelVersion":"gemini-4-argon-high"},"traceId":"t1"}

data: {"response":{"candidates":[{"content":{"role":"model","parts":[{"text":"lo"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":3,"candidatesTokenCount":2,"totalTokenCount":5}},"traceId":"t2"}

`

func TestUnwrapResponseBodyMergesFrames(t *testing.T) {
	merged, err := unwrapResponseBody([]byte(streamFixture))
	if err != nil {
		t.Fatalf("unwrapResponseBody returned error: %v", err)
	}

	var payload struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"content"`
			FinishReason string `json:"finishReason"`
		} `json:"candidates"`
		UsageMetadata struct {
			PromptTokenCount int `json:"promptTokenCount"`
		} `json:"usageMetadata"`
		ModelVersion string `json:"modelVersion"`
	}
	if err := common.Unmarshal(merged, &payload); err != nil {
		t.Fatalf("merged body is not valid json: %v", err)
	}
	if len(payload.Candidates) != 1 {
		t.Fatalf("candidates = %d, want 1", len(payload.Candidates))
	}
	if got := payload.Candidates[0].Content.Parts; len(got) != 1 || got[0].Text != "Hello" {
		t.Errorf("parts = %+v, want a single text part \"Hello\"", got)
	}
	if got := payload.Candidates[0].FinishReason; got != "STOP" {
		t.Errorf("finishReason = %q, want STOP", got)
	}
	if got := payload.UsageMetadata.PromptTokenCount; got != 3 {
		t.Errorf("promptTokenCount = %d, want 3", got)
	}
	if got := payload.ModelVersion; got != "gemini-4-argon-high" {
		t.Errorf("modelVersion = %q, want gemini-4-argon-high", got)
	}
}

func TestUnwrapResponseBodyKeepsThoughtParts(t *testing.T) {
	body := `data: {"response":{"candidates":[{"content":{"parts":[{"text":"think","thought":true},{"text":"a"}]}}]}}

data: {"response":{"candidates":[{"content":{"parts":[{"text":"-more","thought":true},{"text":"b"}]},"finishReason":"STOP"}]}}

`
	merged, err := unwrapResponseBody([]byte(body))
	if err != nil {
		t.Fatalf("unwrapResponseBody returned error: %v", err)
	}
	var payload struct {
		Candidates []struct {
			Content struct {
				Parts []map[string]any `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
	}
	if err := common.Unmarshal(merged, &payload); err != nil {
		t.Fatalf("merged body is not valid json: %v", err)
	}
	parts := payload.Candidates[0].Content.Parts
	if len(parts) != 2 {
		t.Fatalf("parts = %d, want 2 (one thought part and one text part)", len(parts))
	}
	if thought, _ := parts[0]["thought"].(bool); !thought || parts[0]["text"] != "think-more" {
		t.Errorf("thought part = %+v, want merged thought text", parts[0])
	}
	if parts[1]["text"] != "ab" {
		t.Errorf("text part = %+v, want merged text \"ab\"", parts[1])
	}
}

func TestUnwrapResponseBodyReportsUpstreamError(t *testing.T) {
	_, err := unwrapResponseBody([]byte(`data: {"error":{"code":429,"message":"quota exhausted","status":"RESOURCE_EXHAUSTED"}}`))
	if err == nil {
		t.Fatal("want an error for an upstream error frame")
	}
	if !strings.Contains(err.Error(), "quota exhausted") {
		t.Errorf("error = %v, want it to carry the upstream message", err)
	}
}

func TestUnwrapReaderRewritesEnvelope(t *testing.T) {
	body := io.NopCloser(strings.NewReader(streamFixture + "data: [DONE]\n\n"))
	reader := newUnwrapReader(body)

	output, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("reading unwrapped stream failed: %v", err)
	}
	text := string(output)
	if strings.Contains(text, `"response"`) {
		t.Errorf("unwrapped stream still contains the response envelope:\n%s", text)
	}
	if !strings.Contains(text, `"parts":[{"text":"Hel"}]`) {
		t.Errorf("unwrapped stream is missing the first frame:\n%s", text)
	}
	if !strings.Contains(text, "data: [DONE]") {
		t.Errorf("unwrapped stream is missing the done marker:\n%s", text)
	}
	if err := reader.Close(); err != nil {
		t.Errorf("Close returned error: %v", err)
	}
}
