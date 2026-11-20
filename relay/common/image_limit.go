package common

import (
	"strings"

	"github.com/QuantumNous/new-api/relaykit/dto"
	kitutil "github.com/QuantumNous/new-api/relaykit/relayconvert/kitutil"
	"github.com/QuantumNous/new-api/relaykit/types"
)

// RequestImageCount returns how many times the request sends an image to the
// upstream. Every relay format has its own image parts, so each is read where
// that format declares them: the OpenAI Chat image_url, the Responses
// input_image, the Claude image blocks of the system prompt, the messages and
// nested tool results, and the Gemini image parts. An image that appears again
// counts again, because the upstream validates the occurrence count of the
// parts it receives, not how many different images they hold. A format that
// carries no images counts zero.
func RequestImageCount(relayFormat types.RelayFormat, request dto.Request) int {
	switch typed := request.(type) {
	case *dto.GeneralOpenAIRequest:
		if relayFormat != types.RelayFormatOpenAI {
			return 0
		}
		return countChatImages(typed)
	case *dto.OpenAIResponsesRequest:
		if relayFormat != types.RelayFormatOpenAIResponses {
			return 0
		}
		return countResponseImages(typed)
	case *dto.ClaudeRequest:
		if relayFormat != types.RelayFormatClaude {
			return 0
		}
		return countClaudeImages(typed)
	case *dto.GeminiChatRequest:
		if relayFormat != types.RelayFormatGemini {
			return 0
		}
		return countGeminiImages(typed)
	default:
		return 0
	}
}

func countChatImages(request *dto.GeneralOpenAIRequest) int {
	count := 0
	for _, message := range request.Messages {
		for _, part := range message.ParseContent() {
			if part.Type != dto.ContentTypeImageURL {
				continue
			}
			if image := part.GetImageMedia(); image != nil && image.Url != "" {
				count++
			}
		}
	}
	return count
}

// countGeminiImages counts the inline and file images of a request, also the
// ones a function response replays to the model.
func countGeminiImages(request *dto.GeminiChatRequest) int {
	count := 0
	collect := func(part dto.GeminiPart) {
		if part.InlineData != nil && part.InlineData.Data != "" && isImageMimeType(part.InlineData.MimeType) {
			count++
		}
		if part.FileData != nil && part.FileData.FileUri != "" && isImageMimeType(part.FileData.MimeType) {
			count++
		}
	}
	for _, content := range request.Contents {
		for _, part := range content.Parts {
			collect(part)
			if part.FunctionResponse == nil || len(part.FunctionResponse.Parts) == 0 {
				continue
			}
			var replay []dto.GeminiPart
			if err := kitutil.Unmarshal(part.FunctionResponse.Parts, &replay); err != nil {
				continue
			}
			for _, replayed := range replay {
				collect(replayed)
			}
		}
	}
	return count
}

func isImageMimeType(mimeType string) bool {
	return strings.HasPrefix(strings.ToLower(mimeType), "image/")
}

// countResponseImages counts the input_image parts of a Responses request.
// Each item addresses its image through image_url as a string or as an object
// with a url member.
func countResponseImages(request *dto.OpenAIResponsesRequest) int {
	count := 0
	for _, part := range request.ParseInput() {
		if part.Type != "input_image" {
			continue
		}
		if part.ImageUrl != "" {
			count++
		}
	}
	return count
}

// countClaudeImages counts the image blocks of a Claude request. Images
// travel inside the system prompt, inside message content, and inside the
// content of a tool_result block that replays screenshots to the model.
func countClaudeImages(request *dto.ClaudeRequest) int {
	count := 0
	collect := func(blocks []dto.ClaudeMediaMessage) {
		for _, block := range blocks {
			if block.Type == "image" {
				if source := block.ToFileSource(); source != nil {
					count++
				}
				continue
			}
			if block.Type != "tool_result" || block.IsStringContent() {
				continue
			}
			for _, part := range block.ParseMediaContent() {
				if part.Type != "image" {
					continue
				}
				if source := part.ToFileSource(); source != nil {
					count++
				}
			}
		}
	}
	if !request.IsStringSystem() {
		collect(request.ParseSystem())
	}
	for _, message := range request.Messages {
		if message.IsStringContent() {
			continue
		}
		blocks, _ := message.ParseContent()
		collect(blocks)
	}
	return count
}
