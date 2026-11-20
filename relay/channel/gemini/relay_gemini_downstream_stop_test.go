package gemini

import (
	"context"
	"net/http/httptest"
	"testing"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newGeminiStopCase(t *testing.T) (*gin.Context, *relaycommon.RelayInfo) {
	t.Helper()
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
	return c, &relaycommon.RelayInfo{StreamStatus: relaycommon.NewStreamStatus()}
}

func TestIsGeminiDownstreamStop(t *testing.T) {
	t.Parallel()

	t.Run("the request context answers for a client that left", func(t *testing.T) {
		t.Parallel()
		c, info := newGeminiStopCase(t)
		ctx, cancel := context.WithCancel(c.Request.Context())
		c.Request = c.Request.WithContext(ctx)
		cancel()

		assert.True(t, isGeminiDownstreamStop(c, info))
	})

	t.Run("a recorded client end counts when the context is still live", func(t *testing.T) {
		t.Parallel()
		c, info := newGeminiStopCase(t)
		info.StreamStatus.SetEndReason(relaycommon.StreamEndReasonClientGone, context.Canceled)

		assert.True(t, isGeminiDownstreamStop(c, info))
	})

	t.Run("a stream the callback stopped is not a downstream stop", func(t *testing.T) {
		t.Parallel()
		c, info := newGeminiStopCase(t)
		info.StreamStatus.SetEndReason(relaycommon.StreamEndReasonHandlerStop, nil)

		assert.False(t, isGeminiDownstreamStop(c, info))
	})

	t.Run("a stream still running is not a downstream stop", func(t *testing.T) {
		t.Parallel()
		c, info := newGeminiStopCase(t)

		require.Equal(t, relaycommon.StreamEndReasonNone, info.StreamStatus.EndReason())
		assert.False(t, isGeminiDownstreamStop(c, info))
	})

	t.Run("a missing status or info is not a downstream stop", func(t *testing.T) {
		t.Parallel()
		c, _ := newGeminiStopCase(t)

		assert.False(t, isGeminiDownstreamStop(c, nil))
		assert.False(t, isGeminiDownstreamStop(c, &relaycommon.RelayInfo{}))
	})
}
