package gemini

import (
	"os"
	"testing"

	"github.com/gin-gonic/gin"
)

// Parallel tests create gin contexts, and gin keeps its mode in a package
// variable, so setting it from each test races. Set once before any test runs.
func TestMain(m *testing.M) {
	gin.SetMode(gin.TestMode)
	os.Exit(m.Run())
}
