package embedded

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v8/gptload-embedded/modelcatalog"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/registry"
	"github.com/tidwall/gjson"
)

func TestCodexChatPreservesUltra(t *testing.T) {
	transport := environmentRoundTripperFunc(func(r *http.Request) (*http.Response, error) {
		b, err := io.ReadAll(r.Body)
		if err != nil {
			return nil, err
		}
		t.Logf("outgoing effort=%q", gjson.GetBytes(b, "reasoning.effort").String())
		if got := gjson.GetBytes(b, "reasoning.effort").String(); got != "ultra" {
			t.Errorf("effort=%q, expected ultra", got)
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader("data: " + string(wsCompleted("chat-ultra")) + "\n\n")), Request: r}, nil
	})
	ctx := context.WithValue(t.Context(), "cliproxy.roundtripper", http.RoundTripper(transport))
	_, err := NewCodexHTTPExecutor().ExecuteCanonical(ctx, "ultra-test", CodexCredential{Type: ProviderCodex, AccessToken: "synthetic", RefreshToken: "synthetic", AccountID: "synthetic"}, ExecuteRequest{Model: "gpt-6.1-sol", Format: "openai", Payload: []byte(`{"model":"gpt-6.1-sol","messages":[{"role":"user","content":"hello"}],"reasoning_effort":"ultra"}`)})
	if err != nil {
		t.Fatalf("advertised effort ultra was rejected: %v", err)
	}
}

func TestCodexRegisteredLevelsMatchClientContract(t *testing.T) {
	var models []struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(modelcatalog.JSON(), &models); err != nil {
		t.Fatal(err)
	}
	for _, model := range models {
		levels := modelcatalog.ReasoningLevels(model.ID)
		if len(levels) == 0 {
			continue
		}
		info := registry.LookupModelInfo(model.ID, ProviderCodex)
		if info == nil || info.Thinking == nil || !reflect.DeepEqual(info.Thinking.Levels, levels) {
			t.Errorf("registered reasoning levels differ from client contract for %s", model.ID)
		}
	}
}
