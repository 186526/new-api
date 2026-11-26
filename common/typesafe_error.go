package common

import (
	"fmt"
	"net/http"

	"github.com/QuantumNous/new-api/relaykit/systemone"

	"github.com/gin-gonic/gin"
)

// WriteTypeSafeError answers a failed request in the TypeSafe document family:
// {"detail":{...}} for every failure, with the apology the upstream writes, and
// the request id appended so a caller can quote it. The request id is the same
// suffix the OpenAI shape appends.
func WriteTypeSafeError(c *gin.Context, status int, message string) {
	if c == nil {
		return
	}
	message = MessageWithRequestId(message, c.GetString(RequestIdKey))
	c.JSON(status, systemone.DetailDocument(systemone.ErrorTypeForStatus(status), message))
}

// WriteTypeSafeValidationError answers a request the schema rejected with the
// array form of "detail". It is the only 4xx that carries an array, which is
// what a generated client reads to locate the offending field.
func WriteTypeSafeValidationError(c *gin.Context, status int, validationErrors []systemone.ValidationError) {
	if c == nil {
		return
	}
	c.JSON(status, systemone.ValidationDocument(validationErrors))
}

// TypeSafeStatusForErrorCode answers the status the TypeSafe document uses for a
// new-api error, so a caller reads the meaning rather than the internal code.
// Everything the vocabulary does not name keeps the status the error already
// carried.
func TypeSafeStatusForErrorCode(status int, code string) int {
	switch code {
	case "invalid_request", "bad_request_body", "model_not_found":
		if status == http.StatusInternalServerError {
			return http.StatusBadRequest
		}
	}
	return status
}

// TypeSafeMessage builds the one-sentence explanation the object form carries,
// preferring the error's own text and falling back to the status phrase.
func TypeSafeMessage(status int, err error) string {
	if err != nil {
		if text := err.Error(); text != "" {
			return text
		}
	}
	return fmt.Sprintf("Request failed with status %d", status)
}
