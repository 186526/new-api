package service

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/dto"
)

const (
	antigravityOAuthClientID     = "1071006060591-tmhssin2h21lcre235vtolojh4g403ep.apps.googleusercontent.com"
	antigravityOAuthClientSecret = "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"
	antigravityOAuthTokenURL     = "https://oauth2.googleapis.com/token"
	antigravityDefaultBaseURL    = "https://daily-cloudcode-pa.googleapis.com"
	antigravityUserAgent         = "antigravity/hub/2.24.0 (aidev_client; os_type=linux; arch=amd64)"
	antigravityTokenSafetyWindow = 5 * time.Minute
	antigravityRefreshTimeout    = 30 * time.Second
	antigravityDefaultTierID     = "free-tier"
)

// AntigravityOAuthKey 是 Antigravity 渠道 key 列里保存的 JSON。
type AntigravityOAuthKey struct {
	AccessToken  string `json:"access_token,omitempty"`
	RefreshToken string `json:"refresh_token,omitempty"`
	ProjectID    string `json:"project_id,omitempty"`
	Email        string `json:"email,omitempty"`
	Expired      string `json:"expired,omitempty"`
	LastRefresh  string `json:"last_refresh,omitempty"`
	Type         string `json:"type,omitempty"`
}

func ParseAntigravityOAuthKey(raw string) (*AntigravityOAuthKey, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, errors.New("antigravity channel: empty oauth key")
	}
	if !strings.HasPrefix(trimmed, "{") {
		return nil, errors.New("antigravity channel: key must be a JSON object with refresh_token")
	}
	var key AntigravityOAuthKey
	if err := common.Unmarshal([]byte(trimmed), &key); err != nil {
		return nil, errors.New("antigravity channel: invalid oauth key json")
	}
	key.AccessToken = strings.TrimSpace(key.AccessToken)
	key.RefreshToken = strings.TrimSpace(key.RefreshToken)
	key.ProjectID = strings.TrimSpace(key.ProjectID)
	if key.RefreshToken == "" {
		return nil, errors.New("antigravity channel: refresh_token is required")
	}
	return &key, nil
}

func (k *AntigravityOAuthKey) accessTokenExpiry() time.Time {
	if k.Expired == "" {
		return time.Time{}
	}
	expiry, err := time.Parse(time.RFC3339, k.Expired)
	if err != nil {
		return time.Time{}
	}
	return expiry
}

// AntigravityCredential 是一次上游请求需要的全部认证信息。
type AntigravityCredential struct {
	AccessToken string
	ProjectID   string
	BaseURL     string
	UserAgent   string
}

type antigravityCredentialEntry struct {
	accessToken string
	projectID   string
	expiresAt   time.Time
	keyHash     [32]byte
}

var (
	antigravityCredentialMu    sync.Mutex
	antigravityCredentialCache = map[int]*antigravityCredentialEntry{}
)

// ResetAntigravityCredentialCache 清掉某个渠道的进程内缓存，渠道凭据被改写后调用。
func ResetAntigravityCredentialCache(channelID int) {
	antigravityCredentialMu.Lock()
	delete(antigravityCredentialCache, channelID)
	antigravityCredentialMu.Unlock()
}

// EnsureAntigravityCredential 返回可用的 access token 与 project id。
// access token 距过期不足 5 分钟时用 refresh_token 刷新，缺 project id 时调 loadCodeAssist；
// 两者发生变化时写回渠道 key，避免每个请求都重新换取。
func EnsureAntigravityCredential(
	ctx context.Context,
	channelID int,
	rawKey string,
	baseURL string,
	settings dto.ChannelSettings,
) (*AntigravityCredential, error) {
	key, err := ParseAntigravityOAuthKey(rawKey)
	if err != nil {
		return nil, err
	}
	if baseURL == "" {
		baseURL = antigravityDefaultBaseURL
	}
	baseURL = strings.TrimRight(baseURL, "/")

	keyHash := sha256.Sum256([]byte(rawKey))
	antigravityCredentialMu.Lock()
	entry := antigravityCredentialCache[channelID]
	if entry != nil && entry.keyHash == keyHash &&
		entry.projectID != "" && entry.accessToken != "" &&
		time.Until(entry.expiresAt) > antigravityTokenSafetyWindow {
		cached := &AntigravityCredential{
			AccessToken: entry.accessToken,
			ProjectID:   entry.projectID,
			BaseURL:     baseURL,
			UserAgent:   antigravityUserAgent,
		}
		antigravityCredentialMu.Unlock()
		return cached, nil
	}
	antigravityCredentialMu.Unlock()

	client, err := GetHttpClientWithProxySettings(settings.Proxy, settings)
	if err != nil {
		return nil, err
	}
	callCtx, cancel := context.WithTimeout(ctx, antigravityRefreshTimeout)
	defer cancel()

	changed := false
	if key.AccessToken == "" || time.Until(key.accessTokenExpiry()) <= antigravityTokenSafetyWindow {
		result, refreshErr := refreshAntigravityAccessToken(callCtx, client, key.RefreshToken)
		if refreshErr != nil {
			if key.AccessToken == "" {
				return nil, refreshErr
			}
			// 刷新失败但手里还有未过期的 token，先用着，下次请求再试。
			if time.Until(key.accessTokenExpiry()) <= 0 {
				return nil, refreshErr
			}
		} else {
			key.AccessToken = result.AccessToken
			if result.RefreshToken != "" {
				key.RefreshToken = result.RefreshToken
			}
			key.Expired = result.ExpiresAt.Format(time.RFC3339)
			key.LastRefresh = time.Now().Format(time.RFC3339)
			changed = true
		}
	}

	if key.ProjectID == "" {
		projectID, projectErr := loadAntigravityProject(callCtx, client, baseURL, key.AccessToken)
		if projectErr != nil {
			return nil, projectErr
		}
		key.ProjectID = projectID
		changed = true
	}

	if changed {
		key.Type = "antigravity"
		encoded, marshalErr := common.Marshal(key)
		if marshalErr == nil && channelID > 0 {
			if dbErr := model.DB.Model(&model.Channel{}).Where("id = ?", channelID).Update("key", string(encoded)).Error; dbErr == nil {
				model.InitChannelCache()
			}
		}
	}

	expiresAt := key.accessTokenExpiry()
	antigravityCredentialMu.Lock()
	antigravityCredentialCache[channelID] = &antigravityCredentialEntry{
		accessToken: key.AccessToken,
		projectID:   key.ProjectID,
		expiresAt:   expiresAt,
		keyHash:     sha256.Sum256(mustMarshalAntigravityKey(key)),
	}
	antigravityCredentialMu.Unlock()

	return &AntigravityCredential{
		AccessToken: key.AccessToken,
		ProjectID:   key.ProjectID,
		BaseURL:     baseURL,
		UserAgent:   antigravityUserAgent,
	}, nil
}

func mustMarshalAntigravityKey(key *AntigravityOAuthKey) []byte {
	encoded, err := common.Marshal(key)
	if err != nil {
		return nil
	}
	return encoded
}

type antigravityTokenResult struct {
	AccessToken  string
	RefreshToken string
	ExpiresAt    time.Time
}

func refreshAntigravityAccessToken(ctx context.Context, client *http.Client, refreshToken string) (*antigravityTokenResult, error) {
	rt := strings.TrimSpace(refreshToken)
	if rt == "" {
		return nil, errors.New("empty refresh_token")
	}
	form := url.Values{}
	form.Set("client_id", antigravityOAuthClientID)
	form.Set("client_secret", antigravityOAuthClientSecret)
	form.Set("grant_type", "refresh_token")
	form.Set("refresh_token", rt)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, antigravityOAuthTokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	// Antigravity 客户端刷新 token 时用的是 Go 默认 UA。
	req.Header.Set("User-Agent", "Go-http-client/2.0")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var payload struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int    `json:"expires_in"`
		Error        string `json:"error"`
		ErrorDesc    string `json:"error_description"`
	}
	if err := common.DecodeJson(resp.Body, &payload); err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if payload.Error != "" {
			return nil, fmt.Errorf("antigravity oauth refresh failed: status=%d error=%s %s", resp.StatusCode, payload.Error, payload.ErrorDesc)
		}
		return nil, fmt.Errorf("antigravity oauth refresh failed: status=%d", resp.StatusCode)
	}
	if strings.TrimSpace(payload.AccessToken) == "" {
		return nil, errors.New("antigravity oauth refresh response missing access_token")
	}
	expiresIn := payload.ExpiresIn
	if expiresIn <= 0 {
		expiresIn = 3600
	}
	return &antigravityTokenResult{
		AccessToken:  strings.TrimSpace(payload.AccessToken),
		RefreshToken: strings.TrimSpace(payload.RefreshToken),
		ExpiresAt:    time.Now().Add(time.Duration(expiresIn) * time.Second),
	}, nil
}

// loadAntigravityProject 依次尝试 loadCodeAssist 与 onboardUser。
func loadAntigravityProject(ctx context.Context, client *http.Client, baseURL, accessToken string) (string, error) {
	loadBody := map[string]any{"metadata": map[string]any{"ideType": "ANTIGRAVITY"}}
	respBody, status, err := antigravityPost(ctx, client, baseURL, ":loadCodeAssist", accessToken, loadBody)
	if err != nil {
		return "", err
	}
	if status < 200 || status >= 300 {
		return "", fmt.Errorf("antigravity loadCodeAssist failed: status=%d body=%s", status, truncateForLog(respBody))
	}
	projectID := extractAntigravityProject(respBody)
	if projectID != "" {
		return projectID, nil
	}

	tierID := extractAntigravityDefaultTier(respBody)
	onboardBody := map[string]any{
		"tier_id":  tierID,
		"metadata": map[string]any{"ide_type": "ANTIGRAVITY", "ide_name": "antigravity"},
	}
	onboardBodyRaw, status, err := antigravityPost(ctx, client, baseURL, ":onboardUser", accessToken, onboardBody)
	if err != nil {
		return "", err
	}
	if status < 200 || status >= 300 {
		return "", fmt.Errorf("antigravity onboardUser failed: status=%d body=%s", status, truncateForLog(onboardBodyRaw))
	}
	if projectID = extractAntigravityProject(onboardBodyRaw); projectID == "" {
		return "", fmt.Errorf("antigravity: no cloudaicompanionProject in loadCodeAssist/onboardUser response")
	}
	return projectID, nil
}

func antigravityPost(ctx context.Context, client *http.Client, baseURL, method string, accessToken string, body any) ([]byte, int, error) {
	payload, err := common.Marshal(body)
	if err != nil {
		return nil, 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(baseURL, "/")+"/v1internal"+method, strings.NewReader(string(payload)))
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("User-Agent", antigravityUserAgent)
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, resp.StatusCode, err
	}
	return respBody, resp.StatusCode, nil
}

func extractAntigravityProject(body []byte) string {
	var payload map[string]any
	if err := common.Unmarshal(body, &payload); err != nil {
		return ""
	}
	if v, ok := payload["response"].(map[string]any); ok {
		payload = v
	}
	for _, key := range []string{"cloudaicompanionProject", "projectId", "project"} {
		switch value := payload[key].(type) {
		case string:
			if trimmed := strings.TrimSpace(value); trimmed != "" {
				return trimmed
			}
		case map[string]any:
			if id, ok := value["id"].(string); ok && strings.TrimSpace(id) != "" {
				return strings.TrimSpace(id)
			}
		}
	}
	return ""
}

func extractAntigravityDefaultTier(body []byte) string {
	var payload map[string]any
	if err := common.Unmarshal(body, &payload); err != nil {
		return antigravityDefaultTierID
	}
	if v, ok := payload["response"].(map[string]any); ok {
		payload = v
	}
	if tiers, ok := payload["allowedTiers"].([]any); ok {
		for _, tier := range tiers {
			entry, ok := tier.(map[string]any)
			if !ok || entry["isDefault"] != true {
				continue
			}
			if id, ok := entry["id"].(string); ok && strings.TrimSpace(id) != "" {
				return strings.TrimSpace(id)
			}
		}
	}
	if current, ok := payload["currentTier"].(map[string]any); ok {
		if id, ok := current["id"].(string); ok && strings.TrimSpace(id) != "" {
			return strings.TrimSpace(id)
		}
	}
	return antigravityDefaultTierID
}

func truncateForLog(body []byte) string {
	const limit = 200
	if len(body) <= limit {
		return string(body)
	}
	return string(body[:limit]) + "..."
}
