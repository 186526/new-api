package image_compress

import (
	"strings"

	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/tidwall/gjson"
)

func collectSlots(root gjson.Result, format types.RelayFormat, yield func(imageSlot) bool) {
	emit := func(slot imageSlot) bool {
		return slot.value.Type != gjson.String || slot.value.Str == "" || yield(slot)
	}
	var walk func(gjson.Result, int) bool
	walk = func(node gjson.Result, depth int) bool {
		if depth > 64 {
			return true
		}
		if node.IsArray() {
			stopped := false
			node.ForEach(func(_, child gjson.Result) bool { stopped = !walk(child, depth+1); return !stopped })
			return !stopped
		}
		if !node.IsObject() {
			return true
		}
		kind := node.Get("type").Str
		switch format {
		case types.RelayFormatOpenAI, types.RelayFormatOpenAIResponses:
			if kind == "image_url" || (format == types.RelayFormatOpenAIResponses && kind == "input_image") {
				image := node.Get("image_url")
				if image.IsObject() {
					image = image.Get("url")
				}
				return emit(imageSlot{value: image, style: "url"})
			}
		case types.RelayFormatClaude:
			if kind == "image" {
				source := node.Get("source")
				switch source.Get("type").Str {
				case "base64":
					if source.Get("media_type").Type != gjson.String {
						return true
					}
					return emit(imageSlot{value: source.Get("data"), media: source.Get("media_type")})
				case "url":
					return emit(imageSlot{value: source.Get("url"), source: source, style: "claude_url"})
				}
			}
		case types.RelayFormatGemini:
			for _, names := range [][2]string{{"inlineData", "mimeType"}, {"inline_data", "mime_type"}} {
				inline := node.Get(names[0])
				mime := inline.Get(names[1])
				if strings.HasPrefix(strings.ToLower(mime.Str), "image/") && !emit(imageSlot{value: inline.Get("data"), media: mime}) {
					return false
				}
			}
		}
		var fields []string
		switch format {
		case types.RelayFormatOpenAI:
			fields = []string{"messages", "content"}
		case types.RelayFormatOpenAIResponses:
			fields = []string{"input", "content", "output"}
		case types.RelayFormatClaude:
			fields = []string{"system", "messages", "content"}
		case types.RelayFormatGemini:
			fields = []string{"requests", "request", "contents", "parts", "systemInstruction", "system_instruction", "functionResponse", "function_response"}
		}
		for _, field := range fields {
			if !walk(node.Get(field), depth+1) {
				return false
			}
		}
		return true
	}
	walk(root, 0)
}
