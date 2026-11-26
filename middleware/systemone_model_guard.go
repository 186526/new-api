package middleware

import (
	"net/http"

	"github.com/QuantumNous/new-api/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"

	"github.com/gin-gonic/gin"
)

// SystemOneModelGuard refuses a decision request that names a model the contract
// does not serve. Without it the request reaches channel selection, finds no
// channel, and answers 500 for a mistake the caller can fix; retrying that
// request never succeeds. A model the contract does serve but no channel
// currently holds still answers the ordinary no-channel error, because that one
// is a capacity problem and may clear.
//
// It runs before channel selection on the relay group, where the legacy alias
// also lives, and passes every request outside the contract through.
func SystemOneModelGuard() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !relayconstant.IsSystemOnePath(c.Request.URL.Path) {
			c.Next()
			return
		}
		request := &dto.SystemOneRequest{}
		if err := common.UnmarshalBodyReusable(c, request); err != nil {
			// The body is validated later, where a failure is reported with the
			// field locations a client reads; this check only needs a name.
			c.Next()
			return
		}
		if request.Model != "" && !common.IsSystemOneModel(request.Model) {
			common.WriteTypeSafeError(c, http.StatusBadRequest,
				"The model '"+request.Model+"' is not served by the System One endpoint")
			c.Abort()
			return
		}
		c.Next()
	}
}
