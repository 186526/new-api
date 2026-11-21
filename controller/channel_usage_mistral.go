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

// mistralUsageBudget is the merged monthly allowance of the usage dialog.
// Mistral bills Vibe and Studio from one "Included Vibe & Studio usage" figure,
// so the account reports one row.
type mistralUsageBudget struct {
	Scope           string  `json:"scope"`
	UsagePercentage float64 `json:"usage_percentage"`
	RemainingRatio  float64 `json:"remaining_ratio"`
	LimitUSD        float64 `json:"limit_usd"`
	RemainingUSD    float64 `json:"remaining_usd"`
	ResetAt         int64   `json:"reset_at"`
}

// mistralUsage is the usage dialog payload. Amounts are derived from the plan
// amount the allowance source reports, and stay zero when it omitted one.
type mistralUsage struct {
	ChannelName string               `json:"channel_name"`
	Budgets     []mistralUsageBudget `json:"budgets"`
	PaygEnabled bool                 `json:"payg_enabled"`
	FetchedAt   int64                `json:"fetched_at"`
}

// newMistralUsage turns the merged monthly allowance into the usage dialog
// payload.
func newMistralUsage(account service.MistralQuotaAccount, quota *service.MistralQuota) mistralUsage {
	budgets := make([]mistralUsageBudget, 0, 1)
	if budget := quota.Allowance; budget != nil {
		remainingRatio := clampRatio(1 - budget.UsagePercentage/100)
		budgets = append(budgets, mistralUsageBudget{
			Scope:           service.MistralBudgetScopeVibe,
			UsagePercentage: budget.UsagePercentage,
			RemainingRatio:  remainingRatio,
			LimitUSD:        budget.LimitUSD,
			RemainingUSD:    budget.LimitUSD * remainingRatio,
			ResetAt:         unixSeconds(budget.ResetAt),
		})
	}
	return mistralUsage{
		ChannelName: account.Channel,
		Budgets:     budgets,
		PaygEnabled: quota.PaygEnabled,
		FetchedAt:   quota.FetchedAt.Unix(),
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

// GetMistralChannelUsage 返回账号的月度额度；数据来自按渠道 key 读取的 billing 端点，不落库。
func GetMistralChannelUsage(c *gin.Context) {
	channelId, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		common.ApiError(c, fmt.Errorf("invalid channel id: %w", err))
		return
	}
	// The channel key decides which plan the allowance is read for, so the row is
	// loaded with its key instead of without one.
	ch, err := model.GetChannelById(channelId, true)
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

	account, err := findMistralQuotaAccount(ch.Name)
	if err != nil {
		common.SysError(common.LogText("failed to find mistral quota account: channel_id=%d, error=%s", ch.Id, err.Error()))
		c.JSON(http.StatusOK, gin.H{"success": false, "message": fmt.Sprintf("no Mistral quota account is configured for channel %q", ch.Name)})
		return
	}
	account.ChannelKey = mistralChannelKey(ch)

	ctx, cancel := context.WithTimeout(c.Request.Context(), channelUsageFetchTimeout)
	defer cancel()

	quota, err := service.FetchMistralQuota(ctx, account)
	if err != nil {
		common.SysError(common.LogText("failed to fetch mistral usage: channel_id=%d, error=%s", ch.Id, err.Error()))
		var authErr *service.MistralQuotaAuthError
		if errors.As(err, &authErr) {
			c.JSON(http.StatusOK, gin.H{"success": false, "message": "Mistral rejected the credential of this channel; update the key or the cookie in the quota account configuration"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "Failed to fetch usage. Please try again later"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": newMistralUsage(account, quota)})
}
