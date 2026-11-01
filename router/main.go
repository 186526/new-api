package router

import (
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/middleware"

	"github.com/gin-gonic/gin"
)

func SetRouter(router *gin.Engine, assets WebAssets) {
	SetApiRouter(router)
	SetDashboardRouter(router)
	SetRelayRouter(router)
	SetTaskPluginProtocolRouter(router)
	SetVideoRouter(router)
	SetTaskRouter(router)
	pluginDispatcher := SetPluginRouter(router)
	frontendBaseUrl := os.Getenv("FRONTEND_BASE_URL")
	if common.IsMasterNode && frontendBaseUrl != "" {
		frontendBaseUrl = ""
		common.SysLog(common.LogText("FRONTEND_BASE_URL is ignored on master node"))
	}
	if frontendBaseUrl == "" {
		if len(assets.IndexPage) == 0 {
			// A standalone web image serves the dashboard; this process only
			// answers the API routes registered above.
			router.NoRoute(
				pluginDispatcher,
				middleware.RouteTag("web"),
				middleware.AccessTokenAudit(),
				func(c *gin.Context) {
					c.String(http.StatusNotFound, "404 page not found")
				},
			)
			return
		}
		SetWebRouter(router, assets, pluginDispatcher)
		return
	}
	frontendBaseUrl = strings.TrimSuffix(frontendBaseUrl, "/")
	router.NoRoute(
		pluginDispatcher,
		middleware.RouteTag("web"),
		middleware.AccessTokenAudit(),
		func(c *gin.Context) {
			c.Redirect(http.StatusMovedPermanently, fmt.Sprintf("%s%s", frontendBaseUrl, c.Request.RequestURI))
		},
	)
}
