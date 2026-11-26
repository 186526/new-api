// Package systemone holds the wire contract of the TypeSafe System One API:
// the model list, the request and response documents, and the two error
// documents the upstream returns. The relay layer writes these documents, and
// the shapes come from https://api.typesafe.ai/openapi.json
// (components.schemas.ModelMetadataList, ValidationError, HTTPBadRequestError).
package systemone

import "encoding/json"

// ModelMetadata is one entry of GET /v1/models. All three fields are required by
// the upstream schema, so a model with no description or release date still
// carries the keys, empty rather than absent.
type ModelMetadata struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	ReleaseDate string `json:"release_date"`
}

// ModelMetadataList is the whole GET /v1/models document. The schema requires
// the single "models" key and nothing else.
type ModelMetadataList struct {
	Models []ModelMetadata `json:"models"`
}

// NewModelMetadataList answers an empty list rather than a null one, because the
// schema types "models" as an array.
func NewModelMetadataList(models []ModelMetadata) ModelMetadataList {
	if models == nil {
		models = []ModelMetadata{}
	}
	return ModelMetadataList{Models: models}
}

// ValidationError is one element of the "detail" array a 422 carries. Input and
// Context are omitted when empty: the upstream shortens both and a client reads
// only loc, msg and type.
type ValidationError struct {
	Loc   []any  `json:"loc"`
	Msg   string `json:"msg"`
	Type  string `json:"type"`
	Input string `json:"input,omitempty"`
	Ctx   any    `json:"ctx,omitempty"`
}

// FieldValidationError builds the element for one rejected field, with the
// location the upstream uses: the request location ("body") then the field path.
func FieldValidationError(fieldPath []any, msg, errorType string) ValidationError {
	loc := make([]any, 0, len(fieldPath)+1)
	loc = append(loc, "body")
	loc = append(loc, fieldPath...)
	return ValidationError{Loc: loc, Msg: msg, Type: errorType}
}

// DetailError is the "detail" object every error other than a validation
// failure carries, for example the authentication error of a rejected API key.
type DetailError struct {
	ErrorType string `json:"error_type"`
	Message   string `json:"message"`
}

// ValidationDocument wraps the array in the single "detail" key the 422 schema
// requires.
func ValidationDocument(errors []ValidationError) map[string]any {
	if errors == nil {
		errors = []ValidationError{}
	}
	return map[string]any{"detail": errors}
}

// DetailDocument wraps the object in the same "detail" key the 400 schema
// requires.
func DetailDocument(errorType, message string) map[string]any {
	return map[string]any{"detail": DetailError{ErrorType: errorType, Message: message}}
}

// MarshalDetailDocument returns the bytes of DetailDocument, for the paths that
// write a body themselves instead of through gin.
func MarshalDetailDocument(errorType, message string) ([]byte, error) {
	return json.Marshal(DetailDocument(errorType, message))
}

// Error types the upstream writes in "detail.error_type". They are the
// vocabulary https://api.typesafe.ai/api/errors.md describes.
const (
	ErrorTypeAuthentication = "authentication_error"
	ErrorTypeInvalidRequest = "invalid_request_error"
	ErrorTypeRateLimit      = "rate_limit_error"
	ErrorTypeServer         = "server_error"
)

// ErrorTypeForStatus maps a status code to the "error_type" the upstream uses
// for it. A status the vocabulary does not name keeps the closest wider class,
// so a caller always reads a known value.
func ErrorTypeForStatus(status int) string {
	switch {
	case status == 401 || status == 403:
		return ErrorTypeAuthentication
	case status == 429:
		return ErrorTypeRateLimit
	case status >= 500:
		return ErrorTypeServer
	default:
		return ErrorTypeInvalidRequest
	}
}

// MaxRequestBodyBytes is the ceiling the upstream applies to one System One
// request body, compressed or not. A larger body is refused before it is read,
// because the transfer would end in the same refusal.
const MaxRequestBodyBytes = 32 << 20
