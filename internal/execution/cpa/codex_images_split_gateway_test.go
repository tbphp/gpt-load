package cpa

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"gpt-load/internal/channel"
	"gpt-load/internal/dialect"
	"gpt-load/internal/gateway"
	"gpt-load/internal/health"
	"gpt-load/internal/platform/httproute"
	"gpt-load/internal/state"
)

// Replay native Codex Images across the real CPA executor, subscription adapter, and gateway.
func TestCodexImagesGatewayPreservesSplitCompletedEvent(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const upstream = "gpt-image-2.6-sunburst"
	const alias = "gpt-image-2"
	const keepalive = "event: keepalive\ndata: {\"type\":\"keepalive\"}\n\n"
	for _, tt := range []struct{ name, model, payload, eventModel string }{
		{"small_aliased_event", alias, "aA==", ""},
		{"split_aliased_event", alias, strings.Repeat("A", 64<<10), upstream},
		{"split_original_name_event", upstream, strings.Repeat("A", 64<<10), ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			adapter, _, registry, keys, row := newAdapterFixture(t, credentialJSON("access", "refresh", time.Now().Add(time.Hour)))
			ref, ok := registry.CredentialRef(row.ID)
			if !ok {
				t.Fatal("credential missing")
			}
			manager := state.NewManager()
			_, err := manager.Publish(state.CompileInput{
				ChannelRegistry: channel.NewRegistry(),
				Groups:          []state.GroupConfig{{ID: row.GroupID, Name: "codex", ChannelID: channel.Codex, ConnectionType: "subscription", Params: json.RawMessage("{}"), Models: []state.ModelConfig{{ID: upstream, Alias: alias}, {ID: upstream}}, Enabled: true}},
				Credentials:     []state.CredentialConfig{{ID: row.ID, GroupID: row.GroupID, Version: ref.Version, IdentityGeneration: ref.IdentityGeneration, Fingerprint: ref.Fingerprint, Status: state.CredentialStatusActive}},
				AccessKeys:      []state.AccessKeyConfig{{ID: 1, Name: "client", KeyHash: keys.Hash("gl-client"), Status: state.AccessKeyStatusActive}},
			})
			if err != nil {
				t.Fatal(err)
			}
			logs := &searchRequestLogSink{}
			handler := gateway.NewHandler(manager, registry, keys, gateway.NewExecutionForwarder(contextTransportCPAExecutor{adapter}), dialect.NewSet(dialect.NewOpenAIImages()), health.NewStatsStore(), health.NewMutationCoordinator(), nil, logs, nil)
			engine := gin.New()
			routes, err := httproute.NewRegistry(handler.HTTPModule())
			if err != nil {
				t.Fatal(err)
			}
			if err := routes.Bind(engine); err != nil {
				t.Fatal(err)
			}
			final := "event: image_generation.completed\ndata: {\"type\":\"image_generation.completed\",\"b64_json\":\"" + tt.payload + "\"}\n\n"
			wantFinal := final
			if tt.eventModel != "" {
				final = strings.TrimSuffix(final, "}\n\n") + ",\"model\":\"" + tt.eventModel + "\"}\n\n"
				// The alias rewriter re-encodes object fields in sorted order.
				wantFinal = "event: image_generation.completed\ndata: {\"b64_json\":\"" + tt.payload + "\",\"model\":\"" + tt.model + "\",\"type\":\"image_generation.completed\"}\n\n"
			}
			transport := roundTripperFunc(func(r *http.Request) (*http.Response, error) {
				if r.URL.Path != "/backend-api/codex/images/generations" {
					t.Errorf("unexpected upstream path %q", r.URL.Path)
				}
				// MultiReader returns the keepalive separately; the SDK reads the final event in 32 KiB chunks.
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(io.MultiReader(strings.NewReader(keepalive), strings.NewReader(final))), Request: r}, nil
			})
			request := httptest.NewRequest("POST", "/v1/images/generations", strings.NewReader(`{"model":"`+tt.model+`","prompt":"draw a circle","stream":true}`))
			request = request.WithContext(context.WithValue(t.Context(), "cliproxy.roundtripper", http.RoundTripper(transport)))
			request.Header.Set("Authorization", "Bearer gl-client")
			request.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			engine.ServeHTTP(rec, request)
			if len(logs.events) != 1 {
				t.Fatalf("request logs = %d", len(logs.events))
			}
			e := logs.events[0]
			if rec.Code != 200 || rec.Body.String() != keepalive+wantFinal || e.Status != "success" {
				t.Fatalf("HTTP=%d response_bytes=%d expected_bytes=%d keepalive_only=%t log_status=%s error_code=%s", rec.Code, rec.Body.Len(), len(keepalive+wantFinal), rec.Body.String() == keepalive, e.Status, e.ErrorCode)
			}
		})
	}
}
