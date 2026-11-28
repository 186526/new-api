package router

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
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

	compressed := httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodGet, "/systemone/v1/models", nil)
	request.Header.Set("Authorization", "Bearer "+key)
	request.Header.Set("Accept-Encoding", "gzip")
	engine.ServeHTTP(compressed, request)
	require.Equal(t, http.StatusOK, compressed.Code)
	require.Equal(t, "gzip", compressed.Result().Header.Get("Content-Encoding"))
	reader, err := gzip.NewReader(compressed.Body)
	require.NoError(t, err)
	defer reader.Close()
	payload, err := io.ReadAll(reader)
	require.NoError(t, err)
	require.JSONEq(t, recorder.Body.String(), string(payload))
}

func TestSystemOneModelsRespectTokenModelLimits(t *testing.T) {
	_, user := setupSystemOneTestToken(t)
	require.NoError(t, model.DB.AutoMigrate(&model.Channel{}, &model.Model{}, &model.Vendor{}))
	require.NoError(t, model.DB.Create(&[]model.Ability{
		{Group: "default", Model: "jev-latest", ChannelId: 1, Enabled: true},
		{Group: "default", Model: "jev-preview@thinking:on", ChannelId: 1, Enabled: true},
		{Group: "default", Model: "jev-restricted", ChannelId: 1, Enabled: true},
		{Group: "default", Model: "jev-disabled", ChannelId: 1, Enabled: false},
		{Group: "other", Model: "jev-other-group", ChannelId: 1, Enabled: true},
	}).Error)
	previousSelfUseMode := operation_setting.SelfUseModeEnabled
	operation_setting.SelfUseModeEnabled = true
	t.Cleanup(func() { operation_setting.SelfUseModeEnabled = previousSelfUseMode })

	engine := gin.New()
	SetRelayRouter(engine)
	SetSystemOneRouter(engine)

	for _, test := range []struct {
		name        string
		modelLimits string
		want        []string
	}{
		{"restricted", "jev-latest,jev-preview,jev-disabled,jev-other-group", []string{"jev-latest", "jev-preview@thinking:on"}},
		{"tools only", constant.ToolsSearchModel + "," + constant.ToolsFetchModel, []string{}},
	} {
		t.Run(test.name, func(t *testing.T) {
			key := "systemone" + strings.ReplaceAll(test.name, " ", "")
			require.NoError(t, model.DB.Create(&model.Token{
				UserId: user.Id, Key: key, Status: common.TokenStatusEnabled, ExpiredTime: -1, UnlimitedQuota: true,
				ModelLimitsEnabled: true, ModelLimits: test.modelLimits,
			}).Error)

			for _, list := range []struct{ path, field, nameField string }{
				{"/systemone/v1/models", "models", "name"},
				{"/v1/models", "data", "id"},
			} {
				recorder := httptest.NewRecorder()
				request := httptest.NewRequest(http.MethodGet, list.path, nil)
				request.Header.Set("Authorization", "Bearer "+key)
				engine.ServeHTTP(recorder, request)

				require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
				var document map[string]json.RawMessage
				require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &document))
				var items []map[string]json.RawMessage
				require.NoError(t, json.Unmarshal(document[list.field], &items))
				require.NotNil(t, items, list.path)
				names := make([]string, 0, len(items))
				for _, item := range items {
					var name string
					require.NoError(t, json.Unmarshal(item[list.nameField], &name))
					names = append(names, name)
				}
				assert.ElementsMatch(t, test.want, names, list.path)
			}

			const body = `{"model":"jev-restricted","state":"x","questions":{"q":{"type":"noul","instructions":"y"}}}`
			decision := serveSystemOneDecision(engine, "/systemone/v1/systemone", key, "", body)
			require.Equal(t, http.StatusForbidden, decision.Code, decision.Body.String())
		})
	}
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

func TestSystemOneRequestDecompressionErrors(t *testing.T) {
	engine := gin.New()
	SetRelayRouter(engine)
	SetSystemOneRouter(engine)

	for _, route := range []struct {
		path     string
		typeSafe bool
	}{
		{"/systemone/v1/systemone", true},
		{"/v1/systemone", true},
		{"/v1/chat/completions", false},
	} {
		for _, input := range []struct {
			name, encoding string
			status         int
			message        string
		}{
			{"invalid gzip", "gzip", http.StatusBadRequest, "Invalid gzip request body"},
			{"stacked coding", "gzip, br", http.StatusUnsupportedMediaType, "Stacked Content-Encoding values are not supported"},
			{"repeated coding fields", "gzip|br", http.StatusUnsupportedMediaType, "Stacked Content-Encoding values are not supported"},
			{"unknown coding", "snappy", http.StatusUnsupportedMediaType, "Unsupported Content-Encoding"},
		} {
			t.Run(route.path+"/"+input.name, func(t *testing.T) {
				request := httptest.NewRequest(http.MethodPost, route.path, strings.NewReader(`{"model":"jev-latest"}`))
				request.Header.Set("Content-Type", "application/json")
				for _, coding := range strings.Split(input.encoding, "|") {
					request.Header.Add("Content-Encoding", coding)
				}
				recorder := httptest.NewRecorder()
				engine.ServeHTTP(recorder, request)

				require.Equal(t, input.status, recorder.Code, recorder.Body.String())
				if route.typeSafe {
					message := common.MessageWithRequestId(input.message, "")
					require.JSONEq(t, `{"detail":{"error_type":"invalid_request_error","message":"`+message+`"}}`, recorder.Body.String())
					return
				}
				if input.encoding == "snappy" {
					message := common.MessageWithRequestId(common.LogText("unsupported Content-Encoding: %s", input.encoding), "")
					require.JSONEq(t, `{"error":{"code":"","message":"`+message+`","type":"new_api_error"}}`, recorder.Body.String())
					return
				}
				require.Empty(t, recorder.Body.String())
			})
		}
	}
}

func TestSystemOneResponseEncoding(t *testing.T) {
	key, _ := setupSystemOneTestToken(t)
	require.NoError(t, model.DB.AutoMigrate(&model.Channel{}))
	oldCount, oldLog, oldBatch, oldCache := constant.CountToken, common.LogConsumeEnabled, common.BatchUpdateEnabled, common.MemoryCacheEnabled
	oldRatios, err := json.Marshal(ratio_setting.GetModelRatioCopy())
	require.NoError(t, err)
	quota := operation_setting.GetQuotaSetting()
	oldPreconsume := quota.EnableFreeModelPreConsume
	t.Cleanup(func() {
		constant.CountToken, common.LogConsumeEnabled, common.BatchUpdateEnabled, common.MemoryCacheEnabled = oldCount, oldLog, oldBatch, oldCache
		quota.EnableFreeModelPreConsume = oldPreconsume
		require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(string(oldRatios)))
	})
	constant.CountToken, common.LogConsumeEnabled, common.BatchUpdateEnabled, common.MemoryCacheEnabled = false, false, false, false
	quota.EnableFreeModelPreConsume = false
	require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(`{"jev-latest":0}`))
	service.InitHttpClient()

	const successBody = `{"model":"jev-upstream","answers":{"q":{"type":"noul","noul":0.9}},"usage":{"input_tokens":11,"output_tokens":2,"cost":0},"opencode":{"account":"hidden"}}`
	const errorBody = `{"detail":[{"loc":["body","state"],"msg":"Rejected by upstream","type":"value_error"}]}`
	const rateLimitBody = `{"detail":{"error_type":"rate_limit_error","message":"Retry later"}}`
	const fixturePNG = "iVBORw0KGgoAAAANSUhEUgAAABAAAAAQCAIAAACQkWg2AAAAHElEQVR4nGP4TyJgoKcGBgbsaFQDfTUQB4aDBgDAen2fpitsVAAAAABJRU5ErkJggg=="
	largeAnswer := strings.Repeat("synthetic label ", 512)
	largeBody, err := json.Marshal(map[string]any{
		"model": "jev-upstream",
		"answers": map[string]any{"q": map[string]any{
			"type": "choice", "choice": "label", "confidence": 0.9, "probabilities": map[string]float64{"label": 0.9, largeAnswer: 0.1},
		}},
		"usage": map[string]int{"input_tokens": 11, "output_tokens": 2},
	})
	require.NoError(t, err)
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		assert.Equal(t, "/v1/systemone", request.URL.Path)
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Query().Get("fixture") {
		case "reject":
			writer.WriteHeader(http.StatusUnprocessableEntity)
			_, _ = io.WriteString(writer, errorBody)
		case "plain":
			_, _ = io.WriteString(writer, successBody)
		case "unsupported":
			writer.Header().Set("Content-Encoding", "br")
			_, _ = io.WriteString(writer, "not a JSON document")
		case "rate-limit":
			writer.Header().Set("Retry-After", "3")
			writeSystemOneGzipFixture(t, writer, http.StatusTooManyRequests, rateLimitBody)
		case "large":
			writeSystemOneGzipFixture(t, writer, http.StatusOK, string(largeBody))
		case "over-limit":
			writer.Header().Set("Content-Encoding", "gzip")
			encoded, err := gzip.NewWriterLevel(writer, gzip.BestSpeed)
			if !assert.NoError(t, err) {
				return
			}
			_, err = io.WriteString(encoded, successBody)
			if assert.NoError(t, err) {
				padding := strings.Repeat(" ", 64<<10)
				for remaining := (32 << 20) + 1 - len(successBody); remaining > 0; remaining -= len(padding) {
					if _, err := io.WriteString(encoded, padding[:min(remaining, len(padding))]); !assert.NoError(t, err) {
						break
					}
				}
			}
			assert.NoError(t, encoded.Close())
		case "multipart":
			assert.Equal(t, "application/json", request.Header.Get("Content-Type"))
			assert.Equal(t, "true", request.Header.Get("TypeSafe-Danger-Zone-Images"))
			body, err := io.ReadAll(request.Body)
			if !assert.NoError(t, err) {
				return
			}
			assert.JSONEq(t, `{"model":"jev-latest","state":"photo","questions":{"q":{"type":"noul","instructions":"Is there an object?"}},"assets":{"photo":{"type":"image","format":"png","data_base64":"`+fixturePNG+`"}}}`, string(body))
			writeSystemOneGzipFixture(t, writer, http.StatusOK, successBody)
		default:
			assert.Equal(t, "gzip", request.Header.Get("Accept-Encoding"))
			writeSystemOneGzipFixture(t, writer, http.StatusOK, successBody)
		}
	}))
	t.Cleanup(upstream.Close)
	baseURL := upstream.URL
	channel := model.Channel{Type: constant.ChannelTypeOpenAI, Key: "systemone-test-upstream", Status: common.ChannelStatusEnabled, Name: "systemone-test", BaseURL: &baseURL, Models: "jev-latest", Group: "default"}
	require.NoError(t, model.DB.Create(&channel).Error)
	require.NoError(t, model.DB.Create(&model.Ability{Group: "default", Model: "jev-latest", ChannelId: channel.Id, Enabled: true}).Error)

	engine := gin.New()
	SetRelayRouter(engine)
	SetSystemOneRouter(engine)
	const valid = `{"model":"jev-latest","state":"x","questions":{"q":{"type":"noul","instructions":"y"}}}`
	const rewritten = `{"model":"jev-latest","answers":{"q":{"type":"noul","noul":0.9}},"usage":{"input_tokens":11,"output_tokens":2}}`
	for _, path := range []string{"/systemone/v1/systemone", "/v1/systemone"} {
		for _, accept := range []string{"", "identity", "deflate", "gzip", "gzip;q=0", "br", "zstd", "gzip, deflate, br", "gzip, deflate, br, zstd"} {
			t.Run(path+"/"+accept, func(t *testing.T) {
				recorder := serveSystemOneDecision(engine, path, key, accept, valid)
				require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
				require.Empty(t, recorder.Result().Header.Get("Content-Encoding"))
				require.False(t, bytes.HasPrefix(recorder.Body.Bytes(), []byte{0x1f, 0x8b}), "gzip response without Content-Encoding")
				require.JSONEq(t, rewritten, recorder.Body.String())
			})
		}
		for _, probe := range []struct {
			name, suffix, modelKey, body string
			status                       int
		}{
			{"plain upstream", "?fixture=plain", key, valid, http.StatusOK},
			{"upstream refusal", "?fixture=reject", key, valid, http.StatusUnprocessableEntity},
			{"unsupported upstream encoding", "?fixture=unsupported", key, valid, http.StatusBadGateway},
			{"upstream response exceeds decoded limit", "?fixture=over-limit", key, valid, http.StatusBadGateway},
			{"compressed upstream rate limit", "?fixture=rate-limit", key, valid, http.StatusTooManyRequests},
			{"foreign model", "", key, strings.Replace(valid, "jev-latest", "foreign-model", 1), http.StatusBadRequest},
			{"missing key", "", "", valid, http.StatusUnauthorized},
		} {
			t.Run(path+"/"+probe.name, func(t *testing.T) {
				recorder := serveSystemOneDecision(engine, path+probe.suffix, probe.modelKey, "gzip", probe.body)
				require.Equal(t, probe.status, recorder.Code, recorder.Body.String())
				require.Empty(t, recorder.Result().Header.Get("Content-Encoding"))
				switch probe.status {
				case http.StatusOK:
					require.JSONEq(t, rewritten, recorder.Body.String())
				case http.StatusUnprocessableEntity:
					require.JSONEq(t, errorBody, recorder.Body.String())
				case http.StatusTooManyRequests:
					require.Equal(t, "3", recorder.Result().Header.Get("Retry-After"))
					require.JSONEq(t, rateLimitBody, recorder.Body.String())
				default:
					var document map[string]json.RawMessage
					require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &document))
					require.Contains(t, document, "detail")
				}
			})
		}
		t.Run(path+"/large response", func(t *testing.T) {
			for _, accept := range []string{"", "gzip", "gzip;q=0", "gzip, deflate, br, zstd"} {
				recorder := serveSystemOneDecision(engine, path+"?fixture=large", key, accept, valid)
				require.Equal(t, http.StatusOK, recorder.Code)
				require.Empty(t, recorder.Result().Header.Get("Content-Encoding"), "Accept-Encoding %q", accept)
				var document struct {
					Model   string `json:"model"`
					Answers map[string]struct {
						Probabilities map[string]float64 `json:"probabilities"`
					} `json:"answers"`
				}
				require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &document), "Accept-Encoding %q", accept)
				require.Equal(t, "jev-latest", document.Model)
				require.Equal(t, map[string]float64{"label": 0.9, largeAnswer: 0.1}, document.Answers["q"].Probabilities)
			}
		})
		t.Run(path+"/multipart", func(t *testing.T) {
			request := newSystemOneMultipartRequest(t, path+"?fixture=multipart", fixturePNG)
			request.Header.Set("Authorization", "Bearer "+key)
			request.Header.Set("Accept-Encoding", "gzip, deflate, br, zstd")
			recorder := httptest.NewRecorder()
			engine.ServeHTTP(recorder, request)
			require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
			require.Empty(t, recorder.Result().Header.Get("Content-Encoding"))
			require.JSONEq(t, rewritten, recorder.Body.String())
		})
		for _, sample := range []struct {
			name   string
			body   string
			status int
			detail string
		}{
			{"foreign model", `{"model":"gpt-5"}`, http.StatusBadRequest, "not served by the System One endpoint"},
			{"missing model", `{"state":"sample"}`, http.StatusUnprocessableEntity, `"loc":["body","model"]`},
			{"malformed request part", `{`, http.StatusUnprocessableEntity, `"loc":["body","request"]`},
		} {
			t.Run(path+"/multipart/"+sample.name, func(t *testing.T) {
				var body bytes.Buffer
				form := multipart.NewWriter(&body)
				part, err := form.CreateFormField("request")
				require.NoError(t, err)
				_, err = io.WriteString(part, sample.body)
				require.NoError(t, err)
				require.NoError(t, form.Close())
				request := httptest.NewRequest(http.MethodPost, path, &body)
				request.Header.Set("Content-Type", form.FormDataContentType())
				request.Header.Set("Authorization", "Bearer "+key)
				recorder := httptest.NewRecorder()
				engine.ServeHTTP(recorder, request)
				require.Equal(t, sample.status, recorder.Code, recorder.Body.String())
				require.Contains(t, recorder.Body.String(), sample.detail)
			})
		}
	}

	override := `{"Accept-Encoding":"gzip"}`
	require.NoError(t, model.DB.Model(&channel).Update("header_override", override).Error)
	for _, path := range []string{"/systemone/v1/systemone", "/v1/systemone"} {
		t.Run(path+"/upstream encoding override", func(t *testing.T) {
			recorder := serveSystemOneDecision(engine, path, key, "identity", valid)
			require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
			require.Empty(t, recorder.Result().Header.Get("Content-Encoding"))
			require.JSONEq(t, rewritten, recorder.Body.String())
		})
	}
}

func TestSystemOneRejectsADeclaredBodyAbove32MiB(t *testing.T) {
	key, _ := setupSystemOneTestToken(t)
	engine := gin.New()
	SetRelayRouter(engine)
	SetSystemOneRouter(engine)

	for _, path := range []string{"/systemone/v1/systemone", "/v1/systemone"} {
		t.Run(path, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"model":"jev-latest"}`))
			request.ContentLength = (32 << 20) + 1
			request.Header.Set("Authorization", "Bearer "+key)
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Accept-Encoding", "gzip")
			recorder := httptest.NewRecorder()
			engine.ServeHTTP(recorder, request)

			require.Equal(t, http.StatusRequestEntityTooLarge, recorder.Code, recorder.Body.String())
			require.Empty(t, recorder.Result().Header.Get("Content-Encoding"))
			var document struct {
				Detail struct {
					Message string `json:"message"`
				} `json:"detail"`
			}
			require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &document))
			require.Contains(t, document.Detail.Message, "32 MiB")
		})
	}
}

func writeSystemOneGzipFixture(t *testing.T, writer http.ResponseWriter, status int, body string) {
	t.Helper()
	writer.Header().Set("Content-Encoding", "gzip")
	writer.WriteHeader(status)
	encoded := gzip.NewWriter(writer)
	_, err := io.WriteString(encoded, body)
	assert.NoError(t, err)
	assert.NoError(t, encoded.Close())
}

func newSystemOneMultipartRequest(t *testing.T, path, imageBase64 string) *http.Request {
	t.Helper()
	image, err := base64.StdEncoding.DecodeString(imageBase64)
	require.NoError(t, err)
	var body bytes.Buffer
	multipartBody := multipart.NewWriter(&body)
	jsonPart, err := multipartBody.CreatePart(textproto.MIMEHeader{
		"Content-Disposition": {`form-data; name="request"`},
		"Content-Type":        {"application/json"},
	})
	require.NoError(t, err)
	_, err = io.WriteString(jsonPart, `{"model":"jev-latest","state":"photo","questions":{"q":{"type":"noul","instructions":"Is there an object?"}},"assets":{"photo":{"type":"image","part":"image"}}}`)
	require.NoError(t, err)
	imagePart, err := multipartBody.CreatePart(textproto.MIMEHeader{
		"Content-Disposition": {`form-data; name="image"; filename="fixture.png"`},
		"Content-Type":        {"image/png"},
	})
	require.NoError(t, err)
	_, err = imagePart.Write(image)
	require.NoError(t, err)
	require.NoError(t, multipartBody.Close())

	request := httptest.NewRequest(http.MethodPost, path, &body)
	request.Header.Set("Content-Type", multipartBody.FormDataContentType())
	request.Header.Set("TypeSafe-Danger-Zone-Images", "true")
	return request
}

func serveSystemOneDecision(engine *gin.Engine, path, key, accept, body string) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	if key != "" {
		request.Header.Set("Authorization", "Bearer "+key)
	}
	if accept != "" {
		request.Header.Set("Accept-Encoding", accept)
	}
	engine.ServeHTTP(recorder, request)
	return recorder
}
