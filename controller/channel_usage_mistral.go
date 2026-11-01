package controller

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
)

const channelUsageFetchTimeout = 30 * time.Second

// mistralVibeUsage is the usage dialog payload. Amounts are derived from the
// plan amount, which is zero when the account entry omits monthlyLimitUsd.
type mistralVibeUsage struct {
	ChannelName     string  `json:"channel_name"`
	UsagePercentage float64 `json:"usage_percentage"`
	RemainingRatio  float64 `json:"remaining_ratio"`
	MonthlyLimitUSD float64 `json:"monthly_limit_usd"`
	RemainingUSD    float64 `json:"remaining_usd"`
	ResetAt         int64   `json:"reset_at"`
	PaygEnabled     bool    `json:"payg_enabled"`
	FetchedAt       int64   `json:"fetched_at"`
}

// newMistralVibeUsage turns the Vibe allowance into the usage dialog payload.
func newMistralVibeUsage(account service.MistralVibeAccount, quota *service.MistralVibeQuota) mistralVibeUsage {
	remainingRatio := clampRatio(1 - quota.UsagePercentage/100)
	remainingUSD := 0.0
	if account.MonthlyLimitUSD > 0 {
		remainingUSD = account.MonthlyLimitUSD * remainingRatio
	}
	return mistralVibeUsage{
		ChannelName:     account.Channel,
		UsagePercentage: quota.UsagePercentage,
		RemainingRatio:  remainingRatio,
		MonthlyLimitUSD: account.MonthlyLimitUSD,
		RemainingUSD:    remainingUSD,
		ResetAt:         unixSeconds(quota.ResetAt),
		PaygEnabled:     quota.PaygEnabled,
		FetchedAt:       quota.FetchedAt.Unix(),
	}
}

// clampRatio keeps a ratio inside [0, 1] and turns NaN into 0, so the value
// survives JSON encoding.
func clampRatio(ratio float64) float64 {
	if ratio != ratio || ratio < 0 {
		return 0
	}
	if ratio > 1 {
		return 1
	}
	return ratio
}

// unixSeconds reports a timestamp in Unix seconds, or 0 when it is unset.
func unixSeconds(at time.Time) int64 {
	if at.IsZero() {
		return 0
	}
	return at.Unix()
}

// GetMistralChannelUsage 返回 Vibe 账号的月度额度；数据来自 Vibe 网页接口，不落库。
func GetMistralChannelUsage(c *gin.Context) {
	channelId, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		common.ApiError(c, fmt.Errorf("invalid channel id: %w", err))
		return
	}
	ch, err := model.GetChannelById(channelId, false)
	if err != nil {
		common.SysError(common.LogText("failed to load channel %d for mistral usage: %s", channelId, err.Error()))
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "channel not found"})
		return
	}
	if ch == nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "channel not found"})
		return
	}
	if ch.Type != constant.ChannelTypeMistral {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "channel type is not Mistral"})
		return
	}

	account, err := findMistralVibeAccount(ch.Name)
	if err != nil {
		common.SysError(common.LogText("failed to find mistral vibe account: channel_id=%d, error=%s", ch.Id, err.Error()))
		c.JSON(http.StatusOK, gin.H{"success": false, "message": fmt.Sprintf("no Mistral Vibe account is configured for channel %q", ch.Name)})
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), channelUsageFetchTimeout)
	defer cancel()

	quota, err := service.FetchMistralVibeQuota(ctx, account)
	if err != nil {
		common.SysError(common.LogText("failed to fetch mistral vibe usage: channel_id=%d, error=%s", ch.Id, err.Error()))
		var authErr *service.MistralVibeAuthError
		if errors.As(err, &authErr) {
			c.JSON(http.StatusOK, gin.H{"success": false, "message": "Mistral Vibe session was rejected; update the cookie in the quota account configuration"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "Failed to fetch usage. Please try again later"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": newMistralVibeUsage(account, quota)})
}
