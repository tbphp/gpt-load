package storage

import (
	"reflect"
	"testing"
)

func TestGlobalModelPriceSchemaStatementsSupportExternalDrivers(t *testing.T) {
	tests := []struct {
		name   string
		driver string
		want   []string
	}{
		{
			name:   "PostgreSQL",
			driver: "postgres",
			want: []string{
				"DROP INDEX idx_model_prices_scope_model",
				"ALTER TABLE model_prices DROP COLUMN price_scope_key",
				"CREATE UNIQUE INDEX idx_model_prices_model ON model_prices(model_id)",
			},
		},
		{
			name:   "PostgreSQL alias",
			driver: "postgresql",
			want: []string{
				"DROP INDEX idx_model_prices_scope_model",
				"ALTER TABLE model_prices DROP COLUMN price_scope_key",
				"CREATE UNIQUE INDEX idx_model_prices_model ON model_prices(model_id)",
			},
		},
		{
			name:   "MySQL",
			driver: "mysql",
			want: []string{
				"ALTER TABLE model_prices DROP INDEX idx_model_prices_scope_model, DROP COLUMN price_scope_key, ADD UNIQUE INDEX idx_model_prices_model (model_id)",
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := globalModelPriceSchemaStatements(test.driver)
			if err != nil {
				t.Fatalf("globalModelPriceSchemaStatements() error = %v", err)
			}
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("globalModelPriceSchemaStatements() = %#v, want %#v", got, test.want)
			}
		})
	}
}

func TestGlobalModelPriceSchemaStatementsRejectUnsupportedDriver(t *testing.T) {
	if _, err := globalModelPriceSchemaStatements("sqlserver"); err == nil {
		t.Fatal("globalModelPriceSchemaStatements() accepted an unsupported driver")
	}
}
