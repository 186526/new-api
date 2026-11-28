package model

import (
	"context"

	"github.com/QuantumNous/new-api/common"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	SystemInstanceStatusOnline = "online"
	SystemInstanceStatusStale  = "stale"

	SystemInstanceStaleAfterSeconds int64 = 90
)

type SystemInstance struct {
	NodeName     string `json:"node_name" gorm:"type:varchar(128);primaryKey"`
	Info         string `json:"info" gorm:"type:text"`
	StartedAt    int64  `json:"started_at" gorm:"bigint;index"`
	LastSeenAt   int64  `json:"last_seen_at" gorm:"bigint;index"`
	CreatedAt    int64  `json:"created_at" gorm:"bigint;index"`
	UpdatedAt    int64  `json:"updated_at" gorm:"bigint;index"`
	ProcessID    string `json:"-" gorm:"type:varchar(36)"`
	ExpiresAt    int64  `json:"-" gorm:"bigint;index;default:0"`
	HostNodeName string `json:"-" gorm:"type:varchar(253)"`
}

type SystemInstanceHeartbeat struct {
	NodeName     string
	Info         any
	StartedAt    int64
	LastSeenAt   int64
	ProcessID    string
	ExpiresAt    int64
	HostNodeName string
}

type SystemInstanceResponse struct {
	NodeName           string `json:"node_name"`
	Status             string `json:"status"`
	StaleAfterSeconds  int64  `json:"stale_after_seconds"`
	StartedAt          int64  `json:"started_at"`
	LastSeenAt         int64  `json:"last_seen_at"`
	Info               any    `json:"info"`
	Ephemeral          bool   `json:"ephemeral"`
	HostNodeName       string `json:"host_node_name,omitempty"`
	ExpireAfterSeconds int64  `json:"expire_after_seconds,omitempty"`
	Expired            bool   `json:"expired"`
}

func (instance *SystemInstance) BeforeCreate(_ *gorm.DB) error {
	now := common.GetTimestamp()
	if instance.CreatedAt == 0 {
		instance.CreatedAt = now
	}
	if instance.UpdatedAt == 0 {
		instance.UpdatedAt = now
	}
	return nil
}

func UpsertSystemInstance(ctx context.Context, heartbeat SystemInstanceHeartbeat) error {
	infoText, err := marshalSystemInstanceInfo(heartbeat.Info)
	if err != nil {
		return err
	}
	instance := &SystemInstance{
		NodeName: heartbeat.NodeName, Info: infoText, StartedAt: heartbeat.StartedAt,
		LastSeenAt: heartbeat.LastSeenAt, UpdatedAt: heartbeat.LastSeenAt,
		ProcessID: heartbeat.ProcessID, ExpiresAt: heartbeat.ExpiresAt, HostNodeName: heartbeat.HostNodeName,
	}
	return DB.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "node_name"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"info", "started_at", "last_seen_at", "updated_at", "process_id", "expires_at", "host_node_name",
		}),
	}).Create(instance).Error
}

func UnregisterSystemInstance(ctx context.Context, nodeName, processID string) error {
	return DB.WithContext(ctx).Where("node_name = ? AND process_id = ?", nodeName, processID).Delete(&SystemInstance{}).Error
}

func DeleteExpiredSystemInstances(ctx context.Context, now int64) (int64, error) {
	result := DB.WithContext(ctx).Where("expires_at > 0 AND expires_at <= ?", now).Delete(&SystemInstance{})
	return result.RowsAffected, result.Error
}

func ListSystemInstances() ([]*SystemInstance, error) {
	var instances []*SystemInstance
	err := DB.Order("last_seen_at desc").Find(&instances).Error
	return instances, err
}

func DeleteStaleSystemInstance(nodeName string, now int64) (bool, error) {
	result := DB.Where("node_name = ? AND last_seen_at < ?", nodeName, now-SystemInstanceStaleAfterSeconds).Delete(&SystemInstance{})
	return result.RowsAffected > 0, result.Error
}

func (instance *SystemInstance) ToResponse(now int64) SystemInstanceResponse {
	status := SystemInstanceStatusOnline
	if now-instance.LastSeenAt > SystemInstanceStaleAfterSeconds {
		status = SystemInstanceStatusStale
	}
	ephemeral := instance.ExpiresAt > 0
	var expireAfter int64
	if ephemeral {
		expireAfter = instance.ExpiresAt - instance.LastSeenAt
	}
	return SystemInstanceResponse{
		ExpireAfterSeconds: expireAfter,
		NodeName:           instance.NodeName,
		Status:             status,
		StaleAfterSeconds:  SystemInstanceStaleAfterSeconds,
		StartedAt:          instance.StartedAt,
		LastSeenAt:         instance.LastSeenAt,
		Info:               decodeSystemInstanceInfo(instance.Info),
		Ephemeral:          ephemeral,
		Expired:            ephemeral && instance.ExpiresAt <= now,
		HostNodeName:       instance.HostNodeName,
	}
}

func marshalSystemInstanceInfo(v any) (string, error) {
	if v == nil {
		return "", nil
	}
	data, err := common.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func decodeSystemInstanceInfo(data string) any {
	if data == "" {
		return nil
	}
	var value any
	if err := common.UnmarshalJsonStr(data, &value); err != nil {
		return data
	}
	return value
}
