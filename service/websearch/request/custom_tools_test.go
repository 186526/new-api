package request

import (
	"io"
	"net"
	"net/http"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/service/websearch/abstract"
	"github.com/stretchr/testify/require"
)

type customToolRoundTripFunc func(*http.Request) (*http.Response, error)

func (transport customToolRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return transport(req)
}

func TestCustomToolRequestUsesOperationAuthOnly(t *testing.T) {
	const id = 9
	contract := abstract.ContractToolsSearch
	sealed, err := abstract.SealCustomToolCredential(id, contract, "synthetic-operation-key")
	require.NoError(t, err)
	client := &http.Client{Transport: customToolRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		require.Equal(t, "https://tools.example.com/base/lookup", req.URL.String())
		require.Equal(t, "synthetic-operation-key", req.Header.Get("x-api-key"))
		require.Empty(t, req.Header.Get("Authorization"))
		require.Nil(t, req.GetBody)
		body, err := io.ReadAll(req.Body)
		require.NoError(t, err)
		require.Contains(t, string(body), `"query":"fixture"`)
		return &http.Response{StatusCode: http.StatusTooManyRequests, Body: io.NopCloser(strings.NewReader("unsafe upstream body")), Header: http.Header{"Retry-After": {"1"}}}, nil
	})}
	result, err := sendCustomTool(t.Context(), abstract.ChannelContext{ChannelID: id, BaseURL: "https://tools.example.com/base", Key: "synthetic-channel-key"}, Query{
		Contract: contract, Text: "fixture", CustomOperation: &abstract.CustomToolOperation{Protocol: abstract.CustomToolsProtocol, Path: "/lookup", Credential: sealed},
		Prepare: func(req *http.Request) error { req.Header.Set("Authorization", "synthetic-override"); return nil },
	}, client)
	require.NoError(t, err)
	require.Equal(t, http.StatusTooManyRequests, result.StatusCode)
	require.Equal(t, "1", result.Header.Get("Retry-After"))
	require.Empty(t, result.Body)
}

func TestCustomToolResponseIsBoundedAndUncompressed(t *testing.T) {
	response := &http.Response{Body: io.NopCloser(strings.NewReader(strings.Repeat("x", customToolResponseLimit+1))), Header: make(http.Header)}
	_, err := readCustomToolBody(response)
	require.ErrorIs(t, err, ErrCustomToolResponse)
	response.Header.Set("Content-Encoding", "gzip")
	_, err = readCustomToolBody(response)
	require.ErrorIs(t, err, ErrCustomToolResponse)
}

func TestCustomToolOnlyAcceptsProtocolEvidence(t *testing.T) {
	search := Query{Contract: abstract.ContractToolsSearch}
	_, err := normalizeCustomToolBody(search, []byte(`{"error":{"code":"denied"},"results":[]}`))
	require.ErrorIs(t, err, ErrCustomToolResponse)
	body, err := normalizeCustomToolBody(search, []byte(`{"results":[]}`))
	require.NoError(t, err)
	parser, err := abstract.ParserFor("exa")
	require.NoError(t, err)
	_, err = parser.ParseSearch(body)
	require.ErrorIs(t, err, abstract.ErrEmptyResults)
	fetch := Query{Contract: abstract.ContractToolsFetch, URL: "https://example.com/"}
	body, err = normalizeCustomToolBody(fetch, []byte(`{"results":[{"url":"https://example.org/","text":"unrelated"}]}`))
	require.NoError(t, err)
	require.Empty(t, body)
	body, err = normalizeCustomToolBody(fetch, []byte(`{"results":[{"url":"https://example.com/","text":"matched"}]}`))
	require.NoError(t, err)
	page, err := parser.ParseFetch(body)
	require.NoError(t, err)
	require.Equal(t, "matched", page.Content)
}

func TestCustomToolTransportUsesPublicAddressesOnly(t *testing.T) {
	for _, ip := range []string{"127.0.0.1", "10.0.0.1", "169.254.1.1", "::1", "fc00::1", "2001:db8::1"} {
		require.False(t, publicToolAddress("tools.example.com", net.ParseIP(ip)), ip)
	}
	require.True(t, publicToolAddress("tools.example.com", net.ParseIP("8.8.8.8")))
	transport := customToolClient.Transport.(*http.Transport)
	require.Nil(t, transport.Proxy)
	require.ErrorIs(t, customToolClient.CheckRedirect(nil, nil), http.ErrUseLastResponse)
}
