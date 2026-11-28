package service

import (
	"context"
	"os"
	"runtime"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"

	"github.com/google/uuid"
)

const (
	systemInstanceReportInterval    = 30 * time.Second
	systemInstanceDBTimeout         = 5 * time.Second
	defaultSystemInstanceTTLSeconds = 180
)

type SystemInstanceInfo struct {
	SchemaVersion int                       `json:"schema_version"`
	Node          common.NodeIdentity       `json:"node"`
	Role          SystemInstanceRoleInfo    `json:"role"`
	Runtime       SystemInstanceRuntimeInfo `json:"runtime"`
	Host          SystemInstanceHostInfo    `json:"host"`
	Resources     SystemInstanceResources   `json:"resources"`
	Extra         map[string]any            `json:"extra,omitempty"`
}

type SystemInstanceRoleInfo struct {
	IsMaster   bool   `json:"is_master"`
	ServerRole string `json:"server_role"`
	Region     string `json:"region,omitempty"`
}

type SystemInstanceRuntimeInfo struct {
	Version   string `json:"version"`
	GOOS      string `json:"goos"`
	GOARCH    string `json:"goarch"`
	StartedAt int64  `json:"started_at"`
}

type SystemInstanceHostInfo struct {
	Hostname string `json:"hostname"`
}

type SystemInstanceResources struct {
	CPU     SystemInstanceResourceUsage  `json:"cpu"`
	Memory  SystemInstanceResourceUsage  `json:"memory"`
	Storage SystemInstanceStorageMetrics `json:"storage"`
}

type SystemInstanceResourceUsage struct {
	UsagePercent float64 `json:"usage_percent"`
}

type SystemInstanceStorageMetrics struct {
	TotalBytes  uint64  `json:"total_bytes"`
	UsedBytes   uint64  `json:"used_bytes"`
	FreeBytes   uint64  `json:"free_bytes"`
	UsedPercent float64 `json:"used_percent"`
}

func StartSystemInstanceReporter() func() {
	hostname, _ := os.Hostname()
	identity := common.GetSystemInstanceIdentity(hostname)
	if common.SystemInstanceReporterEnabled() && identity.Key == "" {
		logger.LogWarn(context.Background(), "system instance report disabled: node name is empty")
	}
	report := common.SystemInstanceReporterEnabled() && identity.Key != ""
	cleanup := common.RunsAdmin()
	if !report && !cleanup {
		return func() {}
	}
	ttl := int64(common.GetEnvOrDefault("SYSTEM_INSTANCE_AUTO_EXPIRE_SECONDS", defaultSystemInstanceTTLSeconds))
	if ttl <= model.SystemInstanceStaleAfterSeconds || ttl > 86400 {
		ttl = defaultSystemInstanceTTLSeconds
	}
	reporter := systemInstanceReporter{identity: identity, hostname: hostname, processID: uuid.NewString(), ttl: ttl}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(systemInstanceReportInterval)
		defer ticker.Stop()
		for ctx.Err() == nil {
			if report {
				runSystemInstanceDBOperation(ctx, "report", reporter.report)
			}
			if cleanup {
				runSystemInstanceDBOperation(ctx, "cleanup", func(dbCtx context.Context) error {
					_, err := model.DeleteExpiredSystemInstances(dbCtx, common.GetTimestamp())
					return err
				})
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	return func() {
		cancel()
		waitCtx, waitCancel := context.WithTimeout(context.Background(), systemInstanceDBTimeout)
		defer waitCancel()
		select {
		case <-done:
		case <-waitCtx.Done():
			logger.LogWarn(context.Background(), "system instance reporter did not stop before deadline; skipped unregister")
			return
		}
		if report {
			runSystemInstanceDBOperation(waitCtx, "unregister", func(dbCtx context.Context) error {
				return model.UnregisterSystemInstance(dbCtx, reporter.identity.Key, reporter.processID)
			})
		}
	}
}

func runSystemInstanceDBOperation(parent context.Context, operation string, run func(context.Context) error) {
	if parent.Err() != nil {
		return
	}
	ctx, cancel := context.WithTimeout(parent, systemInstanceDBTimeout)
	defer cancel()
	if err := run(ctx); err != nil && parent.Err() == nil {
		logger.LogWarn(parent, common.LogText("system instance %s failed: %v", operation, err))
	}
}

type systemInstanceReporter struct {
	identity  common.SystemInstanceIdentity
	hostname  string
	processID string
	ttl       int64
}

func (reporter systemInstanceReporter) report(ctx context.Context) error {
	systemStatus := common.GetSystemStatus()
	diskInfo := common.GetDiskSpaceInfo()
	info := SystemInstanceInfo{
		SchemaVersion: 1,
		Node:          reporter.identity.NodeIdentity,
		Role: SystemInstanceRoleInfo{
			IsMaster:   common.IsMasterNode,
			ServerRole: common.ServerRole,
			Region:     common.Region,
		},
		Runtime: SystemInstanceRuntimeInfo{
			Version:   common.Version,
			GOOS:      runtime.GOOS,
			GOARCH:    runtime.GOARCH,
			StartedAt: common.StartTime,
		},
		Host: SystemInstanceHostInfo{
			Hostname: reporter.hostname,
		},
		Resources: SystemInstanceResources{
			CPU: SystemInstanceResourceUsage{
				UsagePercent: systemStatus.CPUUsage,
			},
			Memory: SystemInstanceResourceUsage{
				UsagePercent: systemStatus.MemoryUsage,
			},
			Storage: SystemInstanceStorageMetrics{
				TotalBytes:  diskInfo.Total,
				UsedBytes:   diskInfo.Used,
				FreeBytes:   diskInfo.Free,
				UsedPercent: diskInfo.UsedPercent,
			},
		},
	}
	now := common.GetTimestamp()
	var expiresAt int64
	if reporter.identity.Ephemeral {
		expiresAt = now + reporter.ttl
	}
	return model.UpsertSystemInstance(ctx, model.SystemInstanceHeartbeat{
		NodeName: reporter.identity.Key, Info: info, StartedAt: common.StartTime,
		LastSeenAt: now, ProcessID: reporter.processID, ExpiresAt: expiresAt,
		HostNodeName: reporter.identity.HostNodeName,
	})
}
