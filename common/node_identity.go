package common

import (
	"crypto/sha256"
	"fmt"
	"os"
	"strings"
)

type NodeIdentity struct {
	Name                    string `json:"name"`
	Source                  string `json:"source"`
	ManuallyConfigured      bool   `json:"manually_configured"`
	ShouldConfigureManually bool   `json:"should_configure_manually"`
}

func initNodeNameIdentity() {
	if envNodeName := os.Getenv("NODE_NAME"); envNodeName != "" {
		NodeName = envNodeName
		NodeNameSource = NodeNameSourceManual
		NodeNameManuallyConfigured = true
		return
	}

	hostname, _ := os.Hostname()
	NodeName = hostname
	NodeNameSource = NodeNameSourceHostname
	NodeNameManuallyConfigured = false
}

type SystemInstanceIdentity struct {
	NodeIdentity
	Key          string
	Ephemeral    bool
	HostNodeName string
}

func GetSystemInstanceIdentity(hostname string) SystemInstanceIdentity {
	podName := strings.TrimSpace(os.Getenv("POD_NAME"))
	podUID := strings.TrimSpace(os.Getenv("POD_UID"))
	identity := SystemInstanceIdentity{
		NodeIdentity: GetNodeIdentity(),
		Ephemeral: podName != "" || podUID != "" || os.Getenv("KUBERNETES_SERVICE_HOST") != "" ||
			GetEnvOrDefaultBool("INSTANCE_EPHEMERAL", false),
	}
	if identity.Ephemeral {
		if podName == "" {
			podName = hostname
		}
		identity.NodeIdentity = NodeIdentity{Name: podName, Source: "pod"}
		identity.HostNodeName = strings.TrimSpace(os.Getenv("POD_HOST_NODE_NAME"))
		if podUID != "" {
			identity.Key = "pod:" + podUID
		} else if podName != "" {
			identity.Key = fmt.Sprintf("pod-name:%x", sha256.Sum256([]byte(podName)))
		}
		return identity
	}
	if strings.TrimSpace(identity.Name) == "" {
		identity.NodeIdentity = NodeIdentity{Name: hostname, Source: NodeNameSourceHostname, ShouldConfigureManually: true}
	}
	identity.Key = identity.Name
	return identity
}

func SystemInstanceReporterEnabled() bool {
	return GetEnvOrDefaultBool("SYSTEM_INSTANCE_REPORTER_ENABLED", true)
}

func GetNodeIdentity() NodeIdentity {
	return NodeIdentity{
		Name:                    NodeName,
		Source:                  NodeNameSource,
		ManuallyConfigured:      NodeNameManuallyConfigured,
		ShouldConfigureManually: !NodeNameManuallyConfigured,
	}
}
