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
)

func TestRerankValidationFallbackRecoversOnlyAfterSuccessfulProbe(t *testing.T) {
	for _, status := range []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusTooManyRequests, http.StatusInternalServerError} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			group := state.GroupView{ValidationModel: "reranker"}
			setValidationChannel(&group, channel.OpenAICompatible, json.RawMessage(`{"base_url":"https://example.com/v1"}`))
			worker := newValidationWorkerForTest(validationSnapshot(map[uint]state.GroupView{1: group}), []state.CredentialRef{{ID: 7, GroupID: 1, EncryptedValue: "key-7"}}, &validationProbeRecorder{})
			var protocols []protocol.Protocol
			worker.executor = scriptedDiscoveryExecutor{execute: func(_ context.Context, spec execution.AttemptSpec) execution.AttemptResult {
				protocols = append(protocols, spec.ClientProtocol)
				if spec.ClientProtocol == protocol.Rerank {
					return execution.AttemptResult{DispatchState: execution.DispatchMaybeSent, ResponseStarted: true, StatusCode: 200, Header: http.Header{}}
				}
				return validationStartedProbeFailure(status, execution.FailureHintModelUnavailable, execution.ErrorScopeModel, "model_not_found", "unsupported model")
			}}
			worker.Validate(context.Background())
			want := []protocol.Protocol{protocol.OpenAICompletions}
			if status == http.StatusBadRequest {
				want = append(want, protocol.OpenAIEmbeddings, protocol.Rerank)
			}
			if !reflect.DeepEqual(protocols, want) {
				t.Fatalf("probes=%v want=%v", protocols, want)
			}
			if status == http.StatusBadRequest && !reflect.DeepEqual(worker.recorder.events(), []string{"registry.recover:7", "stats.reset:7"}) {
				t.Fatalf("events=%v", worker.recorder.events())
			}
			if status != http.StatusBadRequest && len(worker.recorder.events()) != 0 {
				t.Fatalf("unexpected recovery: %v", worker.recorder.events())
			}
		})
	}
}
