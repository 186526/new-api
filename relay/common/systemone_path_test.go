package common

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// The upstream serves only /v1/systemone, so a request that arrived under the
// contract prefix must be forwarded under that path or the upstream answers 404.
func TestSystemOneUpstreamRequestPath(t *testing.T) {
	cases := map[string]string{
		"/systemone/v1/systemone":         "/v1/systemone",
		"/systemone/v1/systemone?a=1&b=2": "/v1/systemone?a=1&b=2",
		"/v1/systemone":                   "/v1/systemone",
		"/v1/chat/completions":            "/v1/chat/completions",
		"":                                "",
	}
	for input, expected := range cases {
		assert.Equal(t, expected, SystemOneUpstreamRequestPath(input), input)
	}
}
