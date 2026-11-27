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
	TextQuery string `json:"text_query,omitempty"`
	URL       string `json:"url,omitempty"`
	Model     string `json:"model,omitempty"`
	// NumResults caps how many results a rendered search answer carries. The
	// upstream decides how many it returns, so this is applied after the answer
	// arrives; zero leaves it uncapped.
	NumResults int `json:"num_results,omitempty"`
	// MaxResults is the same cap under the name an earlier revision of the
	// client documentation used. It is read as a fallback so a caller that
	// followed that name is capped rather than silently ignored.
	MaxResults int             `json:"max_results,omitempty"`
	RawBody    json.RawMessage `json:"-"`
}

// ResultCount is the cap the caller asked for, under either name. NumResults
// wins when both are present, because it is the name the client writes today.
func (r *MoonshotServiceRequest) ResultCount() int {
	if r.NumResults > 0 {
		return r.NumResults
	}
	return r.MaxResults
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
