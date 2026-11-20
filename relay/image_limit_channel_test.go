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

// TestCheckRequestImageLimitIgnoresTheStubChannelSettings guards the source of
// the limit: the stub channel the relay path holds reports zero, so a check
// fed from it refuses nothing, while the settings the distributor stored for
// the selected channel carry the limit.
func TestCheckRequestImageLimitIgnoresTheStubChannelSettings(t *testing.T) {
	channel := imageLimitTestStubChannel()
	selected := dto.ChannelSettings{MaxImagesPerRequest: 20}
	request := imageLimitTestRequest(t, types.RelayFormatOpenAI, imageUrls(21))

	assert.Nil(t, CheckRequestImageLimit(request, types.RelayFormatOpenAI, channel.GetSetting().MaxImagesPerRequest, channel.Id, channel.Name, imageLimitTestModel),
		"the stub channel's own settings leave the channel unlimited")
	assert.NotNil(t, CheckRequestImageLimit(request, types.RelayFormatOpenAI, selected.MaxImagesPerRequest, channel.Id, channel.Name, imageLimitTestModel),
		"the selected channel settings refuse the 21st image")
}

// TestCheckRequestImageLimitRefusesTheImagePastTheChannelLimit reproduces the
// production request: the stub channel has no settings, so the limit can only
// come from the selected channel settings. The 21st image must be refused.
func TestCheckRequestImageLimitRefusesTheImagePastTheChannelLimit(t *testing.T) {
	channel := imageLimitTestStubChannel()
	require.Nil(t, channel.Setting, "the stub getChannel builds carries no settings")
	selected := dto.ChannelSettings{MaxImagesPerRequest: 20}

	over := imageLimitTestRequest(t, types.RelayFormatOpenAI, imageUrls(21))
	apiErr := CheckRequestImageLimit(over, types.RelayFormatOpenAI, selected.MaxImagesPerRequest, channel.Id, channel.Name, imageLimitTestModel)
	require.NotNil(t, apiErr, "21 images pass the limit of 20")
	assert.Equal(t, imageLimitErrorCode, apiErr.GetErrorCode())
	assert.True(t, types.IsSkipRetryError(apiErr))
	assert.False(t, types.IsChannelError(apiErr))

	atLimit := imageLimitTestRequest(t, types.RelayFormatOpenAI, imageUrls(20))
	assert.Nil(t, CheckRequestImageLimit(atLimit, types.RelayFormatOpenAI, selected.MaxImagesPerRequest, channel.Id, channel.Name, imageLimitTestModel),
		"20 images stay within the limit of 20")
}

// TestCheckRequestImageLimitLeavesChannelUnlimitedWithoutSettings covers a
// selected channel that sets no limit: it refuses nothing.
func TestCheckRequestImageLimitLeavesChannelUnlimitedWithoutSettings(t *testing.T) {
	channel := imageLimitTestStubChannel()
	unlimited := dto.ChannelSettings{}
	request := imageLimitTestRequest(t, types.RelayFormatOpenAI, imageUrls(21))
	assert.Nil(t, CheckRequestImageLimit(request, types.RelayFormatOpenAI, unlimited.MaxImagesPerRequest, channel.Id, channel.Name, imageLimitTestModel),
		"a channel without a limit refuses nothing")
}
