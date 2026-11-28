package controller

import (
	"bytes"
	"encoding/json"
	"image"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/service/image_compress"
	setting "github.com/QuantumNous/new-api/setting/image_compression_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestPreviewImageCompression(t *testing.T) {
	settings := setting.Defaults()
	settings.MinDimension = 256
	settings.MaxDimension = 256
	draft, err := json.Marshal(settings)
	require.NoError(t, err)

	var source bytes.Buffer
	require.NoError(t, png.Encode(&source, image.NewNRGBA(image.Rect(0, 0, 512, 384))))

	router := gin.New()
	router.POST("/preview", PreviewImageCompression)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, newImageCompressionPreviewRequest(t, &source, string(draft)))
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.Equal(t, "no-store", response.Header().Get("Cache-Control"))
	var result struct {
		Success bool `json:"success"`
		Data    struct {
			image_compress.Comparison
			ImageBase64 []byte `json:"image_base64"`
			ImageMIME   string `json:"image_mime"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &result))
	require.True(t, result.Success)
	require.Equal(t, 512, result.Data.Original.Width)
	require.Equal(t, 256, result.Data.Result.Width)
	require.Equal(t, 192, result.Data.Result.Height)
	require.Positive(t, result.Data.Saved)
	require.Equal(t, "image/png", result.Data.ImageMIME)
	require.Len(t, result.Data.ImageBase64, result.Data.Result.Bytes)
	decoded, err := png.Decode(bytes.NewReader(result.Data.ImageBase64))
	require.NoError(t, err)
	require.Equal(t, image.Rect(0, 0, 256, 192), decoded.Bounds())
}

func TestPreviewImageCompressionBoundsResponseImage(t *testing.T) {
	for _, size := range []int{2 << 20, (2 << 20) + 1} {
		data := compressionPreviewData(image_compress.PreviewResult{
			Comparison: image_compress.Comparison{
				Outcome: "compressed",
				Result:  image_compress.ImageInfo{Bytes: size},
			},
			Image: make([]byte, size),
			MIME:  "image/png",
		})
		require.Equal(t, size, data.Result.Bytes)
		if size == 2<<20 {
			require.Len(t, data.ImageBase64, size)
			require.Equal(t, "image/png", data.ImageMIME)
		} else {
			require.Empty(t, data.ImageBase64)
			require.Empty(t, data.ImageMIME)
		}
	}
}

func TestPreviewImageCompressionRejectsOversizedDraft(t *testing.T) {
	router := gin.New()
	router.POST("/preview", PreviewImageCompression)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, newImageCompressionPreviewRequest(t, strings.NewReader("image"), strings.Repeat("x", maxPreviewSettingsBytes+1)))
	require.Equal(t, http.StatusRequestEntityTooLarge, response.Code, response.Body.String())
	require.Equal(t, "no-store", response.Header().Get("Cache-Control"))
}

func newImageCompressionPreviewRequest(t *testing.T, source io.Reader, draft string) *http.Request {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("image", "sample.png")
	require.NoError(t, err)
	_, err = io.Copy(part, source)
	require.NoError(t, err)
	require.NoError(t, writer.WriteField("settings", draft))
	require.NoError(t, writer.Close())
	request := httptest.NewRequest(http.MethodPost, "/preview", &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	return request
}
