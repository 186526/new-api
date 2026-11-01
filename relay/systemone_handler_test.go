package relay

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildSystemOneRequestBodyPreservesUnknownFields(t *testing.T) {
	raw := []byte(`{
		"model":"jev-1.13",
		"state":{"ticket":"Checkout is broken."},
		"questions":{"is_bug":{"type":"noul","instructions":"Is this a defect?"}},
		"future_field":{"nested":true}
	}`)

	out, err := buildSystemOneRequestBody(raw, "jev-1.13", "oc/jev-1.13-free")
	require.NoError(t, err)

	var body map[string]any
	require.NoError(t, common.Unmarshal(out, &body))
	assert.Equal(t, "oc/jev-1.13-free", body["model"])
	require.Contains(t, body, "state")
	require.Contains(t, body, "questions")
	require.Contains(t, body, "future_field")

	state, ok := body["state"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "Checkout is broken.", state["ticket"])
}

func TestBuildSystemOneRequestBodyNoMappingKeepsRawBytes(t *testing.T) {
	raw := []byte(`{"model":"jev-1.13","state":"x","questions":{"q":{"type":"noul"}}}`)
	out, err := buildSystemOneRequestBody(raw, "jev-1.13", "jev-1.13")
	require.NoError(t, err)
	assert.Equal(t, raw, out)
}

func TestBuildSystemOneRequestBodyRejectsEmptyBody(t *testing.T) {
	_, err := buildSystemOneRequestBody(nil, "jev-1.13", "jev-1.13")
	require.Error(t, err)
}

func TestSystemOneUsageReadsUpstreamCounts(t *testing.T) {
	body := []byte(`{"model":"jev-1.13-free","answers":{"q":{"type":"noul","noul":0.89}},` +
		`"usage":{"input_tokens":274,"output_tokens":20,"cost":0}}`)

	usage := systemOneUsage(body)
	require.NotNil(t, usage)
	assert.Equal(t, 274, usage.PromptTokens)
	assert.Equal(t, 20, usage.CompletionTokens)
	assert.Equal(t, 294, usage.TotalTokens)
}

func TestSystemOneUsageWithoutCountsReturnsNil(t *testing.T) {
	assert.Nil(t, systemOneUsage([]byte(`{"answers":{}}`)))
	assert.Nil(t, systemOneUsage([]byte(`{"usage":{"input_tokens":0,"output_tokens":0}}`)))
	assert.Nil(t, systemOneUsage([]byte(`not json`)))
}

func TestRewriteSystemOneResponseUsesRequestedModelAndDropsAccountBlock(t *testing.T) {
	body := []byte(`{"model":"jev-1.13-free","answers":{"affordable":{"type":"noul","noul":0.99}},` +
		`"usage":{"input_tokens":397,"output_tokens":22,"cost":0,` +
		`"opencode":{"billing_source":"free","list_cost":0}}}`)

	rewritten := rewriteSystemOneResponse(body, "jev-1.13")

	var response map[string]any
	require.NoError(t, common.Unmarshal(rewritten, &response))
	assert.Equal(t, "jev-1.13", response["model"])

	usage, ok := response["usage"].(map[string]any)
	require.True(t, ok)
	require.NotContains(t, usage, "opencode")
	assert.EqualValues(t, 397, usage["input_tokens"])
	assert.EqualValues(t, 22, usage["output_tokens"])

	answers, ok := response["answers"].(map[string]any)
	require.True(t, ok)
	require.Contains(t, answers, "affordable")
}

func TestRewriteSystemOneResponseKeepsNonJSONBody(t *testing.T) {
	body := []byte(`not json`)
	assert.Equal(t, body, rewriteSystemOneResponse(body, "jev-1.13"))
}

func TestRewriteSystemOneResponseKeepsModelWhenRequestedNameMissing(t *testing.T) {
	rewritten := rewriteSystemOneResponse([]byte(`{"model":"jev-1.13-free"}`), "")

	var response map[string]any
	require.NoError(t, common.Unmarshal(rewritten, &response))
	assert.Equal(t, "jev-1.13-free", response["model"])
}
