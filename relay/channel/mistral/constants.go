package mistral

import (
	"net/http"
	"strings"
)

var ModelList = []string{
	"open-mistral-7b",
	"open-mixtral-8x7b",
	"mistral-small-latest",
	"mistral-medium-latest",
	"mistral-large-latest",
	"mistral-embed",
}

var ChannelName = "mistral"

const (
	// mistralVibeAPIKeyPrefix marks a Vibe Code plan key, which is issued on
	// chat.mistral.ai rather than in the API console.
	mistralVibeAPIKeyPrefix = "mstrl_"
	// mistralVibeUserAgent is the User-Agent the Vibe CLI sends for model
	// calls. Relayed plan-key traffic uses it so it looks like Vibe CLI traffic
	// to the upstream.
	mistralVibeUserAgent = "mistral-client-python/Mistral-Vibe/3.0.4"
)

// ApplyUpstreamUserAgent makes a Vibe plan key's upstream requests look like
// Vibe CLI traffic. Other keys keep the User-Agent the caller already set.
func ApplyUpstreamUserAgent(apiKey string, headers http.Header) {
	if strings.HasPrefix(strings.TrimSpace(apiKey), mistralVibeAPIKeyPrefix) {
		headers.Set("User-Agent", mistralVibeUserAgent)
	}
}
