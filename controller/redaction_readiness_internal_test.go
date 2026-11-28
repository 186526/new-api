package controller

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/QuantumNous/new-api/common"
)

func TestRedactionCapabilityProbeContract(t *testing.T) {
	previousRole := common.ServerRole
	t.Cleanup(func() { common.ServerRole = previousRole })
	const podUID = "fixture-pod"
	for _, test := range []struct {
		name, role, uid, method, target string
		status                          int
	}{
		{"proxy", common.ServerRoleProxy, podUID, http.MethodGet, RedactionCapabilityPath, http.StatusOK},
		{"all", common.ServerRoleAll, podUID, http.MethodGet, RedactionCapabilityPath, http.StatusOK},
		{"admin", common.ServerRoleAdmin, podUID, http.MethodGet, RedactionCapabilityPath, http.StatusServiceUnavailable},
		{"missing UID", common.ServerRoleProxy, "", http.MethodGet, RedactionCapabilityPath, http.StatusServiceUnavailable},
		{"wrong path", common.ServerRoleProxy, podUID, http.MethodGet, "/healthz", http.StatusNotFound},
		{"query", common.ServerRoleProxy, podUID, http.MethodGet, RedactionCapabilityPath + "?format=json", http.StatusNotFound},
		{"wrong method", common.ServerRoleProxy, podUID, http.MethodPost, RedactionCapabilityPath, http.StatusMethodNotAllowed},
	} {
		t.Run(test.name, func(t *testing.T) {
			common.ServerRole = test.role
			t.Setenv("POD_UID", test.uid)
			response := httptest.NewRecorder()
			RedactionCapabilityProbe(response, httptest.NewRequest(test.method, test.target, nil))
			if response.Code != test.status || response.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("probe returned status %d and headers %v; want status %d and no-store", response.Code, response.Header(), test.status)
			}
			if test.status == http.StatusOK {
				assertRedactionCapabilityResponse(t, response, test.uid, test.role)
				return
			}
			if response.Body.Len() != 0 {
				t.Fatalf("error response has a body: %q", response.Body.String())
			}
			if test.status == http.StatusMethodNotAllowed && response.Header().Get("Allow") != http.MethodGet {
				t.Fatalf("Allow = %q, want GET", response.Header().Get("Allow"))
			}
		})
	}
}

func assertRedactionCapabilityResponse(t *testing.T, response *httptest.ResponseRecorder, uid, role string) {
	t.Helper()
	if got := response.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", got)
	}
	var got map[string]json.RawMessage
	if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	want := map[string]json.RawMessage{
		"pod_uid":                  json.RawMessage(`"` + uid + `"`),
		"role":                     json.RawMessage(`"` + role + `"`),
		"input_protocol_version":   json.RawMessage(`1`),
		"restore_protocol_version": json.RawMessage(`1`),
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("probe response = %s, want only UID, role and protocol versions 1", response.Body)
	}
}
