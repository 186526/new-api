package router

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestToolsAdminRoutesRequireAuthentication(t *testing.T) {
	engine := gin.New()
	RegisterToolsAdminRoutes(engine.Group("/api"))
	for _, route := range engine.Routes() {
		t.Run(route.Method+route.Path, func(t *testing.T) {
			response := httptest.NewRecorder()
			engine.ServeHTTP(response, httptest.NewRequest(route.Method, route.Path, nil))
			require.Equal(t, http.StatusUnauthorized, response.Code)
		})
	}
	require.Len(t, engine.Routes(), 6)
}
