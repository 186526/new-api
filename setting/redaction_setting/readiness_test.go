package redaction_setting

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
)

func readinessFixture(now time.Time) readinessDocument {
	return readinessDocument{
		Version: ReadinessVersion, VerifiedAt: now,
		ExpiresAt: now.Add(MaxReadinessLifetime), RelayPodUIDs: []string{"fixture-pod"},
		EnableKeys: true, Restore: true,
	}
}

func encodeReadinessFixture(t *testing.T, document readinessDocument) []byte {
	t.Helper()
	contents, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	return contents
}

func TestParseReadinessRoleAndPod(t *testing.T) {
	now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	contents := encodeReadinessFixture(t, readinessFixture(now))
	for _, test := range []struct {
		name, role, podUID string
		want               ReadinessState
	}{
		{"admin", common.ServerRoleAdmin, "fixture-pod", ReadinessState{EnableKeys: true, ConfigureRestore: true}},
		{"proxy", common.ServerRoleProxy, "fixture-pod", ReadinessState{RestoreDispatch: true}},
		{"proxy different pod", common.ServerRoleProxy, "other-pod", ReadinessState{}},
		{"proxy missing pod", common.ServerRoleProxy, "", ReadinessState{}},
		{"all", common.ServerRoleAll, "fixture-pod", ReadinessState{EnableKeys: true, ConfigureRestore: true, RestoreDispatch: true}},
		{"unknown role", "unknown", "fixture-pod", ReadinessState{}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := parseReadiness(contents, now, test.role, test.podUID); got != test.want {
				t.Fatalf("parseReadiness = %+v, want %+v", got, test.want)
			}
		})
	}
}

func TestParseReadinessRejectsInvalidDocuments(t *testing.T) {
	now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		name   string
		mutate func(*readinessDocument)
	}{
		{"wrong version", func(doc *readinessDocument) { doc.Version++ }},
		{"future verification", func(doc *readinessDocument) { doc.VerifiedAt = now.Add(time.Second) }},
		{"expired at boundary", func(doc *readinessDocument) {
			doc.VerifiedAt, doc.ExpiresAt = now.Add(-time.Second), now
		}},
		{"lifetime over limit", func(doc *readinessDocument) { doc.ExpiresAt = doc.ExpiresAt.Add(time.Nanosecond) }},
		{"missing pod list", func(doc *readinessDocument) { doc.RelayPodUIDs = nil }},
		{"duplicate pod", func(doc *readinessDocument) { doc.RelayPodUIDs = []string{"fixture-pod", "fixture-pod"} }},
		{"empty pod UID", func(doc *readinessDocument) { doc.RelayPodUIDs = []string{"fixture-pod", ""} }},
	} {
		t.Run(test.name, func(t *testing.T) {
			document := readinessFixture(now)
			test.mutate(&document)
			assertReadinessClosed(t, encodeReadinessFixture(t, document), now)
		})
	}

	valid := encodeReadinessFixture(t, readinessFixture(now))
	t.Run("unknown field", func(t *testing.T) {
		contents := `{"unexpected":true,` + string(valid[1:])
		assertReadinessClosed(t, []byte(contents), now)
	})
	t.Run("trailing document", func(t *testing.T) {
		assertReadinessClosed(t, append(valid, []byte(` {}`)...), now)
	})
}

func assertReadinessClosed(t *testing.T, contents []byte, now time.Time) {
	t.Helper()
	if got := parseReadiness(contents, now, common.ServerRoleAll, "fixture-pod"); got != (ReadinessState{}) {
		t.Fatalf("parseReadiness = %+v, want all capabilities disabled", got)
	}
}

func TestReadinessRemainsDisabledWithValidDocument(t *testing.T) {
	previousRole := common.ServerRole
	common.ServerRole = common.ServerRoleAll
	t.Cleanup(func() { common.ServerRole = previousRole })
	t.Setenv("POD_UID", "fixture-pod")
	path := filepath.Join(t.TempDir(), "readiness.json")
	t.Setenv(ReadinessPathEnv, path)
	contents := encodeReadinessFixture(t, readinessFixture(time.Now().Add(-time.Second)))
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	if got := Readiness(); got != (ReadinessState{}) {
		t.Fatalf("Readiness = %+v, want all capabilities disabled", got)
	}
	if ResponseRestoreReady() {
		t.Fatal("response restoration is enabled")
	}
}
