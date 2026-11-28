package controller

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/service/websearch/abstract"
	"github.com/gin-gonic/gin"
)

func GetChannelToolCapabilities(ctx *gin.Context) {
	common.ApiSuccess(ctx, abstract.ChannelToolCapabilities())
}

func PreflightChannelTools(ctx *gin.Context) {
	var draft struct {
		Type         int    `json:"type"`
		ModelMapping string `json:"model_mapping"`
		BaseURL      string `json:"base_url"`
		Setting      string `json:"setting"`
	}
	if err := ctx.ShouldBindJSON(&draft); err != nil {
		common.ApiErrorT(ctx, "Invalid parameters")
		return
	}
	var settings dto.ChannelSettings
	if draft.Setting != "" && common.Unmarshal([]byte(draft.Setting), &settings) != nil {
		common.ApiErrorT(ctx, "Invalid parameters")
		return
	}
	search := abstract.PreferredChannelOperation(draft.Type, settings.WebSearchProvider, abstract.ContractToolsSearch)
	fetch := abstract.PreferredChannelOperation(draft.Type, settings.WebSearchProvider, abstract.ContractToolsFetch)
	common.ApiSuccess(ctx, gin.H{
		"missing_search_mapping": abstract.MissingOperationMapping(search, draft.ModelMapping, abstract.ContractToolsSearch),
		"missing_fetch_mapping":  abstract.MissingOperationMapping(fetch, draft.ModelMapping, abstract.ContractToolsFetch),
		"unsupported_endpoint":   (search.Provider != "" && !search.SupportsEndpoint(draft.BaseURL)) || (fetch.Provider != "" && !fetch.SupportsEndpoint(draft.BaseURL)),
	})
}
