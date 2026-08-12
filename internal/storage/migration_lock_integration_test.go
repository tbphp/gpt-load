package storage

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"gpt-load/internal/platform/config"
)

// TestExternalPostgresMigrationLockTimesOut is opt-in with the external
// database matrix. It holds the process-level advisory lock on one pinned
// connection and verifies a second connection observes its context deadline.
func TestExternalPostgresMigrationLockTimesOut(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("GPT_LOAD_DATABASE_TEST_DSN"))
	if dsn == "" {
		t.Skip("GPT_LOAD_DATABASE_TEST_DSN is not set")
	}
	db, err := OpenWithSource(dsn, config.DatabaseSourceExternal)
	if err != nil {
		t.Fatalf("OpenWithSource() error = %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if db.Dialector.Name() != "postgres" {
		t.Skip("advisory-lock timeout setup is specific to PostgreSQL")
	}

	connection, err := sqlDB.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = connection.Close() })
	if _, err := connection.ExecContext(
		t.Context(),
		`SELECT pg_advisory_lock(hashtext('gpt-load:schema-migrations:v2'))`,
	); err != nil {
		t.Fatalf("hold PostgreSQL migration lock: %v", err)
	}
	t.Cleanup(func() {
		_, _ = connection.ExecContext(
			context.Background(),
			`SELECT pg_advisory_unlock(hashtext('gpt-load:schema-migrations:v2'))`,
		)
	})

	ctx, cancel := context.WithTimeout(t.Context(), 250*time.Millisecond)
	defer cancel()
	err = acquirePostgresMigrationLock(ctx, db, 10*time.Millisecond)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("acquirePostgresMigrationLock() error = %v, want deadline exceeded", err)
	}
}
