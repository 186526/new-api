package systemone

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDetailDocuments(t *testing.T) {
	encoded, err := json.Marshal(DetailDocument(ErrorTypeAuthentication, "Invalid API key"))
	require.NoError(t, err)
	assert.JSONEq(t, `{"detail":{"error_type":"authentication_error","message":"Invalid API key"}}`, string(encoded))

	encoded, err = json.Marshal(ValidationDocument([]ValidationError{
		FieldValidationError([]any{"questions", "q", "criteria"}, "Field required", "missing"),
	}))
	require.NoError(t, err)
	assert.JSONEq(t, `{"detail":[{"loc":["body","questions","q","criteria"],"msg":"Field required","type":"missing"}]}`, string(encoded))
}

func TestNewModelMetadataListWritesAnArray(t *testing.T) {
	encoded, err := json.Marshal(NewModelMetadataList(nil))
	require.NoError(t, err)
	assert.JSONEq(t, `{"models":[]}`, string(encoded))

	encoded, err = json.Marshal(NewModelMetadataList([]ModelMetadata{{Name: "jev-latest"}}))
	require.NoError(t, err)
	// release_date is required by the schema, so it stays present when empty.
	assert.JSONEq(t, `{"models":[{"name":"jev-latest","description":"","release_date":""}]}`, string(encoded))
}

func TestErrorTypeForStatus(t *testing.T) {
	assert.Equal(t, ErrorTypeAuthentication, ErrorTypeForStatus(401))
	assert.Equal(t, ErrorTypeAuthentication, ErrorTypeForStatus(403))
	assert.Equal(t, ErrorTypeRateLimit, ErrorTypeForStatus(429))
	assert.Equal(t, ErrorTypeServer, ErrorTypeForStatus(503))
	assert.Equal(t, ErrorTypeInvalidRequest, ErrorTypeForStatus(422))
}
