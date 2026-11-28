package router

import (
	"github.com/QuantumNous/new-api/controller"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/gin-gonic/gin"
)

func RegisterToolsAdminRoutes(apiRouter *gin.RouterGroup) {
	tools := apiRouter.Group("/tools-api", middleware.RootAuth())
	tools.GET("/summary", controller.GetWebSearchSummary)
	tools.POST("/simulate", controller.SimulateWebSearch)
	tools.GET("/group-policies", controller.GetWebSearchGroupPolicies)
	tools.PUT("/group-policies", controller.UpdateWebSearchGroupPolicies)
	options := apiRouter.Group("/option", middleware.RootAuth())
	options.GET("/websearch_cache", controller.GetWebSearchCacheStatus)
	options.DELETE("/websearch_cache", controller.ClearWebSearchCache)
}
