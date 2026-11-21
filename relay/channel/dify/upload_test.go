package dify

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testPNGBase64 = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg=="

// newDifyTestContext builds the context requestOpenAI2Dify reads its request id
// from.
func newDifyTestContext(requestID string) *gin.Context {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	c.Set(common.RequestIdKey, requestID)
	return c
}

// imageRequest is a one-turn request carrying the given data URLs as image
// parts, the shape requestOpenAI2Dify reads.
func imageRequest(imageURLs ...string) dto.GeneralOpenAIRequest {
	parts := make([]dto.MediaContent, 0, len(imageURLs)+1)
	parts = append(parts, dto.MediaContent{Type: dto.ContentTypeText, Text: "describe"})
	for _, imageURL := range imageURLs {
		parts = append(parts, dto.MediaContent{
			Type:     dto.ContentTypeImageURL,
			ImageUrl: &dto.MessageImageUrl{Url: imageURL, MimeType: "image/png"},
		})
	}
	return dto.GeneralOpenAIRequest{
		Model:    "dify-bot",
		Messages: []dto.Message{{Role: "user", Content: parts}},
	}
}

func newDifyRelayInfo(baseURL string) *relaycommon.RelayInfo {
	return &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: baseURL, ApiKey: "app-test-key"}}
}

// TestOversizedImageIsRejectedBeforeDecoding lowers the upload ceiling and
// passes a data URL whose base64 is longer than it. The request must be refused
// without decoding the payload, so the test never allocates the image.
func TestOversizedImageIsRejectedBeforeDecoding(t *testing.T) {
	previous := constant.MaxFileDownloadMB
	constant.MaxFileDownloadMB = 1
	t.Cleanup(func() { constant.MaxFileDownloadMB = previous })

	service.InitHttpClient()
	var dialed bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		dialed = true
	}))
	defer server.Close()

	c := newDifyTestContext("req-oversized")
	oversized := "data:image/png;base64," + strings.Repeat("A", 2<<20)

	_, err := requestOpenAI2Dify(c, newDifyRelayInfo(server.URL), imageRequest(oversized))

	require.Error(t, err)
	assert.True(t, isUploadRejection(err), "an oversized image is refused by new-api itself, got %v", err)
	assert.Contains(t, err.Error(), "past the 1 MB upload limit")
	assert.False(t, dialed, "the upload URL must not be dialed for a request new-api refuses")
}

// TestUploadURLMustMatchTheChannelBaseURL checks the string handed to
// fmt.Sprintf: a file: scheme, a foreign host, or userinfo in the URL is
// refused before any request is made, while the configured base URL passes.
func TestUploadURLMustMatchTheChannelBaseURL(t *testing.T) {
	for _, tc := range []struct {
		name, baseURL, wantReason string
	}{
		{name: "file scheme", baseURL: "file:///tmp/dify", wantReason: "must be http or https"},
		{name: "foreign host", baseURL: "https://evil.example/api", wantReason: "is not the channel base url host"},
		{name: "empty base url", baseURL: "", wantReason: "must be http or https"},
		{name: "credentials in base url", baseURL: "https://user:pass@dify.example", wantReason: "must not carry credentials"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			uploadURL := fmt.Sprintf("%s/v1/files/upload", tc.baseURL)
			err := validateUploadURL(uploadURL, "https://dify.example")
			require.Error(t, err)
			assert.True(t, isUploadRejection(err), "want an upload rejection, got %v", err)
			assert.Contains(t, err.Error(), tc.wantReason)
		})
	}

	assert.NoError(t, validateUploadURL("https://dify.example/v1/files/upload", "https://dify.example/"))
	assert.NoError(t, validateUploadURL("http://dify.example/v1/files/upload", "http://dify.example"))
}

// TestFileSchemeBaseURLIsRejected checks the same refusal through the request
// converter: a channel whose base URL is not HTTP(S) never gets dialed.
func TestFileSchemeBaseURLIsRejected(t *testing.T) {
	c := newDifyTestContext("req-file-scheme")

	_, err := requestOpenAI2Dify(c, newDifyRelayInfo("file:///tmp/dify"), imageRequest("data:image/png;base64,"+testPNGBase64))

	require.Error(t, err)
	assert.True(t, isUploadRejection(err), "want an upload rejection, got %v", err)
	assert.Contains(t, err.Error(), "must be http or https")
}

// TestMalformedImageIsDroppedWithoutDialing keeps the uploader's old surface: a
// payload that does not decode leaves the image out of the request and logs,
// rather than failing the whole conversion.
func TestMalformedImageIsDroppedWithoutDialing(t *testing.T) {
	previous := constant.MaxFileDownloadMB
	constant.MaxFileDownloadMB = 8
	t.Cleanup(func() { constant.MaxFileDownloadMB = previous })

	var dialed bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		dialed = true
	}))
	defer server.Close()

	c := newDifyTestContext("req-malformed")
	converted, err := requestOpenAI2Dify(c, newDifyRelayInfo(server.URL), imageRequest("data:image/png;base64,not-base64!!"))

	require.NoError(t, err)
	assert.Empty(t, converted.Files)
	assert.False(t, dialed, "a payload that does not decode must not reach the upload url")
}

// TestMultiImageRequestBuildsAMultipartBody uploads two images to a stub Dify
// endpoint and checks the form it receives: one user field per file, one file
// part named image.png holding exactly the decoded bytes, and the file ids
// collected back into the converted request.
func TestMultiImageRequestBuildsAMultipartBody(t *testing.T) {
	previous := constant.MaxFileDownloadMB
	constant.MaxFileDownloadMB = 8
	t.Cleanup(func() { constant.MaxFileDownloadMB = previous })
	service.InitHttpClient()

	first := []byte("first-image-bytes")
	second := []byte("second-image-bytes")
	dataURL := func(payload []byte) string {
		return "data:image/png;base64," + base64.StdEncoding.EncodeToString(payload)
	}

	var users, filenames []string
	var contents [][]byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/v1/files/upload", r.URL.Path)
		assert.Equal(t, "Bearer app-test-key", r.Header.Get("Authorization"))
		reader, err := r.MultipartReader()
		require.NoError(t, err)
		for {
			part, err := reader.NextPart()
			if err == io.EOF {
				break
			}
			require.NoError(t, err)
			payload, err := io.ReadAll(part)
			require.NoError(t, err)
			switch part.FormName() {
			case "user":
				users = append(users, string(payload))
			case "file":
				filenames = append(filenames, part.FileName())
				contents = append(contents, payload)
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"id": fmt.Sprintf("file-%d", len(contents))})
	}))
	defer server.Close()

	requestID := "req-multi"
	c := newDifyTestContext(requestID)
	converted, err := requestOpenAI2Dify(c, newDifyRelayInfo(server.URL), imageRequest(dataURL(first), dataURL(second)))

	require.NoError(t, err)
	require.NotNil(t, converted)
	assert.Equal(t, []string{"image.png", "image.png"}, filenames)
	assert.Equal(t, [][]byte{first, second}, contents)
	assert.Equal(t, []string{"chatcmpl-" + requestID, "chatcmpl-" + requestID}, users)
	assert.Equal(t, []string{"file-1", "file-2"}, []string{
		converted.Files[0].UploadFileId,
		converted.Files[1].UploadFileId,
	})
	assert.Equal(t, "local_file", converted.Files[0].TransferMode)
	assert.Equal(t, "image", converted.Files[0].Type)
	assert.Contains(t, converted.Query, "USER: \ndescribe")
}

// TestRemoteImagesKeepTheRemoteURLPath checks the branches that send no upload:
// a remote http image is passed through as remote_url and never dialed.
func TestRemoteImagesKeepTheRemoteURLPath(t *testing.T) {
	c := newDifyTestContext("req-remote")

	converted, err := requestOpenAI2Dify(c, newDifyRelayInfo("https://dify.example"), imageRequest("https://cdn.example/cat.png"))

	require.NoError(t, err)
	require.Len(t, converted.Files, 1)
	assert.Equal(t, "remote_url", converted.Files[0].TransferMode)
	assert.Equal(t, "https://cdn.example/cat.png", converted.Files[0].URL)
}

// TestUpstreamUploadFailureDropsTheImage keeps the surface the uploader always
// had: a failing upstream is logged and the image is left out of the request,
// not turned into a client error.
func TestUpstreamUploadFailureDropsTheImage(t *testing.T) {
	service.InitHttpClient()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("upstream exploded"))
	}))
	defer server.Close()

	c := newDifyTestContext("req-upstream-failure")
	converted, err := requestOpenAI2Dify(c, newDifyRelayInfo(server.URL), imageRequest("data:image/png;base64,"+testPNGBase64))

	require.NoError(t, err)
	assert.Empty(t, converted.Files)
}

// isUploadRejection reports whether err is one of the refusals new-api makes
// itself, as opposed to any other conversion failure.
func isUploadRejection(err error) bool {
	var rejection *uploadRejection
	return errors.As(err, &rejection)
}
