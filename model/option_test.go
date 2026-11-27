package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/QuantumNous/new-api/setting/websearch_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestUpdateOptionMapRepublishesWebSearchCandidateOrder checks that the layered
// config branch of updateOptionMap reaches websearch_setting.UpdateAndSync, so
// the new order is readable without a restart.
func TestUpdateOptionMapRepublishesWebSearchCandidateOrder(t *testing.T) {
	originalMap := common.OptionMap
	savedConfigs := config.GlobalConfig.ExportAllConfigs()
	common.OptionMap = map[string]string{}
	t.Cleanup(func() {
		common.OptionMap = originalMap
		require.NoError(t, config.GlobalConfig.LoadFromDB(savedConfigs))
		// LoadFromDB writes the struct but does not publish it.
		require.NoError(t, websearch_setting.UpdateAndSync())
	})

	require.NoError(t, updateOptionMap("websearch_setting.candidate_order", "exa,local"))

	assert.Equal(t, "exa,local", common.OptionMap["websearch_setting.candidate_order"])
	assert.Equal(t, []string{"exa", "local"}, websearch_setting.ProviderOrder())
}
