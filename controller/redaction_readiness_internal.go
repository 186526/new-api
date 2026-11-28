package controller

import (
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/redaction_setting"
)

const RedactionCapabilityPath = "/internal/redaction/capabilities"

func StartRedactionCapabilityProbe() func() {
	address := os.Getenv("REDACTION_CAPABILITY_ADDR")
	if address == "" || os.Getenv("POD_UID") == "" ||
		(common.ServerRole != common.ServerRoleProxy && common.ServerRole != common.ServerRoleAll) {
		return func() {}
	}
	listener, err := net.Listen("tcp", address)
	if err != nil {
		common.SysError(common.LogText("redaction capability probe unavailable: %v", err))
		return func() {}
	}
	server := &http.Server{
		Handler:           http.HandlerFunc(RedactionCapabilityProbe),
		ReadHeaderTimeout: 2 * time.Second,
		ReadTimeout:       2 * time.Second,
		WriteTimeout:      2 * time.Second,
		MaxHeaderBytes:    8 << 10,
	}
	server.SetKeepAlivesEnabled(false)
	done := make(chan struct{})
	go func() {
		defer close(done)
		if err := server.Serve(listener); !errors.Is(err, http.ErrServerClosed) {
			common.SysError(common.LogText("redaction capability probe stopped: %v", err))
		}
	}()
	return sync.OnceFunc(func() {
		_ = server.Close()
		<-done
	})
}

// RedactionCapabilityProbe must be served on a separate listener reachable only by the cluster verifier.
func RedactionCapabilityProbe(writer http.ResponseWriter, request *http.Request) {
	writer.Header().Set("Cache-Control", "no-store")
	if request.URL.Path != RedactionCapabilityPath || request.URL.RawQuery != "" {
		writer.WriteHeader(http.StatusNotFound)
		return
	}
	if request.Method != http.MethodGet {
		writer.Header().Set("Allow", http.MethodGet)
		writer.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	podUID := os.Getenv("POD_UID")
	role := common.ServerRole
	if podUID == "" || (role != common.ServerRoleProxy && role != common.ServerRoleAll) {
		writer.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	writer.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(writer).Encode(struct {
		PodUID                 string `json:"pod_uid"`
		Role                   string `json:"role"`
		InputProtocolVersion   int    `json:"input_protocol_version"`
		RestoreProtocolVersion int    `json:"restore_protocol_version"`
	}{podUID, role, redaction_setting.InputProtocolVersion, redaction_setting.RestoreProtocolVersion})
}
