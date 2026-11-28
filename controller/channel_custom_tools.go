package controller

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service/websearch/abstract"
	"github.com/QuantumNous/new-api/service/websearch/request"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type channelCustomToolStatus struct {
	Protocol   string `json:"protocol,omitempty"`
	Path       string `json:"path,omitempty"`
	Configured bool   `json:"configured"`
	Verified   bool   `json:"verified"`
}

func customToolStatus(channel *model.Channel, contract abstract.Contract, operation *abstract.CustomToolOperation) channelCustomToolStatus {
	if operation == nil {
		return channelCustomToolStatus{}
	}
	return channelCustomToolStatus{
		Protocol: operation.Protocol, Path: operation.Path,
		Configured: operation.Credential != "", Verified: operation.Verified(channel.Id, contract, channel.GetBaseURL()),
	}
}

func readChannelCustomTools(ctx *gin.Context) (*model.Channel, abstract.CustomTools, bool) {
	id, err := strconv.Atoi(ctx.Param("id"))
	if err != nil || id <= 0 {
		customToolError(ctx, http.StatusBadRequest, "invalid_channel_id")
		return nil, abstract.CustomTools{}, false
	}
	channel, err := model.LoadChannelCustomTools(ctx.Request.Context(), id)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		customToolError(ctx, http.StatusNotFound, "custom_tool_channel_not_found")
		return nil, abstract.CustomTools{}, false
	}
	if err != nil {
		customToolError(ctx, http.StatusInternalServerError, "custom_tool_read_failed")
		return nil, abstract.CustomTools{}, false
	}
	tools, err := abstract.DecodeCustomTools(channel.CustomTools)
	if err != nil {
		customToolError(ctx, http.StatusInternalServerError, "custom_tool_read_failed")
		return nil, abstract.CustomTools{}, false
	}
	return channel, tools, true
}

func GetChannelCustomTools(ctx *gin.Context) {
	channel, tools, ok := readChannelCustomTools(ctx)
	if !ok {
		return
	}
	common.ApiSuccess(ctx, gin.H{
		"search": customToolStatus(channel, abstract.ContractToolsSearch, tools.Search),
		"fetch":  customToolStatus(channel, abstract.ContractToolsFetch, tools.Fetch),
	})
}

func customToolContract(ctx *gin.Context) (abstract.Contract, bool) {
	switch ctx.Param("operation") {
	case "search":
		return abstract.ContractToolsSearch, true
	case "fetch":
		return abstract.ContractToolsFetch, true
	default:
		customToolError(ctx, http.StatusBadRequest, "invalid_custom_tool_operation")
		return "", false
	}
}

func SaveChannelCustomTool(ctx *gin.Context) {
	contract, ok := customToolContract(ctx)
	if !ok {
		return
	}
	ctx.Request.Body = http.MaxBytesReader(ctx.Writer, ctx.Request.Body, 8192)
	var input struct {
		Enabled *bool  `json:"enabled"`
		Path    string `json:"path"`
		APIKey  string `json:"api_key"`
	}
	if err := ctx.ShouldBindJSON(&input); err != nil || input.Enabled == nil {
		customToolError(ctx, http.StatusBadRequest, "invalid_custom_tool_configuration")
		return
	}
	channel, tools, ok := readChannelCustomTools(ctx)
	if !ok {
		return
	}
	operation := tools.Operation(contract)
	var next *abstract.CustomToolOperation
	if *input.Enabled {
		next = &abstract.CustomToolOperation{Protocol: abstract.CustomToolsProtocol, Path: input.Path}
		if _, err := next.Endpoint(channel.GetBaseURL()); err != nil {
			customToolError(ctx, http.StatusBadRequest, "invalid_custom_tool_endpoint")
			return
		}
		if input.APIKey != "" {
			sealed, err := abstract.SealCustomToolCredential(channel.Id, contract, input.APIKey)
			if err != nil {
				customToolError(ctx, http.StatusBadRequest, "invalid_custom_tool_credential")
				return
			}
			next.Credential = sealed
		} else if saved := *operation; saved != nil {
			next.Credential = saved.Credential
			if saved.Path == next.Path && saved.Protocol == next.Protocol {
				next.Proof = saved.Proof
			}
		} else {
			customToolError(ctx, http.StatusBadRequest, "missing_custom_tool_credential")
			return
		}
	}
	*operation = next
	if storeChannelCustomTools(ctx, channel, tools) {
		recordManageAudit(ctx, "channel.custom_tool.save", gin.H{"channel_id": channel.Id, "operation": ctx.Param("operation"), "configured": next != nil})
		common.ApiSuccess(ctx, customToolStatus(channel, contract, next))
	}
}

var customToolVerificationSlots = make(chan struct{}, 8)

func VerifyChannelCustomTool(ctx *gin.Context) {
	contract, ok := customToolContract(ctx)
	if !ok {
		return
	}
	channel, tools, ok := readChannelCustomTools(ctx)
	if !ok {
		return
	}
	operation := *tools.Operation(contract)
	if operation == nil {
		customToolError(ctx, http.StatusBadRequest, "custom_tool_not_configured")
		return
	}
	select {
	case customToolVerificationSlots <- struct{}{}:
		defer func() { <-customToolVerificationSlots }()
	default:
		customToolError(ctx, http.StatusTooManyRequests, "custom_tool_verification_busy")
		return
	}
	channelContext := abstract.ChannelContext{ChannelID: channel.Id, BaseURL: channel.GetBaseURL()}
	if err := request.VerifyCustomTool(ctx.Request.Context(), channelContext, contract, operation); err != nil {
		if operation.Proof != "" && ctx.Request.Context().Err() == nil {
			operation.Proof = ""
			if !storeChannelCustomTools(ctx, channel, tools) {
				return
			}
		}
		code := "custom_tool_request_failed"
		if errors.Is(err, request.ErrCustomToolResponse) {
			code = "custom_tool_invalid_response"
		}
		recordManageAudit(ctx, "channel.custom_tool.verify", gin.H{"channel_id": channel.Id, "operation": ctx.Param("operation"), "verified": false})
		customToolError(ctx, http.StatusBadGateway, code)
		return
	}
	operation.Proof = operation.ProofFor(channel.Id, contract, channel.GetBaseURL())
	if storeChannelCustomTools(ctx, channel, tools) {
		recordManageAudit(ctx, "channel.custom_tool.verify", gin.H{"channel_id": channel.Id, "operation": ctx.Param("operation"), "verified": true})
		common.ApiSuccess(ctx, customToolStatus(channel, contract, operation))
	}
}

func storeChannelCustomTools(ctx *gin.Context, channel *model.Channel, tools abstract.CustomTools) bool {
	encoded, err := json.Marshal(tools)
	if err == nil {
		err = model.UpdateChannelCustomTools(ctx.Request.Context(), channel, string(encoded))
	}
	if errors.Is(err, model.ErrCustomToolsChanged) {
		customToolError(ctx, http.StatusConflict, "custom_tool_configuration_changed")
		return false
	}
	if err != nil {
		customToolError(ctx, http.StatusInternalServerError, "custom_tool_write_failed")
		return false
	}
	model.InitChannelCache()
	return true
}

func customToolError(ctx *gin.Context, status int, code string) {
	ctx.JSON(status, gin.H{"success": false, "message": code, "message_key": code})
}
