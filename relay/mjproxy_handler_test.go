package relay

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/system_setting"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

const (
	midjourneyImageTestId     = "midjourney-image-test-1"
	midjourneyImageTestStatus = "SUCCESS"
)

// setupMidjourneyImageRoute points the image route at an in-memory database
// holding one task whose image the stub upstream serves, and returns the router
// plus the path counter of that upstream.
func setupMidjourneyImageRoute(t *testing.T) (*gin.Engine, *atomic.Int32) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	previousDB := model.DB
	previousCryptoSecret := common.CryptoSecret
	previousServerAddress := system_setting.ServerAddress
	previousForward := setting.MjForwardUrlEnabled
	previousSSRF := system_setting.GetFetchSetting().EnableSSRFProtection
	previousPrivate := system_setting.GetFetchSetting().AllowPrivateIp
	service.InitHttpClient()
	t.Cleanup(func() {
		model.DB = previousDB
		common.CryptoSecret = previousCryptoSecret
		system_setting.ServerAddress = previousServerAddress
		setting.MjForwardUrlEnabled = previousForward
		system_setting.GetFetchSetting().EnableSSRFProtection = previousSSRF
		system_setting.GetFetchSetting().AllowPrivateIp = previousPrivate
	})

	hits := &atomic.Int32{}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte("stub-image-bytes"))
	}))
	t.Cleanup(upstream.Close)

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.Midjourney{}, &model.Channel{}))
	model.DB = db
	require.NoError(t, db.Create(&model.Midjourney{
		UserId:    7,
		MjId:      midjourneyImageTestId,
		ImageUrl:  upstream.URL + "/image.png",
		Status:    midjourneyImageTestStatus,
		ChannelId: 4242,
	}).Error)

	common.CryptoSecret = "midjourney-image-test-secret"
	system_setting.ServerAddress = "https://relay.example.test"
	system_setting.GetFetchSetting().EnableSSRFProtection = false
	system_setting.GetFetchSetting().AllowPrivateIp = true
	setting.MjForwardUrlEnabled = true

	router := gin.New()
	router.GET("/mj/image/:id", RelayMidjourneyImage)
	return router, hits
}

func getMidjourneyImage(router http.Handler, rawTarget string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodGet, rawTarget, nil)
	request.RemoteAddr = "198.51.100.10:1234"
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}

func signedMidjourneyImagePath(t *testing.T, mjId string) string {
	t.Helper()
	access, err := service.IssueTaskArtifactAccess(mjId, midjourneyImageCapabilityObject)
	require.NoError(t, err)
	return "/mj/image/" + url.PathEscape(mjId) + "?" + service.TaskArtifactAccessQueryParameter + "=" + url.QueryEscape(access)
}

func TestBuildMidjourneyImageURLEmitsAVerifiableSignature(t *testing.T) {
	previousCryptoSecret := common.CryptoSecret
	previousServerAddress := system_setting.ServerAddress
	t.Cleanup(func() {
		common.CryptoSecret = previousCryptoSecret
		system_setting.ServerAddress = previousServerAddress
	})
	common.CryptoSecret = "midjourney-image-url-secret"
	system_setting.ServerAddress = "https://relay.example.test/"

	finished := BuildMidjourneyImageURL(midjourneyImageTestId, true)
	parsed, err := url.Parse(finished)
	require.NoError(t, err)
	assert.Equal(t, "/mj/image/"+midjourneyImageTestId, parsed.Path)
	assert.Empty(t, parsed.Query().Get("rand"), "a finished task needs no cache buster")
	assert.True(t, service.VerifyTaskArtifactAccess(parsed.Query().Get(service.TaskArtifactAccessQueryParameter), midjourneyImageTestId, midjourneyImageCapabilityObject),
		"the emitted URL verifies for its own id")
	assert.False(t, service.VerifyTaskArtifactAccess(parsed.Query().Get(service.TaskArtifactAccessQueryParameter), midjourneyImageTestId+"-other", midjourneyImageCapabilityObject),
		"the signature is bound to the id and nothing else")

	running := BuildMidjourneyImageURL(midjourneyImageTestId, false)
	runningParsed, err := url.Parse(running)
	require.NoError(t, err)
	assert.NotEmpty(t, runningParsed.Query().Get("rand"), "a running task keeps the cache buster")
	assert.True(t, service.VerifyTaskArtifactAccess(runningParsed.Query().Get(service.TaskArtifactAccessQueryParameter), midjourneyImageTestId, midjourneyImageCapabilityObject),
		"the cache buster is not part of the signed value")

	common.CryptoSecret = ""
	assert.Empty(t, BuildMidjourneyImageURL(midjourneyImageTestId, true), "no secret, no signed URL")
}

func TestMidjourneyTaskDtoCarriesTheSignedImageURL(t *testing.T) {
	previousCryptoSecret := common.CryptoSecret
	previousServerAddress := system_setting.ServerAddress
	previousForward := setting.MjForwardUrlEnabled
	t.Cleanup(func() {
		common.CryptoSecret = previousCryptoSecret
		system_setting.ServerAddress = previousServerAddress
		setting.MjForwardUrlEnabled = previousForward
	})
	common.CryptoSecret = "midjourney-image-url-secret"
	system_setting.ServerAddress = "https://relay.example.test"
	setting.MjForwardUrlEnabled = true

	running := coverMidjourneyTaskDto(nil, &model.Midjourney{MjId: midjourneyImageTestId, ImageUrl: "https://upstream.test/1.png", Status: "IN_PROGRESS"})
	parsed, err := url.Parse(running.ImageUrl)
	require.NoError(t, err)
	assert.Equal(t, "/mj/image/"+midjourneyImageTestId, parsed.Path)
	assert.NotEmpty(t, parsed.Query().Get("rand"))
	assert.True(t, service.VerifyTaskArtifactAccess(parsed.Query().Get(service.TaskArtifactAccessQueryParameter), midjourneyImageTestId, midjourneyImageCapabilityObject))

	finished := coverMidjourneyTaskDto(nil, &model.Midjourney{MjId: midjourneyImageTestId, ImageUrl: "https://upstream.test/1.png", Status: "SUCCESS"})
	finishedParsed, err := url.Parse(finished.ImageUrl)
	require.NoError(t, err)
	assert.Empty(t, finishedParsed.Query().Get("rand"))
	assert.True(t, service.VerifyTaskArtifactAccess(finishedParsed.Query().Get(service.TaskArtifactAccessQueryParameter), midjourneyImageTestId, midjourneyImageCapabilityObject))
}

func TestMidjourneyImageRouteNeedsTheSignature(t *testing.T) {
	router, upstreamHits := setupMidjourneyImageRoute(t)

	signed := getMidjourneyImage(router, signedMidjourneyImagePath(t, midjourneyImageTestId))
	require.Equal(t, http.StatusOK, signed.Code, signed.Body.String())
	assert.Equal(t, "stub-image-bytes", signed.Body.String())
	assert.Equal(t, "image/png", signed.Header().Get("Content-Type"))
	assert.Equal(t, "private, no-store", signed.Header().Get("Cache-Control"))

	access, err := service.IssueTaskArtifactAccess(midjourneyImageTestId, midjourneyImageCapabilityObject)
	require.NoError(t, err)
	wrongId := midjourneyImageTestId[:len(midjourneyImageTestId)-1] + "2"
	tampered := utils2FlippedSignature(t, access)

	unsigned := getMidjourneyImage(router, "/mj/image/"+midjourneyImageTestId)
	unknown := getMidjourneyImage(router, "/mj/image/midjourney-image-does-not-exist")
	badSignature := getMidjourneyImage(router, "/mj/image/"+midjourneyImageTestId+"?"+service.TaskArtifactAccessQueryParameter+"="+url.QueryEscape(tampered))
	otherId := getMidjourneyImage(router, "/mj/image/"+wrongId+"?"+service.TaskArtifactAccessQueryParameter+"="+url.QueryEscape(access))

	for name, response := range map[string]*httptest.ResponseRecorder{
		"unsigned":                unsigned,
		"unknown id":              unknown,
		"bad signature":           badSignature,
		"signature of another id": otherId,
	} {
		assert.Equal(t, http.StatusNotFound, response.Code, "%s must not read the image", name)
		assert.Equal(t, "private, no-store", response.Header().Get("Cache-Control"), name)
		assert.Equal(t, unknown.Body.String(), response.Body.String(), "%s must be told exactly what an unknown id is told", name)
		assert.NotContains(t, response.Body.String(), "stub-image-bytes", name)
	}
	assert.NotContains(t, strings.ToLower(unsigned.Body.String()), "signature")
	assert.Equal(t, int32(1), upstreamHits.Load(), "only the signed request reaches the image")
}

// utils2FlippedSignature returns a signature of the right shape that is not the
// one it was given, so the route reads it as a wrong signature rather than as a
// malformed parameter.
func utils2FlippedSignature(t *testing.T, access string) string {
	t.Helper()
	flipped := []byte(access)
	if flipped[0] == 'A' {
		flipped[0] = 'B'
	} else {
		flipped[0] = 'A'
	}
	return string(flipped)
}
