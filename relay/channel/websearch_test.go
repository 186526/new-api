package channel_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/relay/channel"
	"github.com/QuantumNous/new-api/relay/channel/antigravity"
	"github.com/QuantumNous/new-api/relay/channel/codex"
	"github.com/QuantumNous/new-api/relay/channel/deepseek"
	"github.com/QuantumNous/new-api/relay/channel/newapi"
	"github.com/QuantumNous/new-api/relay/channel/sub2api"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/service/websearch/abstract"
	"github.com/gin-gonic/gin"
)

func TestWebSearchAdaptorPreparation(t *testing.T) {
	const baseURL = "https://example.test"
	for _, test := range []struct {
		name                                       string
		adaptor                                    channel.WebSearchAdaptor
		channelType                                int
		contract                                   abstract.Contract
		path, key, mapping, model, bearer, account string
	}{
		{name: "DeepSeek search for Moonshot", adaptor: &deepseek.Adaptor{}, channelType: constant.ChannelTypeDeepSeek, contract: abstract.ContractMoonshotSearch, path: "/v1/tools/search", key: "test-key", bearer: "test-key"},
		{name: "New API fetch for Moonshot", adaptor: &newapi.Adaptor{}, channelType: constant.ChannelTypeNewAPI, contract: abstract.ContractMoonshotFetch, path: "/v1/tools/fetch", key: "test-key", bearer: "test-key"},
		{name: "Sub2API search for Alpha", adaptor: &sub2api.Adaptor{}, channelType: constant.ChannelTypeSub2API, contract: abstract.ContractAlphaSearch, path: "/v1/tools/search", key: "test-key", bearer: "test-key"},
		{name: "Codex search for Moonshot", adaptor: &codex.Adaptor{}, channelType: constant.ChannelTypeCodex, contract: abstract.ContractMoonshotSearch, path: "/backend-api/codex/alpha/search", key: `{"access_token":"test-key","account_id":"test-account"}`, mapping: `{"tools-search":"test-model"}`, model: "test-model", bearer: "test-key", account: "test-account"},
		{name: "Antigravity prepares its own request", adaptor: &antigravity.Adaptor{}, channelType: constant.ChannelTypeAntigravity, contract: abstract.ContractMoonshotSearch},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
			ctx.Request = httptest.NewRequest(http.MethodPost, "/client/search", nil)
			ctx.Set("model_mapping", test.mapping)
			info := &relaycommon.RelayInfo{
				RequestURLPath: ctx.Request.URL.Path,
				RelayMode:      relayconstant.RelayModeMoonshotSearch,
				ChannelMeta:    &relaycommon.ChannelMeta{ChannelType: test.channelType, ChannelBaseUrl: baseURL, ApiKey: test.key},
			}
			wantMeta := abstract.ChannelWebSearchCapabilities(test.channelType)
			if meta := test.adaptor.SearchProviderMeta(); meta != wantMeta || !meta.Supports(test.contract) {
				t.Fatalf("capabilities = %+v; want support for %s in %+v", meta, test.contract, wantMeta)
			}
			upstream, err := test.adaptor.PrepareWebSearch(ctx, info, test.contract)
			if err != nil {
				t.Fatal(err)
			}
			if upstream.Provider != wantMeta.Provider || upstream.Model != test.model {
				t.Fatalf("prepared provider/model = %q/%q; want %q/%q", upstream.Provider, upstream.Model, wantMeta.Provider, test.model)
			}
			if info.RequestURLPath != "/client/search" || info.RelayMode != relayconstant.RelayModeMoonshotSearch || info.UpstreamModelName != "" {
				t.Fatal("preparation changed original relay metadata")
			}
			if test.path == "" {
				if upstream.Endpoint != "" || upstream.Prepare != nil {
					t.Fatal("requester-managed transport unexpectedly prepared a channel URL or headers")
				}
				return
			}
			if upstream.Endpoint != baseURL+test.path || upstream.Prepare == nil {
				t.Fatalf("endpoint = %q; want %q", upstream.Endpoint, baseURL+test.path)
			}
			request := httptest.NewRequest(http.MethodPost, upstream.Endpoint, nil)
			if err := upstream.Prepare(request); err != nil {
				t.Fatal(err)
			}
			if request.Header.Get("Authorization") != "Bearer "+test.bearer || request.Header.Get("Chatgpt-Account-Id") != test.account {
				t.Fatal("request preparation did not match the channel's test credentials")
			}
		})
	}
}

func TestWebSearchAdaptorRejectsUnsupportedOperation(t *testing.T) {
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	for _, contract := range []abstract.Contract{abstract.ContractMoonshotSearch, abstract.ContractToolsFetch} {
		if _, err := (&codex.Adaptor{}).PrepareWebSearch(ctx, info, contract); !errors.Is(err, abstract.ErrUnsupportedChannel) {
			t.Fatalf("contract %s: err = %v, want unsupported channel", contract, err)
		}
	}
}
