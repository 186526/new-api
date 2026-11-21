package controller

import (
	"net/url"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/system_setting"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMidjourneyItemsWithSignedImageURLSignsEveryRow covers the task list the
// dashboard reads: every row carries a signature that verifies for that row's
// id, and the cache buster appears only while the task is still running.
func TestMidjourneyItemsWithSignedImageURLSignsEveryRow(t *testing.T) {
	previousCryptoSecret := common.CryptoSecret
	previousServerAddress := system_setting.ServerAddress
	previousForward := setting.MjForwardUrlEnabled
	t.Cleanup(func() {
		common.CryptoSecret = previousCryptoSecret
		system_setting.ServerAddress = previousServerAddress
		setting.MjForwardUrlEnabled = previousForward
	})
	common.CryptoSecret = "midjourney-list-secret"
	system_setting.ServerAddress = "https://relay.example.test/"
	setting.MjForwardUrlEnabled = true

	running := &model.Midjourney{MjId: "mj-running", ImageUrl: "https://upstream.test/running.png", Status: "IN_PROGRESS"}
	finished := &model.Midjourney{MjId: "mj-finished", ImageUrl: "https://upstream.test/finished.png", Status: "SUCCESS"}
	items := []*model.Midjourney{running, finished}

	midjourneyItemsWithSignedImageURL(items)

	runningURL := parseMidjourneyImageURL(t, running.ImageUrl)
	assert.Equal(t, "https://relay.example.test/mj/image/mj-running", runningURL.address)
	assert.NotEmpty(t, runningURL.rand, "a running task keeps the cache buster")
	assert.True(t, service.VerifyTaskArtifactAccess(runningURL.access, "mj-running", "image"),
		"the running row verifies for its own id")
	assert.False(t, service.VerifyTaskArtifactAccess(runningURL.access, "mj-finished", "image"),
		"the signature is bound to the id")

	finishedURL := parseMidjourneyImageURL(t, finished.ImageUrl)
	assert.Equal(t, "https://relay.example.test/mj/image/mj-finished", finishedURL.address)
	assert.Empty(t, finishedURL.rand, "a finished task needs no cache buster")
	assert.True(t, service.VerifyTaskArtifactAccess(finishedURL.access, "mj-finished", "image"),
		"the finished row verifies for its own id")
}

// TestMidjourneyItemsWithSignedImageURLKeepsTheRowWithoutASecret covers a
// process that cannot issue a signature: the row keeps the address the database
// holds instead of receiving a URL the image route would answer with 404.
func TestMidjourneyItemsWithSignedImageURLKeepsTheRowWithoutASecret(t *testing.T) {
	previousCryptoSecret := common.CryptoSecret
	previousForward := setting.MjForwardUrlEnabled
	t.Cleanup(func() {
		common.CryptoSecret = previousCryptoSecret
		setting.MjForwardUrlEnabled = previousForward
	})
	common.CryptoSecret = ""
	setting.MjForwardUrlEnabled = true

	item := &model.Midjourney{MjId: "mj-running", ImageUrl: "https://upstream.test/running.png", Status: "IN_PROGRESS"}
	midjourneyItemsWithSignedImageURL([]*model.Midjourney{item})

	assert.Equal(t, "https://upstream.test/running.png", item.ImageUrl)
}

type midjourneyImageURLParts struct {
	address string
	access  string
	rand    string
}

func parseMidjourneyImageURL(t *testing.T, imageURL string) midjourneyImageURLParts {
	t.Helper()
	parsed, err := url.Parse(imageURL)
	require.NoError(t, err)
	access := parsed.Query().Get(service.TaskArtifactAccessQueryParameter)
	require.NotEmpty(t, access, "the emitted URL carries a signature")
	return midjourneyImageURLParts{
		address: parsed.Scheme + "://" + parsed.Host + parsed.Path,
		access:  access,
		rand:    parsed.Query().Get("rand"),
	}
}
