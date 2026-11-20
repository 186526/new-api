package gemini

import (
	"os"
	"testing"

	"github.com/QuantumNous/new-api/constant"
	"github.com/gin-gonic/gin"
)

// TestMain gives the tests the streaming timeout they used to set piecemeal in
// constant.StreamingTimeout. The tests run in parallel, so that package global
// is written once here and never by a test.
func TestMain(m *testing.M) {
	gin.SetMode(gin.TestMode)
	constant.StreamingTimeout = 300
	os.Exit(m.Run())
}
