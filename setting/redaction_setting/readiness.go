package redaction_setting

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"time"

	"github.com/QuantumNous/new-api/common"
)

const ReadinessPathEnv = "REDACTION_READINESS_FILE"
const ReadinessVersion = 1
const MaxReadinessLifetime = 30 * time.Second
const maxReadinessBytes = 256 << 10

// A document alone cannot authorize release before deployment isolation and verification are demonstrated.
const readinessReleaseVerified = false

type readinessDocument struct {
	Version      int       `json:"version"`
	VerifiedAt   time.Time `json:"verified_at"`
	ExpiresAt    time.Time `json:"expires_at"`
	RelayPodUIDs []string  `json:"relay_pod_uids"`
	EnableKeys   bool      `json:"enable_keys"`
	Restore      bool      `json:"restore"`
}

type ReadinessState struct {
	EnableKeys       bool
	ConfigureRestore bool
	RestoreDispatch  bool
}

func Readiness() ReadinessState {
	if !readinessReleaseVerified {
		return ReadinessState{}
	}
	path := os.Getenv(ReadinessPathEnv)
	if path == "" {
		return ReadinessState{}
	}
	file, err := os.Open(path)
	if err != nil {
		return ReadinessState{}
	}
	defer file.Close()
	contents, err := io.ReadAll(io.LimitReader(file, maxReadinessBytes+1))
	if err != nil || len(contents) > maxReadinessBytes {
		return ReadinessState{}
	}
	return parseReadiness(contents, time.Now(), common.ServerRole, os.Getenv("POD_UID"))
}

func parseReadiness(contents []byte, now time.Time, role, podUID string) ReadinessState {
	var document readinessDocument
	decoder := json.NewDecoder(bytes.NewReader(contents))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&document) != nil || decoder.Decode(new(json.RawMessage)) != io.EOF || !document.validAt(now) {
		return ReadinessState{}
	}
	admin := role == common.ServerRoleAdmin || role == common.ServerRoleAll
	relay := role == common.ServerRoleProxy || role == common.ServerRoleAll
	state := ReadinessState{EnableKeys: admin && document.EnableKeys, ConfigureRestore: admin && document.Restore}
	if relay && document.Restore {
		for _, verifiedUID := range document.RelayPodUIDs {
			if verifiedUID == podUID {
				state.RestoreDispatch = true
				break
			}
		}
	}
	return state
}

func (document readinessDocument) validAt(now time.Time) bool {
	if document.Version != ReadinessVersion || document.VerifiedAt.IsZero() || now.Before(document.VerifiedAt) ||
		!now.Before(document.ExpiresAt) || document.ExpiresAt.Sub(document.VerifiedAt) > MaxReadinessLifetime || len(document.RelayPodUIDs) == 0 {
		return false
	}
	seen := make(map[string]struct{}, len(document.RelayPodUIDs))
	for _, uid := range document.RelayPodUIDs {
		if _, duplicate := seen[uid]; duplicate || uid == "" {
			return false
		}
		seen[uid] = struct{}{}
	}
	return true
}

func ResponseRestoreReady() bool { return Readiness().RestoreDispatch }
