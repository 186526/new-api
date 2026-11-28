package common

import (
	"io"
	"net/http"

	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service/image_compress"
)

type ImageCompressionMeta struct {
	Processed          int                   `json:"processed"`
	SavedBytes         int                   `json:"saved_bytes"`
	ElapsedMS          int64                 `json:"elapsed_ms"`
	Skipped            int                   `json:"skipped"`
	Failed             int                   `json:"failed"`
	Reason             string                `json:"reason,omitempty"`
	OutboundStatus     string                `json:"outbound_status,omitempty"`
	OutboundCompressed *int                  `json:"outbound_compressed,omitempty"`
	OutboundSavedBytes *int                  `json:"outbound_saved_bytes,omitempty"`
	Preprocessing      *ImageCompressionWork `json:"preprocessing,omitempty"`

	evidence *image_compress.OutboundEvidence
}

type ImageCompressionWork struct {
	Processed  int   `json:"processed"`
	SavedBytes int   `json:"saved_bytes"`
	ElapsedMS  int64 `json:"elapsed_ms"`
	Skipped    int   `json:"skipped"`
	Failed     int   `json:"failed"`
}

func (meta *ImageCompressionMeta) TrackOutboundImages(before, after []byte, format types.RelayFormat) {
	meta.evidence = image_compress.TrackOutboundImages(before, after, format)
}

func (meta *ImageCompressionMeta) ObserveHTTPBody(request *http.Request, format types.RelayFormat) {
	if !meta.canObserve(format) {
		return
	}
	if request == nil || request.GetBody == nil {
		meta.OutboundStatus = "not_replayable"
		return
	}
	if request.ContentLength > image_compress.OutboundObservationLimit {
		meta.OutboundStatus = "body_too_large"
		return
	}
	body, err := request.GetBody()
	if err != nil {
		meta.OutboundStatus = "read_failed"
		return
	}
	defer body.Close()
	raw, err := io.ReadAll(io.LimitReader(body, image_compress.OutboundObservationLimit+1))
	if err != nil {
		meta.OutboundStatus = "read_failed"
		return
	}
	meta.ObserveOutboundJSON(raw, format)
}

func (meta *ImageCompressionMeta) ObserveOutboundJSON(raw []byte, format types.RelayFormat) {
	if !meta.canObserve(format) {
		return
	}
	if len(raw) > image_compress.OutboundObservationLimit {
		meta.OutboundStatus = "body_too_large"
		return
	}
	if !image_compress.IsOutboundJSONObject(raw) {
		meta.OutboundStatus = "invalid_json"
		return
	}
	confirmed := min(meta.Processed, meta.evidence.Match(raw, format))
	meta.OutboundCompressed = &confirmed
	if confirmed == meta.Processed {
		saved := meta.SavedBytes
		meta.OutboundSavedBytes = &saved
		meta.OutboundStatus = "observed"
	} else {
		meta.OutboundStatus = "unconfirmed"
	}
}

func (meta *ImageCompressionMeta) canObserve(format types.RelayFormat) bool {
	if meta == nil || meta.Reason != "compressed" || meta.Processed <= 0 {
		return false
	}
	meta.OutboundCompressed, meta.OutboundSavedBytes = nil, nil
	if meta.evidence == nil {
		meta.OutboundStatus = "body_too_large"
		return false
	}
	if !image_compress.SupportsOutboundImageObservation(format) {
		meta.OutboundStatus = "unsupported"
		return false
	}
	return true
}
