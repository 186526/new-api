package dto

import (
	"encoding/json"
	"net/http"

	"github.com/QuantumNous/new-api/relaykit/types"
)

// MoonshotServiceRequest is a Kimi Code search or fetch request
// (/moonshot.service/v1/search, /moonshot.service/v1/fetch). The client sends
// either text_query or url and never a model, so Model holds the routing name
// the distributor selects the channel with. RawBody preserves the original
// JSON, which the search provider forwards with unknown fields intact.
type MoonshotServiceRequest struct {
	TextQuery string          `json:"text_query,omitempty"`
	URL       string          `json:"url,omitempty"`
	Model     string          `json:"model,omitempty"`
	RawBody   json.RawMessage `json:"-"`
}

func (r *MoonshotServiceRequest) GetTokenCountMeta() *types.TokenCountMeta {
	return &types.TokenCountMeta{
		CombineText: string(r.RawBody),
		TokenType:   types.TokenTypeTokenizer,
	}
}

func (r *MoonshotServiceRequest) IsStream(_ *http.Request) bool {
	return false
}

func (r *MoonshotServiceRequest) SetModelName(modelName string) {
	if modelName != "" {
		r.Model = modelName
	}
}
