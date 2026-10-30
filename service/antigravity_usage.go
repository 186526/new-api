package service

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
)

const antigravityUsageTimeout = 30 * time.Second

// AntigravityUsageBucket 是上游的一个配额窗口，remainingFraction 为 1 表示完全未使用。
type AntigravityUsageBucket struct {
	BucketID          string  `json:"bucket_id,omitempty"`
	ModelID           string  `json:"model_id,omitempty"`
	DisplayName       string  `json:"display_name,omitempty"`
	Window            string  `json:"window,omitempty"`
	Description       string  `json:"description,omitempty"`
	TokenType         string  `json:"token_type,omitempty"`
	RemainingFraction float64 `json:"remaining_fraction"`
	ResetTime         string  `json:"reset_time,omitempty"`
}

// AntigravityUsageGroup 把同一个套餐下的窗口归为一组，例如 Gemini Models 与第三方模型。
type AntigravityUsageGroup struct {
	DisplayName string                   `json:"display_name,omitempty"`
	Description string                   `json:"description,omitempty"`
	Buckets     []AntigravityUsageBucket `json:"buckets"`
}

type AntigravityUsage struct {
	Groups    []AntigravityUsageGroup `json:"groups"`
	FetchedAt int64                   `json:"fetched_at"`
}

// FetchAntigravityChannelUsage 优先读取账号级的窗口汇总，取不到时回退到按模型的配额。
func FetchAntigravityChannelUsage(ctx context.Context, ch *model.Channel) (*AntigravityUsage, error) {
	if ch == nil {
		return nil, errors.New("channel is nil")
	}
	settings := ch.GetSetting()
	baseURL := strings.TrimSpace(ch.GetBaseURL())
	if baseURL == "" {
		baseURL = constant.GetChannelBaseURL(constant.ChannelTypeAntigravity)
	}

	callCtx, cancel := context.WithTimeout(ctx, antigravityUsageTimeout)
	defer cancel()

	credential, err := EnsureAntigravityCredential(callCtx, ch.Id, ch.Key, baseURL, settings)
	if err != nil {
		return nil, err
	}
	client, err := GetHttpClientWithProxySettings(settings.Proxy, settings)
	if err != nil {
		return nil, err
	}

	summaryBody, summaryStatus, summaryErr := antigravityPost(callCtx, client, credential.BaseURL, ":retrieveUserQuotaSummary", credential.AccessToken, map[string]any{})
	if summaryErr == nil && summaryStatus >= 200 && summaryStatus < 300 {
		if groups := parseAntigravityQuotaSummary(summaryBody); len(groups) > 0 {
			return &AntigravityUsage{Groups: groups, FetchedAt: time.Now().Unix()}, nil
		}
	}

	quotaBody, quotaStatus, quotaErr := antigravityPost(callCtx, client, credential.BaseURL, ":retrieveUserQuota", credential.AccessToken, map[string]any{})
	if quotaErr != nil {
		if summaryErr != nil {
			return nil, summaryErr
		}
		return nil, quotaErr
	}
	if quotaStatus < 200 || quotaStatus >= 300 {
		return nil, fmt.Errorf("antigravity retrieveUserQuota failed: status=%d body=%s", quotaStatus, truncateForLog(quotaBody))
	}
	buckets := parseAntigravityQuotaBuckets(quotaBody)
	if len(buckets) == 0 {
		return nil, errors.New("antigravity upstream returned no quota bucket")
	}
	return &AntigravityUsage{
		Groups:    []AntigravityUsageGroup{{Buckets: buckets}},
		FetchedAt: time.Now().Unix(),
	}, nil
}

func parseAntigravityQuotaSummary(body []byte) []AntigravityUsageGroup {
	var payload struct {
		Groups []struct {
			DisplayName string `json:"displayName"`
			Description string `json:"description"`
			Buckets     []struct {
				BucketID          string  `json:"bucketId"`
				DisplayName       string  `json:"displayName"`
				Window            string  `json:"window"`
				Description       string  `json:"description"`
				RemainingFraction float64 `json:"remainingFraction"`
				ResetTime         string  `json:"resetTime"`
			} `json:"buckets"`
		} `json:"groups"`
	}
	if err := common.Unmarshal(body, &payload); err != nil {
		return nil
	}
	groups := make([]AntigravityUsageGroup, 0, len(payload.Groups))
	for _, group := range payload.Groups {
		buckets := make([]AntigravityUsageBucket, 0, len(group.Buckets))
		for _, bucket := range group.Buckets {
			buckets = append(buckets, AntigravityUsageBucket{
				BucketID:          strings.TrimSpace(bucket.BucketID),
				DisplayName:       strings.TrimSpace(bucket.DisplayName),
				Window:            strings.TrimSpace(bucket.Window),
				Description:       strings.TrimSpace(bucket.Description),
				RemainingFraction: bucket.RemainingFraction,
				ResetTime:         strings.TrimSpace(bucket.ResetTime),
			})
		}
		if len(buckets) == 0 {
			continue
		}
		groups = append(groups, AntigravityUsageGroup{
			DisplayName: strings.TrimSpace(group.DisplayName),
			Description: strings.TrimSpace(group.Description),
			Buckets:     buckets,
		})
	}
	return groups
}

func parseAntigravityQuotaBuckets(body []byte) []AntigravityUsageBucket {
	var payload struct {
		Buckets []struct {
			ModelID           string  `json:"modelId"`
			TokenType         string  `json:"tokenType"`
			RemainingFraction float64 `json:"remainingFraction"`
			ResetTime         string  `json:"resetTime"`
		} `json:"buckets"`
	}
	if err := common.Unmarshal(body, &payload); err != nil {
		return nil
	}
	buckets := make([]AntigravityUsageBucket, 0, len(payload.Buckets))
	for _, bucket := range payload.Buckets {
		modelID := strings.TrimSpace(bucket.ModelID)
		if modelID == "" {
			continue
		}
		if _, internal := antigravityInternalModels[modelID]; internal {
			continue
		}
		buckets = append(buckets, AntigravityUsageBucket{
			ModelID:           modelID,
			DisplayName:       modelID,
			TokenType:         strings.TrimSpace(bucket.TokenType),
			RemainingFraction: bucket.RemainingFraction,
			ResetTime:         strings.TrimSpace(bucket.ResetTime),
		})
	}
	sort.Slice(buckets, func(i, j int) bool { return buckets[i].ModelID < buckets[j].ModelID })
	return buckets
}
