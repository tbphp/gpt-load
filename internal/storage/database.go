package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/sirupsen/logrus"
	gormmysql "gorm.io/driver/mysql"
	gormpostgres "gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"gpt-load/internal/platform/config"
)

var databaseLogger = newDatabaseLogger(os.Stdout)

func newDatabaseLogger(output io.Writer) logger.Interface {
	base := logger.New(log.New(output, "\r\n", log.LstdFlags), logger.Config{
		SlowThreshold:             200 * time.Millisecond,
		LogLevel:                  logger.Warn,
		IgnoreRecordNotFoundError: true,
		ParameterizedQueries:      true,
		Colorful:                  true,
	})
	return databaseLogFilter{Interface: base}
}

type databaseLogFilter struct {
	logger.Interface
}

func (filter databaseLogFilter) LogMode(level logger.LogLevel) logger.Interface {
	return databaseLogFilter{Interface: filter.Interface.LogMode(level)}
}

func (filter databaseLogFilter) Trace(
	ctx context.Context,
	begin time.Time,
	query func() (string, int64),
	err error,
) {
	if ctx != nil &&
		errors.Is(ctx.Err(), context.Canceled) &&
		errors.Is(err, context.Canceled) {
		return
	}
	filter.Interface.Trace(ctx, begin, query, err)
}

func (filter databaseLogFilter) ParamsFilter(
	ctx context.Context,
	query string,
	params ...interface{},
) (string, []interface{}) {
	if paramsFilter, ok := filter.Interface.(gorm.ParamsFilter); ok {
		return paramsFilter.ParamsFilter(ctx, query, params...)
	}
	return query, nil
}

// Open opens a database using a fully resolved DSN.
// Resolving an empty DSN to DATA_DIR belongs to platform/config.
func Open(dsn string) (*gorm.DB, error) {
	return OpenWithSource(dsn, config.DatabaseSourceExternal)
}

// OpenWithSource opens a database and applies file controls only when the
// application owns the managed SQLite location.
func OpenWithSource(dsn string, source config.DatabaseSource) (*gorm.DB, error) {
	return openWithSourceAndPool(dsn, source, config.DefaultDatabasePoolConfig())
}

// OpenConfigured opens the database using the process configuration resolved
// by platform/config. Storage never reads environment variables directly.
func OpenConfigured(cfg *config.Config) (*gorm.DB, error) {
	if cfg == nil {
		return nil, fmt.Errorf("open database: configuration is unavailable")
	}
	return openWithSourceAndPool(
		cfg.DatabaseDSN,
		cfg.DatabaseMetadata.Source,
		cfg.DatabasePool,
	)
}

func openWithSourceAndPool(
	dsn string,
	source config.DatabaseSource,
	pool config.DatabasePoolConfig,
) (*gorm.DB, error) {
	database, err := config.ParseDatabaseDSN(dsn)
	if err != nil {
		return nil, err
	}
	switch source {
	case config.DatabaseSourceManaged:
	case config.DatabaseSourceExternal:
	default:
		return nil, fmt.Errorf("open database: unsupported database source")
	}

	if database.Driver == config.DatabaseDriverSQLite {
		if source == config.DatabaseSourceExternal {
			logExternalDatabaseSource(database.Driver)
		}
		return openSQLite(database.DSN, source, pool)
	}
	if source == config.DatabaseSourceManaged {
		return nil, fmt.Errorf("open %s database: managed source is only supported by SQLite", databaseDisplayName(database.Driver))
	}
	logExternalDatabaseSource(database.Driver)
	dialector, err := newDatabaseDialector(database)
	if err != nil {
		return nil, err
	}
	return openDatabase(database.Driver, dialector, pool)
}

// openDatabase is the shared GORM/SQL lifecycle for every supported driver.
// Driver-specific behavior is limited to dialector construction and the
// SQLite runtime hook in sqlite.go.
func openDatabase(
	driver config.DatabaseDriver,
	dialector gorm.Dialector,
	pool config.DatabasePoolConfig,
) (*gorm.DB, error) {
	db, err := gorm.Open(dialector, &gorm.Config{
		Logger:         databaseLogger,
		TranslateError: true,
	})
	if err != nil {
		return nil, fmt.Errorf("open %s database: %w", databaseDisplayName(driver), err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("get %s connection pool: %w", databaseDisplayName(driver), err)
	}
	configureDatabasePool(sqlDB, driver, pool)
	if err := sqlDB.PingContext(context.Background()); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("ping %s database: %w", databaseDisplayName(driver), err)
	}
	return db, nil
}

func configureDatabasePool(
	sqlDB *sql.DB,
	driver config.DatabaseDriver,
	pool config.DatabasePoolConfig,
) {
	maxOpenConnections, maxIdleConnections := databasePoolLimits(driver, pool)
	sqlDB.SetMaxOpenConns(maxOpenConnections)
	sqlDB.SetMaxIdleConns(maxIdleConnections)
}

func databasePoolLimits(
	driver config.DatabaseDriver,
	pool config.DatabasePoolConfig,
) (int, int) {
	if driver == config.DatabaseDriverSQLite {
		// SQLite's single-writer runtime and shared :memory: compatibility both
		// require one physical connection.
		return 1, 1
	}
	return pool.MaxOpenConnections, pool.MaxIdleConnections
}

func newDatabaseDialector(database config.DatabaseConfig) (gorm.Dialector, error) {
	switch database.Driver {
	case config.DatabaseDriverSQLite:
		return sqlite.Open(database.DSN), nil
	case config.DatabaseDriverMySQL:
		dsn, err := mysqlDSNFromURL(database.DSN)
		if err != nil {
			return nil, err
		}
		return gormmysql.Open(dsn), nil
	case config.DatabaseDriverPostgreSQL:
		// Schema migrations rename/rebuild tables while the process is running.
		// pgx's implicit statement cache otherwise can retain a result shape from
		// the legacy table and fail the first query against the rebuilt table.
		return gormpostgres.New(gormpostgres.Config{
			DSN:                  database.DSN,
			PreferSimpleProtocol: true,
		}), nil
	default:
		return nil, fmt.Errorf("unsupported database driver")
	}
}

func mysqlDSNFromURL(rawDSN string) (string, error) {
	parsed, err := url.Parse(rawDSN)
	if err != nil || !strings.EqualFold(parsed.Scheme, "mysql") || parsed.Host == "" {
		return "", fmt.Errorf("DATABASE_DSN has an invalid MySQL URL")
	}
	databaseName := strings.TrimPrefix(parsed.EscapedPath(), "/")
	if databaseName == "" || strings.Contains(strings.TrimPrefix(parsed.Path, "/"), "/") {
		return "", fmt.Errorf("DATABASE_DSN MySQL URL must include one database name")
	}
	if parsed.Fragment != "" {
		return "", fmt.Errorf("DATABASE_DSN MySQL URL must not include a fragment")
	}
	query, err := url.ParseQuery(parsed.RawQuery)
	if err != nil {
		return "", fmt.Errorf("DATABASE_DSN has an invalid MySQL query")
	}
	// Keep the application-visible behavior stable across MySQL installations:
	// parseTime is required for time-valued driver fields, clientFoundRows is
	// required by existing RowsAffected contracts, and utf8mb4/binary lets
	// connection literals represent exact identifiers. Schema migration 0001
	// enforces the corresponding binary identity on model_prices.model_id.
	query.Set("parseTime", "true")
	query.Set("clientFoundRows", "true")
	if query.Get("charset") == "" {
		query.Set("charset", "utf8mb4")
	}
	if query.Get("collation") == "" {
		query.Set("collation", "utf8mb4_bin")
	}

	credentials := ""
	if parsed.User != nil {
		credentials = parsed.User.Username()
		if password, ok := parsed.User.Password(); ok {
			credentials += ":" + password
		}
		credentials += "@"
	}
	return credentials + "tcp(" + parsed.Host + ")/" + databaseName + "?" + query.Encode(), nil
}

func logExternalDatabaseSource(driver config.DatabaseDriver) {
	logrus.WithFields(logrus.Fields{
		"database_source": config.DatabaseSourceExternal,
		"database_driver": driver,
	}).Info("Database storage is managed by the operator")
}

func databaseDisplayName(driver config.DatabaseDriver) string {
	switch driver {
	case config.DatabaseDriverSQLite:
		return "SQLite"
	case config.DatabaseDriverMySQL:
		return "MySQL"
	case config.DatabaseDriverPostgreSQL:
		return "PostgreSQL"
	default:
		return string(driver)
	}
}
