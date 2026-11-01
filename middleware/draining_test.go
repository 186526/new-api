package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

func newDrainingServer(draining *Draining) *gin.Engine {
	gin.SetMode(gin.TestMode)
	server := gin.New()
	server.GET("/healthz", draining.Readiness)
	server.Use(draining.RejectNewRequests())
	server.POST("/v1/chat/completions", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"relayed": true})
	})
	return server
}

func TestReadinessStaysOKBeforeShutdown(t *testing.T) {
	draining := NewDraining()
	server := newDrainingServer(draining)

	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/healthz", nil))

	assert.Equal(t, http.StatusOK, recorder.Code)
}

func TestReadinessReportsDrainingAfterShutdown(t *testing.T) {
	draining := NewDraining()
	server := newDrainingServer(draining)
	draining.Begin()

	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/healthz", nil))

	assert.Equal(t, http.StatusServiceUnavailable, recorder.Code)
}

func TestRelayRequestsAreRefusedOnlyAfterShutdown(t *testing.T) {
	draining := NewDraining()
	server := newDrainingServer(draining)

	accepted := httptest.NewRecorder()
	server.ServeHTTP(accepted, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil))
	assert.Equal(t, http.StatusOK, accepted.Code)

	draining.Begin()

	refused := httptest.NewRecorder()
	server.ServeHTTP(refused, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil))
	assert.Equal(t, http.StatusServiceUnavailable, refused.Code)
}
