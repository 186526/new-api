package relay

import (
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/QuantumNous/new-api/common"
	appmetrics "github.com/QuantumNous/new-api/pkg/metrics"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	hostreasoning "github.com/QuantumNous/new-api/setting/reasoning"
)

// imageLimitErrorCode is the error code a client treats as a context
// overflow: it compacts the conversation and resends the request without the
// compacted images, so a request refused for its image count succeeds once
// the conversation is compacted.
const imageLimitErrorCode types.ErrorCode = "context_length_exceeded"

// CheckRequestImageLimit refuses a request that carries more image
// occurrences than the limit of the model it is sent to. The limit is the
// upstream's own, so a request holding exactly that many images still reaches
// the upstream, and the first one past it is refused. A zero limit leaves the
// channel unlimited, and a relay format without images is never refused.
func CheckRequestImageLimit(
	request dto.Request,
	relayFormat types.RelayFormat,
	limit int,
	channelId int,
	channelName string,
	model string,
) *types.NewAPIError {
	if limit <= 0 {
		return nil
	}
	images := relaycommon.RequestImageCount(relayFormat, request)
	return refuseImagesPastLimit(images, limit, channelId, channelName, model)
}

func refuseImagesPastLimit(images int, limit int, channelId int, channelName string, model string) *types.NewAPIError {
	if images <= limit {
		return nil
	}
	appmetrics.ObserveImageLimitRejection(channelName, channelId, model)
	message := fmt.Sprintf(
		"Total number of images (%d) exceeds the maximum allowed of %d. Request a smaller batch of images or compact the conversation and retry.",
		images, limit,
	)
	return types.WithOpenAIError(types.OpenAIError{
		Message: message,
		Type:    "invalid_request_error",
		Code:    imageLimitErrorCode,
	}, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
}

// CheckRequestImageLimitForModels refuses a request the same way as
// CheckRequestImageLimit, but takes the limit of the models the channel
// settings configure: the requested model, the names its reasoning suffix
// strips to, and the name the channel's model_mapping sends it under. A model
// without a configured limit falls back to the channel-wide limit.
func CheckRequestImageLimitForModels(
	request dto.Request,
	relayFormat types.RelayFormat,
	settings dto.ChannelSettings,
	modelMapping string,
	channelId int,
	channelName string,
	model string,
) *types.NewAPIError {
	images := relaycommon.RequestImageCount(relayFormat, request)
	limit := settings.ImageLimitForModel(UpstreamModelNames(settings, modelMapping, model)...)
	if images > limit {
		return refuseImagesPastLimit(images, limit, channelId, channelName, model)
	}
	return nil
}

// UpstreamModelNames returns the names a request for the model may reach the
// upstream under, in the order the per-model image limits are looked up: the
// requested name, the name its reasoning suffix strips to, and the name the
// channel's model_mapping sends it under.
func UpstreamModelNames(settings dto.ChannelSettings, modelMapping string, model string) []string {
	names := make([]string, 0, 4)
	addName := func(name string) {
		if name == "" || slices.Contains(names, name) {
			return
		}
		names = append(names, name)
	}
	addName(strings.TrimSpace(model))
	if baseName := hostreasoning.BaseModelName(model); baseName != model {
		addName(baseName)
		addName(resolveModelMapping(settings, modelMapping, baseName))
	}
	addName(resolveModelMapping(settings, modelMapping, model))
	return names
}

// resolveModelMapping walks the channel's model_mapping the way the relay's
// ModelMappedHelper does: a chain is followed to its end, a model that maps to
// itself stays where it is, and a cycle leaves the model unmapped. The mapping
// string is what the distributor stored for the selected channel.
func resolveModelMapping(settings dto.ChannelSettings, modelMapping string, model string) string {
	if modelMapping == "" || modelMapping == "{}" {
		return model
	}
	modelMap := make(map[string]string)
	if err := common.UnmarshalJsonStr(modelMapping, &modelMap); err != nil {
		return model
	}
	current := model
	visited := map[string]bool{current: true}
	for {
		mapped, exists := modelMap[current]
		if !exists || mapped == "" {
			return current
		}
		if visited[mapped] {
			if mapped == current {
				return current
			}
			return model
		}
		visited[mapped] = true
		current = mapped
	}
}
