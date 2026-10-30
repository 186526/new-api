package service

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relay/channel/antigravity/modelname"
)

const antigravityModelsTimeout = 30 * time.Second

// antigravityInternalModels 是 fetchAvailableModels 返回的内部模型，
// 它们不能作为对话模型使用，拉取模型列表时丢弃。
var antigravityInternalModels = map[string]struct{}{
	"chat_20706":                  {},
	"chat_23310":                  {},
	"tab_flash_lite_preview":      {},
	"tab_jump_flash_lite_preview": {},
	"gemini-2.5-flash-thinking":   {},
	"gemini-2.5-pro":              {},
}

// FetchAntigravityChannelModels 调上游 fetchAvailableModels 拿到该账号可用的模型名。
func FetchAntigravityChannelModels(ch *model.Channel) ([]string, error) {
	if ch == nil {
		return nil, fmt.Errorf("channel is nil")
	}
	settings := ch.GetSetting()
	baseURL := strings.TrimSpace(ch.GetBaseURL())
	if baseURL == "" {
		baseURL = constant.GetChannelBaseURL(constant.ChannelTypeAntigravity)
	}

	ctx, cancel := context.WithTimeout(context.Background(), antigravityModelsTimeout)
	defer cancel()

	credential, err := EnsureAntigravityCredential(ctx, ch.Id, ch.Key, baseURL, settings)
	if err != nil {
		return nil, err
	}

	client, err := GetHttpClientWithProxySettings(settings.Proxy, settings)
	if err != nil {
		return nil, err
	}
	body, status, err := antigravityPost(ctx, client, credential.BaseURL, ":fetchAvailableModels", credential.AccessToken,
		map[string]any{"project": credential.ProjectID})
	if err != nil {
		return nil, err
	}
	if status < 200 || status >= 300 {
		return nil, fmt.Errorf("antigravity fetchAvailableModels failed: status=%d body=%s", status, truncateForLog(body))
	}

	var payload struct {
		Models map[string]any `json:"models"`
	}
	if err := common.Unmarshal(body, &payload); err != nil {
		return nil, err
	}
	models := usableAntigravityModels(payload.Models)
	if len(models) == 0 {
		return nil, fmt.Errorf("antigravity fetchAvailableModels returned no usable model")
	}
	return models, nil
}

// usableAntigravityModels 过滤内部模型，并把带档位后缀的模型名折回基础名后去重排序。
func usableAntigravityModels(raw map[string]any) []string {
	seen := make(map[string]struct{}, len(raw))
	models := make([]string, 0, len(raw))
	for name := range raw {
		trimmed := strings.TrimSpace(name)
		if trimmed == "" {
			continue
		}
		if _, internal := antigravityInternalModels[trimmed]; internal {
			continue
		}
		collapsed := modelname.Collapse(trimmed)
		if _, duplicate := seen[collapsed]; duplicate {
			continue
		}
		seen[collapsed] = struct{}{}
		models = append(models, collapsed)
	}
	sort.Strings(models)
	return models
}
