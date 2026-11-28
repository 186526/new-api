package image_compress

import (
	"context"
	"encoding/binary"
	"image"
)

// For extended WebP, DecodeConfig reports the canvas, whereas Decode allocates
// using the frame header. Require agreement before calling the full decoder.
func checkWebPFrame(ctx context.Context, raw []byte, config image.Config) error {
	for offset := 12; len(raw)-offset >= 8; {
		if err := ctx.Err(); err != nil {
			return err
		}
		size := uint64(binary.LittleEndian.Uint32(raw[offset+4:]))
		padded := size + size%2
		if padded > uint64(len(raw)-offset-8) {
			break
		}
		data := raw[offset+8 : offset+8+int(size)]
		var width, height int
		switch string(raw[offset : offset+4]) {
		case "VP8 ":
			if len(data) < 10 {
				return ErrImageData
			}
			width = int(binary.LittleEndian.Uint16(data[6:]) & 0x3fff)
			height = int(binary.LittleEndian.Uint16(data[8:]) & 0x3fff)
		case "VP8L":
			if len(data) < 5 || data[0] != 0x2f {
				return ErrImageData
			}
			bits := binary.LittleEndian.Uint32(data[1:])
			width = int(bits&0x3fff) + 1
			height = int((bits>>14)&0x3fff) + 1
		default:
			offset += 8 + int(padded)
			continue
		}
		if width != config.Width || height != config.Height {
			return ErrImageData
		}
		return nil
	}
	return ErrImageData
}
