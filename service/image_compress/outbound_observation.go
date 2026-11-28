package image_compress

import (
	"crypto/sha256"
	"strings"

	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/tidwall/gjson"
)

// OutboundObservationLimit bounds the extra inspection independently of the
// compression work budget. Larger requests still relay normally.
const OutboundObservationLimit = 8 << 20

type imageDigest [sha256.Size]byte

type OutboundEvidence struct {
	replacements map[imageDigest]int
	baseline     map[imageDigest]int
}

func TrackOutboundImages(before, after []byte, format types.RelayFormat) *OutboundEvidence {
	if len(before) > OutboundObservationLimit || len(after) > OutboundObservationLimit {
		return nil
	}
	baseline := outboundImageCounts(before, format)
	replacements := outboundImageCounts(after, format)
	for digest, count := range replacements {
		if count <= baseline[digest] {
			delete(replacements, digest)
		}
	}
	for digest := range baseline {
		if replacements[digest] == 0 {
			delete(baseline, digest)
		}
	}
	return &OutboundEvidence{replacements: replacements, baseline: baseline}
}

// Match counts confirmed compressed occurrences. If an identical image was
// already present before the rewrite, reserve its occurrences before crediting
// any replacements. This deliberately undercounts ambiguous final bodies.
func (evidence *OutboundEvidence) Match(body []byte, format types.RelayFormat) int {
	if evidence == nil {
		return 0
	}
	sent := outboundImageCounts(body, format)
	confirmed := 0
	for digest, prepared := range evidence.replacements {
		baseline := evidence.baseline[digest]
		confirmed += max(0, min(prepared, sent[digest])-baseline)
	}
	return confirmed
}

func outboundImageCounts(body []byte, format types.RelayFormat) map[imageDigest]int {
	counts := make(map[imageDigest]int)
	collectSlots(gjson.ParseBytes(body), format, func(slot imageSlot) bool {
		if slot.remote() {
			return true
		}
		payload := slot.value.Str
		if slot.style == "url" {
			header, data, ok := strings.Cut(payload, ",")
			if !ok || !strings.HasSuffix(header, ";base64") {
				return true
			}
			payload = data
		}
		if payload != "" {
			counts[sha256.Sum256([]byte(payload))]++
		}
		return true
	})
	return counts
}

func SupportsOutboundImageObservation(format types.RelayFormat) bool {
	switch format {
	case types.RelayFormatOpenAI, types.RelayFormatOpenAIResponses, types.RelayFormatClaude, types.RelayFormatGemini:
		return true
	default:
		return false
	}
}

func IsOutboundJSONObject(body []byte) bool {
	return gjson.ValidBytes(body) && gjson.ParseBytes(body).IsObject()
}
