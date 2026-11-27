package codex

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
	"github.com/samber/lo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetRequestURLAlphaSearch(t *testing.T) {
	adaptor := &Adaptor{}
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelType:    constant.ChannelTypeCodex,
			ChannelBaseUrl: "https://chatgpt.com",
		},
		RelayMode: relayconstant.RelayModeAlphaSearch,
	}

	url, err := adaptor.GetRequestURL(info)
	require.NoError(t, err)
	assert.Equal(t, "https://chatgpt.com/backend-api/codex/alpha/search", url)
}

// The Codex backend rejects these fields, so the adaptor clears them rather
// than forwarding what the client sent.
func TestConvertOpenAIResponsesRequestDropsPenalties(t *testing.T) {
	adaptor := &Adaptor{}
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeCodex},
		RelayMode:   relayconstant.RelayModeResponses,
	}

	converted, err := adaptor.ConvertOpenAIResponsesRequest(nil, info, dto.OpenAIResponsesRequest{
		Model:            "gpt-5-codex",
		Input:            json.RawMessage(`"hello"`),
		MaxOutputTokens:  lo.ToPtr(uint(128)),
		Temperature:      lo.ToPtr(1.0),
		TopP:             lo.ToPtr(0.9),
		TopLogProbs:      lo.ToPtr(2),
		FrequencyPenalty: json.RawMessage(`1.5`),
		PresencePenalty:  json.RawMessage(`1.5`),
		Truncation:       json.RawMessage(`"auto"`),
		Metadata:         json.RawMessage(`{"a":"b"}`),
		User:             json.RawMessage(`"abc"`),
	})
	require.NoError(t, err)

	request, ok := converted.(dto.OpenAIResponsesRequest)
	require.True(t, ok)
	// max_output_tokens is accepted, so a caller's cap survives.
	assert.NotNil(t, request.MaxOutputTokens)
	assert.Nil(t, request.Temperature)
	// top_p was forwarded and the backend answered "Unsupported parameter:
	// top_p" to every Playground request.
	assert.Nil(t, request.TopP)
	assert.Nil(t, request.TopLogProbs)
	assert.Nil(t, request.FrequencyPenalty)
	assert.Nil(t, request.PresencePenalty)
	assert.Nil(t, request.Truncation)
	assert.Nil(t, request.Metadata)
	assert.Nil(t, request.User)
}

func TestNonStreamingResponsesReadsCodexEventsWithoutContentType(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var payload dto.OpenAIResponsesRequest
		if json.NewDecoder(request.Body).Decode(&payload) != nil || !lo.FromPtr(payload.Stream) || payload.TopP != nil || request.Header.Get("Accept") != "text/event-stream" {
			http.Error(writer, "invalid upstream request", http.StatusBadRequest)
			return
		}
		writer.Header()["Content-Type"] = nil
		_, _ = writer.Write([]byte("data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_1\",\"status\":\"completed\",\"output\":[{\"type\":\"message\",\"role\":\"assistant\",\"content\":[{\"type\":\"output_text\",\"text\":\"PONG\"}]}],\"usage\":{\"input_tokens\":3,\"output_tokens\":2,\"total_tokens\":5}}}\n\n"))
	}))
	defer server.Close()

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	info := &relaycommon.RelayInfo{
		RelayMode: relayconstant.RelayModeResponses,
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelType: constant.ChannelTypeCodex, ChannelBaseUrl: server.URL,
			ApiKey: `{"access_token":"test-token","account_id":"test-account"}`,
		},
	}
	adaptor := &Adaptor{}
	converted, err := adaptor.ConvertOpenAIResponsesRequest(ctx, info, dto.OpenAIResponsesRequest{
		Input: json.RawMessage(`"hello"`), Stream: lo.ToPtr(false), TopP: lo.ToPtr(0.9),
	})
	require.NoError(t, err)
	body, err := json.Marshal(converted)
	require.NoError(t, err)
	upstream, err := adaptor.DoRequest(ctx, info, strings.NewReader(string(body)))
	require.NoError(t, err)
	response := upstream.(*http.Response)
	defer response.Body.Close()
	require.Equal(t, http.StatusOK, response.StatusCode)
	require.Equal(t, "text/event-stream", response.Header.Get("Content-Type"))
	usage, apiErr := adaptor.DoResponse(ctx, response, info)
	require.Nil(t, apiErr)
	require.Equal(t, 5, usage.(*dto.Usage).TotalTokens)
	require.False(t, info.IsStream)
	require.Equal(t, "application/json", recorder.Header().Get("Content-Type"))
	require.True(t, json.Valid(recorder.Body.Bytes()))
	require.Contains(t, recorder.Body.String(), "PONG")
}

// A protocol this channel does not speak is the caller's mistake, so it answers
// 400 and is not retried. It used to answer 500, which read as a server fault
// and invited a retry that could never succeed.
func TestUnsupportedEndpointsAnswerBadRequest(t *testing.T) {
	adaptor := &Adaptor{}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeCodex}}

	cases := map[string]error{
		"/v1/chat/completions": func() error {
			_, err := adaptor.ConvertOpenAIRequest(nil, info, &dto.GeneralOpenAIRequest{Model: "gpt-6.2-sol"})
			return err
		}(),
		"/v1/messages": func() error {
			_, err := adaptor.ConvertClaudeRequest(nil, info, &dto.ClaudeRequest{Model: "gpt-6.2-sol"})
			return err
		}(),
		"/v1/embeddings": func() error {
			_, err := adaptor.ConvertEmbeddingRequest(nil, info, dto.EmbeddingRequest{Model: "gpt-6.2-sol"})
			return err
		}(),
	}
	for name, err := range cases {
		require.Error(t, err, name)
		apiErr, ok := err.(*types.NewAPIError)
		require.True(t, ok, "%s: error is %T, not *types.NewAPIError", name, err)
		assert.Equal(t, http.StatusBadRequest, apiErr.StatusCode, name)
		assert.Equal(t, types.ErrorCodeConvertRequestFailed, apiErr.GetErrorCode(), name)
	}
}
