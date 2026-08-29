package storage

import (
	"strings"
	"testing"

	"gpt-load/internal/platform/config"
)

func TestDatabasePoolLimitsUseConfiguredValuesForNetworkDatabases(t *testing.T) {
	pool := config.DatabasePoolConfig{
		MaxOpenConnections: 24,
		MaxIdleConnections: 12,
	}

	for _, driver := range []config.DatabaseDriver{
		config.DatabaseDriverMySQL,
		config.DatabaseDriverPostgreSQL,
	} {
		maxOpen, maxIdle := databasePoolLimits(driver, pool)
		if maxOpen != 24 || maxIdle != 12 {
			t.Fatalf("databasePoolLimits(%q) = %d/%d, want 24/12", driver, maxOpen, maxIdle)
		}
	}
}

func TestDatabasePoolLimitsForceSQLiteSingleConnection(t *testing.T) {
	maxOpen, maxIdle := databasePoolLimits(config.DatabaseDriverSQLite, config.DatabasePoolConfig{
		MaxOpenConnections: 24,
		MaxIdleConnections: 12,
	})
	if maxOpen != 1 || maxIdle != 1 {
		t.Fatalf("databasePoolLimits(SQLite) = %d/%d, want 1/1", maxOpen, maxIdle)
	}
}

func TestNewDatabaseDialectorSelectsAllSupportedDrivers(t *testing.T) {
	for _, test := range []struct {
		name       string
		dsn        string
		wantDriver config.DatabaseDriver
	}{
		{name: "sqlite", dsn: ":memory:", wantDriver: config.DatabaseDriverSQLite},
		{name: "mysql", dsn: "mysql://user:password@db.example:3306/gpt_load", wantDriver: config.DatabaseDriverMySQL},
		{name: "postgres", dsn: "postgres://user:password@db.example:5432/gpt_load", wantDriver: config.DatabaseDriverPostgreSQL},
	} {
		t.Run(test.name, func(t *testing.T) {
			database, err := config.ParseDatabaseDSN(test.dsn)
			if err != nil {
				t.Fatalf("ParseDatabaseDSN() error = %v", err)
			}
			dialector, err := newDatabaseDialector(database)
			if err != nil {
				t.Fatalf("newDatabaseDialector() error = %v", err)
			}
			if got := dialector.Name(); got != string(test.wantDriver) {
				t.Fatalf("dialector.Name() = %q, want %q", got, test.wantDriver)
			}
		})
	}
}

func TestMySQLURLConvertsToDriverDSN(t *testing.T) {
	got, err := mysqlDSNFromURL("mysql://user:p%40ss@db.example:3306/gpt_load?tls=true")
	if err != nil {
		t.Fatalf("mysqlDSNFromURL() error = %v", err)
	}
	want := "user:p@ss@tcp(db.example:3306)/gpt_load?charset=utf8mb4&clientFoundRows=true&collation=utf8mb4_bin&parseTime=true&tls=true"
	if got != want {
		t.Fatalf("mysqlDSNFromURL() = %q, want %q", got, want)
	}
}

func TestMySQLURLOverridesDriverInvariants(t *testing.T) {
	got, err := mysqlDSNFromURL("mysql://user:password@db.example/gpt_load?parseTime=false&clientFoundRows=false&charset=latin1&collation=utf8mb4_general_ci")
	if err != nil {
		t.Fatalf("mysqlDSNFromURL() error = %v", err)
	}
	want := "user:password@tcp(db.example)/gpt_load?charset=latin1&clientFoundRows=true&collation=utf8mb4_general_ci&parseTime=true"
	if got != want {
		t.Fatalf("mysqlDSNFromURL() = %q, want %q", got, want)
	}
}

func TestMySQLURLRejectsMissingDatabaseName(t *testing.T) {
	if _, err := mysqlDSNFromURL("mysql://user:password@db.example"); err == nil {
		t.Fatal("mysqlDSNFromURL() error = nil, want missing database name error")
	}
}

func TestOpenSQLiteURLUsesCommonLifecycleAndSQLiteRuntime(t *testing.T) {
	db, err := Open("sqlite:///:memory:")
	if err != nil {
		t.Fatalf("Open(sqlite URL) error = %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("db.DB() error = %v", err)
	}
	t.Cleanup(func() {
		if err := sqlDB.Close(); err != nil {
			t.Errorf("close database: %v", err)
		}
	})

	if !db.Config.TranslateError {
		t.Fatal("TranslateError = false, want true")
	}
	if err := sqlDB.Ping(); err != nil {
		t.Fatalf("Ping() error = %v", err)
	}
	stats := sqlDB.Stats()
	if stats.MaxOpenConnections != 1 {
		t.Fatalf("SQLite pool max open connections = %d, want 1", stats.MaxOpenConnections)
	}
}

func TestOpenNetworkDatabaseRejectsManagedSourceBeforeConnecting(t *testing.T) {
	for _, dsn := range []string{
		"mysql://user:password@db.example:3306/gpt_load",
		"postgres://user:password@db.example:5432/gpt_load",
	} {
		_, err := OpenWithSource(dsn, config.DatabaseSourceManaged)
		if err == nil {
			t.Fatalf("OpenWithSource(%q, managed) error = nil, want source validation error", dsn)
		}
		if !strings.Contains(err.Error(), "managed source") {
			t.Fatalf("OpenWithSource(%q, managed) error = %v, want managed-source error", dsn, err)
		}
	}
}
