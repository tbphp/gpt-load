package storage

import (
	"reflect"
	"testing"
)

func TestMySQLModelPriceIdentityStatementsPreserveExactModelIDs(t *testing.T) {
	got, err := mysqlModelPriceIdentityStatements("mysql")
	if err != nil {
		t.Fatalf("mysqlModelPriceIdentityStatements() error = %v", err)
	}
	want := []string{
		"ALTER TABLE model_prices MODIFY COLUMN model_id varchar(255) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("mysqlModelPriceIdentityStatements() = %#v, want %#v", got, want)
	}
}

func TestMySQLModelPriceIdentityStatementsLeaveCaseSensitiveDriversUntouched(t *testing.T) {
	for _, driver := range []string{"sqlite", "postgres", "postgresql"} {
		t.Run(driver, func(t *testing.T) {
			got, err := mysqlModelPriceIdentityStatements(driver)
			if err != nil {
				t.Fatalf("mysqlModelPriceIdentityStatements(%q) error = %v", driver, err)
			}
			if len(got) != 0 {
				t.Fatalf("mysqlModelPriceIdentityStatements(%q) = %#v, want no statements", driver, got)
			}
		})
	}
}

func TestMySQLModelPriceIdentityStatementsRejectUnsupportedDriver(t *testing.T) {
	if _, err := mysqlModelPriceIdentityStatements("sqlserver"); err == nil {
		t.Fatal("mysqlModelPriceIdentityStatements() accepted an unsupported driver")
	}
}
