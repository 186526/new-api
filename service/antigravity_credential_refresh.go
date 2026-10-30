package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
)

const antigravityRefreshSingleTimeout = 30 * time.Second

// RefreshAntigravityChannelCredential 刷新一个 Antigravity 渠道的 access token，
// 缺少 project id 时一并补齐，然后写回渠道 key 并清掉进程内缓存。
func RefreshAntigravityChannelCredential(ctx context.Context, channelID int) (*AntigravityOAuthKey, *model.Channel, error) {
	ch, err := model.GetChannelById(channelID, true)
	if err != nil {
		return nil, nil, err
	}
	if ch == nil {
		return nil, nil, fmt.Errorf("channel not found")
	}
	if ch.Type != constant.ChannelTypeAntigravity {
		return nil, nil, fmt.Errorf("channel type is not Antigravity")
	}

	key, err := ParseAntigravityOAuthKey(strings.TrimSpace(ch.Key))
	if err != nil {
		return nil, nil, err
	}

	settings := ch.GetSetting()
	baseURL := strings.TrimSpace(ch.GetBaseURL())
	if baseURL == "" {
		baseURL = constant.GetChannelBaseURL(constant.ChannelTypeAntigravity)
	}

	client, err := GetHttpClientWithProxySettings(settings.Proxy, settings)
	if err != nil {
		return nil, nil, err
	}
	refreshCtx, cancel := context.WithTimeout(ctx, antigravityRefreshSingleTimeout)
	defer cancel()

	result, err := refreshAntigravityAccessToken(refreshCtx, client, key.RefreshToken)
	if err != nil {
		return nil, nil, err
	}
	key.AccessToken = result.AccessToken
	if result.RefreshToken != "" {
		key.RefreshToken = result.RefreshToken
	}
	key.Expired = result.ExpiresAt.Format(time.RFC3339)
	key.LastRefresh = time.Now().Format(time.RFC3339)
	key.Type = "antigravity"

	if key.ProjectID == "" {
		projectID, projectErr := loadAntigravityProject(refreshCtx, client, baseURL, key.AccessToken)
		if projectErr != nil {
			return nil, nil, projectErr
		}
		key.ProjectID = projectID
	}

	encoded, err := common.Marshal(key)
	if err != nil {
		return nil, nil, err
	}
	if err := model.DB.Model(&model.Channel{}).Where("id = ?", ch.Id).Update("key", string(encoded)).Error; err != nil {
		return nil, nil, err
	}
	ResetAntigravityCredentialCache(ch.Id)
	model.InitChannelCache()

	return key, ch, nil
}
