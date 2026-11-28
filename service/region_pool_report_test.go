package service

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/require"
)

func TestRegionReportV2SenderRequiresHTTPSAndDedicatedToken(t *testing.T) {
	legacy := strings.Repeat("l", 32)
	t.Setenv("REGION_INTERNAL_TOKEN", legacy)
	sender := RegionReportSenderV2{BaseURL: "https://region-admin.invalid", Region: "test", Token: legacy}
	_, err := sender.endpoint()
	require.Error(t, err)
	sender.Token = strings.Repeat("v", 32)
	sender.BaseURL = "http://region-admin.invalid"
	_, err = sender.endpoint()
	require.Error(t, err)
	sender.BaseURL = "https://region-admin.invalid"
	endpoint, err := sender.endpoint()
	require.NoError(t, err)
	require.Equal(t, sender.BaseURL+RegionReportV2Path, endpoint)
}

func TestDecodeRegionReportReceipt(t *testing.T) {
	report := model.RegionReportV2{Version: model.RegionReportProtocolVersion, Region: "test", SubjectType: model.RegionSubjectUser,
		SubjectID: "1", Generation: 3, Seq: 2, Delta: -50, PayloadHash: strings.Repeat("a", 64)}
	for _, legacyBody := range []string{"", `{}`, `{"status":"received"}`} {
		_, err := decodeRegionReportReceipt([]byte(legacyBody), report)
		require.Error(t, err)
	}
	for _, change := range []func(*model.RegionReportReceipt){
		func(receipt *model.RegionReportReceipt) { receipt.AppliedAt = 0 },
		func(receipt *model.RegionReportReceipt) { receipt.Version-- },
		func(receipt *model.RegionReportReceipt) { receipt.Generation-- },
		func(receipt *model.RegionReportReceipt) { receipt.Seq-- },
		func(receipt *model.RegionReportReceipt) { receipt.Delta++ },
		func(receipt *model.RegionReportReceipt) { receipt.PayloadHash = strings.Repeat("b", 64) },
	} {
		receipt := model.RegionReportReceipt{RegionReportV2: report, AppliedAt: 123}
		change(&receipt)
		body, err := json.Marshal(receipt)
		require.NoError(t, err)
		_, err = decodeRegionReportReceipt(body, report)
		require.Error(t, err)
	}
	receipt := model.RegionReportReceipt{RegionReportV2: report, AppliedAt: 123}
	body, err := json.Marshal(receipt)
	require.NoError(t, err)
	actual, err := decodeRegionReportReceipt(body, report)
	require.NoError(t, err)
	require.Equal(t, receipt, actual)
}
