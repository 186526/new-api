package controller

import (
	"errors"
	"io"
	"net/http"

	"github.com/QuantumNous/new-api/service/image_compress"
	setting "github.com/QuantumNous/new-api/setting/image_compression_setting"
	"github.com/gin-gonic/gin"
)

const (
	maxPreviewImageBytes    = 16 << 20
	maxPreviewSettingsBytes = 80 << 10
	maxPreviewResultBytes   = 2 << 20
)

type imageCompressionPreviewData struct {
	image_compress.Comparison
	ImageBase64 []byte `json:"image_base64,omitempty"`
	ImageMIME   string `json:"image_mime,omitempty"`
}

func compressionPreviewData(result image_compress.PreviewResult) imageCompressionPreviewData {
	data := imageCompressionPreviewData{Comparison: result.Comparison}
	if result.Outcome == "compressed" && len(result.Image) > 0 && len(result.Image) <= maxPreviewResultBytes {
		data.ImageBase64 = result.Image
		data.ImageMIME = result.MIME
	}
	return data
}

func PreviewImageCompression(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxPreviewImageBytes+maxPreviewSettingsBytes+(32<<10))
	image, draft, err := readCompressionPreview(c.Request)
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) || errors.Is(err, image_compress.ErrImageLimit) {
			c.JSON(http.StatusRequestEntityTooLarge, gin.H{"success": false, "message": "Image preview exceeds the size limit"})
			return
		}
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "Choose one image and provide draft settings"})
		return
	}
	settings, err := setting.DecodeDraft(draft)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": err.Error()})
		return
	}
	result, err := image_compress.Preview(c.Request.Context(), settings, image)
	if err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"success": false, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": compressionPreviewData(result)})
}

func readCompressionPreview(req *http.Request) (image, draft []byte, err error) {
	reader, err := req.MultipartReader()
	if err != nil {
		return nil, nil, err
	}
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, nil, err
		}
		switch part.FormName() {
		case "image":
			if image != nil {
				return nil, nil, image_compress.ErrImageData
			}
			image, err = io.ReadAll(io.LimitReader(part, maxPreviewImageBytes+1))
			if len(image) > maxPreviewImageBytes {
				return nil, nil, image_compress.ErrImageLimit
			}
		case "settings":
			if draft != nil {
				return nil, nil, image_compress.ErrImageData
			}
			draft, err = io.ReadAll(io.LimitReader(part, maxPreviewSettingsBytes+1))
			if len(draft) > maxPreviewSettingsBytes {
				return nil, nil, image_compress.ErrImageLimit
			}
		default:
			return nil, nil, image_compress.ErrImageData
		}
		if err != nil {
			return nil, nil, err
		}
	}
	if len(image) == 0 || len(draft) == 0 {
		return nil, nil, image_compress.ErrImageData
	}
	return image, draft, nil
}
