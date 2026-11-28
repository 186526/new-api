package request

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/service/websearch/abstract"
	"github.com/QuantumNous/new-api/setting/websearch_setting"
)

const customToolResponseLimit = 1 << 20

var (
	ErrCustomToolRequest        = errors.New("custom_tool_request_failed")
	ErrCustomToolResponse       = errors.New("custom_tool_invalid_response")
	customToolIPv6Public        = netip.MustParsePrefix("2000::/3")
	customToolIPv6Tunnel        = netip.MustParsePrefix("2002::/16")
	customToolIPv6Documentation = netip.MustParsePrefix("3fff::/20")
	customToolNetworkPolicy     = &common.SSRFProtection{AllowedPorts: []int{443}, ApplyIPFilterForDomain: true}
	customToolClient            = &http.Client{
		Timeout:       10 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		Transport: &http.Transport{
			DialContext: dialCustomTool, DisableCompression: true, DisableKeepAlives: true,
			TLSHandshakeTimeout: 4 * time.Second, MaxResponseHeaderBytes: 32 << 10,
		},
	}
)

func dialCustomTool(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil || port != "443" || customToolNetworkPolicy.ValidateNetworkTarget(host, 443) != nil {
		return nil, ErrCustomToolRequest
	}
	host = strings.TrimSuffix(host, ".")
	if net.ParseIP(host) == nil {
		// An absolute DNS name cannot be rewritten by resolver search domains.
		host += "."
	}
	resolved, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil || len(resolved) == 0 || len(resolved) > 32 {
		return nil, ErrCustomToolRequest
	}
	for _, ip := range resolved {
		if ip.Zone != "" || !publicToolAddress(host, ip.IP) {
			return nil, ErrCustomToolRequest
		}
	}
	dialer := net.Dialer{Timeout: 3 * time.Second}
	for _, ip := range resolved {
		if conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(ip.IP.String(), port)); err == nil {
			return conn, nil
		}
	}
	return nil, ErrCustomToolRequest
}

func publicToolAddress(host string, ip net.IP) bool {
	address, ok := netip.AddrFromSlice(ip)
	if !ok || !address.IsGlobalUnicast() || customToolNetworkPolicy.ValidateResolvedIP(host, ip) != nil {
		return false
	}
	address = address.Unmap()
	return address.Is4() || (customToolIPv6Public.Contains(address) && !customToolIPv6Tunnel.Contains(address) && !customToolIPv6Documentation.Contains(address))
}

func VerifyCustomTool(ctx context.Context, channel abstract.ChannelContext, contract abstract.Contract, operation *abstract.CustomToolOperation) error {
	query := Query{Contract: contract, Text: "IANA example domains", URL: "https://example.com/", Count: 1, CustomOperation: operation}
	answer, err := sendCustomTool(ctx, channel, query, customToolClient)
	if err != nil {
		return err
	}
	if answer.StatusCode < 200 || answer.StatusCode >= 300 || len(answer.Body) == 0 {
		return ErrCustomToolResponse
	}
	if contract.IsSearch() {
		parser, err := abstract.ParserFor(websearch_setting.ProviderExa)
		if err != nil {
			return ErrCustomToolResponse
		}
		if _, err = parser.ParseSearch(answer.Body); err != nil {
			return ErrCustomToolResponse
		}
	}
	return nil
}

func sendCustomTool(ctx context.Context, channel abstract.ChannelContext, query Query, client *http.Client) (Upstream, error) {
	endpoint, err := query.CustomOperation.Endpoint(channel.BaseURL)
	if err != nil {
		return Upstream{}, notSent(err)
	}
	key, err := query.CustomOperation.OpenCredential(channel.ChannelID, query.Contract)
	if err != nil {
		return Upstream{}, notSent(err)
	}
	var payload any = exaSearchPayloadOf(query)
	if query.Contract.IsFetch() {
		payload = exaFetchPayload{URLs: []string{query.URL}, Text: exaText{MaxCharacters: exaTextCharacters}}
	}
	encoded, err := common.Marshal(payload)
	if err != nil {
		return Upstream{}, notSent(abstract.ErrCustomToolConfig)
	}
	req, err := exaRequest(ctx, endpoint, key, encoded, nil)
	if err != nil {
		return Upstream{}, notSent(abstract.ErrCustomToolConfig)
	}
	req.GetBody = nil
	response, err := client.Do(req)
	if err != nil {
		return Upstream{}, sentError{ErrCustomToolRequest}
	}
	defer response.Body.Close()
	answer := Upstream{Family: websearch_setting.ProviderExa, StatusCode: response.StatusCode, Header: make(http.Header)}
	answer.Header.Set("Content-Type", "application/json")
	if value := response.Header.Get("Retry-After"); len(value) <= 64 {
		if _, err := http.ParseTime(value); err == nil {
			answer.Header.Set("Retry-After", value)
		} else if value != "" && strings.Trim(value, "0123456789") == "" {
			answer.Header.Set("Retry-After", value)
		}
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return answer, nil
	}
	body, err := readCustomToolBody(response)
	if err != nil {
		return Upstream{}, sentError{err}
	}
	answer.Body, err = normalizeCustomToolBody(query, body)
	if err != nil {
		return Upstream{}, sentError{err}
	}
	return answer, nil
}

func readCustomToolBody(response *http.Response) ([]byte, error) {
	if encoding := response.Header.Get("Content-Encoding"); encoding != "" && !strings.EqualFold(encoding, "identity") {
		return nil, ErrCustomToolResponse
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, customToolResponseLimit+1))
	if err != nil || len(body) > customToolResponseLimit {
		return nil, ErrCustomToolResponse
	}
	return body, nil
}

func normalizeCustomToolBody(query Query, body []byte) ([]byte, error) {
	var result struct {
		Results []struct {
			URL string `json:"url"`
		} `json:"results"`
		Error any `json:"error"`
	}
	if err := common.Unmarshal(body, &result); err != nil || result.Error != nil || result.Results == nil {
		return nil, ErrCustomToolResponse
	}
	if query.Contract.IsFetch() {
		entry, err := normalizeExaContents(query.URL, forwardEntry{StatusCode: http.StatusOK, Body: body})
		if err != nil {
			return nil, ErrCustomToolResponse
		}
		return entry.Body, nil
	}
	for _, item := range result.Results {
		if exaDocumentURL(item.URL) == "" {
			return nil, ErrCustomToolResponse
		}
	}
	if len(result.Results) == 0 {
		return body, nil
	}
	if parser, err := abstract.ParserFor(websearch_setting.ProviderExa); err == nil {
		if _, err := parser.ParseSearch(body); err == nil {
			return body, nil
		}
	}
	return nil, ErrCustomToolResponse
}
