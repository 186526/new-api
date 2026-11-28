package middleware

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestTokenAuthRedactsBeforeRequestStorage(t *testing.T) {
	require.NoError(t, i18n.Init())
	setupDashboardAuthMiddlewareTest(t)
	t.Setenv("LOG_SQL_DSN", "")
	require.NoError(t, model.InitLogDB())
	require.NoError(t, model.DB.AutoMigrate(&model.Token{}))
	user := createMiddlewarePATUser(t, "redaction-http", "unused-pat")
	token := model.Token{UserId: user.Id, Key: "syntheticfixturetoken", Status: common.TokenStatusEnabled, ExpiredTime: -1, UnlimitedQuota: true, RedactionEnabled: true}
	require.NoError(t, model.DB.Create(&token).Error)
	gateway := gin.New()
	gateway.Use(DecompressRequestMiddleware(), BodyStorageCleanup(), TokenAuth())
	gateway.POST("/v1/chat/completions", func(ctx *gin.Context) {
		require.Empty(t, ctx.Request.Header.Get("X-Client-Note"))
		require.Empty(t, ctx.Request.Header.Get("Authorization"))
		require.Empty(t, ctx.Request.Header.Get("Accept"))
		require.Equal(t, "application/json", ctx.Request.Header.Get("Content-Type"))
		body, err := common.GetBodyStorage(ctx)
		require.NoError(t, err)
		payload, err := io.ReadAll(body)
		require.NoError(t, err)
		ctx.Data(http.StatusOK, "application/json", payload)
	})
	const address = "sample-redaction@example.test"
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"fixture","messages":[{"role":"user","content":"`+address+`"}],"vendor_field":true}`))
	request.Header.Set("Content-Type", "application/json; note="+address)
	request.Header.Set("Authorization", "Bearer sk-"+token.Key)
	request.Header.Set("X-Client-Note", address)
	request.Header.Set("Accept", "application/json; note="+address)
	response := httptest.NewRecorder()
	gateway.ServeHTTP(response, request)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.NotContains(t, response.Body.String(), address)
	require.Contains(t, response.Body.String(), "EMAIL_")
	require.Contains(t, response.Body.String(), `"vendor_field":true`)

	for _, test := range []struct {
		path   string
		body   string
		status int
	}{
		{"/v1/responses", `{"input":[{"role":"user","content":[{"type":"input_text","text":"` + address + `"}]}]}`, http.StatusOK},
		{"/v1/responses/compact", `{"model":"fixture","input":"` + address + `"}`, http.StatusOK},
		{"/v1/tools/search", `{"query":"` + address + `"}`, http.StatusOK},
		{"/v1/alpha/search", `{"commands":{"search_query":[{"q":"` + address + `"}]}}`, http.StatusOK},
		{"/moonshot.service/v1/search", `{"text_query":"` + address + `"}`, http.StatusOK},
		{"/v1/tools/fetch", `{"url":"https://example.test/?owner=sample-redaction%40example.test"}`, http.StatusBadRequest},
		{"/moonshot.service/v1/fetch", `{"url":"https://example.test/path"}`, http.StatusOK},
	} {
		t.Run(test.path, func(t *testing.T) {
			gateway.POST(test.path, func(ctx *gin.Context) {
				storage, err := common.GetBodyStorage(ctx)
				require.NoError(t, err)
				payload, err := storage.Bytes()
				require.NoError(t, err)
				ctx.Data(http.StatusOK, "application/json", payload)
			})
			req := httptest.NewRequest(http.MethodPost, test.path, strings.NewReader(test.body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", "Bearer sk-"+token.Key)
			res := httptest.NewRecorder()
			gateway.ServeHTTP(res, req)
			require.Equal(t, test.status, res.Code, res.Body.String())
			require.NotContains(t, res.Body.String(), address)
			if test.status == http.StatusOK && strings.Contains(test.body, address) {
				require.Contains(t, res.Body.String(), "EMAIL_")
			}
		})
	}

	for _, test := range []struct{ method, path, upgrade string }{
		{http.MethodGet, "/v1/realtime", "websocket"},
		{http.MethodGet, "/v1/responses/compact", ""},
		{http.MethodGet, "/v1/responses", ""},
		{http.MethodPost, "/v1/responses/compact/unknown", ""},
	} {
		t.Run(test.method+" "+test.path, func(t *testing.T) {
			method, path := test.method, test.path
			gateway.Handle(method, path, func(ctx *gin.Context) { t.Error("unsupported relay reached its handler") })
			request := httptest.NewRequest(method, path, nil)
			body := &redactionReadProbe{}
			request.Body = body
			if test.upgrade != "" {
				request.Header.Set("Upgrade", test.upgrade)
			}
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Authorization", "Bearer sk-"+token.Key)
			response := httptest.NewRecorder()
			gateway.ServeHTTP(response, request)
			require.Equal(t, http.StatusForbidden, response.Code)
			require.False(t, body.read)
		})
	}
}

type redactionReadProbe struct{ read bool }

func (probe *redactionReadProbe) Read([]byte) (int, error) {
	probe.read = true
	return 0, io.EOF
}

func (*redactionReadProbe) Close() error { return nil }
