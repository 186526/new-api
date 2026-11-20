package dto

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestValidateHTTPTransportImageLimit(t *testing.T) {
	for _, tc := range []struct {
		name      string
		maxImages int
		wantError bool
	}{
		{name: "unset keeps the channel unlimited", maxImages: 0},
		{name: "a limit is accepted", maxImages: 20},
		{name: "a negative limit is rejected", maxImages: -1, wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			settings := ChannelSettings{MaxImagesPerRequest: tc.maxImages}
			err := settings.ValidateHTTPTransport()
			if tc.wantError {
				assert.Error(t, err)
				return
			}
			assert.NoError(t, err)
		})
	}
}
