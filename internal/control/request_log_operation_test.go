package control

import (
	"testing"

	"gpt-load/internal/execution"
)

func TestRequestLogQueryFiltersLogicalOperation(t *testing.T) {
	t.Parallel()
	for _, operation := range []execution.Operation{execution.OperationChatCompletion, execution.OperationWebSearch, execution.OperationResponsesRetrieve} {
		query, apiErr := parseRequestLogQuery("operation=" + string(operation))
		if apiErr != nil || query.Operation != operation {
			t.Fatalf("operation %q parsed as %#v, error = %v", operation, query.Operation, apiErr)
		}
	}
	for _, query := range []string{"operation=unknown", "operation=", "operation=web_search&operation=chat_completion"} {
		if _, apiErr := parseRequestLogQuery(query); apiErr == nil {
			t.Fatalf("query %q accepted", query)
		}
	}
	if requestLogQueryUsesInternalFields("operation=web_search") {
		t.Fatal("logical operation must remain available in self-scoped logs")
	}
}
