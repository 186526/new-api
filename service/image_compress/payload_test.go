package image_compress

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/setting/image_compression_setting"
	"github.com/tidwall/gjson"
)

func fixture(t *testing.T) []byte {
	t.Helper()
	original := image.NewNRGBA(image.Rect(0, 0, 512, 384))
	for y := range 384 {
		for x := range 512 {
			original.SetNRGBA(x, y, color.NRGBA{R: uint8(x), B: uint8(y), A: 120})
		}
	}
	var raw bytes.Buffer
	encoder := png.Encoder{CompressionLevel: png.NoCompression}
	if err := encoder.Encode(&raw, original); err != nil {
		t.Fatal(err)
	}
	return raw.Bytes()
}

func TestRewriteImageFormats(t *testing.T) {
	payload := base64.StdEncoding.EncodeToString(fixture(t))
	for _, tc := range []struct {
		name     string
		format   types.RelayFormat
		template string
	}{
		{"chat", types.RelayFormatOpenAI, `{"messages":[{"content":[{"type":"image_url","image_url":{"url":"data:image/png;base64,IMAGE","detail":"low"}}]}], "untouched": 1e99}`},
		{"responses", types.RelayFormatOpenAIResponses, `{"input":[{"type":"function_call_output","output":[{"type":"input_image","image_url":"data:image/png;base64,IMAGE"}]}], "untouched": 1e99}`},
		{"claude", types.RelayFormatClaude, `{"messages":[{"content":[{"type":"tool_result","content":[{"type":"image","source":{"type":"base64","media_type":"image/png","data":"IMAGE"}}]}]}], "untouched": 1e99}`},
		{"gemini", types.RelayFormatGemini, `{"requests":[{"request":{"contents":[{"parts":[{"functionResponse":{"parts":[{"inlineData":{"mimeType":"image/png","data":"IMAGE"}}]}}]}]}}], "untouched": 1e99}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			settings := image_compression_setting.Defaults()
			settings.Enabled = true
			settings.MinDimension = 256
			settings.MaxDimension = 256
			raw := []byte(strings.ReplaceAll(tc.template, "IMAGE", payload))
			out, stats, err := Rewrite(context.Background(), settings, tc.format, raw, nil)
			if err != nil || stats.Processed != 1 || stats.Saved <= 0 {
				t.Fatalf("stats=%+v, err=%v", stats, err)
			}
			if !json.Valid(out) || !bytes.Contains(out, []byte(`"untouched": 1e99`)) || bytes.Contains(out, []byte(payload)) {
				t.Fatal("invalid replacement or incidental request change")
			}
			collectSlots(gjson.ParseBytes(out), tc.format, func(slot imageSlot) bool {
				_, encoded, hasURI := strings.Cut(slot.value.Str, ",")
				if !hasURI {
					encoded = slot.value.Str
				}
				decoded, err := base64.StdEncoding.DecodeString(encoded)
				if err != nil {
					t.Fatal(err)
				}
				result, err := png.Decode(bytes.NewReader(decoded))
				if err != nil {
					t.Fatal(err)
				}
				_, _, _, alpha := result.At(2, 2).RGBA()
				if result.Bounds().Dx() != 256 || result.Bounds().Dy() != 192 || alpha == 0xffff {
					t.Fatal("wrong size or alpha lost")
				}
				return true
			})
		})
	}
}
