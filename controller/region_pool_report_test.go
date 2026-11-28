package controller

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestRegionReportV2RequiresItsOwnRegionCredential(t *testing.T) {
	legacy := strings.Repeat("l", 32)
	regional := strings.Repeat("v", 32)
	t.Setenv("REGION_INTERNAL_TOKEN", legacy)
	_, err := NewRegionReportV2Handler(map[string]string{"west": legacy})
	require.Error(t, err)
	_, err = NewRegionReportV2Handler(map[string]string{"west": regional, "east": regional})
	require.Error(t, err)
	handler, err := NewRegionReportV2Handler(map[string]string{"west": regional})
	require.NoError(t, err)
	engine := gin.New()
	engine.POST(service.RegionReportV2Path, handler)
	payload := `{"version":2,"region":"east","subject_type":"user","subject_id":"1","generation":1,"seq":1,"delta":10,"payload_hash":"` + strings.Repeat("a", 64) + `"}`
	for _, credential := range []struct{ header, value string }{
		{"X-Region-Token", legacy}, {service.RegionReportV2TokenHeader, legacy}, {service.RegionReportV2TokenHeader, regional},
	} {
		request := httptest.NewRequest(http.MethodPost, service.RegionReportV2Path, strings.NewReader(payload))
		request.Header.Set(credential.header, credential.value)
		response := httptest.NewRecorder()
		engine.ServeHTTP(response, request)
		require.Equal(t, http.StatusUnauthorized, response.Code)
	}
}
