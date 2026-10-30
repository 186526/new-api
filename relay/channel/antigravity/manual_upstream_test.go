package antigravity

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/service"
)

func TestManualUpstream(t *testing.T) {
	if os.Getenv("AGY_MANUAL_TEST") != "1" {
		t.Skip("set AGY_MANUAL_TEST=1 to run against the live upstream")
	}
	raw, err := os.ReadFile(os.Getenv("AGY_TOKEN_FILE"))
	if err != nil {
		t.Fatal(err)
	}
	var stored struct {
		Token struct {
			RefreshToken string `json:"refresh_token"`
		} `json:"token"`
	}
	if err := json.Unmarshal(raw, &stored); err != nil {
		t.Fatal(err)
	}
	if stored.Token.RefreshToken == "" {
		t.Fatal("token file has no refresh_token")
	}
	key := fmt.Sprintf(`{"refresh_token":%q}`, stored.Token.RefreshToken)

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	cred, err := service.EnsureAntigravityCredential(ctx, 0, key, "", dto.ChannelSettings{})
	if err != nil {
		t.Fatalf("credential: %v", err)
	}
	t.Logf("credential ok: project=%q access_token_len=%d ua=%q", cred.ProjectID, len(cred.AccessToken), cred.UserAgent)

	models, err := service.FetchAntigravityChannelModels(&model.Channel{Id: 0, Key: key, Type: 64})
	if err != nil {
		t.Fatalf("fetch models: %v", err)
	}
	t.Logf("models (%d): %v", len(models), models)

	for _, modelName := range []string{"gemini-3-flash", "claude-sonnet-5-5-high"} {
		runPrompt(ctx, t, cred, modelName)
	}

	ch := &model.Channel{Id: 0, Key: key, Type: 64}
	usage, err := service.FetchAntigravityChannelUsage(ctx, ch)
	if err != nil {
		t.Fatalf("usage: %v", err)
	}
	if len(usage.Groups) == 0 {
		t.Fatal("usage returned no group")
	}
	for _, group := range usage.Groups {
		t.Logf("usage group %q buckets=%d", group.DisplayName, len(group.Buckets))
		for _, bucket := range group.Buckets {
			t.Logf("  %s window=%s remaining=%.4f reset=%s", bucket.DisplayName, bucket.Window, bucket.RemainingFraction, bucket.ResetTime)
		}
	}
}

func runPrompt(ctx context.Context, t *testing.T, cred *service.AntigravityCredential, modelName string) {
	t.Helper()
	request := &dto.GeminiChatRequest{
		Contents: []dto.GeminiChatContent{{
			Role:  "user",
			Parts: []dto.GeminiPart{{Text: "Reply with the single word: pong"}},
		}},
	}
	envelope, err := buildRequestBody(modelName, cred.ProjectID, request)
	if err != nil {
		t.Fatalf("%s: build envelope: %v", modelName, err)
	}
	body, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%s: envelope keys=%v requestType=%v session_id_set=%v", modelName, mapKeys(envelope), envelope["requestType"], strings.Contains(string(body), "sessionId"))

	url := strings.TrimRight(cred.BaseURL, "/") + streamPath + "?alt=sse"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(string(body)))
	if err != nil {
		t.Fatal(err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+cred.AccessToken)
	httpReq.Header.Set("User-Agent", cred.UserAgent)

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		t.Fatalf("%s: upstream request: %v", modelName, err)
	}
	defer resp.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		t.Fatal(err)
	}
	frames := 0
	for _, line := range strings.Split(string(payload), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "data:") {
			frames++
		}
	}
	t.Logf("%s: status=%d content_type=%q bytes=%d sse_frames=%d", modelName, resp.StatusCode, resp.Header.Get("Content-Type"), len(payload), frames)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("%s: upstream body: %s", modelName, clipPayload(payload))
	}

	unwrapped, err := unwrapResponseBody(payload)
	if err != nil {
		t.Fatalf("%s: unwrap aggregated response: %v", modelName, err)
	}
	var merged map[string]any
	if err := json.Unmarshal(unwrapped, &merged); err != nil {
		t.Fatalf("%s: merged response is not json: %v", modelName, err)
	}
	text := firstText(merged)
	if text == "" {
		t.Fatalf("%s: merged response has no text: %s", modelName, clipPayload(unwrapped))
	}
	t.Logf("%s: aggregated text=%q model_version=%v usage=%v", modelName, text, merged["modelVersion"], merged["usageMetadata"] != nil)
}

func mapKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	return keys
}

func firstText(merged map[string]any) string {
	candidates, _ := merged["candidates"].([]any)
	for _, candidate := range candidates {
		entry, _ := candidate.(map[string]any)
		content, _ := entry["content"].(map[string]any)
		parts, _ := content["parts"].([]any)
		var builder strings.Builder
		for _, part := range parts {
			partMap, _ := part.(map[string]any)
			if text, ok := partMap["text"].(string); ok {
				builder.WriteString(text)
			}
		}
		if builder.Len() > 0 {
			return builder.String()
		}
	}
	return ""
}

func clipPayload(raw []byte) string {
	const limit = 400
	if len(raw) <= limit {
		return string(raw)
	}
	return string(raw[:limit]) + "..."
}
