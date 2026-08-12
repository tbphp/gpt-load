package dbtx

import (
	"context"
	"reflect"
	"testing"

	gormsqlite "github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestCapabilitiesForDriverPreserveDatabaseTransactionSemantics(t *testing.T) {
	tests := []struct {
		name     string
		driver   string
		writeSQL []string
		readSQL  []string
	}{
		{name: "sqlite", driver: "sqlite", writeSQL: []string{"BEGIN IMMEDIATE"}, readSQL: []string{"BEGIN"}},
		{name: "mysql", driver: "mysql", writeSQL: []string{"BEGIN"}, readSQL: []string{
			"SET TRANSACTION ISOLATION LEVEL REPEATABLE READ",
			"START TRANSACTION WITH CONSISTENT SNAPSHOT",
		}},
		{name: "postgres", driver: "postgres", writeSQL: []string{"BEGIN"}, readSQL: []string{"BEGIN ISOLATION LEVEL REPEATABLE READ"}},
		{name: "postgresql alias", driver: "postgresql", writeSQL: []string{"BEGIN"}, readSQL: []string{"BEGIN ISOLATION LEVEL REPEATABLE READ"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			capabilities, err := CapabilitiesForDriver(test.driver)
			if err != nil {
				t.Fatalf("CapabilitiesForDriver(%q) error = %v", test.driver, err)
			}
			writeSQL, err := capabilities.beginStatements(Write)
			if err != nil || !reflect.DeepEqual(writeSQL, test.writeSQL) {
				t.Fatalf("write begin SQL = %#v/%v, want %#v/nil", writeSQL, err, test.writeSQL)
			}
			readSQL, err := capabilities.beginStatements(ReadSnapshot)
			if err != nil || !reflect.DeepEqual(readSQL, test.readSQL) {
				t.Fatalf("read begin SQL = %#v/%v, want %#v/nil", readSQL, err, test.readSQL)
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
