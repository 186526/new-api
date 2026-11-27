package constant

import "github.com/QuantumNous/new-api/relaykit/types"

// EndpointType moved to types with the conversion kit; aliases keep host
// code compiling unchanged.
type EndpointType = types.EndpointType

const (
	EndpointTypeOpenAI                = types.EndpointTypeOpenAI
	EndpointTypeOpenAIResponse        = types.EndpointTypeOpenAIResponse
	EndpointTypeOpenAIResponseCompact = types.EndpointTypeOpenAIResponseCompact
	EndpointTypeOpenAIAlphaSearch     = types.EndpointTypeOpenAIAlphaSearch
	EndpointTypeDeepSeekToolsSearch   = types.EndpointTypeDeepSeekToolsSearch
	EndpointTypeDeepSeekToolsFetch    = types.EndpointTypeDeepSeekToolsFetch
	EndpointTypeMoonshotServiceSearch = types.EndpointTypeMoonshotServiceSearch
	EndpointTypeMoonshotServiceFetch  = types.EndpointTypeMoonshotServiceFetch
	EndpointTypeSystemOne             = types.EndpointTypeSystemOne
	EndpointTypeAnthropic             = types.EndpointTypeAnthropic
	EndpointTypeGemini                = types.EndpointTypeGemini
	EndpointTypeJinaRerank            = types.EndpointTypeJinaRerank
	EndpointTypeImageGeneration       = types.EndpointTypeImageGeneration
	EndpointTypeEmbeddings            = types.EndpointTypeEmbeddings
	EndpointTypeOpenAIVideo           = types.EndpointTypeOpenAIVideo
)

// Model names the distributor assigns to every search and fetch route, whose
// bodies carry no model. They name the capability, not one protocol: a channel
// that lists them serves /v1/tools/* and /moonshot.service/v1/* alike, because
// both reach the same upstream through the same pipeline. They are also the
// billing names.
const (
	ToolsSearchModel = "tools-search"
	ToolsFetchModel  = "tools-fetch"
)
