package storage

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"strings"

	"github.com/glebarez/sqlite"
	"github.com/sirupsen/logrus"
	gormmysql "gorm.io/driver/mysql"
	gormpostgres "gorm.io/driver/postgres"
	"gorm.io/gorm"

	"gpt-load/internal/platform/config"
)

const (
	databaseMaxOpenConnections = 10
	databaseMaxIdleConnections = 5
)

// openDatabase is the shared GORM/SQL lifecycle for every supported driver.
// Driver-specific behavior is limited to dialector construction and the
// SQLite runtime hook in db.go.
func openDatabase(driver config.DatabaseDriver, dialector gorm.Dialector) (*gorm.DB, error) {
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
	configureDatabasePool(sqlDB, driver)
	if err := sqlDB.PingContext(context.Background()); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("ping %s database: %w", databaseDisplayName(driver), err)
	}
	return db, nil
}

func configureDatabasePool(sqlDB *sql.DB, driver config.DatabaseDriver) {
	maxOpenConnections := databaseMaxOpenConnections
	maxIdleConnections := databaseMaxIdleConnections
	if driver == config.DatabaseDriverSQLite {
		// SQLite's single-writer runtime and shared :memory: compatibility both
		// require one physical connection.
		maxOpenConnections = 1
		maxIdleConnections = 1
	}
	sqlDB.SetMaxOpenConns(maxOpenConnections)
	sqlDB.SetMaxIdleConns(maxIdleConnections)
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
		return gormpostgres.Open(database.DSN), nil
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
	// connection literals represent exact identifiers. Schema migration 0004
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
