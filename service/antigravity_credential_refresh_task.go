package service

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"

	"github.com/bytedance/gopkg/util/gopool"
)

const (
	antigravityCredentialRefreshTickInterval = 10 * time.Minute
	antigravityCredentialRefreshThreshold    = 2 * time.Hour
	antigravityCredentialRefreshBatchSize    = 200
)

var (
	antigravityCredentialRefreshOnce    sync.Once
	antigravityCredentialRefreshRunning atomic.Bool
)

// StartAntigravityCredentialAutoRefreshTask 定期刷新即将过期的 Antigravity 凭据，
// 让渠道不会因为 refresh_token 长时间闲置或 access_token 过期而失败。
func StartAntigravityCredentialAutoRefreshTask() {
	antigravityCredentialRefreshOnce.Do(func() {
		if !common.IsMasterNode {
			return
		}

		gopool.Go(func() {
			logger.LogInfo(context.Background(), common.LogText(
				"antigravity credential auto-refresh task started: tick=%s threshold=%s",
				antigravityCredentialRefreshTickInterval, antigravityCredentialRefreshThreshold))

			ticker := time.NewTicker(antigravityCredentialRefreshTickInterval)
			defer ticker.Stop()

			runAntigravityCredentialAutoRefreshOnce()
			for range ticker.C {
				runAntigravityCredentialAutoRefreshOnce()
			}
		})
	})
}

func runAntigravityCredentialAutoRefreshOnce() {
	if !antigravityCredentialRefreshRunning.CompareAndSwap(false, true) {
		return
	}
	defer antigravityCredentialRefreshRunning.Store(false)

	ctx := context.Background()

	offset := 0
	for {
		var channels []*model.Channel
		err := model.DB.
			Select("id", "name", "key", "status", "channel_info", "setting").
			Where("type = ? AND (status = ? OR status = ?)",
				constant.ChannelTypeAntigravity,
				common.ChannelStatusEnabled,
				common.ChannelStatusAutoDisabled,
			).
			Order("id asc").
			Limit(antigravityCredentialRefreshBatchSize).
			Offset(offset).
			Find(&channels).Error
		if err != nil {
			logger.LogError(ctx, common.LogText("antigravity credential auto-refresh: query channels failed: %v", err))
			return
		}
		if len(channels) == 0 {
			return
		}
		offset += antigravityCredentialRefreshBatchSize

		for _, ch := range channels {
			if ch == nil || ch.ChannelInfo.IsMultiKey {
				continue
			}
			key, parseErr := ParseAntigravityOAuthKey(strings.TrimSpace(ch.Key))
			if parseErr != nil {
				continue
			}
			if time.Until(key.accessTokenExpiry()) > antigravityCredentialRefreshThreshold && key.ProjectID != "" {
				continue
			}
			if _, _, refreshErr := RefreshAntigravityChannelCredential(ctx, ch.Id); refreshErr != nil {
				logger.LogError(ctx, common.LogText(
					"antigravity credential auto-refresh: channel %d failed: %v", ch.Id, refreshErr))
			}
		}
	}
}
