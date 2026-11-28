package model

import (
	"path/filepath"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupSystemInstanceDB(t *testing.T) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "instances.db")), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	previousDB := DB
	DB = db
	t.Cleanup(func() {
		DB = previousDB
		_ = sqlDB.Close()
	})
	require.NoError(t, db.AutoMigrate(&SystemInstance{}))
}

func TestSystemInstanceUnregisterPreservesReplacement(t *testing.T) {
	setupSystemInstanceDB(t)
	ctx := t.Context()
	heartbeat := SystemInstanceHeartbeat{
		NodeName: "pod:uid", ProcessID: "old", StartedAt: 100, LastSeenAt: 100,
		ExpiresAt: 280, HostNodeName: "host-1",
	}
	require.NoError(t, UpsertSystemInstance(ctx, heartbeat))
	heartbeat.ProcessID = "replacement"
	require.NoError(t, UpsertSystemInstance(ctx, heartbeat))
	require.NoError(t, UnregisterSystemInstance(ctx, heartbeat.NodeName, "old"))
	rows, err := ListSystemInstances()
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, "replacement", rows[0].ProcessID)
	response := rows[0].ToResponse(100)
	require.True(t, response.Ephemeral)
	require.Equal(t, "host-1", response.HostNodeName)
	require.NoError(t, UnregisterSystemInstance(ctx, heartbeat.NodeName, "replacement"))
	rows, err = ListSystemInstances()
	require.NoError(t, err)
	require.Empty(t, rows)
}

func TestSystemInstanceExpiryOnlyRemovesExpiredPod(t *testing.T) {
	setupSystemInstanceDB(t)
	ctx := t.Context()
	require.NoError(t, DB.Exec("INSERT INTO system_instances (node_name, last_seen_at) VALUES (?, ?)", "old-static", 10).Error)
	for _, heartbeat := range []SystemInstanceHeartbeat{
		{NodeName: "pod:first", LastSeenAt: 100, ExpiresAt: 280},
		{NodeName: "pod:second", LastSeenAt: 130, ExpiresAt: 310},
	} {
		heartbeat.Info = map[string]any{"node": map[string]string{"name": "same-pod-name"}}
		require.NoError(t, UpsertSystemInstance(ctx, heartbeat))
	}
	deleted, err := DeleteExpiredSystemInstances(ctx, 280)
	require.NoError(t, err)
	require.EqualValues(t, 1, deleted)
	rows, err := ListSystemInstances()
	require.NoError(t, err)
	require.Len(t, rows, 2)
	require.Equal(t, "pod:second", rows[0].NodeName)
	require.Equal(t, "old-static", rows[1].NodeName)
	require.False(t, rows[1].ToResponse(280).Ephemeral)
}
