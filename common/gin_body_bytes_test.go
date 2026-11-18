package common

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetRequestBodyBytesReportsWhatWasRead(t *testing.T) {
	gin.SetMode(gin.TestMode)
	payload := bytes.Repeat([]byte("a"), 200<<10)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	// A streamed request is chunked, so the client sends no Content-Length.
	c.Request = httptest.NewRequest(http.MethodPost, "http://example.com/v1/chat/completions", bytes.NewReader(payload))
	c.Request.ContentLength = -1

	assert.EqualValues(t, 0, GetRequestBodyBytes(c), "an unread body has no size")

	storage, err := GetBodyStorage(c)
	require.NoError(t, err)
	defer storage.Close()

	assert.EqualValues(t, len(payload), GetRequestBodyBytes(c))
}
