package controller

import (
	"context"
	"fmt"
	"net/http"
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/dto"

	"github.com/gin-gonic/gin"
)

// commandCodeUsageAccount is one channel key of the usage dialog. The key
// itself is never part of the payload.
type commandCodeUsageAccount struct {
	RemainingUSD     float64                  `json:"remaining_usd"`
	MonthlyCredits   float64                  `json:"monthly_credits"`
	PurchasedCredits float64                  `json:"purchased_credits"`
	FreeCredits      float64                  `json:"free_credits"`
	Windows          []commandCodeUsageWindow `json:"windows"`
}

// commandCodeUsageWindow is one rolling window in the usage dialog. ResetAt is
// Unix seconds, or 0 when the upstream reports no reset.
type commandCodeUsageWindow struct {
	Name    string  `json:"name"`
	UsedUSD float64 `json:"used_usd"`
	CapUSD  float64 `json:"cap_usd"`
	ResetAt int64   `json:"reset_at"`
}

// newCommandCodeUsageAccount maps one key's credits to the usage dialog shape.
func newCommandCodeUsageAccount(credits commandCodeCredits) commandCodeUsageAccount {
	return commandCodeUsageAccount{
		RemainingUSD:     credits.remainingUSD(),
		MonthlyCredits:   credits.Credits.MonthlyCredits,
		PurchasedCredits: credits.Credits.PurchasedCredits,
		FreeCredits:      credits.Credits.FreeCredits,
		Windows: []commandCodeUsageWindow{
			newCommandCodeUsageWindow(commandCodeWindowFiveHour, credits.WindowLimits.FiveHour),
			newCommandCodeUsageWindow(commandCodeWindowWeekly, credits.WindowLimits.Weekly),
		},
	}
}

func newCommandCodeUsageWindow(name string, window commandCodeCreditWindow) commandCodeUsageWindow {
	resetAt := int64(0)
	if window.ResetAt > 0 {
		resetAt = window.ResetAt / 1000
	}
	return commandCodeUsageWindow{
		Name:    name,
		UsedUSD: window.Used,
		CapUSD:  window.Cap,
		ResetAt: resetAt,
	}
}

// isCommandCodeUsageChannel reports whether the usage dialog can query this
// channel. The channel may be disabled, because the dialog also opens for a
// channel that was switched off.
func isCommandCodeUsageChannel(channel *model.Channel) bool {
	return channel.Type == constant.ChannelTypeOpenAI &&
		channel.GetSetting().BalanceSource == dto.BalanceSourceCommandCode
}

// GetCommandCodeChannelUsage returns the credits behind every key of a Command
// Code channel. A key that fails is left out, so the other keys still show.
func GetCommandCodeChannelUsage(c *gin.Context) {
	channelId, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		common.ApiError(c, fmt.Errorf("invalid channel id: %w", err))
		return
	}
	ch, err := model.GetChannelById(channelId, true)
	if err != nil {
		common.SysError(common.LogText("failed to load channel %d for command code usage: %s", channelId, err.Error()))
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "channel not found"})
		return
	}
	if ch == nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "channel not found"})
		return
	}
	if !isCommandCodeUsageChannel(ch) {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "channel balance source is not Command Code"})
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), channelUsageFetchTimeout)
	defer cancel()

	accounts, err := fetchCommandCodeBalances(ctx, ch)
	if err != nil {
		common.SysError(common.LogText("failed to fetch command code usage: channel_id=%d, error=%s", ch.Id, err.Error()))
		c.JSON(http.StatusOK, gin.H{"success": false, "message": fmt.Sprintf("Failed to fetch usage: %s", err.Error())})
		return
	}
	usage := make([]commandCodeUsageAccount, 0, len(accounts))
	for _, credits := range accounts {
		usage = append(usage, newCommandCodeUsageAccount(credits))
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"accounts": usage}})
}
