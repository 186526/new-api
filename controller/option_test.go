package controller

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/QuantumNous/new-api/setting/websearch_setting"
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

func TestWebSearchGroupPoliciesOptionRejectsInvalidReplacement(t *testing.T) {
	const key = "websearch_setting.group_policies"
	modelManagementDB(t, "sqlite", "")
	previousConfig := config.GlobalConfig.ExportAllConfigs()
	t.Cleanup(func() {
		require.NoError(t, config.GlobalConfig.LoadFromDB(previousConfig))
		require.NoError(t, websearch_setting.UpdateAndSync())
	})

	getOption := func() string {
		var response struct {
			Data []model.Option `json:"data"`
		}
		modelManagementRequest(t, GetOptions, http.MethodGet, "/api/option/", nil, &response)
		for _, option := range response.Data {
			if option.Key == key {
				return option.Value
			}
		}
		return ""
	}
	putOption := func(value any) bool {
		var response struct {
			Success bool `json:"success"`
		}
		modelManagementRequest(t, UpdateOption, http.MethodPut, "/api/option/", OptionUpdateRequest{Key: key, Value: value}, &response)
		return response.Success
	}

	getVersion := func() string {
		var response struct {
			Data model.WebSearchGroupOptions `json:"data"`
		}
		modelManagementRequest(t, GetWebSearchGroupPolicies, http.MethodGet, "/api/tools-api/group-policies", nil, &response)
		return response.Data.Version
	}
	putVersioned := func(value, version string) bool {
		var response struct {
			Success bool `json:"success"`
		}
		modelManagementRequest(t, UpdateWebSearchGroupPolicies, http.MethodPut, "/api/tools-api/group-policies", model.WebSearchGroupOptions{Value: value, Version: version}, &response)
		return response.Success
	}
	const saved = `{"default":{"tools/search":{"mode":"disabled"},"moonshot/fetch":{"mode":"prefer","providers":["exa","local"]}}}`
	originalVersion := getVersion()
	require.False(t, putOption(saved))
	require.True(t, putVersioned(saved, originalVersion))
	assert.Equal(t, saved, getOption())
	assert.False(t, putVersioned(`{}`, originalVersion))
	for _, invalid := range []string{
		``, `null`, `{"":{}}`, `{"auto":{}}`, `{"default":null}`,
		`{"default":{"TOOLS/search":{"mode":"disabled"}}}`,
		`{"default":{"tools/search":{"mode":"allow"}}}`,
		`{"default":{"tools/search":{"Mode":"disabled"}}}`,
		`{"default":{"tools/search":{"mode":"disabled","providers":[]}}}`,
		`{"default":{"tools/search":{"mode":"prefer","providers":null}}}`,
		`{"default":{"tools/search":{"mode":"prefer","providers":["deepseek","deepseek"]}}}`,
		`{"default":{"tools/search":{"mode":"prefer","providers":["invalid"]}}}`,
		`{"default":{"tools/search":{"mode":"disabled","mode":"inherit"}}}`,
		`{"default":{"tools/search":{"mode":"disabled"},"tools/search":{"mode":"inherit"}}}`,
		`{"default":{},"default":{}}`,
	} {
		assert.False(t, putVersioned(invalid, getVersion()), "input: %v", invalid)
		assert.Equal(t, saved, getOption())
	}
	require.True(t, putVersioned(`{}`, getVersion()))
	assert.Equal(t, `{}`, getOption())
}
