package dbtx

import (
	"context"
	"testing"

	gormsqlite "github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestCapabilitiesForDriverPreserveDatabaseTransactionSemantics(t *testing.T) {
	tests := []struct {
		name     string
		driver   string
		writeSQL string
		readSQL  string
	}{
		{name: "sqlite", driver: "sqlite", writeSQL: "BEGIN IMMEDIATE", readSQL: "BEGIN"},
		{name: "mysql", driver: "mysql", writeSQL: "BEGIN", readSQL: "START TRANSACTION WITH CONSISTENT SNAPSHOT"},
		{name: "postgres", driver: "postgres", writeSQL: "BEGIN", readSQL: "BEGIN ISOLATION LEVEL REPEATABLE READ"},
		{name: "postgresql alias", driver: "postgresql", writeSQL: "BEGIN", readSQL: "BEGIN ISOLATION LEVEL REPEATABLE READ"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			capabilities, err := CapabilitiesForDriver(test.driver)
			if err != nil {
				t.Fatalf("CapabilitiesForDriver(%q) error = %v", test.driver, err)
			}
			writeSQL, err := capabilities.beginSQL(Write)
			if err != nil || writeSQL != test.writeSQL {
				t.Fatalf("write begin SQL = %q/%v, want %q/nil", writeSQL, err, test.writeSQL)
			}
			readSQL, err := capabilities.beginSQL(ReadSnapshot)
			if err != nil || readSQL != test.readSQL {
				t.Fatalf("read begin SQL = %q/%v, want %q/nil", readSQL, err, test.readSQL)
			}
		})
	}
}

func TestDiscardConnectionTreatsBadConnectionAsSuccessfulCleanup(t *testing.T) {
	db, err := gorm.Open(
		gormsqlite.Open(":memory:"),
		&gorm.Config{Logger: logger.Default.LogMode(logger.Silent)},
	)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get database: %v", err)
	}
	defer sqlDB.Close()

	connection, err := sqlDB.Conn(context.Background())
	if err != nil {
		t.Fatalf("get connection: %v", err)
	}
	if err := discardConnection("test", connection); err != nil {
		t.Fatalf("discardConnection() error = %v, want nil", err)
	}
}
