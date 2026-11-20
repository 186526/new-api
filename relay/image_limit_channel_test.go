package relay

import (
	"testing"

	"github.com/QuantumNous/new-api/constant"
	appmodel "github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// imageLimitTestStubChannel is what getChannel builds for a channel the
// distributor already selected: identity only, no settings.
func imageLimitTestStubChannel() *appmodel.Channel {
	autoBan := 1
	return &appmodel.Channel{Id: 4242, Type: constant.ChannelTypeMistral, Name: "mistral", AutoBan: &autoBan}
}

// selectedChannelImageLimit is the limit the relay path passes to the check:
// the settings the distributor stored for the selected channel.
func selectedChannelImageLimit(limit int) int {
	return ChannelImageLimit(dto.ChannelSettings{MaxImagesPerRequest: limit})
}

// TestChannelImageLimitReadsTheSelectedChannelSettings guards the source of the
// limit: the context settings are complete, while the stub channel the relay
// path holds carries none and would report zero.
func TestChannelImageLimitReadsTheSelectedChannelSettings(t *testing.T) {
	assert.Equal(t, 20, selectedChannelImageLimit(20))
	assert.Equal(t, 0, selectedChannelImageLimit(0))
	assert.Equal(t, 0, imageLimitTestStubChannel().GetSetting().MaxImagesPerRequest,
		"the stub getChannel builds carries no settings of its own")
}

// TestCheckRequestImageLimitRefusesTheImagePastTheChannelLimit reproduces the
// production request: the stub channel has no settings, so the limit can only
// come from the selected channel settings. The 21st image must be refused.
func TestCheckRequestImageLimitRefusesTheImagePastTheChannelLimit(t *testing.T) {
	channel := imageLimitTestStubChannel()
	require.Nil(t, channel.Setting, "the stub getChannel builds carries no settings")

	over := imageLimitTestRequest(t, types.RelayFormatOpenAI, imageUrls(21))
	apiErr := CheckRequestImageLimit(over, types.RelayFormatOpenAI, selectedChannelImageLimit(20), channel.Id, channel.Name, imageLimitTestModel)
	require.NotNil(t, apiErr, "21 images pass the limit of 20")
	assert.Equal(t, imageLimitErrorCode, apiErr.GetErrorCode())
	assert.True(t, types.IsSkipRetryError(apiErr))
	assert.False(t, types.IsChannelError(apiErr))

	atLimit := imageLimitTestRequest(t, types.RelayFormatOpenAI, imageUrls(20))
	assert.Nil(t, CheckRequestImageLimit(atLimit, types.RelayFormatOpenAI, selectedChannelImageLimit(20), channel.Id, channel.Name, imageLimitTestModel),
		"20 images stay within the limit of 20")
}

// TestCheckRequestImageLimitLeavesChannelUnlimitedWithoutSettings covers a
// selected channel that sets no limit: it refuses nothing.
func TestCheckRequestImageLimitLeavesChannelUnlimitedWithoutSettings(t *testing.T) {
	channel := imageLimitTestStubChannel()
	request := imageLimitTestRequest(t, types.RelayFormatOpenAI, imageUrls(21))
	assert.Nil(t, CheckRequestImageLimit(request, types.RelayFormatOpenAI, selectedChannelImageLimit(0), channel.Id, channel.Name, imageLimitTestModel),
		"a channel without a limit refuses nothing")
}
