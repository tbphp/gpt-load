package storage

import (
	"strings"
	"testing"
)

func TestGroupUsageIndexSupportsPageWindowLookup(t *testing.T) {
	db := openInternalMigrationTestDatabase(t)
	if err := applyMigrations(db); err != nil {
		t.Fatal(err)
	}
	var plan []struct{ Detail string }
	if err := db.Raw("EXPLAIN QUERY PLAN SELECT SUM(output_tokens) FROM request_logs WHERE group_id IN (1,2) AND completed_at_ms >= 1000 AND completed_at_ms < 2000 AND attempt_count > 0").Scan(&plan).Error; err != nil {
		t.Fatal(err)
	}
	if len(plan) != 1 || !strings.Contains(plan[0].Detail, "idx_request_logs_group_completed") || !strings.Contains(plan[0].Detail, "group_id=? AND completed_at_ms") {
		t.Fatalf("current-page lookup must use group and time together: %+v", plan)
	}
	if err := applyMigrations(db); err != nil {
		t.Fatalf("repeat migration: %v", err)
	}
}
