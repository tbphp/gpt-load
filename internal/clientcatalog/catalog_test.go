package clientcatalog

import (
	"encoding/json"
	"fmt"
	"testing"

	"gpt-load/internal/catalog"
	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
	"gpt-load/internal/state"
)

func TestCatalogBudgetCountsActualEscapedJSONAndKeepsMaximalPrefix(t *testing.T) {
	for _, version := range []string{"0.159.2", "0.154.0"} {
		t.Run(version, func(t *testing.T) {
			snapshot := &state.ConfigSnapshot{
				ExecutionCandidates:  state.ExecutionCandidateIndex{protocol.OpenAIResponses: {execution.OperationResponsesCreate: {}}},
				ClientModelOverrides: map[string]catalog.ClientModelOverrides{},
			}
			for index := range 12 {
				name := fmt.Sprintf("gpt-6-test-%02d", index)
				snapshot.ExecutionCandidates[protocol.OpenAIResponses][execution.OperationResponsesCreate][name] = []state.RouteTarget{{GroupID: 1}}
				display := "中文 <model> & \"quoted\"\n\u2028"
				snapshot.ClientModelOverrides[name] = catalog.ClientModelOverrides{DisplayName: &display}
			}
			full, err := Build(snapshot, state.AccessKeyView{}, version, LimitBytes)
			if err != nil {
				t.Fatal(err)
			}
			if full.IncludedCount != 12 || full.SelectedBytes != int64(len(full.Body)) || full.ResponseBytes != full.SelectedBytes {
				t.Fatalf("full budget = %+v", full)
			}
			var wire struct {
				Models []json.RawMessage `json:"models"`
			}
			if err := json.Unmarshal(full.Body, &wire); err != nil {
				t.Fatal(err)
			}
			for index, item := range wire.Models {
				if full.Entries[index].Bytes != int64(len(item)) {
					t.Fatalf("entry %d has incorrect UTF-8/escaping size", index)
				}
			}
			for _, adjustment := range []int64{-1, 0, 1} {
				limit := full.ResponseBytes + adjustment
				result, err := Build(snapshot, state.AccessKeyView{}, version, limit)
				want := 12
				if adjustment == -1 {
					want = 11
				}
				if err != nil || result.IncludedCount != want || int64(len(result.Body)) > limit || !json.Valid(result.Body) {
					t.Fatalf("limit %d: count=%d, err=%v", limit, result.IncludedCount, err)
				}
				if result.SelectedBytes != full.SelectedBytes {
					t.Fatal("budget lost overflowing configured entries")
				}
			}
			filtered, err := Build(snapshot, state.AccessKeyView{Filters: state.FilterSet{Groups: map[uint]struct{}{2: {}}}}, version, LimitBytes)
			if err != nil || string(filtered.Body) != `{"models":[]}` {
				t.Fatalf("permission-filtered catalog = %s, %v", filtered.Body, err)
			}
		})
	}
}
