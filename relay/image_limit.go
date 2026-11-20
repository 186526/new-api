package relay

import (
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strings"

	"github.com/QuantumNous/new-api/common"
	appmetrics "github.com/QuantumNous/new-api/pkg/metrics"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	hostreasoning "github.com/QuantumNous/new-api/setting/reasoning"
)

// imageLimitErrorCode is the error code a client treats as a context
// overflow: it compacts the conversation and resends the request without the
// compacted images, so a request refused for its image count succeeds once
// the conversation is compacted.
const imageLimitErrorCode types.ErrorCode = "context_length_exceeded"

// imageLimitRetryHint closes every image refusal with the same action, whether
// new-api refused the request before the upstream saw it or wrote it from the
// upstream's own refusal.
const imageLimitRetryHint = "Request a smaller batch of images or compact the conversation and retry."

// upstreamImageLimitPattern matches the reply of an upstream refusing a request
// for its image count, for example `Total number of images exceeds the maximum
// allowed of 8.` The count the upstream read is not part of the match, so a
// session or plan named in the sentence, or a second sentence after it, still
// matches.
var upstreamImageLimitPattern = regexp.MustCompile(`(?i)images?\b[^.]*?\bexceeds?\b[^.]*?\bmaximum allowed\b`)

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
		"Total number of images (%d) exceeds the maximum allowed of %d. %s",
		images, limit, imageLimitRetryHint,
	)
	return types.WithOpenAIError(types.OpenAIError{
		Message: message,
		Type:    "invalid_request_error",
		Code:    imageLimitErrorCode,
	}, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
}

// TranslateUpstreamImageLimitError turns an upstream refusal for the image
// count into the local image refusal, so the client reads it as a context
// overflow and compacts the conversation instead of failing on a 400 it cannot
// act on. The upstream tried and answered, so the rewritten error skips the
// retry and names no channel fault: the channel stays enabled.
//
// The message keeps the upstream wording and its own limit, so the client and
// the operator both see what the upstream allowed. The wording is read through
// MaskSensitiveError, so a key the upstream echoes is masked the same way the
// envelope masks it. An error of another kind, including one that names a
// channel fault, is returned unchanged.
func TranslateUpstreamImageLimitError(err *types.NewAPIError) *types.NewAPIError {
	if err == nil || err.StatusCode != http.StatusBadRequest {
		return err
	}
	// An error new-api raised itself, before or after an upstream answer, is
	// not the upstream's wording, even when it quotes the request.
	if service.RelayErrorSource(err) != "upstream" || !upstreamImageLimitPattern.MatchString(err.Error()) {
		return err
	}
	// A body that names a channel fault keeps its own code, so the channel is
	// still disabled the way the upstream asked for. RelayErrorSource already
	// answers "channel" for such an error, which the source guard above turns
	// away, and IsChannelError repeats that answer for a reader of this file.
	if types.IsChannelError(err) {
		return err
	}
	return types.WithOpenAIError(types.OpenAIError{
		Message: strings.TrimSpace(err.MaskSensitiveError()) + " " + imageLimitRetryHint,
		Type:    "invalid_request_error",
		Code:    imageLimitErrorCode,
	}, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
}

// refuseModelMappingCycle answers a request whose channel model_mapping forms
// a cycle. The relay turns such a mapping into a failed request before the
// upstream sees it, so the image check refuses it the same way instead of
// comparing the images against a limit the request will never be sent with.
func refuseModelMappingCycle(err error) *types.NewAPIError {
	return types.NewErrorWithStatusCode(
		err,
		types.ErrorCodeInvalidRequest,
		http.StatusBadRequest,
		types.ErrOptionWithSkipRetry(),
	)
}

// CheckRequestImageLimitForModels refuses a request the same way as
// CheckRequestImageLimit, but takes the limit of the models the channel
// settings configure: the requested model, the names its reasoning suffix
// strips to, and the name the channel's model_mapping sends it under. A model
// without a configured limit falls back to the channel-wide limit, and a zero
// limit leaves the channel unlimited.
func CheckRequestImageLimitForModels(
	request dto.Request,
	relayFormat types.RelayFormat,
	settings dto.ChannelSettings,
	modelMapping string,
	channelId int,
	channelName string,
	model string,
) *types.NewAPIError {
	names, err := UpstreamModelNames(settings, modelMapping, model)
	if err != nil {
		return refuseModelMappingCycle(err)
	}
	images := relaycommon.RequestImageCount(relayFormat, request)
	limit := settings.ImageLimitForModel(names...)
	if limit <= 0 {
		return nil
	}
	if images > limit {
		return refuseImagesPastLimit(images, limit, channelId, channelName, model)
	}
	return nil
}

// UpstreamModelNames returns the names a request for the model may reach the
// upstream under, in the order the per-model image limits are looked up: the
// requested name, the name its reasoning suffix strips to, and the name the
// channel's model_mapping sends it under. A mapping cycle is an error, because
// the relay refuses such a request before it reaches the upstream.
func UpstreamModelNames(settings dto.ChannelSettings, modelMapping string, model string) ([]string, error) {
	names := make([]string, 0, 4)
	addName := func(name string) {
		if name == "" || slices.Contains(names, name) {
			return
		}
		names = append(names, name)
	}
	addName(strings.TrimSpace(model))
	if baseName := hostreasoning.BaseModelName(model); baseName != model {
		mappedBase, err := resolveModelMapping(modelMapping, baseName)
		if err != nil {
			return nil, err
		}
		addName(baseName)
		addName(mappedBase)
	}
	mapped, err := resolveModelMapping(modelMapping, model)
	if err != nil {
		return nil, err
	}
	addName(mapped)
	return names, nil
}

// modelMappingCycleError reports a model_mapping whose chain returns to a
// model it already passed, which no request can be sent under.
type modelMappingCycleError struct {
	model string
}

func (e *modelMappingCycleError) Error() string {
	return "model_mapping_contains_cycle: " + e.model
}

// resolveModelMapping walks the channel's model_mapping the way the relay's
// ModelMappedHelper does, so the limit is read for the model the request is
// sent under: a chain is followed to its end and a model that maps to itself
// stays where it is. A chain that returns to a model it already passed is
// reported as a *modelMappingCycleError, the same condition ModelMappedHelper
// refuses. The mapping string is what the distributor stored for the selected
// channel; an unreadable one leaves the model unmapped.
func resolveModelMapping(modelMapping string, model string) (string, error) {
	if modelMapping == "" || modelMapping == "{}" {
		return model, nil
	}
	modelMap := make(map[string]string)
	if err := common.UnmarshalJsonStr(modelMapping, &modelMap); err != nil {
		return model, nil
	}
	current := model
	visited := map[string]bool{current: true}
	for {
		mapped, exists := modelMap[current]
		if !exists || mapped == "" {
			return current, nil
		}
		if visited[mapped] {
			if mapped == current {
				return current, nil
			}
			return "", &modelMappingCycleError{model: model}
		}
		visited[mapped] = true
		current = mapped
	}
}
