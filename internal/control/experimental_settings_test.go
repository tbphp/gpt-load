package control

import (
	"reflect"
	"testing"

	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
	"gpt-load/internal/state"
)

// 决策模型选项同时列出 Jev 与 Google 原生决策路由。
func TestDecisionModelOptionsIncludeGeminiDecisionsRoutes(t *testing.T) {
	t.Parallel()

	snapshot := &state.ConfigSnapshot{
		Groups: map[uint]state.GroupView{1: {Name: "jev"}, 2: {Name: "gemini"}},
		ExecutionCandidates: state.ExecutionCandidateIndex{
			protocol.Decisions:       {execution.OperationDecisionsCreate: {"jev-latest": {{GroupID: 1}}}},
			protocol.GeminiDecisions: {execution.OperationDecisionsCreate: {"gemini-decision-1": {{GroupID: 2}}}},
		},
	}
	wantRoutes := []DecisionRouteOption{
		{GroupID: 2, GroupName: "gemini", Models: []string{"gemini-decision-1"}},
		{GroupID: 1, GroupName: "jev", Models: []string{"jev-latest"}},
	}
	if routes := decisionRouteOptions(snapshot); !reflect.DeepEqual(routes, wantRoutes) {
		t.Fatalf("decisionRouteOptions() = %#v, want %#v", routes, wantRoutes)
	}
	if models := decisionModelNames(snapshot); !reflect.DeepEqual(models, []string{"gemini-decision-1", "jev-latest"}) {
		t.Fatalf("decisionModelNames() = %#v", models)
	}
}
