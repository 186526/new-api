package middleware

import (
	"github.com/QuantumNous/new-api/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"

	"github.com/gin-gonic/gin"
)

// SystemOneErrorShape marks a request under the System One contract as answering
// in the TypeSafe document family. It runs before the group that authenticates,
// because a rejected key is written by that group and must already be in the
// shape the caller's client parses.
func SystemOneErrorShape() gin.HandlerFunc {
	return func(c *gin.Context) {
		if relayconstant.IsSystemOnePath(c.Request.URL.Path) {
			common.SetErrorShape(c, common.ErrorShapeTypeSafe)
		}
		c.Next()
	}
}
