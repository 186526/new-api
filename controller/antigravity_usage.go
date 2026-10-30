package controller

import (
	"context"
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

// GetAntigravityChannelUsage 返回账号的配额窗口；数据来自上游的 quota 接口，不落库。
func GetAntigravityChannelUsage(c *gin.Context) {
	channelId, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		common.ApiError(c, fmt.Errorf("invalid channel id: %w", err))
		return
	}
	ch, err := model.GetChannelById(channelId, true)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if ch == nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "channel not found"})
		return
	}
	if ch.Type != constant.ChannelTypeAntigravity {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "channel type is not Antigravity"})
		return
	}
	if ch.ChannelInfo.IsMultiKey {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "multi-key channel is not supported"})
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()

	usage, err := service.FetchAntigravityChannelUsage(ctx, ch)
	if err != nil {
		common.SysError(common.LogText("failed to fetch antigravity usage: %s", err.Error()))
		common.ApiErrorT(c, "Failed to fetch usage. Please try again later")
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": usage})
}
