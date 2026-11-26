package common

import (
	"strings"

	"github.com/QuantumNous/new-api/constant"
)

// IsSystemOneModel reports whether a model name belongs to the decision family
// the System One contract serves. A request that names any other model cannot be
// answered by the contract, and retrying it on another channel never will.
func IsSystemOneModel(modelName string) bool {
	return strings.HasPrefix(modelName, constant.SystemOneModelPrefix)
}
