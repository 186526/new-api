package common

import (
	"encoding/json"
	"testing"

	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testImageAlpha = "data:image/png;base64,AAAA"
	testImageBeta  = "data:image/png;base64,BBBB"
)

func openAIChatRequestWithImages(urls ...string) *dto.GeneralOpenAIRequest {
	parts := make([]any, 0, len(urls)+1)
	parts = append(parts, map[string]any{"type": "text", "text": "look"})
	for _, url := range urls {
		parts = append(parts, map[string]any{
			"type":      "image_url",
			"image_url": map[string]any{"url": url},
		})
	}
	return &dto.GeneralOpenAIRequest{
		Model:    "claude-sonnet-4.5",
		Messages: []dto.Message{{Role: "user", Content: parts}},
	}
}

func responsesRequestWithImages(urls ...string) *dto.OpenAIResponsesRequest {
	content := make([]any, 0, len(urls)+1)
	content = append(content, map[string]any{"type": "input_text", "text": "look"})
	for _, url := range urls {
		content = append(content, map[string]any{"type": "input_image", "image_url": url})
	}
	input := []any{map[string]any{"type": "message", "role": "user", "content": content}}
	raw, _ := json.Marshal(input)
	return &dto.OpenAIResponsesRequest{Model: "gpt-5.1", Input: raw}
}

func claudeRequestWithImages(urls ...string) *dto.ClaudeRequest {
	content := make([]any, 0, len(urls)+1)
	content = append(content, map[string]any{"type": "text", "text": "look"})
	for _, url := range urls {
		content = append(content, map[string]any{
			"type":   "image",
			"source": map[string]any{"type": "url", "url": url},
		})
	}
	return &dto.ClaudeRequest{
		Model:    "claude-sonnet-4.5",
		Messages: []dto.ClaudeMessage{{Role: "user", Content: content}},
	}
}

func claudeRequestWithSystemImage(images ...string) *dto.ClaudeRequest {
	blocks := make([]any, 0, len(images))
	for _, url := range images {
		blocks = append(blocks, map[string]any{
			"type":   "image",
			"source": map[string]any{"type": "url", "url": url},
		})
	}
	return &dto.ClaudeRequest{
		Model:    "claude-sonnet-4.5",
		System:   blocks,
		Messages: []dto.ClaudeMessage{{Role: "user", Content: "hi"}},
	}
}

func claudeRequestWithToolResultImage(images ...string) *dto.ClaudeRequest {
	content := make([]any, 0, len(images))
	for _, url := range images {
		content = append(content, map[string]any{
			"type":   "image",
			"source": map[string]any{"type": "url", "url": url},
		})
	}
	return &dto.ClaudeRequest{
		Model: "claude-sonnet-4.5",
		Messages: []dto.ClaudeMessage{
			{Role: "assistant", Content: []any{map[string]any{"type": "tool_use", "id": "toolu_1", "name": "screenshot", "input": map[string]any{}}}},
			{Role: "user", Content: []any{map[string]any{"type": "tool_result", "tool_use_id": "toolu_1", "content": content}}},
		},
	}
}

func geminiRequestWithImages(urls ...string) *dto.GeminiChatRequest {
	parts := make([]dto.GeminiPart, 0, len(urls)+1)
	parts = append(parts, dto.GeminiPart{Text: "look"})
	for _, url := range urls {
		parts = append(parts, dto.GeminiPart{
			InlineData: &dto.GeminiInlineData{MimeType: "image/png", Data: url},
		})
	}
	return &dto.GeminiChatRequest{
		Contents: []dto.GeminiChatContent{{Role: "user", Parts: parts}},
	}
}

// geminiBatchRequest wraps the given requests as the elements of a
// generateContent batch request, the shape a batched prediction sends.
func geminiBatchRequest(requests ...*dto.GeminiChatRequest) *dto.GeminiChatRequest {
	batch := &dto.GeminiChatRequest{}
	for _, request := range requests {
		batch.Requests = append(batch.Requests, *request)
	}
	return batch
}

// A batch request carries its images inside requests[], and the relay sends
// every element upstream, so the count has to cover them all.
func TestRequestImageCountCountsGeminiBatchRequestImages(t *testing.T) {
	cases := []struct {
		name    string
		request *dto.GeminiChatRequest
		want    int
	}{
		{
			name: "every element counts",
			request: geminiBatchRequest(
				geminiRequestWithImages(testImageAlpha, testImageBeta),
				geminiRequestWithImages(testImageAlpha),
				&dto.GeminiChatRequest{Contents: []dto.GeminiChatContent{{Role: "user", Parts: []dto.GeminiPart{{Text: "no image"}}}}},
			),
			want: 3,
		},
		{
			name: "an element repeats an image",
			request: geminiBatchRequest(
				geminiRequestWithImages(testImageAlpha, testImageAlpha),
			),
			want: 2,
		},
		{
			name: "contents and requests are both counted",
			request: func() *dto.GeminiChatRequest {
				batch := geminiBatchRequest(geminiRequestWithImages(testImageBeta, testImageBeta))
				batch.Contents = geminiRequestWithImages(testImageAlpha).Contents
				return batch
			}(),
			want: 3,
		},
		{
			name: "an element carries its own requests",
			request: geminiBatchRequest(
				geminiBatchRequest(
					geminiRequestWithImages(testImageAlpha),
					geminiRequestWithImages(testImageAlpha, testImageBeta),
				),
				geminiRequestWithImages(testImageBeta),
			),
			want: 4,
		},
		{
			name: "a batched file image counts",
			request: geminiBatchRequest(&dto.GeminiChatRequest{Contents: []dto.GeminiChatContent{{Role: "user", Parts: []dto.GeminiPart{
				{FileData: &dto.GeminiFileData{MimeType: "image/jpeg", FileUri: "https://example.test/a.jpg"}},
			}}}}),
			want: 1,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, RequestImageCount(types.RelayFormatGemini, tc.request))
		})
	}
}

func TestRequestImageCountCountsEveryRelayFormat(t *testing.T) {
	cases := []struct {
		name        string
		relayFormat types.RelayFormat
		request     dto.Request
		want        int
	}{
		{name: "openai chat", relayFormat: types.RelayFormatOpenAI, request: openAIChatRequestWithImages(testImageAlpha, testImageBeta), want: 2},
		{name: "openai responses", relayFormat: types.RelayFormatOpenAIResponses, request: responsesRequestWithImages(testImageAlpha, testImageBeta), want: 2},
		{name: "openai responses with image_url object", relayFormat: types.RelayFormatOpenAIResponses, request: &dto.OpenAIResponsesRequest{Model: "gpt-5.1", Input: json.RawMessage(`[{"type":"message","role":"user","content":[{"type":"input_image","image_url":{"url":"` + testImageAlpha + `"}}]}]`)}, want: 1},
		{name: "claude messages", relayFormat: types.RelayFormatClaude, request: claudeRequestWithImages(testImageAlpha, testImageBeta), want: 2},
		{name: "claude system prompt", relayFormat: types.RelayFormatClaude, request: claudeRequestWithSystemImage(testImageAlpha), want: 1},
		{name: "claude tool result", relayFormat: types.RelayFormatClaude, request: claudeRequestWithToolResultImage(testImageBeta), want: 1},
		{name: "gemini generate content", relayFormat: types.RelayFormatGemini, request: geminiRequestWithImages(testImageAlpha, testImageBeta), want: 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, RequestImageCount(tc.relayFormat, tc.request))
		})
	}
}

// The upstream counts image occurrences, not distinct images: the Mistral API
// refuses nine parts that hold three different images, so a repeated image
// must count again here.
func TestRequestImageCountCountsRepeatedImagesEveryTime(t *testing.T) {
	cases := []struct {
		name        string
		relayFormat types.RelayFormat
		request     dto.Request
		want        int
	}{
		{name: "openai chat", relayFormat: types.RelayFormatOpenAI, request: openAIChatRequestWithImages(testImageAlpha, testImageAlpha, testImageBeta), want: 3},
		{name: "openai responses", relayFormat: types.RelayFormatOpenAIResponses, request: responsesRequestWithImages(testImageAlpha, testImageAlpha, testImageBeta), want: 3},
		{name: "claude messages", relayFormat: types.RelayFormatClaude, request: claudeRequestWithImages(testImageAlpha, testImageAlpha, testImageBeta), want: 3},
		{name: "claude system prompt", relayFormat: types.RelayFormatClaude, request: claudeRequestWithSystemImage(testImageAlpha, testImageAlpha), want: 2},
		{name: "claude tool result", relayFormat: types.RelayFormatClaude, request: claudeRequestWithToolResultImage(testImageBeta, testImageBeta), want: 2},
		{name: "gemini generate content", relayFormat: types.RelayFormatGemini, request: geminiRequestWithImages(testImageAlpha, testImageAlpha, testImageBeta), want: 3},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, RequestImageCount(tc.relayFormat, tc.request),
				"a repeated image counts once per occurrence")
		})
	}
}

func TestRequestImageCountCountsTheSameImageAcrossMessages(t *testing.T) {
	request := &dto.ClaudeRequest{
		Model: "claude-sonnet-4.5",
		Messages: []dto.ClaudeMessage{
			{Role: "user", Content: []any{map[string]any{
				"type":   "image",
				"source": map[string]any{"type": "url", "url": testImageAlpha},
			}}},
			{Role: "user", Content: []any{map[string]any{
				"type":   "image",
				"source": map[string]any{"type": "url", "url": testImageAlpha},
			}}},
		},
	}
	assert.Equal(t, 2, RequestImageCount(types.RelayFormatClaude, request))
}

func TestRequestImageCountIgnoresNonImageParts(t *testing.T) {
	request := &dto.GeneralOpenAIRequest{
		Model: "claude-sonnet-4.5",
		Messages: []dto.Message{{Role: "user", Content: []any{
			map[string]any{"type": "text", "text": "hello"},
			map[string]any{"type": "file", "file": map[string]any{
				"filename":  "notes.txt",
				"file_data": "data:text/plain;base64,Q0NDRA==",
			}},
		}}},
	}
	assert.Equal(t, 0, RequestImageCount(types.RelayFormatOpenAI, request))
}

func TestRequestImageCountSkipsFormatsWithoutImages(t *testing.T) {
	request := openAIChatRequestWithImages(testImageAlpha)
	assert.Equal(t, 0, RequestImageCount(types.RelayFormatEmbedding, request))
	assert.Equal(t, 0, RequestImageCount(types.RelayFormatRerank, request))
	assert.Equal(t, 0, RequestImageCount(types.RelayFormatOpenAI, nil))
}

func TestRequestImageCountSkipsImagesWithoutData(t *testing.T) {
	request := &dto.GeneralOpenAIRequest{
		Model: "claude-sonnet-4.5",
		Messages: []dto.Message{{Role: "user", Content: []any{
			map[string]any{"type": "image_url", "image_url": map[string]any{"url": ""}},
			map[string]any{"type": "image_url", "image_url": map[string]any{"url": testImageAlpha}},
		}}},
	}
	assert.Equal(t, 1, RequestImageCount(types.RelayFormatOpenAI, request))
}

func TestRequestImageCountCountsGeminiFileImages(t *testing.T) {
	request := &dto.GeminiChatRequest{
		Contents: []dto.GeminiChatContent{{Role: "user", Parts: []dto.GeminiPart{
			{FileData: &dto.GeminiFileData{MimeType: "image/jpeg", FileUri: "https://example.test/a.jpg"}},
			{FileData: &dto.GeminiFileData{MimeType: "video/mp4", FileUri: "https://example.test/a.mp4"}},
			{InlineData: &dto.GeminiInlineData{MimeType: "audio/wav", Data: testImageBeta}},
		}}},
	}
	assert.Equal(t, 1, RequestImageCount(types.RelayFormatGemini, request))
}

// A tool response replayed in an agent loop carries the screenshot the tool
// produced inside functionResponse.parts, which arrives as raw bytes and must
// decode into the same parts the request itself uses.
func TestRequestImageCountCountsGeminiFunctionResponseImages(t *testing.T) {
	replay, err := json.Marshal([]dto.GeminiPart{
		{InlineData: &dto.GeminiInlineData{MimeType: "image/png", Data: testImageAlpha}},
		{InlineData: &dto.GeminiInlineData{MimeType: "image/png", Data: testImageBeta}},
		{InlineData: &dto.GeminiInlineData{MimeType: "image/png", Data: testImageAlpha}},
		{Text: "captured the login page"},
	})
	require.NoError(t, err)

	// The replay only counts when it decodes into GeminiPart values; a shape
	// the decoder drops would leave the count at zero.
	var decoded []dto.GeminiPart
	require.NoError(t, json.Unmarshal(replay, &decoded))
	require.Len(t, decoded, 4, "the replayed parts must decode as Gemini parts")
	require.NotNil(t, decoded[0].InlineData, "the replayed image must survive decoding")

	request := &dto.GeminiChatRequest{
		Contents: []dto.GeminiChatContent{
			{Role: "model", Parts: []dto.GeminiPart{{
				FunctionCall: &dto.FunctionCall{FunctionName: "screenshot"},
			}}},
			{Role: "user", Parts: []dto.GeminiPart{{
				FunctionResponse: &dto.GeminiFunctionResponse{
					Name:     "screenshot",
					Response: map[string]any{"status": "ok"},
					Parts:    replay,
				},
			}}},
		},
	}
	assert.Equal(t, 3, RequestImageCount(types.RelayFormatGemini, request),
		"the tool replayed three image parts, one of them a repeat")
}
