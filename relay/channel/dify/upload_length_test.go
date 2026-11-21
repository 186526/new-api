package dify

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/service"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// uploadObserved is one upload as the stub Dify endpoint read it.
type uploadObserved struct {
	declared  int64
	chunked   bool
	read      int64
	fileBytes int
}

// countingBody counts the bytes the handler reads through it.
type countingBody struct {
	io.ReadCloser
	read int
}

func (b *countingBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	b.read += n
	return n, err
}

// recordUploads serves the Dify upload endpoint and records what each upload
// declared and what it delivered.
func recordUploads(t *testing.T) (*httptest.Server, *[]uploadObserved) {
	t.Helper()
	observed := make([]uploadObserved, 0, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := &countingBody{ReadCloser: r.Body}
		r.Body = body
		reader, err := r.MultipartReader()
		require.NoError(t, err)
		files := 0
		for {
			part, err := reader.NextPart()
			if err == io.EOF {
				break
			}
			require.NoError(t, err)
			payload, err := io.ReadAll(part)
			require.NoError(t, err)
			if part.FormName() == "file" {
				files += len(payload)
			}
		}
		observed = append(observed, uploadObserved{
			declared:  r.ContentLength,
			chunked:   len(r.TransferEncoding) > 0,
			read:      int64(body.read),
			fileBytes: files,
		})
		_, _ = fmt.Fprintf(w, `{"id":"file-%d"}`, len(observed))
	}))
	return server, &observed
}

// TestUploadDeclaresTheLengthItStreams uploads two images and requires each
// upload to declare a Content-Length that is exactly the bytes the endpoint
// reads, rather than arriving chunked. The body is streamed through a pipe, so
// the declared length has to come from the form envelope plus the decoded
// count, not from a buffered body.
func TestUploadDeclaresTheLengthItStreams(t *testing.T) {
	previous := constant.MaxFileDownloadMB
	constant.MaxFileDownloadMB = 8
	t.Cleanup(func() { constant.MaxFileDownloadMB = previous })
	service.InitHttpClient()

	first := []byte("first-image-bytes")
	second := []byte(strings.Repeat("second-image-bytes", 32))
	dataURL := func(payload []byte) string {
		return "data:image/png;base64," + base64.StdEncoding.EncodeToString(payload)
	}

	server, observed := recordUploads(t)
	defer server.Close()

	c := newDifyTestContext("req-declared-length")
	_, err := requestOpenAI2Dify(c, newDifyRelayInfo(server.URL), imageRequest(dataURL(first), dataURL(second)))

	require.NoError(t, err)
	require.Len(t, *observed, 2)
	for i, want := range [][]byte{first, second} {
		upload := (*observed)[i]
		assert.False(t, upload.chunked, "upload %d must declare a length instead of streaming chunked", i)
		assert.Greater(t, upload.declared, int64(0), "upload %d declared no length", i)
		assert.Equal(t, upload.read, upload.declared, "upload %d declared %d bytes but the endpoint read %d", i, upload.declared, upload.read)
		assert.Equal(t, len(want), upload.fileBytes, "upload %d carried the wrong image bytes", i)
	}
}

// TestUploadLengthCountsAWrappedPayloadAlone checks the count the declared
// length is built from. base64.StdEncoding.DecodedLen only divides the encoded
// length, so it counts the CRLF separators of a wrapped payload as if they
// decoded, while the decoder skips them: a length derived from that estimate is
// longer than the body and the upload fails once the pipe ends early. The
// payload here is wrapped every 16 characters, which makes the estimate 342
// bytes for a 304-byte payload.
func TestUploadLengthCountsAWrappedPayloadAlone(t *testing.T) {
	previous := constant.MaxFileDownloadMB
	constant.MaxFileDownloadMB = 8
	t.Cleanup(func() { constant.MaxFileDownloadMB = previous })
	service.InitHttpClient()

	payload := []byte(strings.Repeat("wrapped-image-bytes", 16))
	encoded := base64.StdEncoding.EncodeToString(payload)
	var wrapped strings.Builder
	for len(encoded) > 16 {
		wrapped.WriteString(encoded[:16] + "\r\n")
		encoded = encoded[16:]
	}
	wrapped.WriteString(encoded)

	server, observed := recordUploads(t)
	defer server.Close()

	c := newDifyTestContext("req-wrapped-length")
	_, err := requestOpenAI2Dify(c, newDifyRelayInfo(server.URL), imageRequest("data:image/png;base64,"+wrapped.String()))

	require.NoError(t, err)
	require.Len(t, *observed, 1)
	upload := (*observed)[0]
	assert.False(t, upload.chunked)
	assert.Equal(t, upload.read, upload.declared, "the line breaks must not be counted into the declared length")
	assert.Equal(t, len(payload), upload.fileBytes)
}

// TestChunkedUploadWithoutADeclaredLengthStillWorks keeps the form usable when
// the caller cannot know the length: the same envelope streamed through a
// request with no declared length must still parse as the same multipart form
// at the endpoint. Nothing in the upload path depends on ContentLength being
// present for the body to be valid.
func TestChunkedUploadWithoutADeclaredLengthStillWorks(t *testing.T) {
	service.InitHttpClient()

	payload := []byte("chunked-image-bytes")
	encoded := base64.StdEncoding.EncodeToString(payload)
	form, err := newUploadForm("chatcmpl-req-chunked", "image/png")
	require.NoError(t, err)

	var sent bytes.Buffer
	require.NoError(t, form.stream(&sent, encoded))

	server, observed := recordUploads(t)
	defer server.Close()

	body := struct{ io.Reader }{&sent}
	bodyLength := int64(sent.Len())
	req, err := http.NewRequest(http.MethodPost, server.URL+"/v1/files/upload", body)
	require.NoError(t, err)
	req.ContentLength = -1
	req.Header.Set("Content-Type", form.contentType())

	resp, err := service.GetHttpClient().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	_, err = io.Copy(io.Discard, resp.Body)
	require.NoError(t, err)

	require.Len(t, *observed, 1)
	upload := (*observed)[0]
	assert.True(t, upload.chunked, "a body without a declared length goes out chunked")
	assert.Equal(t, bodyLength, upload.read)
	assert.Equal(t, len(payload), upload.fileBytes)
}
