package controller

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
)

// TestMistralSweepReenablesADisabledChannelAfterTheAllowanceResets drives the
// whole dead end end to end: an exhausted merged allowance takes the channel out,
// and the same allowance coming back brings it in again. The sweep used to visit
// enabled channels only, so the channel it disabled was never read again and
// stayed out of service until an admin re-enabled it by hand.
func TestMistralSweepReenablesADisabledChannelAfterTheAllowanceResets(t *testing.T) {
	setupMistralBalanceTest(t)
	captureMistralSweepLog(t)

	exhausted := true
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/trpc/billing.budget" {
			http.NotFound(w, r)
			return
		}
		percentage, limit := 100.0, 150.0
		if !exhausted {
			percentage = 65.79
		}
		_, _ = fmt.Fprintf(w,
			`{"result":{"data":{"json":{"usage_percentage":%v,"initial_budget":%v,"currency":"USD","reset_at":"2026-12-01T00:00:00Z"}}}}`,
			percentage, limit)
	}))
	t.Cleanup(server.Close)
	setMistralQuotaEnv(t, server.URL,
		`[{"channel":"Mistral (pro)","cookie":"ory_session_x=1","vibeKey":"mstrl_vibe","monthlyLimitUsd":150}]`)

	channel := &model.Channel{
		Type: constant.ChannelTypeMistral, Key: "mstrl_vibe", Name: "Mistral (pro)",
		Status: common.ChannelStatusEnabled, OtherInfo: "{}",
	}
	require.NoError(t, model.DB.Create(channel).Error)

	require.NoError(t, updateAllChannelsBalance())
	disabled, err := model.GetChannelById(channel.Id, true)
	require.NoError(t, err)
	require.Equal(t, common.ChannelStatusAutoDisabled, disabled.Status,
		"a channel with nothing left must be disabled by the sweep")

	exhausted = false
	require.NoError(t, updateAllChannelsBalance())
	enabled, err := model.GetChannelById(channel.Id, true)
	require.NoError(t, err)
	require.Equal(t, common.ChannelStatusEnabled, enabled.Status,
		"a refilled allowance must bring the channel back")
}
