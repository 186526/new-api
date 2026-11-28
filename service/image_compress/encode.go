package image_compress

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/gif"
	"image/jpeg"
	"image/png"
	"time"

	s "github.com/QuantumNous/new-api/setting/image_compression_setting"
	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
	"golang.org/x/sync/semaphore"
)

const (
	MaxInputBytes = 50 << 20
	maxPixels     = 16_000_000
	workBudget    = 160 << 20
	codecOverhead = 4 << 20
)

var (
	ErrImageLimit  = errors.New("image exceeds the compression resource limit")
	ErrImageData   = errors.New("image format or data cannot be compressed")
	ErrRemoteImage = errors.New("remote image cannot be fetched under the current security policy")
	errNotSmaller  = errors.New("compressed image is not smaller")
	workers        = semaphore.NewWeighted(2)
)

type ImageInfo struct {
	Width  int    `json:"width"`
	Height int    `json:"height"`
	Bytes  int    `json:"bytes"`
	Format string `json:"format"`
}

type Comparison struct {
	Original ImageInfo `json:"original"`
	Result   ImageInfo `json:"result"`
	Saved    int       `json:"saved_bytes"`
	Elapsed  int64     `json:"elapsed_ms"`
	Outcome  string    `json:"outcome"`
}

type encoded struct {
	data []byte
	mime string
	info Comparison
}

type PreviewResult struct {
	Comparison
	Image []byte
	MIME  string
}

func Preview(ctx context.Context, settings s.Settings, image []byte) (PreviewResult, error) {
	if err := settings.Validate(); err != nil {
		return PreviewResult{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(settings.BudgetMS)*time.Millisecond)
	defer cancel()
	if err := workers.Acquire(ctx, 1); err != nil {
		return PreviewResult{}, err
	}
	defer workers.Release(1)
	result, err := compress(ctx, settings, image, "", workBudget)
	if ctx.Err() != nil {
		return PreviewResult{}, ctx.Err()
	}
	return PreviewResult{Comparison: result.info, Image: result.data, MIME: result.mime}, err
}

func compress(ctx context.Context, settings s.Settings, raw []byte, declaredMIME string, budget int64) (encoded, error) {
	start := time.Now()
	if err := ctx.Err(); err != nil {
		return encoded{}, err
	}
	if len(raw) == 0 || len(raw) > MaxInputBytes {
		return encoded{}, ErrImageLimit
	}
	if settings.SkipAnimatedAndSVG && (declaredMIME == "image/svg+xml" || bytes.HasPrefix(bytes.TrimSpace(raw), []byte("<svg"))) {
		info := ImageInfo{Format: "svg", Bytes: len(raw)}
		return encoded{info: Comparison{Original: info, Result: info, Outcome: "preserved_animation_or_svg"}}, nil
	}
	config, format, err := image.DecodeConfig(contextReader{ctx, bytes.NewReader(raw)})
	if ctx.Err() != nil {
		return encoded{}, ctx.Err()
	}
	if err != nil {
		return encoded{}, ErrImageData
	}
	original := ImageInfo{Width: config.Width, Height: config.Height, Bytes: len(raw), Format: format}
	result := encoded{info: Comparison{Original: original, Result: original}}
	if config.Width <= 0 || config.Height <= 0 || config.Width > maxPixels/config.Height {
		return result, ErrImageLimit
	}
	if settings.SkipAnimatedAndSVG && (format == "gif" || hasAnimation(raw, format)) {
		result.info.Outcome = "preserved_animation_or_svg"
		return result, nil
	}
	if format == "jpeg" && hasExif(raw) {
		result.info.Outcome = "preserved_orientation_metadata"
		return result, nil
	}
	longest := max(config.Width, config.Height)
	if settings.ThresholdEnabled && len(raw) <= settings.MinBytesKB*1024 && longest <= settings.MinDimension {
		result.info.Outcome = "below_threshold"
		return result, nil
	}
	width, height := config.Width, config.Height
	if longest > settings.MaxDimension {
		width = max(1, width*settings.MaxDimension/longest)
		height = max(1, height*settings.MaxDimension/longest)
	}
	if format == "webp" {
		if settings.TargetFormat == "keep_original" {
			result.info.Outcome = "not_smaller"
			return result, nil
		}
		if err := checkWebPFrame(ctx, raw, config); err != nil {
			return result, err
		}
	}
	// Allow for the decoded raster and codec temporaries (GIF quantization,
	// progressive JPEG, interlaced PNG, WebP transforms), destination raster,
	// row storage and output-buffer growth, including overlap during growth.
	weight := int64(config.Width)*int64(config.Height)*24 + int64(width)*int64(height)*12 + int64(width)*64 + int64(len(raw))*4 + codecOverhead
	if weight > budget {
		return result, ErrImageLimit
	}
	out, outFormat, err := reencode(ctx, settings, raw, format, image.Rect(0, 0, width, height))
	result.info.Elapsed = time.Since(start).Milliseconds()
	if ctx.Err() != nil {
		return result, ctx.Err()
	}
	if errors.Is(err, errNotSmaller) {
		result.info.Outcome = "not_smaller"
		return result, nil
	}
	if err != nil {
		return result, err
	}
	result.data, result.mime = out, "image/"+outFormat
	result.info.Result = ImageInfo{Width: width, Height: height, Bytes: len(out), Format: outFormat}
	result.info.Saved = len(raw) - len(out)
	result.info.Outcome = "compressed"
	return result, nil
}

func reencode(ctx context.Context, settings s.Settings, raw []byte, original string, bounds image.Rectangle) ([]byte, string, error) {
	source, _, err := image.Decode(contextReader{ctx, bytes.NewReader(raw)})
	if err != nil {
		if ctx.Err() != nil {
			return nil, "", ctx.Err()
		}
		return nil, "", ErrImageData
	}
	if source.Bounds().Size() != bounds.Size() {
		filter := draw.CatmullRom
		if settings.Resampler == "bilinear" {
			filter = draw.BiLinear
		}
		scaled, err := resize(ctx, source, bounds, filter)
		if err != nil {
			return nil, "", err
		}
		source = scaled
	}
	format := settings.TargetFormat
	if format == "auto_keep_alpha" {
		format = "png"
		isOpaque, err := opaque(ctx, source)
		if err != nil {
			return nil, "", err
		}
		if isOpaque {
			format = "jpeg"
		}
	} else if format == "keep_original" {
		format = original
	}
	output := limitedOutput{ctx: ctx, limit: len(raw)}
	switch format {
	case "jpeg":
		err = jpeg.Encode(&output, source, &jpeg.Options{Quality: settings.JPEGQuality})
	case "png":
		encoder := png.Encoder{CompressionLevel: png.BestSpeed}
		err = encoder.Encode(&output, source)
	case "gif":
		err = gif.Encode(&output, source, nil)
	case "webp":
		// x/image decodes WebP but does not encode it.
		return nil, "", errNotSmaller
	default:
		return nil, "", ErrImageData
	}
	if err != nil {
		return nil, "", err
	}
	if ctx.Err() != nil {
		return nil, "", ctx.Err()
	}
	return output.Bytes(), format, nil
}

type contextReader struct {
	ctx    context.Context
	source *bytes.Reader
}

func (reader contextReader) Read(buffer []byte) (int, error) {
	if err := reader.ctx.Err(); err != nil {
		return 0, err
	}
	return reader.source.Read(buffer)
}

type limitedOutput struct {
	bytes.Buffer
	ctx   context.Context
	limit int
}

func (output *limitedOutput) Write(data []byte) (int, error) {
	if err := output.ctx.Err(); err != nil {
		return 0, err
	}
	if len(data) >= output.limit-output.Len() {
		return 0, errNotSmaller
	}
	return output.Buffer.Write(data)
}

func hasExif(raw []byte) bool {
	for index := 2; index+4 < len(raw) && raw[index] == 0xff; {
		marker := raw[index+1]
		if marker == 0xff {
			index++
			continue
		}
		if marker == 0xda || marker == 0xd9 {
			break
		}
		length := int(raw[index+2])<<8 | int(raw[index+3])
		if length < 2 || index+length+2 > len(raw) {
			break
		}
		if marker == 0xe1 && bytes.HasPrefix(raw[index+4:index+length+2], []byte("Exif\x00\x00")) {
			return true
		}
		index += length + 2
	}
	return false
}

func hasAnimation(raw []byte, format string) bool {
	switch format {
	case "png":
		// acTL precedes the first IDAT in an animated PNG.
		for index := 8; index+12 <= len(raw); {
			length := int(raw[index])<<24 | int(raw[index+1])<<16 | int(raw[index+2])<<8 | int(raw[index+3])
			kind := string(raw[index+4 : index+8])
			if kind == "acTL" {
				return true
			}
			if kind == "IDAT" || length < 0 || length > len(raw)-index-12 {
				return false
			}
			index += length + 12
		}
	case "webp":
		return len(raw) > 20 && string(raw[12:16]) == "VP8X" && raw[20]&2 != 0
	}
	return false
}
