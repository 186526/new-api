package dto

import (
	"testing"

	kitutil "github.com/QuantumNous/new-api/relaykit/relayconvert/kitutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGeminiChatRequestPreservesArgonFields(t *testing.T) {
	raw := []byte(`{
		"contents":[{"role":"user","parts":[{"text":"hello","media_processing":"low"}]}],
		"continuation_token":{"session":"s1"},
		"generationConfig":{"media_processing":{"upscale":true}}
	}`)

	var req GeminiChatRequest
	require.NoError(t, kitutil.Unmarshal(raw, &req))
	require.Len(t, req.Contents, 1)

	assert.JSONEq(t, `{"session":"s1"}`, string(req.ContinuationToken))
	assert.JSONEq(t, `"low"`, string(req.Contents[0].Parts[0].MediaProcessing))
	require.NotNil(t, req.GenerationConfig)
	assert.JSONEq(t, `{"upscale":true}`, string(req.GenerationConfig.MediaProcessing))

	encoded, err := kitutil.Marshal(req)
	require.NoError(t, err)

	var out map[string]any
	require.NoError(t, kitutil.Unmarshal(encoded, &out))
	assert.Contains(t, out, "continuationToken")
	assert.Contains(t, out, "generationConfig")
	generationConfig, ok := out["generationConfig"].(map[string]any)
	require.True(t, ok)
	assert.Contains(t, generationConfig, "mediaProcessing")
}

func TestGeminiChatRequestPreservesCamelCaseArgonFields(t *testing.T) {
	raw := []byte(`{
		"contents":[{"role":"user","parts":[{"text":"hello","mediaProcessing":"high"}]}],
		"continuationToken":{"session":"s2"}
	}`)

	var req GeminiChatRequest
	require.NoError(t, kitutil.Unmarshal(raw, &req))
	require.Len(t, req.Contents, 1)

	assert.JSONEq(t, `{"session":"s2"}`, string(req.ContinuationToken))
	assert.JSONEq(t, `"high"`, string(req.Contents[0].Parts[0].MediaProcessing))
}

func TestGeminiChatCandidateUnmarshalsContinuationToken(t *testing.T) {
	raw := []byte(`{"content":{"role":"model","parts":[{"text":"hi"}]},"continuationToken":{"session":"s3"}}`)

	var candidate GeminiChatCandidate
	require.NoError(t, kitutil.Unmarshal(raw, &candidate))

	assert.JSONEq(t, `{"session":"s3"}`, string(candidate.ContinuationToken))
}
