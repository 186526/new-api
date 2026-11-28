package image_compress

import (
	"context"
	"errors"
	"image"
	"image/color"
	"testing"

	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/setting/image_compression_setting"
	"golang.org/x/image/draw"
)

type cancelOnSample struct {
	image.Image
	cancel context.CancelFunc
	reads  int
}

func (source *cancelOnSample) At(x, y int) color.Color {
	source.reads++
	if source.reads == 8 {
		source.cancel()
	}
	return source.Image.At(x, y)
}

func TestResizeCancelsInsideSinglePixelFootprint(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	source := &cancelOnSample{Image: image.NewNRGBA(image.Rect(0, 0, 16384, 1)), cancel: cancel}
	out, err := resize(ctx, source, image.Rect(0, 0, 1, 1), draw.CatmullRom)
	if out != nil || !errors.Is(err, context.Canceled) || source.reads > pixelBlock+8 {
		t.Fatalf("resize outcome: output=%v, err=%v, samples=%d", out, err, source.reads)
	}
}

func TestRewriteCancellationDiscardsStagedPatches(t *testing.T) {
	const raw = `{"messages":[{"content":[{"type":"image_url","image_url":{"url":"https://example.invalid/a"}},{"type":"image_url","image_url":{"url":"https://example.invalid/b"}}]}]}`
	payload := fixture(t)
	for _, policy := range []string{"keep_original", "reject_request"} {
		t.Run(policy, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			settings := image_compression_setting.Defaults()
			settings.Enabled, settings.CompressRemoteURLs = true, true
			settings.MinDimension, settings.MaxDimension = 256, 256
			settings.FailurePolicy = policy
			calls := 0
			out, stats, err := Rewrite(ctx, settings, types.RelayFormatOpenAI, []byte(raw), func(context.Context, string, int64) ([]byte, string, error) {
				calls++
				if calls == 2 {
					cancel()
				}
				return payload, "image/png", nil
			})
			var expected error
			if policy == "reject_request" {
				expected = context.Canceled
			}
			if calls != 2 || out != nil || !errors.Is(err, expected) || stats.Processed != 0 || stats.Saved != 0 || stats.Skipped != 1 {
				t.Fatalf("rewrite outcome: calls=%d stats=%+v err=%v", calls, stats, err)
			}
		})
	}
}

func TestRewriteRejectsOversizedDownload(t *testing.T) {
	const raw = `{"input":[{"type":"input_image","image_url":"https://example.invalid/image"}]}`
	settings := image_compression_setting.Defaults()
	settings.Enabled, settings.CompressRemoteURLs = true, true
	settings.RemoteMaxMB, settings.FailurePolicy = 1, "reject_request"
	out, _, err := Rewrite(context.Background(), settings, types.RelayFormatOpenAIResponses, []byte(raw), func(_ context.Context, _ string, limit int64) ([]byte, string, error) {
		if limit != 1<<20 {
			t.Fatalf("download limit = %d", limit)
		}
		return make([]byte, limit+1), "image/png", nil
	})
	if out != nil || !errors.Is(err, ErrImageLimit) {
		t.Fatalf("expected download rejection, got %v", err)
	}
}

func TestCodecWorkAndRebuiltBodyUseRemainingBudget(t *testing.T) {
	settings := image_compression_setting.Defaults()
	settings.MinDimension, settings.MaxDimension = 256, 256
	result, err := compress(context.Background(), settings, fixture(t), "", codecOverhead)
	if len(result.data) != 0 || !errors.Is(err, ErrImageLimit) {
		t.Fatalf("expected codec admission failure, got %v", err)
	}
	out, err := applyPatches(context.Background(), []byte(`"X"`), []replacement{{1, 2, []byte("test")}}, 5)
	if out != nil || !errors.Is(err, ErrImageLimit) {
		t.Fatalf("expected rebuilt body admission failure, got %v", err)
	}
}
