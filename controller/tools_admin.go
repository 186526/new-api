package controller

import (
	"errors"
	"net/http"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/service/websearch/request"
	"github.com/QuantumNous/new-api/setting/websearch_setting"
	"github.com/gin-gonic/gin"
)

func toolsAdminSuccess(ctx *gin.Context, data any) {
	ctx.JSON(http.StatusOK, gin.H{"success": true, "data": data})
}

func toolsAdminError(ctx *gin.Context, status int, code, message string) {
	ctx.JSON(status, gin.H{"success": false, "code": code, "message": message})
}

func GetWebSearchSummary(ctx *gin.Context) {
	providers, err := service.WebSearchProviderSummaries(ctx.Request.Context())
	if err != nil {
		toolsAdminError(ctx, 500, "websearch_summary_unavailable", "Tool supply information is unavailable.")
		return
	}
	toolsAdminSuccess(ctx, gin.H{"providers": providers})
}

func SimulateWebSearch(ctx *gin.Context) {
	common.SetContextKey(ctx, constant.ContextKeyAuditLogged, true)
	var input service.WebSearchSimulationInput
	ctx.Request.Body = http.MaxBytesReader(ctx.Writer, ctx.Request.Body, 4096)
	if err := ctx.ShouldBindJSON(&input); err != nil {
		toolsAdminError(ctx, 400, "websearch_invalid_simulation", "Invalid tool simulation.")
		return
	}
	result, err := service.SimulateWebSearchRouting(input)
	if errors.Is(err, service.ErrInvalidWebSearchSimulation) {
		toolsAdminError(ctx, 400, "websearch_invalid_simulation", err.Error())
		return
	}
	if err != nil {
		toolsAdminError(ctx, 500, "websearch_simulation_failed", "Tool route simulation failed.")
		return
	}
	toolsAdminSuccess(ctx, result)
}

func GetWebSearchGroupPolicies(ctx *gin.Context) {
	result, err := model.ReadWebSearchGroupOptions(ctx.Request.Context())
	if err != nil {
		toolsAdminError(ctx, 500, "websearch_policies_unavailable", "Group policies are unavailable.")
		return
	}
	toolsAdminSuccess(ctx, result)
}

func UpdateWebSearchGroupPolicies(ctx *gin.Context) {
	var input model.WebSearchGroupOptions
	ctx.Request.Body = http.MaxBytesReader(ctx.Writer, ctx.Request.Body, 1<<20)
	if err := ctx.ShouldBindJSON(&input); err != nil || input.Version == "" || len(input.Version) > 64 {
		toolsAdminError(ctx, 400, "websearch_invalid_policies", "A policy value and version are required.")
		return
	}
	if err := websearch_setting.ValidateGroupPolicies(input.Value); err != nil {
		toolsAdminError(ctx, 400, "websearch_invalid_policies", err.Error())
		return
	}
	result, err := model.CompareAndSwapWebSearchGroupOptions(ctx.Request.Context(), input.Version, input.Value)
	if errors.Is(err, model.ErrWebSearchGroupPolicyConflict) {
		ctx.JSON(http.StatusConflict, gin.H{"success": false, "code": "websearch_policy_conflict", "message": err.Error(), "data": result})
		return
	}
	if err != nil {
		toolsAdminError(ctx, 500, "websearch_policy_save_failed", "Could not save group policies.")
		return
	}
	toolsAdminSuccess(ctx, result)
}

func GetWebSearchCacheStatus(ctx *gin.Context) {
	scopes := []string{"forward", "redirect"}
	if scope := ctx.Query("scope"); scope != "" {
		scopes = []string{scope}
	}
	statuses := make([]request.CacheStatus, 0, len(scopes))
	for _, scope := range scopes {
		status, err := request.WebSearchCacheStatus(ctx.Request.Context(), scope)
		if err != nil {
			writeWebSearchCacheError(ctx, err)
			return
		}
		statuses = append(statuses, status)
	}
	toolsAdminSuccess(ctx, gin.H{"scopes": statuses})
}

func ClearWebSearchCache(ctx *gin.Context) {
	var input request.CacheSelection
	ctx.Request.Body = http.MaxBytesReader(ctx.Writer, ctx.Request.Body, 32<<10)
	if err := ctx.ShouldBindJSON(&input); err != nil {
		writeWebSearchCacheError(ctx, request.ErrInvalidCacheSelection)
		return
	}
	status, err := request.InvalidateWebSearchCache(ctx.Request.Context(), input)
	if err != nil {
		writeWebSearchCacheError(ctx, err)
		return
	}
	toolsAdminSuccess(ctx, status)
}

func writeWebSearchCacheError(ctx *gin.Context, err error) {
	switch {
	case errors.Is(err, request.ErrInvalidCacheSelection):
		toolsAdminError(ctx, 400, "websearch_invalid_cache_target", err.Error())
	case errors.Is(err, request.ErrCacheInvalidationCapacity):
		toolsAdminError(ctx, 409, "websearch_cache_target_limit", err.Error())
	default:
		toolsAdminError(ctx, 503, "websearch_cache_unavailable", "The configured cache backend is unavailable.")
	}
}
