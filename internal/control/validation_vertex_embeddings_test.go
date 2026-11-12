package control

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"testing"

	"gpt-load/internal/channel"
	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
	"gpt-load/internal/state"
	"gpt-load/internal/storage/models"
)

func TestVertexValidationUsesOnlyCompatibleEmbeddingProbes(t *testing.T) {
	t.Parallel()
	registry := channel.NewRegistry()
	credential, err := json.Marshal(map[string]string{
		"service_account_json": `{"type":"service_account","project_id":"test-project","client_email":"test@example.iam.gserviceaccount.com","private_key":"synthetic"}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		model    string
		primary  protocol.Protocol
		fallback bool
	}{
		{"claude-3-5-sonnet@20240620", protocol.OpenAICompletions, false},
		{"text-embedding-005", protocol.OpenAICompletions, false},
		{"123456789", protocol.Gemini, false},
		{"gemini-embedding-2", protocol.Gemini, true},
	} {
		t.Run(test.model, func(t *testing.T) {
			encodedModels, err := json.Marshal([]GroupModel{{ID: test.model}})
			if err != nil {
				t.Fatal(err)
			}
			stored := models.Group{
				ChannelID: string(channel.GoogleVertex), Params: models.JSON(`{}`), Models: models.JSON(encodedModels),
			}
			service := &Service{channelRegistry: registry}
			if err := service.initializeValidationProtocol(&stored); err != nil {
				t.Fatal(err)
			}
			if stored.ValidationProtocol == nil {
				t.Fatal("default validation protocol is missing")
			}
			if *stored.ValidationProtocol != string(test.primary) {
				t.Errorf("default validation protocol = %s, want %s", *stored.ValidationProtocol, test.primary)
			}

			group := validationGroup(nil, test.model, nil)
			setValidationChannel(&group, channel.GoogleVertex, json.RawMessage(`{}`))
			target, ok := buildGroupValidationTarget(group)
			if !ok || target.protocol != test.primary {
				t.Fatalf("automatic validation target = %+v, %t", target, ok)
			}
			var wantFallback []protocol.Protocol
			if test.fallback {
				wantFallback = []protocol.Protocol{protocol.GeminiEmbeddings}
			}
			if !reflect.DeepEqual(target.fallbackProtocols, wantFallback) {
				t.Fatalf("fallback protocols = %v, want %v", target.fallbackProtocols, wantFallback)
			}

			upstreamFailure := validationStartedProbeFailure(http.StatusNotFound,
				execution.FailureHintModelUnavailable, execution.ErrorScopeModel,
				"model_not_found", "original upstream model unavailable")
			var attempts []protocol.Protocol
			probe := newCredentialProbeExecutor(fixedValidationDecryptor{plaintext: string(credential)}, registry,
				scriptedDiscoveryExecutor{execute: func(_ context.Context, spec execution.AttemptSpec) execution.AttemptResult {
					attempts = append(attempts, spec.ClientProtocol)
					if spec.ClientProtocol == protocol.GeminiEmbeddings {
						return successfulCredentialProbeResult()
					}
					return upstreamFailure
				}})
			result, err := probe.Probe(t.Context(), group, target, state.CredentialRef{
				ID: 1, GroupID: 1, EncryptedValue: "synthetic", Version: 1, IdentityGeneration: 1,
			})
			if err != nil {
				t.Fatal(err)
			}
			wantAttempts := append([]protocol.Protocol{test.primary}, wantFallback...)
			if !reflect.DeepEqual(attempts, wantAttempts) {
				t.Fatalf("probe attempts = %v, want %v", attempts, wantAttempts)
			}
			if test.fallback {
				if !credentialProbePassed(result.result) || result.protocol != protocol.GeminiEmbeddings {
					t.Fatalf("embedding fallback result = %+v", result)
				}
			} else if result.protocol != test.primary || !reflect.DeepEqual(result.result, upstreamFailure) {
				t.Fatalf("upstream failure was replaced: %+v", result)
			}
		})
	}
}
