package storage

import (
	"fmt"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/sirupsen/logrus"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"gpt-load/internal/platform/config"
	"gpt-load/internal/platform/securefile"
)

const sqliteBusyTimeoutMS = 5000

type sqliteTarget struct {
	fileBacked   bool
	databasePath string
	directory    string
}

var databaseLogger = logger.New(log.New(os.Stdout, "\r\n", log.LstdFlags), logger.Config{
	SlowThreshold:        200 * time.Millisecond,
	LogLevel:             logger.Warn,
	ParameterizedQueries: true,
	Colorful:             true,
})

var hardenManagedFileIfExists = securefile.HardenManagedFileIfExists

// Open opens a SQLite database using a fully resolved DSN.
// Resolving an empty DSN to DATA_DIR belongs to platform/config.
func Open(dsn string) (*gorm.DB, error) {
	return OpenWithSource(dsn, config.DatabaseSourceExternal)
}

// OpenWithSource opens a SQLite database and applies file controls only when
// the application owns the managed database location.
func OpenWithSource(dsn string, source config.DatabaseSource) (*gorm.DB, error) {
	dsn = strings.TrimSpace(dsn)
	target, err := parseSQLiteTarget(dsn)
	if err != nil {
		return nil, err
	}
	if err := rejectExistingDatabaseWithoutMigrationLedger(target); err != nil {
		return nil, err
	}
	switch source {
	case config.DatabaseSourceManaged:
		if target.fileBacked {
			if err := secureManagedSQLiteTarget(target); err != nil {
				return nil, err
			}
		}
	case config.DatabaseSourceExternal:
		logrus.WithField("database_source", config.DatabaseSourceExternal).
			Info("SQLite database storage is managed by the operator")
	default:
		return nil, fmt.Errorf("open SQLite database: unsupported database source")
	}

	runtimeDSN, err := withSQLiteRuntimeOptions(dsn, target.fileBacked)
	if err != nil {
		return nil, err
	}
	db, err := gorm.Open(sqlite.Open(runtimeDSN), &gorm.Config{Logger: databaseLogger})
	if err != nil {
		return nil, fmt.Errorf("open SQLite database: %w", err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("get SQLite connection pool: %w", err)
	}
	sqlDB.SetMaxOpenConns(1)
	sqlDB.SetMaxIdleConns(1)
	if err := verifySQLiteRuntime(db, target.fileBacked); err != nil {
		_ = sqlDB.Close()
		return nil, err
	}
	if source == config.DatabaseSourceManaged && target.fileBacked {
		if err := hardenSQLiteRecoverySet(target); err != nil {
			_ = sqlDB.Close()
			return nil, err
		}
	}

	return db, nil
}

// rejectExistingDatabaseWithoutMigrationLedger inspects an existing file with
// SQLite read-only mode before runtime pragmas can change journal state.
func rejectExistingDatabaseWithoutMigrationLedger(target sqliteTarget) error {
	if !target.fileBacked {
		return nil
	}
	if _, err := os.Lstat(target.databasePath); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return fmt.Errorf("inspect SQLite database before migration: %w", err)
	}
	absolutePath, err := filepath.Abs(target.databasePath)
	if err != nil {
		return fmt.Errorf("resolve SQLite database path before migration: %w", err)
	}
	readOnlyDSN := (&url.URL{Scheme: "file", Path: absolutePath}).String() + "?mode=ro&immutable=1"
	db, err := gorm.Open(sqlite.Open(readOnlyDSN), &gorm.Config{Logger: databaseLogger})
	if err != nil {
		return fmt.Errorf("inspect existing SQLite database before migration: %w", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		return fmt.Errorf("inspect existing SQLite database connection: %w", err)
	}
	defer func() { _ = sqlDB.Close() }()

	tables, err := db.Migrator().GetTables()
	if err != nil {
		return fmt.Errorf("inspect existing SQLite tables before migration: %w", err)
	}
	for _, table := range tables {
		if table == migrationLedgerTable {
			return nil
		}
	}
	for _, table := range tables {
		if !strings.HasPrefix(table, "sqlite_") {
			return fmt.Errorf("open SQLite database: non-empty database without schema_migrations")
		}
	}
	return nil
}

// AutoMigrate applies every pending SQLite migration before the application starts.
func AutoMigrate(db *gorm.DB) error {
	return applyMigrations(db)
}

func withSQLiteRuntimeOptions(dsn string, fileBacked bool) (string, error) {
	base, rawQuery, _ := strings.Cut(dsn, "?")
	query, err := url.ParseQuery(rawQuery)
	if err != nil {
		return "", fmt.Errorf("open SQLite database: invalid DSN query: %w", err)
	}
	query.Set("_txlock", "immediate")

	pragmas := make([]string, 0, len(query["_pragma"])+3)
	for _, pragma := range query["_pragma"] {
		name := strings.ToLower(strings.TrimSpace(pragma))
		if index := strings.IndexAny(name, "(="); index >= 0 {
			name = strings.TrimSpace(name[:index])
		}
		switch name {
		case "foreign_keys", "busy_timeout", "journal_mode":
			continue
		default:
			pragmas = append(pragmas, pragma)
		}
	}
	pragmas = append(pragmas,
		"foreign_keys(1)",
		fmt.Sprintf("busy_timeout(%d)", sqliteBusyTimeoutMS),
	)
	if fileBacked {
		pragmas = append(pragmas, "journal_mode(WAL)")
	}
	query["_pragma"] = pragmas
	return base + "?" + query.Encode(), nil
}

func isSQLiteMemoryDSN(dsn string) bool {
	base, _, _ := strings.Cut(dsn, "?")
	if base == ":memory:" {
		return true
	}
	if !strings.HasPrefix(dsn, "file:") {
		return false
	}
	parsed, err := url.Parse(dsn)
	if err != nil {
		return false
	}
	if strings.EqualFold(parsed.Query().Get("mode"), "memory") {
		return true
	}
	return parsed.Path == ":memory:" || parsed.Opaque == ":memory:"
}

func verifySQLiteRuntime(db *gorm.DB, fileBacked bool) error {
	var foreignKeys, busyTimeout int
	if err := db.Raw("PRAGMA foreign_keys").Scan(&foreignKeys).Error; err != nil {
		return fmt.Errorf("verify SQLite foreign_keys: %w", err)
	}
	if foreignKeys != 1 {
		return fmt.Errorf("verify SQLite foreign_keys: got %d, want 1", foreignKeys)
	}
	if err := db.Raw("PRAGMA busy_timeout").Scan(&busyTimeout).Error; err != nil {
		return fmt.Errorf("verify SQLite busy_timeout: %w", err)
	}
	if busyTimeout != sqliteBusyTimeoutMS {
		return fmt.Errorf("verify SQLite busy_timeout: got %d, want %d", busyTimeout, sqliteBusyTimeoutMS)
	}
	if fileBacked {
		var journalMode string
		if err := db.Raw("PRAGMA journal_mode").Scan(&journalMode).Error; err != nil {
			return fmt.Errorf("verify SQLite journal_mode: %w", err)
		}
		if !strings.EqualFold(journalMode, "wal") {
			return fmt.Errorf("verify SQLite journal_mode: got %q, want wal", journalMode)
		}
	}
	return nil
}

func parseSQLiteTarget(dsn string) (sqliteTarget, error) {
	if isSQLiteMemoryDSN(dsn) {
		return sqliteTarget{}, nil
	}
	if err := validateSQLiteDSN(dsn); err != nil {
		return sqliteTarget{}, err
	}

	databasePath, _, _ := strings.Cut(dsn, "?")
	parsed, err := url.Parse(dsn)
	if err != nil {
		return sqliteTarget{}, fmt.Errorf("open SQLite database: invalid DSN: %w", err)
	}
	if strings.HasPrefix(dsn, "file:") {
		databasePath = parsed.Path
		if databasePath == "" {
			databasePath = parsed.Opaque
			databasePath, err = url.PathUnescape(databasePath)
			if err != nil {
				return sqliteTarget{}, fmt.Errorf("open SQLite database: invalid file URI: %w", err)
			}
		}
		if runtime.GOOS == "windows" &&
			len(databasePath) >= 3 &&
			databasePath[0] == '/' &&
			databasePath[2] == ':' &&
			(databasePath[1] >= 'A' && databasePath[1] <= 'Z' ||
				databasePath[1] >= 'a' && databasePath[1] <= 'z') {
			databasePath = databasePath[1:]
		}
		databasePath = filepath.FromSlash(databasePath)
	}
	if databasePath == "" {
		return sqliteTarget{}, fmt.Errorf("open SQLite database: file path is empty")
	}

	directory := filepath.Dir(databasePath)
	return sqliteTarget{
		fileBacked:   true,
		databasePath: databasePath,
		directory:    directory,
	}, nil
}

func secureManagedSQLiteTarget(target sqliteTarget) error {
	if err := securefile.PrepareManagedDataDir(target.directory); err != nil {
		return fmt.Errorf("secure managed SQLite directory: %w", err)
	}
	return hardenSQLiteRecoverySet(target)
}

func hardenSQLiteRecoverySet(target sqliteTarget) error {
	for _, path := range []string{
		target.databasePath,
		target.databasePath + "-wal",
		target.databasePath + "-shm",
	} {
		if err := hardenManagedFileIfExists(path); err != nil {
			return fmt.Errorf("secure managed SQLite recovery set: %w", err)
		}
	}
	return nil
}

func validateSQLiteDSN(dsn string) error {
	if dsn == "" {
		return fmt.Errorf("open SQLite database: DSN is empty")
	}
	if dsn == ":memory:" || filepath.VolumeName(dsn) != "" {
		return nil
	}

	parsed, err := url.Parse(dsn)
	if err != nil {
		return fmt.Errorf("open SQLite database: invalid DSN: %w", err)
	}
	if parsed.Scheme == "file" && !strings.HasPrefix(dsn, "file:") {
		return fmt.Errorf("open SQLite database: unsupported non-canonical DSN scheme")
	}
	if parsed.Scheme != "" && parsed.Scheme != "file" {
		return fmt.Errorf("open SQLite database: unsupported DSN scheme %q", parsed.Scheme)
	}
	return nil
}
