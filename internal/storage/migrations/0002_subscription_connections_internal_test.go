package migrations

import (
	"strings"
	"testing"
)

func TestSubscriptionColumnDDLQuotesIdentifiersByDialect(t *testing.T) {
	column := subscriptionColumn0002{table: "groups", name: "connection_type", base: "varchar(32)"}
	if got := column.sql("mysql"); !strings.HasPrefix(got, "ALTER TABLE `groups` ADD COLUMN `connection_type`") {
		t.Fatalf("MySQL DDL = %q", got)
	}
	if got := column.sql("postgres"); !strings.HasPrefix(got, `ALTER TABLE "groups" ADD COLUMN "connection_type"`) {
		t.Fatalf("PostgreSQL DDL = %q", got)
	}
}
