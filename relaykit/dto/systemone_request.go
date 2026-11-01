package dto

import (
	"encoding/json"
	"net/http"

	"github.com/QuantumNous/new-api/relaykit/types"
)

// SystemOneRequest is a TypeSafe decision-model request. It carries one shared
// state and a set of typed questions instead of chat messages, and answers come
// back as probabilities, labels or scores. RawBody preserves the original JSON
// so state, questions and unknown fields reach the upstream unchanged.
type SystemOneRequest struct {
	Model   string          `json:"model"`
	RawBody json.RawMessage `json:"-"`
}

func (r *SystemOneRequest) GetTokenCountMeta() *types.TokenCountMeta {
	combineText := ""
	if len(r.RawBody) > 0 {
		combineText = string(r.RawBody)
	}
	return &types.TokenCountMeta{
		CombineText: combineText,
		TokenType:   types.TokenTypeTokenizer,
	}
}

func (r *SystemOneRequest) IsStream(_ *http.Request) bool {
	return false
}

func (r *SystemOneRequest) SetModelName(modelName string) {
	if modelName != "" {
		r.Model = modelName
	}
}
