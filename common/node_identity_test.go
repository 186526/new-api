package common

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSystemInstanceIdentityUsesPodUID(t *testing.T) {
	t.Setenv("POD_NAME", "relay-0")
	t.Setenv("POD_UID", "uid-one")
	t.Setenv("POD_HOST_NODE_NAME", "worker-1")
	initialNodeName := NodeName
	first := GetSystemInstanceIdentity("hostname")
	t.Setenv("POD_UID", "uid-two")
	second := GetSystemInstanceIdentity("hostname")

	require.Equal(t, "pod:uid-one", first.Key)
	require.Equal(t, "pod:uid-two", second.Key)
	require.Equal(t, first.Name, second.Name)
	require.Equal(t, "relay-0", second.Name)
	require.Equal(t, "worker-1", second.HostNodeName)
	require.True(t, second.Ephemeral)
	require.False(t, second.ShouldConfigureManually)
	require.Equal(t, initialNodeName, NodeName)
}

func TestSystemInstanceIdentityWithoutDownwardAPIUsesHostname(t *testing.T) {
	t.Setenv("POD_NAME", "")
	t.Setenv("POD_UID", "")
	t.Setenv("KUBERNETES_SERVICE_HOST", "fixture")

	identity := GetSystemInstanceIdentity("pod-hostname")
	require.Equal(t, "pod-hostname", identity.Name)
	require.NotEmpty(t, identity.Key)
	require.True(t, identity.Ephemeral)
	require.False(t, identity.ShouldConfigureManually)
}

func TestSystemInstanceReporterEnabledDefaultsOn(t *testing.T) {
	t.Setenv("SYSTEM_INSTANCE_REPORTER_ENABLED", "")
	require.True(t, SystemInstanceReporterEnabled())
	t.Setenv("SYSTEM_INSTANCE_REPORTER_ENABLED", "false")
	require.False(t, SystemInstanceReporterEnabled())
}
