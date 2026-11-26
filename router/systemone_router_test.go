package router

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupSystemOneTestToken creates a user and a token, and answers the key.
func setupSystemOneTestToken(t *testing.T) (string, model.User) {
	t.Helper()
	setupRelayRouterTestDB(t)

	user := model.User{
		Username: "systemone-user",
		Status:   common.UserStatusEnabled,
		Group:    "default",
		Quota:    100,
	}
	require.NoError(t, model.DB.Create(&user).Error)
	key := "systemonetestkey"
	require.NoError(t, model.DB.Create(&model.Token{
		UserId:         user.Id,
		Key:            key,
		Status:         common.TokenStatusEnabled,
		ExpiredTime:    -1,
		UnlimitedQuota: true,
	}).Error)
	return key, user
}

func TestSystemOneModelsAnswersTheContractDocument(t *testing.T) {
	key, _ := setupSystemOneTestToken(t)
	require.NoError(t, model.DB.Create(&model.Ability{
		Group: "default", Model: "jev-latest", ChannelId: 1, Enabled: true,
	}).Error)
	require.NoError(t, model.DB.Create(&model.Ability{
		Group: "default", Model: "gemini-3.5-flash", ChannelId: 1, Enabled: true,
	}).Error)

	engine := gin.New()
	SetRelayRouter(engine)
	SetSystemOneRouter(engine)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/systemone/v1/models", nil)
	request.Header.Set("Authorization", "Bearer "+key)
	engine.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	var document struct {
		Models []struct {
			Name        string `json:"name"`
			Description string `json:"description"`
			ReleaseDate string `json:"release_date"`
		} `json:"models"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &document))
	require.Len(t, document.Models, 1)
	assert.Equal(t, "jev-latest", document.Models[0].Name)
	assert.Equal(t, "", document.Models[0].ReleaseDate)
}

func TestSystemOneModelsRejectsAnUnauthorizedCallerInContractShape(t *testing.T) {
	setupSystemOneTestToken(t)

	engine := gin.New()
	SetRelayRouter(engine)
	SetSystemOneRouter(engine)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/systemone/v1/models", nil)
	engine.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusUnauthorized, recorder.Code)
	var document struct {
		Detail struct {
			ErrorType string `json:"error_type"`
			Message   string `json:"message"`
		} `json:"detail"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &document), recorder.Body.String())
	assert.Equal(t, "authentication_error", document.Detail.ErrorType)
}

func TestSystemOneSingleModelIsNotFound(t *testing.T) {
	key, _ := setupSystemOneTestToken(t)

	engine := gin.New()
	SetRelayRouter(engine)
	SetSystemOneRouter(engine)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/systemone/v1/models/jev-latest", nil)
	request.Header.Set("Authorization", "Bearer "+key)
	engine.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusNotFound, recorder.Code, recorder.Body.String())
	var document map[string]any
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &document))
	assert.Contains(t, document, "detail")
}

func TestSystemOneRejectsAForeignModelBeforeChannelSelection(t *testing.T) {
	key, _ := setupSystemOneTestToken(t)

	engine := gin.New()
	SetRelayRouter(engine)
	SetSystemOneRouter(engine)

	recorder := httptest.NewRecorder()
	body := `{"model":"gemini-3.5-flash","state":"x","questions":{"q":{"type":"noul","instructions":"y"}}}`
	request := httptest.NewRequest(http.MethodPost, "/systemone/v1/systemone", strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+key)
	request.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusBadRequest, recorder.Code, recorder.Body.String())
	var document map[string]any
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &document))
	assert.Contains(t, document, "detail")
}

// The legacy alias lives on the /v1 relay group, whose channel selection runs
// as a group middleware. A foreign model must be refused before that runs, or
// the caller reads 500 get_channel_failed for a request that can never succeed.
func TestLegacySystemOneAliasRejectsAForeignModelBeforeChannelSelection(t *testing.T) {
	key, _ := setupSystemOneTestToken(t)

	engine := gin.New()
	SetRelayRouter(engine)

	body := `{"model":"gemini-3.5-flash","state":"x","questions":{"q":{"type":"noul","instructions":"y"}}}`
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v1/systemone", strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+key)
	request.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusBadRequest, recorder.Code, recorder.Body.String())
	var document map[string]any
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &document))
	assert.Contains(t, document, "detail")
}

// The alias answers in the TypeSafe family even when authentication fails, so a
// client reads one shape whether or not the key is valid.
func TestLegacySystemOneAliasAnswersAnUnauthorizedCallerInContractShape(t *testing.T) {
	setupSystemOneTestToken(t)

	engine := gin.New()
	SetRelayRouter(engine)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v1/systemone", strings.NewReader(`{"model":"jev-latest"}`))
	request.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusUnauthorized, recorder.Code, recorder.Body.String())
	assert.Contains(t, recorder.Body.String(), `"detail"`)
}
