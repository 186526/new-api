package metrics

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
)

func TestObserveImageLimitRejection(t *testing.T) {
	ObserveImageLimitRejection("", 42, "mistral-large-latest")
	assert.Equal(t, 1.0, testutil.ToFloat64(
		imageLimitRejectionsTotal.WithLabelValues("42", "mistral-large-latest"),
	))

	ObserveImageLimitRejection("named-channel", 42, "mistral-large-latest")
	assert.Equal(t, 1.0, testutil.ToFloat64(
		imageLimitRejectionsTotal.WithLabelValues("named-channel", "mistral-large-latest"),
	))
}
