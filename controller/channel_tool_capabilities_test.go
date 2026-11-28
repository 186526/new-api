package controller

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/service/websearch/abstract"
	"github.com/gin-gonic/gin"
)

func TestGetChannelToolCapabilities(t *testing.T) {
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	GetChannelToolCapabilities(ctx)
	var response struct {
		Success bool
		Data    abstract.ChannelToolCatalog
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	codex := response.Data.ChannelTypes[constant.ChannelTypeCodex]
	if recorder.Code != http.StatusOK || !response.Success || !codex.Search.Supported || codex.Fetch.NativeContracts == nil {
		t.Fatalf("unexpected response: %s", recorder.Body.String())
	}
}

func TestPreflightChannelTools(t *testing.T) {
	for _, mapping := range []string{"", `{"tools-search":"synthetic-model"}`} {
		body, err := json.Marshal(struct {
			Type         int    `json:"type"`
			ModelMapping string `json:"model_mapping"`
		}{constant.ChannelTypeCodex, mapping})
		if err != nil {
			t.Fatal(err)
		}
		recorder := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(recorder)
		ctx.Request = httptest.NewRequest(http.MethodPost, "/api/channel/tool_capabilities/preflight", strings.NewReader(string(body)))
		PreflightChannelTools(ctx)
		var response struct {
			Success bool
			Data    struct {
				Missing bool `json:"missing_search_mapping"`
			}
		}
		if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		if !response.Success || response.Data.Missing != (mapping == "") || strings.Contains(recorder.Body.String(), "synthetic-model") {
			t.Fatalf("unexpected preflight response: %s", recorder.Body.String())
		}
	}
}
