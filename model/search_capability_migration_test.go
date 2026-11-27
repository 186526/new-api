package model

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRenameCapabilityModelsMapsEveryLegacyName(t *testing.T) {
	for legacy, current := range legacyCapabilityModelNames {
		renamed, changed := renameCapabilityModels([]string{"gpt-5.5", legacy})
		assert.True(t, changed, legacy)
		assert.Equal(t, []string{"gpt-5.5", current}, renamed, legacy)
	}
}

func TestRenameCapabilityModelsDropsTheDuplicateAReplacementCreates(t *testing.T) {
	renamed, changed := renameCapabilityModels([]string{"tools-search", "deepseek-search", "moonshot-search"})
	assert.True(t, changed)
	assert.Equal(t, []string{"tools-search"}, renamed)
}

func TestRenameCapabilityModelsLeavesCurrentNamesAlone(t *testing.T) {
	names := []string{"gpt-5.5", "tools-search", "tools-fetch"}
	renamed, changed := renameCapabilityModels(names)
	assert.False(t, changed)
	assert.Equal(t, names, renamed)
}
