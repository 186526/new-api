package newapi

import "github.com/QuantumNous/new-api/constant"

const ChannelName = "newapi"

// ModelList carries only the capability names. Chat models are fetched
// dynamically from the upstream /v1/models, but a capability is not the
// upstream's to advertise: the search pipeline resolves it to this channel's own
// provider, and the admin model picker fills itself from this list, so leaving
// the names out would make the ability impossible to enable.
var ModelList = []string{constant.ToolsSearchModel, constant.ToolsFetchModel}
