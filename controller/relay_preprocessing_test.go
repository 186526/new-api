package controller

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/setting/image_compression_setting"
	"github.com/QuantumNous/new-api/setting/reasoning"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestRelayImageCompressionOutboundBody(t *testing.T) {
	defer allowImageLimitRelayModel(t)()
	previous := image_compression_setting.Get()
	t.Cleanup(func() { image_compression_setting.Publish(previous) })
	settings := image_compression_setting.Defaults()
	settings.Enabled = true
	settings.MinDimension = 256
	settings.MaxDimension = 256
	settings.TargetFormat = "png"
	image_compression_setting.Publish(settings)

	var source bytes.Buffer
	require.NoError(t, png.Encode(&source, image.NewRGBA(image.Rect(0, 0, 640, 480))))
	original := `{"model":"mistral-large-latest", "messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"data:image/png;base64,` + base64.StdEncoding.EncodeToString(source.Bytes()) + `"}}]}]}`
	for _, passthrough := range []bool{false, true} {
		name := "compress"
		if passthrough {
			name = "passthrough"
		}
		t.Run(name, func(t *testing.T) {
			captured := make(chan []byte, 1)
			upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				body, err := io.ReadAll(request.Body)
				if err != nil {
					t.Error(err)
				}
				captured <- body
				writer.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(writer, `{"id":"local","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":1,"total_tokens":11}}`)
			}))
			defer upstream.Close()
			engine := gin.New()
			engine.Use(middleware.BodyStorageCleanup())
			engine.POST("/v1/chat/completions", func(c *gin.Context) {
				selectDistributorChannel(c, upstream.URL, 1)
				common.SetContextKey(c, constant.ContextKeyChannelSetting, dto.ChannelSettings{MaxImagesPerRequest: 1, PassThroughBodyEnabled: passthrough})
				Relay(c, types.RelayFormatOpenAI)
			})
			request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(original))
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			engine.ServeHTTP(response, request)
			require.Equal(t, http.StatusOK, response.Code, response.Body.String())
			select {
			case body := <-captured:
				if passthrough {
					require.Equal(t, original, string(body))
					return
				}
				url := gjson.GetBytes(body, "messages.0.content.0.image_url.url").String()
				_, encoded, ok := strings.Cut(url, ",")
				require.True(t, ok)
				data, err := base64.StdEncoding.DecodeString(encoded)
				require.NoError(t, err)
				bounds, err := png.DecodeConfig(bytes.NewReader(data))
				require.NoError(t, err)
				require.Equal(t, 256, bounds.Width)
				require.Equal(t, 192, bounds.Height)
			default:
				t.Fatal("request did not reach the upstream")
			}
		})
	}
}

func TestRelayImageCompressionFollowsRetryChannel(t *testing.T) {
	previous := image_compression_setting.Get()
	t.Cleanup(func() { image_compression_setting.Publish(previous) })
	settings := image_compression_setting.Defaults()
	settings.Enabled = true
	settings.MinDimension, settings.MaxDimension = 256, 256
	settings.TargetFormat = "png"
	image_compression_setting.Publish(settings)

	var source bytes.Buffer
	require.NoError(t, png.Encode(&source, image.NewRGBA(image.Rect(0, 0, 640, 480))))
	originalURL := "data:image/png;base64," + base64.StdEncoding.EncodeToString(source.Bytes())
	original := `{"model":"ws-billing", "input":[{"role":"user","content":[{"type":"input_image","image_url":"` + originalURL + `"}]}]}`
	for _, firstPassthrough := range []bool{false, true} {
		t.Run(fmt.Sprintf("first_passthrough_%t", firstPassthrough), func(t *testing.T) {
			fixture := newResponsesWSBillingTest(t, `tier("request", fixed(0.002))`, func(*websocket.Conn, *http.Request) {})
			oldRetries := common.RetryTimes
			common.RetryTimes = 1
			t.Cleanup(func() { common.RetryTimes = oldRetries })

			firstBody, fallbackBody := make(chan []byte, 1), make(chan []byte, 1)
			firstURL := imageCompressionRetryUpstream(t, firstBody, http.StatusInternalServerError,
				`{"error":{"type":"server_error","code":"server_error","message":"test retry"}}`)
			fallbackURL := imageCompressionRetryUpstream(t, fallbackBody, http.StatusOK,
				`{"id":"completed","status":"completed","usage":{"input_tokens":1000,"output_tokens":1,"total_tokens":1001}}`)
			fixture.channel.SetSetting(dto.ChannelSettings{PassThroughBodyEnabled: firstPassthrough})
			require.NoError(t, model.DB.Model(fixture.channel).Updates(map[string]any{"base_url": firstURL, "setting": fixture.channel.Setting}).Error)
			require.NoError(t, model.DB.Model(&model.Ability{}).Where("channel_id = ?", fixture.channel.Id).Update("priority", 10).Error)
			fallback := &model.Channel{
				Name: "image-compression-fallback", Type: constant.ChannelTypeOpenAI,
				Key: "local-fallback", Status: common.ChannelStatusEnabled, Models: "ws-billing", Group: "default",
				BaseURL: &fallbackURL, ModelMapping: fixture.channel.ModelMapping,
				Priority: common.GetPointer(int64(0)), Weight: common.GetPointer(uint(1)),
			}
			fallback.SetSetting(dto.ChannelSettings{PassThroughBodyEnabled: !firstPassthrough})
			require.NoError(t, model.DB.Create(fallback).Error)
			require.NoError(t, model.DB.Create(&model.Ability{ChannelId: fallback.Id, Model: "ws-billing", Group: "default", Enabled: true}).Error)
			t.Cleanup(func() {
				require.NoError(t, model.DB.Where("channel_id = ?", fallback.Id).Delete(&model.Ability{}).Error)
				require.NoError(t, model.DB.Delete(fallback).Error)
			})

			request, err := http.NewRequest(http.MethodPost, fixture.gatewayURL+"/v1/responses", strings.NewReader(original))
			require.NoError(t, err)
			request.Header.Set("Authorization", "Bearer sk-"+fixture.token.Key)
			request.Header.Set("Content-Type", "application/json")
			response, err := (&http.Client{Timeout: 3 * time.Second}).Do(request)
			require.NoError(t, err)
			body, err := io.ReadAll(response.Body)
			require.NoError(t, response.Body.Close())
			require.NoError(t, err)
			require.Equal(t, http.StatusOK, response.StatusCode, string(body))
			select {
			case <-fixture.httpDone:
			case <-time.After(3 * time.Second):
				t.Fatal("HTTP relay did not finish")
			}
			assertImageCompressionAttempt(t, firstBody, original, firstPassthrough)
			assertImageCompressionAttempt(t, fallbackBody, original, !firstPassthrough)
			fixture.closeAndWait(t)
			assertResponsesWSAccounting(t, fixture, []int{1000})
		})
	}
}

func imageCompressionRetryUpstream(t *testing.T, captured chan<- []byte, status int, response string) string {
	t.Helper()
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Error(err)
		}
		select {
		case captured <- body:
		default:
			t.Error("unexpected extra upstream attempt")
		}
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(status)
		_, _ = io.WriteString(writer, response)
	}))
	t.Cleanup(upstream.Close)
	return upstream.URL
}

func assertImageCompressionAttempt(t *testing.T, captured <-chan []byte, original string, passthrough bool) {
	t.Helper()
	select {
	case body := <-captured:
		if passthrough {
			require.Equal(t, original, string(body))
			return
		}
		_, encoded, ok := strings.Cut(gjson.GetBytes(body, "input.0.content.0.image_url").String(), ",")
		require.True(t, ok)
		data, err := base64.StdEncoding.DecodeString(encoded)
		require.NoError(t, err)
		bounds, err := png.DecodeConfig(bytes.NewReader(data))
		require.NoError(t, err)
		require.Equal(t, image.Pt(256, 192), image.Pt(bounds.Width, bounds.Height))
	default:
		t.Fatal("request did not reach the upstream")
	}
}

func TestResponsesWebSocketImageCompressionIsScopedToEachCreate(t *testing.T) {
	previous := image_compression_setting.Get()
	t.Cleanup(func() { image_compression_setting.Publish(previous) })
	settings := image_compression_setting.Defaults()
	settings.Enabled = true
	settings.MinDimension, settings.MaxDimension = 256, 256
	settings.TargetFormat = "png"
	image_compression_setting.Publish(settings)
	require.NoError(t, reasoning.LoadAutoEffortOption(`{"enabled":true,"scale_metric":"input_chars","tiers":[{"max_scale":32,"effort":"low"}],"overflow_effort":"high"}`))
	t.Cleanup(func() { require.NoError(t, reasoning.LoadAutoEffortOption("{}")) })
	outbound := make(chan []byte, 2)
	fixture := newResponsesWSBillingTest(t, `tier("request", fixed(0.002))`, func(ws *websocket.Conn, _ *http.Request) {
		for index := 0; index < 2; index++ {
			_, body, err := ws.ReadMessage()
			if err != nil {
				return
			}
			outbound <- body
			terminal := fmt.Sprintf(`{"type":"response.completed","response":{"id":"image_%d","status":"completed","usage":{"input_tokens":1000,"output_tokens":10,"total_tokens":1010}}}`, index)
			if err := ws.WriteMessage(websocket.TextMessage, []byte(terminal)); err != nil {
				return
			}
		}
		_, _, _ = ws.ReadMessage()
	})
	mapping := `{"ws-billing":"gpt-5"}`
	require.NoError(t, model.DB.Model(fixture.channel).Update("model_mapping", mapping).Error)
	for index, size := range []image.Point{{X: 640, Y: 480}, {X: 480, Y: 640}} {
		var source bytes.Buffer
		require.NoError(t, png.Encode(&source, image.NewRGBA(image.Rectangle{Max: size})))
		text := strings.Repeat("x", 2+64*index)
		payload := `{"type":"response.create","model":"ws-billing","input":[{"role":"user","content":[{"type":"input_image","image_url":"data:image/png;base64,` + base64.StdEncoding.EncodeToString(source.Bytes()) + `"},{"type":"input_text","text":"` + text + `"}]}]}`
		require.NoError(t, fixture.client.WriteMessage(websocket.TextMessage, []byte(payload)))
		require.Equal(t, "response.completed", readResponsesWSTestEvent(t, fixture.client)["type"])
		body := <-outbound
		require.Equal(t, []string{"low", "high"}[index], gjson.GetBytes(body, "reasoning.effort").String())
		_, encoded, ok := strings.Cut(gjson.GetBytes(body, "input.0.content.0.image_url").String(), ",")
		require.True(t, ok)
		data, err := base64.StdEncoding.DecodeString(encoded)
		require.NoError(t, err)
		bounds, err := png.DecodeConfig(bytes.NewReader(data))
		require.NoError(t, err)
		want := []image.Point{{X: 256, Y: 192}, {X: 192, Y: 256}}[index]
		require.Equal(t, want, image.Pt(bounds.Width, bounds.Height))
	}
	fixture.closeAndWait(t)
	assertResponsesWSAccounting(t, fixture, []int{1000, 1000})
}

func TestResponsesWebSocketImageCompressionRejectsAndRefunds(t *testing.T) {
	previous := image_compression_setting.Get()
	t.Cleanup(func() { image_compression_setting.Publish(previous) })
	settings := image_compression_setting.Defaults()
	settings.Enabled = true
	settings.FailurePolicy = "reject_request"
	image_compression_setting.Publish(settings)
	fixture := newResponsesWSBillingTest(t, `tier("request", fixed(0.002))`, func(ws *websocket.Conn, _ *http.Request) {
		_ = ws.WriteMessage(websocket.TextMessage, []byte(`{"type":"error","status":500,"message":"unexpected upstream call"}`))
	})
	payload := `{"type":"response.create","model":"ws-billing","input":[{"role":"user","content":[{"type":"input_image","image_url":"data:image/png;base64,YmFk"}]}]}`
	require.NoError(t, fixture.client.WriteMessage(websocket.TextMessage, []byte(payload)))
	response := readResponsesWSTestEvent(t, fixture.client)
	require.Equal(t, "error", response["type"])
	require.Equal(t, float64(http.StatusBadRequest), response["status"])
	require.Zero(t, fixture.connections.Load())
	require.Eventually(t, func() bool {
		var token model.Token
		return model.DB.First(&token, fixture.token.Id).Error == nil && token.RemainQuota == 3000
	}, 3*time.Second, 10*time.Millisecond)
	fixture.closeAndWait(t)
	assertResponsesWSAccounting(t, fixture, nil)
}
