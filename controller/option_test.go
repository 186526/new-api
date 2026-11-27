package controller

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGetOptionsReportsExaKeyPresenceWithoutItsValue requires the option list to
// carry a boolean for the Exa key instead of the key itself, because the
// sensitive-key filter drops the stored value.
func TestGetOptionsReportsExaKeyPresenceWithoutItsValue(t *testing.T) {
	originalMap := common.OptionMap
	common.OptionMap = map[string]string{
		"ResendApiKey":                  "re_123",
		"websearch_setting.exa_api_key": "sk-exa",
	}
	t.Cleanup(func() { common.OptionMap = originalMap })

	getOptions := func() map[string]string {
		recorder := httptest.NewRecorder()
		context, _ := gin.CreateTestContext(recorder)
		context.Request = httptest.NewRequest(http.MethodGet, "/api/option/", nil)

		GetOptions(context)

		require.Equal(t, http.StatusOK, recorder.Code)
		var response struct {
			Success bool `json:"success"`
			Data    []struct {
				Key   string `json:"key"`
				Value string `json:"value"`
			} `json:"data"`
		}
		require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
		require.True(t, response.Success)

		values := make(map[string]string, len(response.Data))
		for _, option := range response.Data {
			values[option.Key] = option.Value
		}
		return values
	}

	values := getOptions()
	assert.NotContains(t, values, "websearch_setting.exa_api_key")
	assert.NotContains(t, values, "ResendApiKey")
	assert.Equal(t, "true", values["ExaKeyConfigured"])
	assert.Equal(t, "true", values["ResendKeyConfigured"])

	common.OptionMap["websearch_setting.exa_api_key"] = "   "
	values = getOptions()
	assert.Equal(t, "false", values["ExaKeyConfigured"])
}
