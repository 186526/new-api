package middleware

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func systemOneGuardRouter(middleware gin.HandlerFunc, path string) *gin.Engine {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.Use(middleware)
	engine.POST(path, func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"reached": true})
	})
	return engine
}

func TestSystemOneBodyLimitRefusesByContentLength(t *testing.T) {
	engine := systemOneGuardRouter(SystemOneBodyLimit(), "/systemone/v1/systemone")

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/systemone/v1/systemone", strings.NewReader("{}"))
	request.ContentLength = 33 << 20
	engine.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusRequestEntityTooLarge, recorder.Code)
	var document map[string]any
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &document))
	assert.Contains(t, document, "detail")
}

func TestSystemOneBodyLimitLetsEveryOtherPathThrough(t *testing.T) {
	engine := systemOneGuardRouter(SystemOneBodyLimit(), "/v1/chat/completions")

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader("{}"))
	request.ContentLength = 33 << 20
	engine.ServeHTTP(recorder, request)

	assert.Equal(t, http.StatusOK, recorder.Code)
}

func TestSystemOneModelGuardRefusesAForeignModel(t *testing.T) {
	engine := systemOneGuardRouter(SystemOneModelGuard(), "/v1/systemone")

	recorder := httptest.NewRecorder()
	body := `{"model":"gemini-3.5-flash","state":"x","questions":{"q":{"type":"noul","instructions":"y"}}}`
	request := httptest.NewRequest(http.MethodPost, "/v1/systemone", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusBadRequest, recorder.Code, recorder.Body.String())
	assert.Contains(t, recorder.Body.String(), `"detail"`)
}

func TestSystemOneModelGuardLetsADecisionModelThrough(t *testing.T) {
	engine := systemOneGuardRouter(SystemOneModelGuard(), "/v1/systemone")

	recorder := httptest.NewRecorder()
	body := `{"model":"jev-latest","state":"x","questions":{"q":{"type":"noul","instructions":"y"}}}`
	request := httptest.NewRequest(http.MethodPost, "/v1/systemone", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(recorder, request)

	assert.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
}

func TestSystemOneModelGuardLeavesOtherPathsAlone(t *testing.T) {
	engine := systemOneGuardRouter(SystemOneModelGuard(), "/v1/chat/completions")

	recorder := httptest.NewRecorder()
	body := `{"model":"gemini-3.5-flash","messages":[]}`
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(recorder, request)

	assert.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
}
