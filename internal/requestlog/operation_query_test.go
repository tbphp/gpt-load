package requestlog

import (
	"context"
	"testing"
	"time"

	"gpt-load/internal/execution"
)

func TestServiceListFiltersLogicalOperationBeforePagination(t *testing.T) {
	db := openRequestLogQueryDB(t)
	completed := time.Now()
	for index, operation := range []execution.Operation{execution.OperationWebSearch, execution.OperationChatCompletion, execution.OperationWebSearch} {
		id := []string{"00000000-0000-4000-8000-000000000701", "00000000-0000-4000-8000-000000000702", "00000000-0000-4000-8000-000000000703"}[index]
		row := requestLogQueryRow(id, completed, 1, "model", nil)
		row.Operation = string(operation)
		createRequestLogQueryRow(t, db, row)
	}
	service := newRequestLogTestService(db)
	query := ListQuery{Operation: execution.OperationWebSearch, Limit: 1}
	page, err := service.List(context.Background(), query)
	if err != nil || len(page.Items) != 1 || page.Items[0].RequestID != "00000000-0000-4000-8000-000000000703" || page.NextCursor == nil {
		t.Fatalf("first page = %#v, error = %v", page, err)
	}
	query.Cursor = page.NextCursor
	page, err = service.List(context.Background(), query)
	if err != nil || len(page.Items) != 1 || page.Items[0].RequestID != "00000000-0000-4000-8000-000000000701" || page.NextCursor != nil {
		t.Fatalf("second page = %#v, error = %v", page, err)
	}
}
