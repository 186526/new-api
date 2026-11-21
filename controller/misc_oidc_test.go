package controller

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// readStatus runs GetStatus against a bare gin context, with an optional
// dashboard bearer token, so the anonymous and signed-in bodies can be compared.
func readStatus(t *testing.T, authorization string) map[string]any {
	t.Helper()
	response := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(response)
	context.Request = httptest.NewRequest(http.MethodGet, "/api/status", nil)
	if authorization != "" {
		context.Request.Header.Set("Authorization", authorization)
	}

	GetStatus(context)

	var payload struct {
		Success bool           `json:"success"`
		Data    map[string]any `json:"data"`
	}
	require.NoError(t, common.Unmarshal(response.Body.Bytes(), &payload))
	require.True(t, payload.Success)
	return payload.Data
}

// The admin sidebar-module config is not for anonymous readers: it is admin
// console configuration, and the login page does not consume it.
func TestGetStatusKeepsSidebarModulesAdminFromAnonymousCallers(t *testing.T) {
	previousMap := common.OptionMap
	common.OptionMap = map[string]string{}
	t.Cleanup(func() { common.OptionMap = previousMap })

	data := readStatus(t, "")

	assert.NotContains(t, data, "SidebarModulesAdmin")
	// HeaderNavModules stays public: the top navigation renders on the
	// signed-out pages and the backend enforces the same value on those routes.
	assert.Contains(t, data, "HeaderNavModules")
}

// A signed-in session still reads the value, because signed-in pages outside
// the admin console gate themselves on it (the playground route reads the chat
// section).
func TestGetStatusReturnsSidebarModulesAdminToSignedInCallers(t *testing.T) {
	user, identity := setupSecurityEnrollmentTest(t)
	require.NoError(t, model.DB.Model(user).Update("role", common.RoleAdminUser).Error)
	require.NoError(t, model.PublishUserAuthCache(user.Id))
	bundle, err := service.CreateLoginSession(user.Id, "password", "127.0.0.1", "status-test")
	require.NoError(t, err)
	require.NotEmpty(t, identity.SessionID)

	previousMap := common.OptionMap
	common.OptionMap = map[string]string{"SidebarModulesAdmin": `{"admin":{"enabled":false}}`}
	t.Cleanup(func() { common.OptionMap = previousMap })

	data := readStatus(t, "Bearer "+bundle.AccessToken)

	assert.Equal(t, `{"admin":{"enabled":false}}`, data["SidebarModulesAdmin"])
}

// A bearer token that is well-formed but not a live dashboard session must not
// be able to read the value: the endpoint is anonymous and accepts anything.
func TestGetStatusKeepsSidebarModulesAdminFromAStaleToken(t *testing.T) {
	previousMap := common.OptionMap
	common.OptionMap = map[string]string{"SidebarModulesAdmin": `{"admin":{"enabled":false}}`}
	t.Cleanup(func() { common.OptionMap = previousMap })

	data := readStatus(t, "Bearer not-a-dashboard-token")

	assert.NotContains(t, data, "SidebarModulesAdmin")
}

func TestGetStatusReturnsEffectiveOIDCDisplayName(t *testing.T) {
	settings := system_setting.GetOIDCSettings()
	originalDisplayName := settings.DisplayName
	originalOptionMap := common.OptionMap
	t.Cleanup(func() {
		settings.DisplayName = originalDisplayName
		common.OptionMap = originalOptionMap
	})
	common.OptionMap = map[string]string{}

	tests := []struct {
		name        string
		displayName string
		want        string
	}{
		{
			name:        "custom name is trimmed",
			displayName: "  Acme SSO  ",
			want:        "Acme SSO",
		},
		{
			name:        "whitespace-only name falls back",
			displayName: "   ",
			want:        "OIDC",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			settings.DisplayName = tt.displayName
			response := httptest.NewRecorder()
			context, _ := gin.CreateTestContext(response)
			context.Request = httptest.NewRequest(http.MethodGet, "/api/status", nil)

			GetStatus(context)

			var payload struct {
				Success bool           `json:"success"`
				Data    map[string]any `json:"data"`
			}
			require.NoError(t, common.Unmarshal(response.Body.Bytes(), &payload))
			require.True(t, payload.Success)
			assert.Equal(t, tt.want, payload.Data["oidc_display_name"])
		})
	}
}
