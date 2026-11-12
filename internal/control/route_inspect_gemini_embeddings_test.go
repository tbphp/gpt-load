package control

import (
	"encoding/json"
	"testing"

	"github.com/gin-gonic/gin"

	"gpt-load/internal/channel"
	"gpt-load/internal/execution"
	"gpt-load/internal/platform/config"
	"gpt-load/internal/protocol"
	"gpt-load/internal/scheduler"
	"gpt-load/internal/state"
)

func TestRouteInspectGeminiEmbeddingsSelectsNativeGeminiGroup(t *testing.T) {
	t.Parallel()
	initControlI18n(t)
	fixture := newServiceFixture(t)
	if _, err := fixture.manager.Publish(state.CompileInput{
		ChannelRegistry: fixture.channelRegistry,
		Groups: []state.GroupConfig{{
			ID: 1, Name: "gemini", ChannelID: channel.Gemini, ConnectionType: "api_key",
			Params: json.RawMessage(`{}`), Models: []state.ModelConfig{{ID: "gemini-embedding-001", Alias: "public"}},
			Enabled: true,
		}},
		AccessKeys: []state.AccessKeyConfig{{
			ID: 10, Name: "client", KeyHash: "hash", Status: state.AccessKeyStatusActive,
		}},
	}); err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	if err := fixture.registry.ReplaceCredentials([]state.CredentialEntry{{
		ID: 100, GroupID: 1, Status: state.CredentialStatusActive,
		Version: 1, IdentityGeneration: 1, Fingerprint: "credential", EncryptedValue: "encrypted",
	}}); err != nil {
		t.Fatalf("ReplaceCredentials() error = %v", err)
	}
	engine := gin.New()
	NewServer(&config.Config{AuthKey: "test-auth-key"}, fixture.service).RegisterRoutes(engine)

	recorder := performRouteInspectRequest(
		engine,
		"test-auth-key",
		`{"protocol":"gemini-embeddings","external_model":"public","access_key_id":10}`,
	)
	got := decodeRouteInspectSuccess(t, recorder)
	if got.Protocol != protocol.GeminiEmbeddings || got.Operation != execution.OperationEmbeddingsCreate ||
		got.RouteRequirement != execution.RouteRequirementNative ||
		len(got.Groups) != 1 || got.Groups[0].RouteMode != execution.RouteNative {
		t.Fatalf("route inspection = %#v", got)
	}
}

func TestRouteInspectOpenAIEmbeddingsSelectsConvertedGeminiGroup(t *testing.T) {
	t.Parallel()
	initControlI18n(t)
	fixture := newServiceFixture(t)
	if _, err := fixture.manager.Publish(state.CompileInput{
		ChannelRegistry: fixture.channelRegistry,
		Groups: []state.GroupConfig{{
			ID: 1, Name: "gemini", ChannelID: channel.Gemini, ConnectionType: "api_key",
			Params: json.RawMessage(`{}`), Models: []state.ModelConfig{{ID: "gemini-embedding-001", Alias: "public"}},
			Enabled: true,
		}},
		AccessKeys: []state.AccessKeyConfig{{
			ID: 10, Name: "client", KeyHash: "hash", Status: state.AccessKeyStatusActive,
		}},
	}); err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	if err := fixture.registry.ReplaceCredentials([]state.CredentialEntry{{
		ID: 100, GroupID: 1, Status: state.CredentialStatusActive,
		Version: 1, IdentityGeneration: 1, Fingerprint: "credential", EncryptedValue: "encrypted",
	}}); err != nil {
		t.Fatalf("ReplaceCredentials() error = %v", err)
	}
	engine := gin.New()
	NewServer(&config.Config{AuthKey: "test-auth-key"}, fixture.service).RegisterRoutes(engine)

	recorder := performRouteInspectRequest(
		engine,
		"test-auth-key",
		`{"protocol":"openai-embeddings","external_model":"public","access_key_id":10}`,
	)
	got := decodeRouteInspectSuccess(t, recorder)
	if got.Protocol != protocol.OpenAIEmbeddings || got.Operation != execution.OperationEmbeddingsCreate ||
		got.RouteRequirement != execution.RouteRequirementAny ||
		len(got.Groups) != 1 || got.Groups[0].RouteMode != execution.RouteConverted {
		t.Fatalf("route inspection = %#v", got)
	}
}

func TestRouteInspectVertexEmbeddingsSelectsNativeAndConvertedRoutes(t *testing.T) {
	t.Parallel()
	initControlI18n(t)
	fixture := newServiceFixture(t)
	if _, err := fixture.manager.Publish(state.CompileInput{
		ChannelRegistry: fixture.channelRegistry,
		Groups: []state.GroupConfig{{
			ID: 1, Name: "vertex", ChannelID: channel.GoogleVertex, ConnectionType: "api_key",
			Params: json.RawMessage(`{"location":"global"}`), Models: []state.ModelConfig{{ID: "gemini-embedding-2", Alias: "public"}},
			Enabled: true,
		}},
		AccessKeys: []state.AccessKeyConfig{{ID: 10, Name: "client", KeyHash: "hash", Status: state.AccessKeyStatusActive}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := fixture.registry.ReplaceCredentials([]state.CredentialEntry{{
		ID: 100, GroupID: 1, Status: state.CredentialStatusActive,
		Version: 1, IdentityGeneration: 1, Fingerprint: "credential", EncryptedValue: "encrypted",
	}}); err != nil {
		t.Fatal(err)
	}
	engine := gin.New()
	NewServer(&config.Config{AuthKey: "test-auth-key"}, fixture.service).RegisterRoutes(engine)
	for _, test := range []struct {
		protocol protocol.Protocol
		mode     execution.RouteMode
	}{
		{protocol.GeminiEmbeddings, execution.RouteNative},
		{protocol.OpenAIEmbeddings, execution.RouteConverted},
	} {
		t.Run(string(test.protocol), func(t *testing.T) {
			recorder := performRouteInspectRequest(engine, "test-auth-key",
				`{"protocol":"`+string(test.protocol)+`","external_model":"public","access_key_id":10}`)
			got := decodeRouteInspectSuccess(t, recorder)
			if got.Protocol != test.protocol || got.Operation != execution.OperationEmbeddingsCreate ||
				!got.Routable || len(got.Groups) != 1 || got.Groups[0].RouteMode != test.mode ||
				got.Groups[0].ChannelID != channel.GoogleVertex || !got.Groups[0].Routable {
				t.Fatalf("route inspection = %#v", got)
			}
		})
	}
}

func TestVertexEmbeddingsRoutingSkipsUnsupportedTargets(t *testing.T) {
	t.Parallel()
	for _, model := range []string{"text-embedding-005", "123456789"} {
		t.Run(model, func(t *testing.T) {
			fixture := newServiceFixture(t)
			snapshot, err := fixture.manager.Publish(state.CompileInput{
				ChannelRegistry: fixture.channelRegistry,
				Groups: []state.GroupConfig{
					{
						ID: 1, Name: "vertex", ChannelID: channel.GoogleVertex, ConnectionType: "api_key",
						Params: json.RawMessage(`{}`), Enabled: true,
						Models: []state.ModelConfig{{ID: model, Alias: "public"}, {ID: "gemini-embedding-2", Alias: "vertex-supported"}},
					},
					{
						ID: 2, Name: "gemini", ChannelID: channel.Gemini, ConnectionType: "api_key",
						Params: json.RawMessage(`{}`), Enabled: true,
						Models: []state.ModelConfig{{ID: "gemini-embedding-2", Alias: "public"}},
					},
				},
				AccessKeys: []state.AccessKeyConfig{{ID: 10, Name: "client", KeyHash: "hash", Status: state.AccessKeyStatusActive}},
			})
			if err != nil {
				t.Fatal(err)
			}
			if err := fixture.registry.ReplaceCredentials([]state.CredentialEntry{
				{ID: 100, GroupID: 1, Status: state.CredentialStatusActive, Version: 1, IdentityGeneration: 1, Fingerprint: "vertex", EncryptedValue: "encrypted"},
				{ID: 200, GroupID: 2, Status: state.CredentialStatusActive, Version: 1, IdentityGeneration: 1, Fingerprint: "gemini", EncryptedValue: "encrypted"},
			}); err != nil {
				t.Fatal(err)
			}
			for _, clientProtocol := range []protocol.Protocol{protocol.GeminiEmbeddings, protocol.OpenAIEmbeddings} {
				t.Run(string(clientProtocol), func(t *testing.T) {
					inspection, err := fixture.service.InspectRoute(routeInspectRequest{
						Protocol: clientProtocol, ExternalModel: "public", AccessKeyID: 10,
					})
					if err != nil {
						t.Fatal(err)
					}
					if !inspection.Routable || len(inspection.Groups) != 1 || inspection.Groups[0].GroupID != 2 {
						t.Errorf("unsupported Vertex route was included: %+v", inspection)
					}
					externalModel := "public"
					iterator := scheduler.New(snapshot, fixture.registry, scheduler.Query{
						ClientProtocol: clientProtocol, Operation: execution.OperationEmbeddingsCreate,
						ExternalModel: &externalModel, AccessKey: snapshot.AccessKeysByID[10],
						PreferredCredentialID: 100,
					})
					selection, err := iterator.Next()
					if err != nil || selection.GroupID != 2 {
						t.Fatalf("selection = %+v, %v; want the compatible Gemini group", selection, err)
					}
					inspection, err = fixture.service.InspectRoute(routeInspectRequest{
						Protocol: clientProtocol, ExternalModel: "vertex-supported", AccessKeyID: 10,
					})
					if err != nil || !inspection.Routable || len(inspection.Groups) != 1 || inspection.Groups[0].GroupID != 1 {
						t.Fatalf("supported Vertex route was lost: %+v, %v", inspection, err)
					}
				})
			}
		})
	}
}
