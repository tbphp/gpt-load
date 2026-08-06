//go:build !windows

package storage

import (
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gorm.io/gorm"

	"gpt-load/internal/platform/config"
)

func TestOpenWithSourceManagedHardensSQLiteRecoverySet(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "managed")
	if err := os.Mkdir(dataDir, 0o777); err != nil {
		t.Fatalf("create managed directory: %v", err)
	}
	if err := os.Chmod(dataDir, 0o777); err != nil {
		t.Fatalf("widen managed directory: %v", err)
	}
	databasePath := filepath.Join(dataDir, "ordinary.db")
	if err := os.WriteFile(databasePath, nil, 0o666); err != nil {
		t.Fatalf("create existing database: %v", err)
	}
	if err := os.Chmod(databasePath, 0o666); err != nil {
		t.Fatalf("widen existing database: %v", err)
	}

	db := openSourceDatabase(
		t,
		databasePath+"?cache=shared",
		config.DatabaseSourceManaged,
	)
	writeSQLiteRecoverySet(t, db)

	assertPathMode(t, dataDir, 0o700)
	assertRecoverySetModes(t, databasePath, 0o600)
}

func TestOpenWithSourceManagedLocatesFileURIAndQueryRecoverySet(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "managed uri")
	databasePath := filepath.Join(dataDir, "uri database.db")
	dsn := (&url.URL{Scheme: "file", Path: databasePath}).String() +
		"?cache=shared&_pragma=synchronous(NORMAL)"

	db := openSourceDatabase(t, dsn, config.DatabaseSourceManaged)
	writeSQLiteRecoverySet(t, db)

	assertPathMode(t, dataDir, 0o700)
	assertRecoverySetModes(t, databasePath, 0o600)
}

func TestOpenWithSourceManagedTreatsOrdinaryModeMemoryQueryAsFileBacked(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "ordinary-mode-memory")
	if err := os.Mkdir(dataDir, 0o777); err != nil {
		t.Fatalf("create managed directory: %v", err)
	}
	if err := os.Chmod(dataDir, 0o777); err != nil {
		t.Fatalf("widen managed directory: %v", err)
	}
	databasePath := filepath.Join(dataDir, "ordinary.db")

	db := openSourceDatabase(
		t,
		databasePath+"?mode=memory",
		config.DatabaseSourceManaged,
	)
	writeSQLiteRecoverySet(t, db)

	if _, err := os.Stat(databasePath); err != nil {
		t.Errorf("ordinary mode=memory DSN did not create disk database: %v", err)
	}
	var journalMode string
	if err := db.Raw("PRAGMA journal_mode").Scan(&journalMode).Error; err != nil {
		t.Fatalf("read journal mode: %v", err)
	}
	if !strings.EqualFold(journalMode, "wal") {
		t.Errorf("ordinary mode=memory journal mode = %q, want wal", journalMode)
	}
	assertPathMode(t, dataDir, 0o700)
	assertRecoverySetModes(t, databasePath, 0o600)
}

func TestOpenWithSourceManagedRejectsNonCanonicalFileSchemeWithoutMutation(t *testing.T) {
	workingDir := t.TempDir()
	previousWorkingDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("get working directory: %v", err)
	}
	if err := os.Chdir(workingDir); err != nil {
		t.Fatalf("change to isolated working directory: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(previousWorkingDir); err != nil {
			t.Errorf("restore working directory: %v", err)
		}
	})

	targetDir := "target"
	databasePath := filepath.Join(targetDir, "managed.db")
	if err := os.Mkdir(targetDir, 0o755); err != nil {
		t.Fatalf("create target directory: %v", err)
	}
	if err := os.Chmod(targetDir, 0o755); err != nil {
		t.Fatalf("set target directory mode: %v", err)
	}
	wantModes := map[string]os.FileMode{
		targetDir:             0o755,
		databasePath:          0o644,
		databasePath + "-wal": 0o664,
		databasePath + "-shm": 0o640,
	}
	for path, mode := range wantModes {
		if path == targetDir {
			continue
		}
		if err := os.WriteFile(path, []byte("operator-owned"), 0o600); err != nil {
			t.Fatalf("create target recovery file: %v", err)
		}
		if err := os.Chmod(path, mode); err != nil {
			t.Fatalf("set target recovery file mode: %v", err)
		}
	}

	const dsn = "FILE:target/managed.db?mode=memory"
	db, err := OpenWithSource(dsn, config.DatabaseSourceManaged)
	if db != nil {
		sqlDB, sqlErr := db.DB()
		if sqlErr == nil {
			_ = sqlDB.Close()
		}
	}
	if err == nil {
		t.Fatal("OpenWithSource(uppercase FILE) error = nil, want fail-closed rejection")
	}
	if !strings.Contains(err.Error(), "unsupported") {
		t.Errorf("OpenWithSource(uppercase FILE) error = %v, want safe unsupported error", err)
	}
	for _, forbidden := range []string{workingDir, databasePath} {
		if strings.Contains(err.Error(), forbidden) {
			t.Errorf("uppercase FILE error exposed %q: %v", forbidden, err)
		}
	}
	for path, want := range wantModes {
		info, statErr := os.Stat(path)
		if statErr != nil {
			t.Errorf("stat pre-existing target path: %v", statErr)
			continue
		}
		if got := info.Mode().Perm(); got != want {
			t.Errorf("target path permissions = %o, want unchanged %o", got, want)
		}
	}
	if _, statErr := os.Stat("FILE:target"); !os.IsNotExist(statErr) {
		t.Errorf("ordinary driver directory stat error = %v, want not created", statErr)
	}
}

func TestOpenWithSourceExternalPreservesDirectoryAndDatabaseModes(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "external")
	if err := os.Mkdir(dataDir, 0o750); err != nil {
		t.Fatalf("create external directory: %v", err)
	}
	if err := os.Chmod(dataDir, 0o750); err != nil {
		t.Fatalf("set external directory mode: %v", err)
	}
	databasePath := filepath.Join(dataDir, "operator.db")
	first := openSourceDatabase(t, databasePath, config.DatabaseSourceExternal)
	if err := AutoMigrate(first); err != nil {
		t.Fatalf("initialize external migration ledger: %v", err)
	}
	writeSQLiteRecoverySet(t, first)

	wantModes := map[string]os.FileMode{
		databasePath:          0o640,
		databasePath + "-wal": 0o660,
		databasePath + "-shm": 0o640,
	}
	for path, mode := range wantModes {
		if err := os.Chmod(path, mode); err != nil {
			t.Fatalf("set external recovery file mode: %v", err)
		}
	}

	second := openSourceDatabase(t, databasePath, config.DatabaseSourceExternal)
	if err := second.Exec("INSERT INTO permission_probe(value) VALUES ('second')").Error; err != nil {
		t.Fatalf("write through second external connection: %v", err)
	}

	assertPathMode(t, dataDir, 0o750)
	for path, mode := range wantModes {
		assertPathMode(t, path, mode)
	}
}

func TestOpenWithSourceManagedFailsWhenRecoverySetCannotBeSecured(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "managed")
	if err := os.Mkdir(dataDir, 0o700); err != nil {
		t.Fatalf("create managed directory: %v", err)
	}
	databasePath := filepath.Join(dataDir, "sensitive.db")
	target := filepath.Join(t.TempDir(), "operator-owned-wal")
	if err := os.WriteFile(target, []byte("do not touch"), 0o644); err != nil {
		t.Fatalf("write symlink target: %v", err)
	}
	if err := os.Chmod(target, 0o644); err != nil {
		t.Fatalf("set symlink target mode: %v", err)
	}
	if err := os.Symlink(target, databasePath+"-wal"); err != nil {
		t.Fatalf("create managed WAL symlink: %v", err)
	}

	t.Run("pre-open symlink", func(t *testing.T) {
		db, err := OpenWithSource(databasePath, config.DatabaseSourceManaged)
		if db != nil {
			sqlDB, sqlErr := db.DB()
			if sqlErr == nil {
				_ = sqlDB.Close()
			}
		}
		if err == nil {
			t.Fatal("OpenWithSource(managed) error = nil, want fail-closed recovery-set rejection")
		}
		for _, forbidden := range []string{dataDir, databasePath, target} {
			if strings.Contains(err.Error(), forbidden) {
				t.Fatalf("managed hardening error exposed %q: %v", forbidden, err)
			}
		}
		if _, statErr := os.Stat(databasePath); !os.IsNotExist(statErr) {
			t.Fatalf("database stat error = %v, want not created", statErr)
		}
		assertPathMode(t, target, 0o644)
	})

	t.Run("post-open hardening failure", func(t *testing.T) {
		postDataDir := filepath.Join(t.TempDir(), "managed")
		postDatabasePath := filepath.Join(postDataDir, "post-open.db")
		originalHarden := hardenManagedFileIfExists
		callCount := 0
		hardenManagedFileIfExists = func(path string) error {
			callCount++
			if callCount == 4 {
				return errors.New("injected hardening failure")
			}
			return originalHarden(path)
		}
		t.Cleanup(func() {
			hardenManagedFileIfExists = originalHarden
		})

		db, err := OpenWithSource(postDatabasePath, config.DatabaseSourceManaged)
		if db != nil {
			sqlDB, sqlErr := db.DB()
			if sqlErr == nil {
				_ = sqlDB.Close()
			}
		}
		if err == nil {
			t.Fatal("OpenWithSource(managed) error = nil, want post-open hardening failure")
		}
		if strings.Contains(err.Error(), postDataDir) ||
			strings.Contains(err.Error(), postDatabasePath) {
			t.Fatalf("post-open hardening error exposed database location: %v", err)
		}
	})
}

func openSourceDatabase(
	t *testing.T,
	dsn string,
	source config.DatabaseSource,
) *gorm.DB {
	t.Helper()

	db, err := OpenWithSource(dsn, source)
	if err != nil {
		t.Fatalf("OpenWithSource(%s) error = %v", source, err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("DB() error = %v", err)
	}
	t.Cleanup(func() {
		if err := sqlDB.Close(); err != nil {
			t.Errorf("close database: %v", err)
		}
	})
	return db
}

func writeSQLiteRecoverySet(t *testing.T, db *gorm.DB) {
	t.Helper()

	if err := db.Exec(
		"CREATE TABLE IF NOT EXISTS permission_probe (value TEXT NOT NULL)",
	).Error; err != nil {
		t.Fatalf("create permission probe table: %v", err)
	}
	if err := db.Exec(
		"INSERT INTO permission_probe(value) VALUES ('probe')",
	).Error; err != nil {
		t.Fatalf("write permission probe: %v", err)
	}
}

func assertRecoverySetModes(t *testing.T, databasePath string, mode os.FileMode) {
	t.Helper()

	for _, path := range []string{
		databasePath,
		databasePath + "-wal",
		databasePath + "-shm",
	} {
		assertPathMode(t, path, mode)
	}
}

func assertPathMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat expected path: %v", err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Fatalf("path permissions = %o, want %o", got, want)
	}
}
