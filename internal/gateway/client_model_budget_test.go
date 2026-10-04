package gateway

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"gpt-load/internal/channel"
	"gpt-load/internal/state"
)

func TestCodexCatalogOverflowReturnsCompleteOrderedPrefix(t *testing.T) {
	handler, manager, _ := newHandlerForTest(t, &scriptedForwarder{})
	models := make([]state.ModelConfig, 100)
	for index := range models {
		models[index] = state.ModelConfig{ID: fmt.Sprintf("gpt-6-test-%03d", index)}
	}
	if _, err := manager.Publish(state.CompileInput{
		ChannelRegistry: channel.NewRegistry(),
		Groups: []state.GroupConfig{{
			ID: 1, Name: "catalog", ChannelID: channel.OpenAI, ConnectionType: "api_key",
			Params: json.RawMessage(`{}`), Models: models, Enabled: true,
		}},
		AccessKeys: []state.AccessKeyConfig{{
			ID: 1, Name: "client", KeyHash: handler.encryption.Hash("gl-client"), Status: state.AccessKeyStatusActive,
		}},
	}); err != nil {
		t.Fatal(err)
	}
	engine := gin.New()
	bindGatewayRoutesForTest(t, engine, handler)
	for _, version := range []string{"0.159.2", "0.154.0"} {
		request := httptest.NewRequest(http.MethodGet, "/v1/models?client_version="+version, nil)
		request.Header.Set("Authorization", "Bearer gl-client")
		recorder := httptest.NewRecorder()
		engine.ServeHTTP(recorder, request)
		var response struct {
			Models []struct {
				Slug string `json:"slug"`
			} `json:"models"`
		}
		if recorder.Code != http.StatusOK || recorder.Body.Len() > 1<<20 || json.Unmarshal(recorder.Body.Bytes(), &response) != nil {
			t.Fatalf("%s catalog = HTTP %d, %d bytes; want valid JSON within 1048576 bytes", version, recorder.Code, recorder.Body.Len())
		}
		if len(response.Models) == 0 || len(response.Models) >= len(models) {
			t.Fatalf("%s prefix count = %d", version, len(response.Models))
		}
		for index, model := range response.Models {
			if model.Slug != models[index].ID {
				t.Fatalf("prefix[%d] = %s, want %s", index, model.Slug, models[index].ID)
			}
		}
		if ids := visibleModelIDs(manager.Current(), state.AccessKeyView{}, "openai-completions"); len(ids) != len(models) {
			t.Fatalf("capacity changed ordinary model permissions: %d", len(ids))
		}
	}
}
