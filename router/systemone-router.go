package router

import (
	"github.com/QuantumNous/new-api/controller"
	"github.com/QuantumNous/new-api/middleware"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/types"

	"github.com/gin-contrib/gzip"
	"github.com/gin-gonic/gin"
)

// SetSystemOneRouter registers the TypeSafe System One contract under its own
// path prefix. The group answers the whole contract in the TypeSafe error
// family, so a client generated from the upstream OpenAPI reads every failure
// the same way; the shared /v1 endpoints keep their OpenAI shapes.
//
// It must run after SetRelayRouter, whose engine-level middleware (body
// decompression, body storage cleanup, stats) every relay route inherits.
func SetSystemOneRouter(router *gin.Engine) {
	group := router.Group(relayconstant.SystemOneContractPathPrefix)
	group.Use(middleware.SystemOneErrorShape())
	group.Use(gzip.Gzip(gzip.DefaultCompression))
	group.Use(middleware.RouteTag("relay"))
	group.Use(middleware.GlobalAPIRateLimit())
	group.Use(middleware.TokenAuth())
	{
		group.GET("/models", controller.ListSystemOneModels)
		// The upstream publishes no single-model endpoint, so a caller that asks
		// for one reads the same 404 a client expects for a missing route.
		group.GET("/models/:name", controller.SystemOneModelNotFound)
	}

	decision := group.Group("/systemone")
	decision.Use(middleware.SystemOneBodyLimit())
	decision.Use(middleware.SystemOneModelGuard())
	decision.Use(middleware.Distribute())
	{
		decision.POST("", func(c *gin.Context) {
			controller.Relay(c, types.RelayFormatSystemOne)
		})
	}
}
