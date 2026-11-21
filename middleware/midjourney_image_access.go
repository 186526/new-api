package middleware

import "time"

const (
	midjourneyImageInvalidAttempt = "invalid-attempt"
	midjourneyImageAccessObject   = "image"
)

func midjourneyImageAccessIP(ip string) string {
	if ip == "" {
		return "unknown"
	}
	return ip
}

// AcquireMidjourneyImageAccess counts one rejected Midjourney image request
// against the caller's per-minute budget. It reports false once that budget is
// spent, so a caller must then answer 429 instead of the 404 it would otherwise
// write, exactly as the task artifact route does for the same attempt.
func AcquireMidjourneyImageAccess(ip, mjId string) bool {
	return taskArtifactAnonymousLimiter.invalidAttempt(time.Now(), midjourneyImageAccessIP(ip)+"\x00"+midjourneyImageInvalidAttempt)
}

// HoldMidjourneyImageAccess takes one concurrency slot for this caller and
// image. It reports false when the global, per-IP or per-image limit is
// reached, and the returned function gives the slot back.
func HoldMidjourneyImageAccess(ip, mjId string) (func(), bool) {
	return taskArtifactAnonymousLimiter.acquire(midjourneyImageAccessIP(ip), midjourneyImageAccessObject, mjId)
}
