package service

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	appmetrics "github.com/QuantumNous/new-api/pkg/metrics"
)

// Region lease maintenance.
//
// The admin tops up leases on demand and folds reported spend back into the
// central balance; a relay instance reports what it spent and asks for more
// when its lease runs dry. Both sides talk over the same JSON endpoints, so a
// region needs nothing but an address and a shared token.

const (
	regionLeasePath  = "/api/internal/region/lease"
	regionReportPath = "/api/internal/region/report"

	regionReportInterval = 15 * time.Second
	regionGrantInterval  = 60 * time.Second
	regionApplyInterval  = 30 * time.Second
)

type regionLeaseRequest struct {
	Region      string `json:"region"`
	SubjectType string `json:"subject_type"`
	SubjectID   string `json:"subject_id"`
}

type regionLeaseReply struct {
	Granted int    `json:"granted"`
	Error   string `json:"error,omitempty"`
}

type regionReportRequest struct {
	Region      string `json:"region"`
	SubjectType string `json:"subject_type"`
	SubjectID   string `json:"subject_id"`
	Seq         int64  `json:"seq"`
	Delta       int64  `json:"delta"`
}

// RegionInternalToken is the shared secret between the admin and its relay
// instances. Without it the internal endpoints refuse every request.
func RegionInternalToken() string {
	return strings.TrimSpace(os.Getenv("REGION_INTERNAL_TOKEN"))
}

// StartRegionLeaseMaintenance starts the loops that keep region leases topped
// up (admin) or reported (relay instance).
func StartRegionLeaseMaintenance() {
	if !model.RegionEnabled() {
		return
	}
	region := common.Region
	if common.RunsAdmin() {
		go runRegionAdminLoop()
	}
	go runRegionReportLoop(region)
}

func runRegionReportLoop(region string) {
	model.SetRegionLeaseRequest(func(subjectType string, subjectID string) error {
		_, err := requestRegionLease(region, subjectType, subjectID)
		return err
	})
	ticker := time.NewTicker(regionReportInterval)
	defer ticker.Stop()
	for {
		reportRegionPending(region)
		pendingReportSnapshot(region)
		publishRegionPoolMetrics(region)
		<-ticker.C
	}
}

// reportRegionPending re-sends batches that failed last time. The sequence
// number is fixed before the first attempt, so a duplicate is harmless.
func reportRegionPending(region string) {
	entries, err := model.PendingRegionReports(region)
	if err != nil {
		common.SysLog(common.LogText("failed to read pending region reports: %s", err.Error()))
		return
	}
	for _, entry := range entries {
		if err := postRegionReport(region, entry); err != nil {
			common.SysLog(common.LogText("failed to report region quota batch %s/%d: %s", entry.SubjectType, entry.Seq, err.Error()))
			continue
		}
		if err := model.ClearPendingRegionReport(region, entry.SubjectType, entry.SubjectID); err != nil {
			common.SysLog(common.LogText("failed to clear reported region batch: %s", err.Error()))
		}
	}
}

func pendingReportSnapshot(region string) {
	subjects, err := model.RegionSubjects(region)
	if err != nil {
		common.SysLog(common.LogText("failed to list region subjects: %s", err.Error()))
		return
	}
	for _, subject := range subjects {
		report, err := model.NextRegionReport(region, subject[0], subject[1])
		if err != nil {
			common.SysLog(common.LogText("failed to snapshot region spend: %s", err.Error()))
			continue
		}
		if report == nil {
			continue
		}
		if err := model.SavePendingRegionReport(report); err != nil {
			common.SysLog(common.LogText("failed to persist region batch: %s", err.Error()))
			continue
		}
	}
}

func postRegionReport(region string, report model.RegionReport) error {
	body, err := json.Marshal(regionReportRequest{
		Region:      region,
		SubjectType: report.SubjectType,
		SubjectID:   report.SubjectID,
		Seq:         report.Seq,
		Delta:       report.Delta,
	})
	if err != nil {
		return err
	}
	return postJSONToAdmin(regionReportPath, body)
}

func requestRegionLease(region string, subjectType string, subjectID string) (int, error) {
	body, err := json.Marshal(regionLeaseRequest{Region: region, SubjectType: subjectType, SubjectID: subjectID})
	if err != nil {
		return 0, err
	}
	response, err := postJSONToAdminWithReply(regionLeasePath, body)
	if err != nil {
		return 0, err
	}
	var reply regionLeaseReply
	if err := json.Unmarshal(response, &reply); err != nil {
		return 0, err
	}
	if reply.Error != "" {
		return reply.Granted, fmt.Errorf("%s", reply.Error)
	}
	if reply.Granted == 0 {
		return 0, model.ErrRegionLeaseUnavailable
	}
	return reply.Granted, nil
}

func adminBaseURL() string {
	if raw := strings.TrimSpace(os.Getenv("REGION_ADMIN_URL")); raw != "" {
		return strings.TrimSuffix(raw, "/")
	}
	port := "3000"
	if common.Port != nil {
		port = strconv.Itoa(*common.Port)
	}
	return "http://127.0.0.1:" + port
}

func postJSONToAdmin(path string, body []byte) error {
	_, err := postJSONToAdminWithReply(path, body)
	return err
}

func postJSONToAdminWithReply(path string, body []byte) ([]byte, error) {
	token := RegionInternalToken()
	if token == "" {
		return nil, fmt.Errorf("REGION_INTERNAL_TOKEN is not configured")
	}
	request, err := http.NewRequest(http.MethodPost, adminBaseURL()+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Region-Token", token)
	client := &http.Client{Timeout: 10 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	var buffer bytes.Buffer
	if _, err := buffer.ReadFrom(response.Body); err != nil {
		return nil, err
	}
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("admin replied %d: %s", response.StatusCode, buffer.String())
	}
	return buffer.Bytes(), nil
}

func runRegionAdminLoop() {
	go applyRegionEventsLoop()
	leaseTicker := time.NewTicker(regionGrantInterval)
	applyTicker := time.NewTicker(regionApplyInterval)
	defer leaseTicker.Stop()
	defer applyTicker.Stop()
	for {
		select {
		case <-leaseTicker.C:
			topUpRegionLeases()
		case <-applyTicker.C:
			applyRegionEvents()
		}
	}
}

// applyRegionEventsLoop folds reported spend on its own cadence: the events
// arrive from another region, so waiting for the lease ticker would add a
// minute of lag to every balance the dashboard shows.
func applyRegionEventsLoop() {
	ticker := time.NewTicker(regionApplyInterval)
	defer ticker.Stop()
	for range ticker.C {
		applyRegionEvents()
	}
}

func applyRegionEvents() {
	applied, err := model.ApplyRegionEvents(500)
	if err != nil {
		common.SysLog(common.LogText("failed to apply region quota events: %s", err.Error()))
	} else if applied > 0 {
		common.SysLog(common.LogText("applied %d region quota events", applied))
	}
}

// topUpRegionLeases tops up the leases of the region this admin serves. The
// admin reads the region's dirty set, which a relay instance fills on every
// spend, so it only touches subjects that are actually active.
func topUpRegionLeases() {
	// Each admin owns its own region. A second region's admin reads the same
	// shared sets only when it is configured with the same Redis.
	regions := []string{common.Region}
	if common.Region == "" {
		known, err := model.KnownRegions()
		if err != nil {
			common.SysLog(common.LogText("failed to list known regions: %s", err.Error()))
			return
		}
		regions = known
	}
	for _, region := range regions {
		subjects, err := model.RegionSubjects(region)
		if err != nil {
			common.SysLog(common.LogText("failed to list subjects of region %s: %s", region, err.Error()))
			continue
		}
		for _, subject := range subjects {
			if _, err := GrantRegionLeaseTopUp(region, subject[0], subject[1]); err != nil {
				common.SysLog(common.LogText("failed to top up lease for %s %s in %s: %s", subject[0], subject[1], region, err.Error()))
			}
		}
	}
}

// GrantRegionLeaseTopUp gives a subject a lease chunk when its remaining lease
// is low, so the next request does not have to wait for an on-demand pull.
func GrantRegionLeaseTopUp(region string, subjectType string, subjectID string) (int, error) {
	chunk := leaseChunkQuota()
	available, err := model.RegionAvailable(region, subjectType, subjectID)
	if err != nil {
		return 0, err
	}
	if available >= chunk/4 {
		return 0, nil
	}
	granted, err := model.GrantRegionLease(region, subjectType, subjectID, chunk)
	if err != nil {
		return 0, err
	}
	return granted, nil
}

func leaseChunkQuota() int {
	chunk, err := strconv.Atoi(strings.TrimSpace(os.Getenv("REGION_LEASE_CHUNK_QUOTA")))
	if err != nil || chunk <= 0 {
		return int(common.QuotaPerUnit)
	}
	return chunk
}

// publishRegionPoolMetrics exports the lease totals of this region.
func publishRegionPoolMetrics(region string) {
	stats, err := model.RegionPoolStats(region)
	if err != nil {
		common.SysLog(common.LogText("failed to read region pool stats: %s", err.Error()))
		return
	}
	for kind, entry := range stats {
		appmetrics.SetRegionPoolSnapshot(region, kind, entry.Available, entry.Outstanding, entry.Unreported)
	}
}
