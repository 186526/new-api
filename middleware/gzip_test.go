package middleware

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func gzipBytes(t *testing.T, payload string) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := gzip.NewWriter(&buffer)
	_, err := writer.Write([]byte(payload))
	require.NoError(t, err)
	require.NoError(t, writer.Close())
	return buffer.Bytes()
}

// runDecompress posts body with the given Content-Encoding and answers what the
// handler behind the middleware read, or the status the middleware settled on.
func runDecompress(t *testing.T, contentEncoding string, body []byte) (int, string) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	router := gin.New()
	readPayload := ""
	router.POST("/", DecompressRequestMiddleware(), func(c *gin.Context) {
		read, err := io.ReadAll(c.Request.Body)
		if err != nil {
			c.AbortWithStatus(http.StatusBadRequest)
			return
		}
		readPayload = string(read)
	})
	request := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	if contentEncoding != "" {
		request.Header.Set("Content-Encoding", contentEncoding)
	}
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	return recorder.Code, readPayload
}

func TestDecompressRequestMiddlewareAcceptsColumnAliases(t *testing.T) {
	payload := gzipBytes(t, `{"model":"jev-latest"}`)
	for _, spelling := range []string{"gzip", "GZIP", " x-gzip ", "X-GZip"} {
		t.Run(spelling, func(t *testing.T) {
			status, read := runDecompress(t, spelling, payload)
			assert.Equal(t, http.StatusOK, status)
			assert.Equal(t, `{"model":"jev-latest"}`, read)
		})
	}
}

func TestDecompressRequestMiddlewareRejectsUnknownCoding(t *testing.T) {
	status, _ := runDecompress(t, "snappy", []byte("payload"))
	assert.Equal(t, http.StatusUnsupportedMediaType, status)
}

func TestDecompressRequestMiddlewareRejectsStackedCodings(t *testing.T) {
	status, _ := runDecompress(t, "gzip, gzip", gzipBytes(t, "payload"))
	assert.Equal(t, http.StatusUnsupportedMediaType, status)
}

func TestDecompressRequestMiddlewareReadsIdentityAsPlain(t *testing.T) {
	status, read := runDecompress(t, "identity", []byte(`{"model":"jev-latest"}`))
	assert.Equal(t, http.StatusOK, status)
	assert.Equal(t, `{"model":"jev-latest"}`, read)
}
