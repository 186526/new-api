package controller

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The channel test used to marshal every converted request, so an adaptor that
// returned the upstream bytes directly had that JSON encoded a second time as a
// base64 string. Mistral answers such a body with 422 "Input should be a valid
// dictionary or object", which the health check read as a dead channel.
func TestChannelTestRequestBodyKeepsAdaptorBytes(t *testing.T) {
	raw := []byte(`{"model":"deepseek-v4-1-flash-1017","messages":[{"role":"user","content":"hi"}],"stream":false}`)

	body, err := channelTestRequestBody(raw)
	require.NoError(t, err)
	assert.Equal(t, raw, body, "an adaptor's own bytes must reach the upstream unchanged, not as base64 of them")
}

func TestChannelTestRequestBodyMarshalsAConvertedObject(t *testing.T) {
	body, err := channelTestRequestBody(map[string]any{"model": "gpt-6-luna"})
	require.NoError(t, err)

	var decoded map[string]any
	require.NoError(t, common.Unmarshal(body, &decoded))
	assert.Equal(t, "gpt-6-luna", decoded["model"])
}
