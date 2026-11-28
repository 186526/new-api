package router

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/service/authz"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestQuotaReconciliationRoutesRemainDisabled(t *testing.T) {
	engine := gin.New()
	RegisterQuotaReconciliationRoutes(engine.Group("/api"))
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		response := quotaReconciliationRequest(engine, method, "/api/audit/quota-reconciliation", "")
		require.Equal(t, http.StatusNotFound, response.Code, method)
	}
}

func TestQuotaReconciliationRoutePermissions(t *testing.T) {
	db := setupQuotaReconciliationAuth(t)
	engine := gin.New()
	mountQuotaReconciliationRoutes(engine.Group("/api"))
	const path = "/api/audit/quota-reconciliation"

	for _, scenario := range []struct {
		name       string
		role       int
		grantAudit bool
		read       int
		post       int
	}{
		{"anonymous", 0, false, http.StatusUnauthorized, http.StatusUnauthorized},
		{"ordinary session", common.RoleCommonUser, false, http.StatusForbidden, http.StatusForbidden},
		{"admin without audit permission", common.RoleAdminUser, false, http.StatusForbidden, http.StatusForbidden},
		{"admin with audit permission", common.RoleAdminUser, true, http.StatusOK, http.StatusForbidden},
		{"root session", common.RoleRootUser, false, http.StatusOK, http.StatusBadRequest},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			var bearer string
			if scenario.role != 0 {
				bearer = quotaReconciliationSession(t, db, scenario.role, scenario.grantAudit)
			}
			read := quotaReconciliationRequest(engine, http.MethodGet, path+"?request_id=missing-fixture", bearer)
			require.Equal(t, scenario.read, read.Code, read.Body.String())
			if scenario.read == http.StatusOK {
				require.Contains(t, read.Body.String(), `"confirmable":false`)
			}
			// An empty body reaches validation only after the write guards pass.
			post := quotaReconciliationRequest(engine, http.MethodPost, path, bearer)
			require.Equal(t, scenario.post, post.Code, post.Body.String())
			if scenario.post == http.StatusBadRequest {
				require.Contains(t, post.Body.String(), "invalid quota review input")
			}
		})
	}
}

func setupQuotaReconciliationAuth(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.UserSession{}, &model.AuditLog{}, &model.QuotaRefundReceipt{}, &model.QuotaReconciliation{}, &model.CasbinRule{}))
	previousMaster := common.IsMasterNode
	t.Cleanup(func() { common.IsMasterNode = previousMaster })
	common.IsMasterNode = false
	require.NoError(t, authz.Init(db))

	previousDB, previousLogDB := model.DB, model.LOG_DB
	previousRedis, previousSecret := common.RedisEnabled, common.SessionSecret
	previousDatabaseType := common.MainDatabaseType()
	t.Cleanup(func() {
		model.DB, model.LOG_DB = previousDB, previousLogDB
		common.RedisEnabled, common.SessionSecret = previousRedis, previousSecret
		common.SetMainDatabaseType(previousDatabaseType)
	})
	model.DB, model.LOG_DB = db, db
	common.RedisEnabled = false
	common.SessionSecret = "quota-reconciliation-test-only-secret"
	common.SetMainDatabaseType(common.DatabaseTypeSQLite)
	return db
}

func quotaReconciliationSession(t *testing.T, db *gorm.DB, role int, grantAudit bool) string {
	t.Helper()
	identity := fmt.Sprintf("review-%d-%t", role, grantAudit)
	user := model.User{Username: identity, AffCode: identity, Role: role, Status: common.UserStatusEnabled, AuthVersion: 1}
	require.NoError(t, db.Create(&user).Error)
	if grantAudit {
		require.NoError(t, db.Create(&model.CasbinRule{
			Ptype: "p", V0: authz.UserSubject(user.Id), V1: authz.AuditRead.Resource, V2: authz.AuditRead.Action, V3: authz.EffectAllow,
		}).Error)
		require.NoError(t, authz.ReloadPolicy())
	}
	bundle, err := service.CreateLoginSession(user.Id, "password", "127.0.0.1", "quota reconciliation test")
	require.NoError(t, err)
	return bundle.AccessToken
}

func quotaReconciliationRequest(engine *gin.Engine, method, path, bearer string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, nil)
	if bearer != "" {
		request.Header.Set("Authorization", "Bearer "+bearer)
	}
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	return response
}
