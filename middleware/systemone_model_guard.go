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
		var modelName string
		if common.IsMultipartFormContentType(c.GetHeader("Content-Type")) {
			request, problems, err := common.ReadSystemOneMultipart(c)
			switch {
			case common.IsRequestBodyTooLargeError(err):
				common.WriteTypeSafeError(c, http.StatusRequestEntityTooLarge, "Request body is larger than the 32 MiB the upstream accepts")
			case err != nil:
				common.WriteTypeSafeError(c, http.StatusBadRequest, "Could not read the request body")
			case len(problems) > 0:
				common.WriteTypeSafeValidationError(c, http.StatusUnprocessableEntity, problems)
			default:
				modelName = request.Model
			}
			if modelName == "" {
				c.Abort()
				return
			}
		} else {
			request := &dto.SystemOneRequest{}
			if err := common.UnmarshalBodyReusable(c, request); err != nil {
				c.Next()
				return
			}
			modelName = request.Model
		}
		if modelName != "" && !common.IsSystemOneModel(modelName) {
			common.WriteTypeSafeError(c, http.StatusBadRequest,
				"The model '"+modelName+"' is not served by the System One endpoint")
			c.Abort()
			return
		}
		c.Next()
	}
}
