package controller

import (
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestToolContractFilterPreservesLogScope(t *testing.T) {
	for _, contract := range []string{"tools/search", "tools/fetch", "alpha/search", "moonshot/search", "moonshot/fetch"} {
		t.Run(contract, func(t *testing.T) {
			ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
			query := url.Values{"tool_contract": {contract}, "provider": {"exa"}, "username": {"other"}, "channel": {"12"}}
			ctx.Request = httptest.NewRequest("GET", "/api/log/self?"+query.Encode(), nil)
			self := logFilterFromQuery(ctx, false)
			require.Equal(t, contract, self.ToolContract)
			require.Empty(t, self.Username)
			require.Zero(t, self.Channel)
			admin := logFilterFromQuery(ctx, true)
			require.Equal(t, contract, admin.ToolContract)
			require.Equal(t, "other", admin.Username)
			require.Equal(t, 12, admin.Channel)
		})
	}
}
