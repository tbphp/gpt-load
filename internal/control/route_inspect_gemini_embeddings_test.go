package control

import (
	"encoding/json"
	"testing"

	"github.com/gin-gonic/gin"

	"gpt-load/internal/channel"
	"gpt-load/internal/execution"
	"gpt-load/internal/platform/config"
	"gpt-load/internal/protocol"
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
