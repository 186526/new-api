package middleware

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service/redaction"
	"github.com/gin-gonic/gin"
)

const httpRestoreEmail = "alice@example.invalid"

func httpRestoreFixture(t *testing.T) (*redaction.Session, string) {
	t.Helper()
	config := redaction.DefaultConfig()
	config.RestorePII = true
	policy, err := redaction.Compile(config, 10, true)
	if err != nil {
		t.Fatal(err)
	}
	session, err := policy.NewSession(nil)
	if err != nil {
		t.Fatal(err)
	}
	placeholder, err := session.Redact(httpRestoreEmail)
	if err != nil || placeholder == httpRestoreEmail || len(placeholder) < 2 {
		t.Fatalf("failed to create placeholder: %v", err)
	}
	return session, placeholder
}

func TestHTTPResponseRedactionJSON(t *testing.T) {
	session, placeholder := httpRestoreFixture(t)
	for _, entry := range []struct {
		name, route, body string
		format            types.RelayFormat
	}{
		{"chat", "/v1/chat/completions", `{"choices":[{"message":{"role":"assistant","content":%s}}]}`, types.RelayFormatOpenAI},
		{"claude", "/v1/messages", `{"role":"assistant","content":[{"type":"text","text":%s}]}`, types.RelayFormatClaude},
		{"responses", "/v1/responses", `{"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":%s}]}]}`, types.RelayFormatOpenAIResponses},
		{"gemini", "/v1beta/models/model:generateContent", `{"candidates":[{"content":{"role":"model","parts":[{"text":%s}]}}]}`, types.RelayFormatGemini},
	} {
		t.Run(entry.name, func(t *testing.T) {
			body := strings.Replace(entry.body, "%s", httpRestoreQuote(placeholder), 1)
			engine := gin.New()
			path := entry.route
			if entry.name == "gemini" {
				path = "/v1beta/models/*path"
			}
			engine.POST(path, func(ctx *gin.Context) {
				ctx.Set(responseRedactionSessionKey, session)
				defer StartHTTPResponseRedaction(ctx, entry.format)(false)
				ctx.Header("Content-Length", strconv.Itoa(len(body)))
				ctx.Header("ETag", `"redacted"`)
				ctx.Data(http.StatusOK, "application/json", []byte(body))
			})
			recorder := httptest.NewRecorder()
			engine.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, entry.route, nil))
			if !strings.Contains(recorder.Body.String(), httpRestoreEmail) {
				t.Fatal("assistant text was not restored")
			}
			if recorder.Header().Get("Content-Length") != strconv.Itoa(recorder.Body.Len()) || recorder.Header().Get("ETag") != "" || recorder.Header().Get("Cache-Control") != "private, no-store" {
				t.Fatal("headers do not describe the restored representation")
			}
		})
	}
}

func TestHTTPResponseRedactionSSE(t *testing.T) {
	session, placeholder := httpRestoreFixture(t)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	writer := &httpResponseRedactionWriter{ResponseWriter: ctx.Writer, restorer: session.NewResponseRestorer(redaction.Responses)}
	render := func(text string) {
		t.Helper()
		if err := (common.CustomEvent{Data: text}).Render(writer); err != nil {
			t.Fatal(err)
		}
		writer.Flush()
	}
	delta := func(text string) string {
		return `data: {"type":"response.output_text.delta","output_index":0,"content_index":0,"item_id":"item","delta":` + httpRestoreQuote(text) + `}`
	}
	writer.Header().Set("Content-Type", "text/event-stream")
	if _, err := writer.WriteString(": PING\n\n"); err != nil {
		t.Fatal(err)
	}
	writer.Flush()
	if !recorder.Flushed || recorder.Body.String() != ": PING\n\n" {
		t.Fatal("heartbeat was not flushed immediately")
	}
	cut := len(placeholder) / 2
	render(delta("ordinary " + placeholder[:cut]))
	if !strings.Contains(recorder.Body.String(), `"delta":"ordinary "`) {
		t.Fatal("ordinary text did not flush before the next delta")
	}
	render(delta(placeholder[cut:]))
	if strings.Count(recorder.Body.String(), httpRestoreEmail) != 1 {
		t.Fatal("split placeholder was not restored")
	}
	for _, frame := range []string{
		`data: {"type":"response.function_call_arguments.delta","delta":` + httpRestoreQuote(placeholder) + `}`,
		"data: [DONE]",
	} {
		start := recorder.Body.Len()
		render(frame)
		if recorder.Body.String()[start:] != frame+"\n\n" {
			t.Fatal("ineligible frame was changed")
		}
	}
	start := recorder.Body.Len()
	if err := (common.CustomEvent{Data: "event: error\n"}).Render(writer); err != nil {
		t.Fatal(err)
	}
	render(delta(placeholder))
	if recorder.Body.String()[start:] != "event: error\n"+delta(placeholder)+"\n\n" {
		t.Fatal("error frame was changed")
	}
}

func TestHTTPResponseRedactionWrappedSSETail(t *testing.T) {
	for _, entry := range []struct {
		name      string
		keepAlive bool
		cleanEOF  bool
	}{
		{name: "stream writer", cleanEOF: true},
		{name: "keepalive writer", keepAlive: true, cleanEOF: true},
		{name: "failed attempt", keepAlive: true},
	} {
		t.Run(entry.name, func(t *testing.T) {
			session, placeholder := httpRestoreFixture(t)
			prefix := placeholder[:len(placeholder)/2]
			engine := gin.New()
			engine.POST("/v1/chat/completions", func(ctx *gin.Context) {
				ctx.Set(responseRedactionSessionKey, session)
				original := ctx.Writer
				finish := StartHTTPResponseRedaction(ctx, types.RelayFormatOpenAI)
				defer func() {
					finish(entry.cleanEOF)
					if ctx.Writer != original {
						t.Error("attempt cleanup did not restore original writer")
					}
				}()
				if entry.keepAlive {
					defer helper.StartStreamKeepAlive(ctx, time.Hour)()
				} else {
					helper.SetEventStreamHeaders(ctx)
				}
				payload := `{"choices":[{"index":0,"delta":{"content":` + httpRestoreQuote(prefix) + `}}]}`
				if err := helper.StringData(ctx, payload); err != nil {
					t.Fatal(err)
				}
			})
			recorder := httptest.NewRecorder()
			engine.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil))
			body := recorder.Body.String()
			if !strings.Contains(body, `"content":""`) {
				t.Fatal("delta did not buffer the known prefix")
			}
			want := 0
			if entry.cleanEOF {
				want = 1
			}
			if strings.Count(body, `"content":`+httpRestoreQuote(prefix)) != want {
				t.Fatalf("unexpected number of literal tail deltas, want %d", want)
			}
			if recorder.Result().Header.Get("Cache-Control") != "private, no-store" {
				t.Fatal("stream response allowed caching")
			}
		})
	}
}

func TestHTTPResponseRedactionBypassAndAttemptCleanup(t *testing.T) {
	session, placeholder := httpRestoreFixture(t)
	body := `{"choices":[{"message":{"role":"assistant","content":` + httpRestoreQuote(placeholder) + `}}]}`
	for _, entry := range []struct {
		name, route, encoding string
		status                int
		cleanup               bool
	}{
		{"controller error after cleanup", "/v1/chat/completions", "", http.StatusOK, true},
		{"upstream error", "/v1/chat/completions", "", http.StatusBadGateway, false},
		{"encoded", "/v1/chat/completions", "gzip", http.StatusOK, false},
		{"non-chat OpenAI route", "/v1/completions", "", http.StatusOK, false},
	} {
		t.Run(entry.name, func(t *testing.T) {
			engine := gin.New()
			engine.POST(entry.route, func(ctx *gin.Context) {
				ctx.Set(responseRedactionSessionKey, session)
				original := ctx.Writer
				cleanup := StartHTTPResponseRedaction(ctx, types.RelayFormatOpenAI)
				if entry.cleanup {
					cleanup(false)
					if ctx.Writer != original {
						t.Error("attempt cleanup did not restore original writer")
					}
				} else {
					defer cleanup(false)
				}
				ctx.Header("Content-Encoding", entry.encoding)
				ctx.Data(entry.status, "application/json", []byte(body))
			})
			recorder := httptest.NewRecorder()
			engine.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, entry.route, nil))
			if recorder.Code != entry.status || recorder.Body.String() != body {
				t.Fatal("ineligible HTTP response was changed")
			}
		})
	}
}

func httpRestoreQuote(text string) string {
	encoded, _ := json.Marshal(text)
	return string(encoded)
}
