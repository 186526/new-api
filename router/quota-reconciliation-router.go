package router

import (
	"net/http"

	"github.com/QuantumNous/new-api/controller"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/service/authz"
	"github.com/gin-gonic/gin"
)

// Keep the endpoints unregistered until the reconciliation evidence and confirmation flow are verified.
const quotaReconciliationRoutesReady = false

func RegisterQuotaReconciliationRoutes(apiRouter *gin.RouterGroup) {
	if !quotaReconciliationRoutesReady {
		return
	}
	middleware.DeclareAccessTokenPermissionRoute(http.MethodGet, joinPaths(apiRouter.BasePath(), quotaReconciliationPath), authz.AuditRead)
	mountQuotaReconciliationRoutes(apiRouter)
}

const quotaReconciliationPath = "/audit/quota-reconciliation"

func mountQuotaReconciliationRoutes(apiRouter *gin.RouterGroup) {
	review := apiRouter.Group(quotaReconciliationPath, middleware.DisableCache())
	review.GET("", middleware.AdminAuth(), middleware.RequirePermission(authz.AuditRead), controller.GetQuotaReconciliation)
	review.POST("", middleware.RootAuth(), requireQuotaReconciliationSession, middleware.RequirePermission(authz.AuditRead), controller.ConfirmQuotaReconciliation)
}

func requireQuotaReconciliationSession(ctx *gin.Context) {
	if _, ok := middleware.GetSessionAuthIdentity(ctx); !ok {
		ctx.AbortWithStatusJSON(http.StatusForbidden, gin.H{"success": false, "message": "A dashboard session is required"})
		return
	}
	ctx.Next()
}
