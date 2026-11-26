package middleware

import (
	"net/http"

	"github.com/QuantumNous/new-api/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/systemone"

	"github.com/gin-gonic/gin"
)

// SystemOneBodyLimit refuses a System One body larger than the upstream accepts.
// The shared MAX_REQUEST_BODY_MB ceiling is far higher than the contract's 32
// MiB, so without this check a caller transfers the whole body before learning
// that the upstream would reject it. Content-Length settles it before a byte is
// read; a chunked body is still cut off at the same ceiling.
//
// It runs on the relay group, where the legacy alias also lives, so it acts only
// on the paths of the contract and passes every other request through.
func SystemOneBodyLimit() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !relayconstant.IsSystemOnePath(c.Request.URL.Path) {
			c.Next()
			return
		}
		limit := int64(systemone.MaxRequestBodyBytes)
		if c.Request.ContentLength > limit {
			common.WriteTypeSafeError(c, http.StatusRequestEntityTooLarge,
				"Request body is larger than the 32 MiB the upstream accepts")
			c.Abort()
			return
		}
		if c.Request.Body != nil {
			c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, limit)
		}
		c.Next()
	}
}
