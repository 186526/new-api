package image_compress

import (
	"context"
	"image"
	"math"

	"golang.org/x/image/draw"
)

const pixelBlock = 1024

type filterAxis struct {
	kernel *draw.Kernel
	ratio  float64
	size   int
}

func (axis filterAxis) span(index int) (center, spread float64, first, end int) {
	center = (float64(index)+0.5)*axis.ratio - 0.5
	spread = max(1, axis.ratio)
	radius := spread * axis.kernel.Support
	return center, spread, max(0, int(math.Ceil(center-radius))), min(axis.size, int(math.Ceil(center+radius)))
}

type sample struct {
	channels [4]float64
	weight   float64
}

func (pixel *sample) add(channels [4]float64, weight float64) {
	for index := range channels {
		pixel.channels[index] += channels[index] * weight
	}
	pixel.weight += weight
}

func (pixel sample) normalized() [4]float64 {
	for index := range pixel.channels {
		pixel.channels[index] /= pixel.weight
	}
	return pixel.channels
}

// Filter premultiplied colors in two passes, retaining only two destination
// rows. Check even inside a single sample's footprint for very wide images.
func resize(ctx context.Context, source image.Image, bounds image.Rectangle, kernel *draw.Kernel) (*image.NRGBA, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	width, height := bounds.Dx(), bounds.Dy()
	original := source.Bounds()
	horizontal := filterAxis{kernel, float64(original.Dx()) / float64(width), original.Dx()}
	vertical := filterAxis{kernel, float64(original.Dy()) / float64(height), original.Dy()}
	scaled := image.NewNRGBA(bounds)
	row := make([]sample, width)
	for y := range height {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		clear(row)
		center, spread, first, end := vertical.span(y)
		for sy := first; sy < end; sy++ {
			weight := kernel.At(math.Abs(float64(sy)-center) / spread)
			if weight == 0 {
				continue
			}
			for x := range row {
				filtered, err := horizontal.pixel(ctx, source, original.Min.Add(image.Pt(0, sy)), x)
				if err != nil {
					return nil, err
				}
				row[x].add(filtered, weight)
			}
		}
		for x, pixel := range row {
			writePixel(scaled.Pix[y*scaled.Stride+x*4:], pixel.normalized())
		}
	}
	return scaled, ctx.Err()
}

func (axis filterAxis) pixel(ctx context.Context, source image.Image, origin image.Point, index int) ([4]float64, error) {
	center, spread, first, end := axis.span(index)
	var pixel sample
	for sx := first; sx < end; sx++ {
		if (sx-first)%pixelBlock == 0 {
			if err := ctx.Err(); err != nil {
				return [4]float64{}, err
			}
		}
		weight := axis.kernel.At(math.Abs(float64(sx)-center) / spread)
		if weight != 0 {
			red, green, blue, alpha := source.At(origin.X+sx, origin.Y).RGBA()
			pixel.add([4]float64{float64(red), float64(green), float64(blue), float64(alpha)}, weight)
		}
	}
	return pixel.normalized(), nil
}

func writePixel(dst []byte, pixel [4]float64) {
	alpha := max(0, min(65535, pixel[3]))
	if alpha > 0 {
		for channel := range 3 {
			dst[channel] = uint8(math.Round(255 * max(0, min(alpha, pixel[channel])) / alpha))
		}
	}
	dst[3] = uint8(math.Round(255 * alpha / 65535))
}

func opaque(ctx context.Context, source image.Image) (bool, error) {
	bounds := source.Bounds()
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			if (x-bounds.Min.X)%pixelBlock == 0 {
				if err := ctx.Err(); err != nil {
					return false, err
				}
			}
			if _, _, _, alpha := source.At(x, y).RGBA(); alpha != 65535 {
				return false, nil
			}
		}
	}
	return true, ctx.Err()
}
