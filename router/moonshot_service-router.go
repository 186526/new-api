package router

import (
	"github.com/QuantumNous/new-api/controller"
	"github.com/QuantumNous/new-api/middleware"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/types"

	"github.com/gin-gonic/gin"
)

// SetMoonshotServiceRouter registers the Kimi Code search and fetch endpoints.
// A client points [services.moonshot_search] and [services.moonshot_fetch] at
// the group prefix and appends /search or /fetch itself.
//
// It must run after SetRelayRouter, whose engine-level middleware (body
// decompression, body storage cleanup, stats) every relay route inherits.
func SetMoonshotServiceRouter(router *gin.Engine) {
	group := router.Group(relayconstant.MoonshotServicePathPrefix)
	group.Use(middleware.RouteTag("relay"))
	group.Use(middleware.GlobalAPIRateLimit())
	group.Use(middleware.TokenAuth())
	group.Use(middleware.Distribute())
	{
		group.POST("/search", func(c *gin.Context) {
			controller.Relay(c, types.RelayFormatMoonshotService)
		})
		group.POST("/fetch", func(c *gin.Context) {
			controller.Relay(c, types.RelayFormatMoonshotService)
		})
	}
}
