package model

import (
	"context"
	"errors"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
)

var ErrCustomToolsChanged = errors.New("custom tool configuration changed")

// UpdateChannelCustomTools compares the exact saved address and configuration used by the caller.
func UpdateChannelCustomTools(ctx context.Context, saved *Channel, configuration string) error {
	query := DB.WithContext(ctx).Model(&Channel{}).Where("id = ? AND type = ?", saved.Id, constant.ChannelTypeAdvancedCustom)
	if saved.BaseURL == nil {
		query = query.Where("base_url IS NULL")
	} else {
		query = query.Where("base_url = ?", *saved.BaseURL)
	}
	if saved.CustomTools == nil {
		query = query.Where("custom_tools IS NULL")
	} else {
		query = query.Where("custom_tools = ?", *saved.CustomTools)
	}
	if saved.CustomTools != nil && *saved.CustomTools == configuration {
		var count int64
		if err := query.Count(&count).Error; err != nil {
			return err
		}
		if count == 1 {
			return nil
		}
		return ErrCustomToolsChanged
	}
	updated := query.Update("custom_tools", configuration)
	if updated.Error != nil {
		return updated.Error
	}
	if updated.RowsAffected != 1 {
		return ErrCustomToolsChanged
	}
	return nil
}

func (channel *Channel) hasIndependentToolRoute(requestPath string) bool {
	if channel.Type != constant.ChannelTypeAdvancedCustom || channel.CustomTools == nil || *channel.CustomTools == "{}" {
		return false
	}
	var operation string
	switch requestPath {
	case "/v1/tools/search", "/v1/alpha/search", "/moonshot.service/v1/search":
		operation = "search"
	case "/v1/tools/fetch", "/moonshot.service/v1/fetch":
		operation = "fetch"
	default:
		return false
	}
	var configured map[string]*struct{}
	return common.UnmarshalJsonStr(*channel.CustomTools, &configured) == nil && configured[operation] != nil
}

func (channel *Channel) ClearCustomTools() {
	channel.CustomTools = common.GetPointer("{}")
	channel.clearCustomTools = true
}

func LoadChannelCustomTools(ctx context.Context, id int) (*Channel, error) {
	var channel Channel
	err := DB.WithContext(ctx).Select("id", "type", "base_url", "custom_tools").Where("id = ? AND type = ?", id, constant.ChannelTypeAdvancedCustom).First(&channel).Error
	return &channel, err
}
