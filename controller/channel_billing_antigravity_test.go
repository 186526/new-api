package controller

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
)

func TestAntigravityQuotaMapsGroupsToWindows(t *testing.T) {
	channel := &model.Channel{Id: 6, Name: "Google Antigravity"}
	usage := &service.AntigravityUsage{
		FetchedAt: 1_793_355_600,
		Groups: []service.AntigravityUsageGroup{
			{
				DisplayName: "Gemini Models",
				Buckets: []service.AntigravityUsageBucket{
					{Window: "5h", RemainingFraction: 1, ResetTime: "2026-10-30T12:00:00Z"},
					{Window: "weekly", RemainingFraction: 0.9681, ResetTime: "2026-11-05T06:00:00Z"},
				},
			},
			{
				Buckets: []service.AntigravityUsageBucket{
					{DisplayName: "gemini-4-argon", RemainingFraction: 0.4},
				},
			},
		},
	}

	quota := antigravityQuota(channel, usage)

	assert.Equal(t, "Google Antigravity", quota.Channel)
	assert.Equal(t, time.Unix(1_793_355_600, 0), quota.RefreshedAt)
	assert.Len(t, quota.Windows, 3)
	assert.Equal(t, "Gemini Models", quota.Windows[0].Scope)
	assert.Equal(t, "5h", quota.Windows[0].Window)
	assert.Equal(t, 1.0, quota.Windows[0].RemainingRatio)
	assert.Equal(t, time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC), quota.Windows[0].ResetAt)
	assert.Equal(t, "gemini-4-argon", quota.Windows[2].Scope)
	assert.Equal(t, "default", quota.Windows[2].Window)
	assert.True(t, quota.Windows[2].ResetAt.IsZero())
}

func TestAntigravityQuotaFallsBackToDefaultScope(t *testing.T) {
	quota := antigravityQuota(&model.Channel{Name: "channel"}, &service.AntigravityUsage{
		Groups: []service.AntigravityUsageGroup{{
			Buckets: []service.AntigravityUsageBucket{{Window: "weekly", RemainingFraction: 0.5}},
		}},
	})

	assert.Equal(t, "default", quota.Windows[0].Scope)
	assert.Equal(t, "weekly", quota.Windows[0].Window)
}
