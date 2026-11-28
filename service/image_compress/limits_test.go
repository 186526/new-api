package image_compress

import (
	"context"
	"errors"
	"testing"

	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/setting/image_compression_setting"
)

func TestRewriteReusesRemoteImageWithoutRemovingOccurrences(t *testing.T) {
	const raw = `{"messages":[{"content":[{"type":"image_url","image_url":{"url":"https://images.example.com/fixture.png"}},{"type":"image_url","image_url":{"url":"https://images.example.com/fixture.png"}}]}]}`
	settings := image_compression_setting.Defaults()
	settings.Enabled = true
	settings.MinDimension, settings.MaxDimension = 256, 256
	payload := fixture(t)
	downloads := 0
	fetch := func(_ context.Context, _ string, _ int64) ([]byte, string, error) {
		downloads++
		return payload, "image/png", nil
	}
	_, stats, err := Rewrite(context.Background(), settings, types.RelayFormatOpenAI, []byte(raw), fetch)
	if err != nil || stats.Processed != 0 || downloads != 0 {
		t.Fatalf("off: downloads=%d, stats=%+v, err=%v", downloads, stats, err)
	}
	settings.CompressRemoteURLs = true
	_, stats, err = Rewrite(context.Background(), settings, types.RelayFormatOpenAI, []byte(raw), fetch)
	if err != nil || stats.Processed != 2 || downloads != 1 {
		t.Fatalf("on: downloads=%d, stats=%+v, err=%v", downloads, stats, err)
	}
	settings.MaxProcessImages = 1
	settings.FailurePolicy = "reject_request"
	rewritten, _, err := Rewrite(context.Background(), settings, types.RelayFormatOpenAI, []byte(raw), fetch)
	if !errors.Is(err, ErrImageLimit) || rewritten != nil {
		t.Fatalf("strict limit must reject entire request; err=%v", err)
	}
}

func TestPreviewCanAttemptCompressionBelowThreshold(t *testing.T) {
	payload := fixture(t)
	settings := image_compression_setting.Defaults()
	settings.MinBytesKB, settings.MinDimension, settings.MaxDimension = 51200, 16384, 256
	preview, err := Preview(context.Background(), settings, payload)
	if err != nil || preview.Outcome != "below_threshold" {
		t.Fatalf("enabled threshold: outcome=%s, err=%v", preview.Outcome, err)
	}
	settings.ThresholdEnabled = false
	preview, err = Preview(context.Background(), settings, payload)
	if err != nil || preview.Outcome != "compressed" || preview.Result.Width != 256 {
		t.Fatalf("disabled threshold: preview=%+v, err=%v", preview, err)
	}
}

func TestPreviewUsesDraftThresholdAndReportsActualBytes(t *testing.T) {
	payload := fixture(t)
	settings := image_compression_setting.Defaults()
	settings.MinDimension, settings.MaxDimension = 256, 256
	preview, err := Preview(context.Background(), settings, payload)
	if err != nil || preview.Result.Width != 256 || preview.Saved != len(payload)-preview.Result.Bytes || preview.Saved <= 0 {
		t.Fatalf("preview=%+v, err=%v", preview, err)
	}
}
