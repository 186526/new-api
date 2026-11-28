package abstract

import (
	"github.com/QuantumNous/new-api/relaykit/dto"
)

// ChannelContext is everything a provider needs to reach its upstream. The
// relay handler builds it so this package never imports the relay layer.
type ChannelContext struct {
	// ChannelID identifies the channel whose stored settings apply.
	ChannelID int
	// ChannelType is constant.ChannelTypeAntigravity and friends.
	ChannelType int
	// BaseURL is the channel's own base URL, empty for the provider default.
	BaseURL string
	// Key is the credential the relay attempt selected, which is the channel
	// key unless the channel holds several.
	Key string
	// Settings holds the channel settings that tune this package.
	Settings           dto.ChannelSettings
	PreferredProviders []string
	OnlyProviders      bool
	// ModelMapping distinguishes a configured Codex search capability from a
	// channel that cannot build a cross-contract search request.
	ModelMapping string
	CustomTools  *string
}
