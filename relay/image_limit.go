package relay

import (
	"fmt"
	"net/http"

	appmetrics "github.com/QuantumNous/new-api/pkg/metrics"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
)

// imageLimitErrorCode is the error code a client treats as a context
// overflow: it compacts the conversation and resends the request without the
// compacted images, so a request refused for its image count succeeds once
// the conversation is compacted.
const imageLimitErrorCode types.ErrorCode = "context_length_exceeded"

// CheckRequestImageLimit refuses a request that carries more distinct images
// than the channel's max_images_per_request. The setting is the upstream's own
// limit, so a request holding exactly that many images still reaches the
// upstream, and the first one past it is refused. A zero limit leaves the
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
