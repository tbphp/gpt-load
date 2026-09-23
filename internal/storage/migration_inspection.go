package storage

import (
	"fmt"
	"strings"

	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

// migrationInspection 在单次只读校验阶段复用元数据。迁移 Up 使用原始 DB，
// 每步 DDL 后再创建新的校验视图，避免读取过期结构。
type migrationInspection struct {
	db          *gorm.DB
	driver      string
	database    string
	schema      string
	tables      map[string]struct{}
	columns     map[string]map[string]struct{}
	indexes     map[string]map[string]struct{}
	constraints map[string]map[string]struct{}
	columnTypes map[string][]gorm.ColumnType
	err         error
}

func migrationTableExists(db *gorm.DB, table string) (bool, error) {
	var count int64
	var query string
	var args []any
	switch strings.ToLower(db.Dialector.Name()) {
	case "mysql":
		query = "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = DATABASE() AND table_name = ? AND table_type = ?"
		args = []any{table, "BASE TABLE"}
	case "postgres", "postgresql":
		query = "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = CURRENT_SCHEMA() AND table_name = ? AND table_type = ?"
		args = []any{table, "BASE TABLE"}
	case "sqlite":
		query = "SELECT COUNT(*) FROM sqlite_master WHERE type = ? AND name = ?"
		args = []any{"table", table}
	default:
		return false, fmt.Errorf("inspect migration table: unsupported database driver %q", db.Dialector.Name())
	}
	if err := db.Raw(query, args...).Scan(&count).Error; err != nil {
		return false, err
	}
	return count > 0, nil
}

func newMigrationInspection(db *gorm.DB) (*migrationInspection, error) {
	inspection := &migrationInspection{
		db:          db,
		driver:      strings.ToLower(db.Dialector.Name()),
		columns:     make(map[string]map[string]struct{}),
		indexes:     make(map[string]map[string]struct{}),
		constraints: make(map[string]map[string]struct{}),
		columnTypes: make(map[string][]gorm.ColumnType),
	}
	switch inspection.driver {
	case "mysql":
		if err := db.Raw("SELECT DATABASE()").Scan(&inspection.schema).Error; err != nil {
			return nil, fmt.Errorf("inspect MySQL migration database: %w", err)
		}
		inspection.database = inspection.schema
	case "postgres", "postgresql":
		if err := db.Raw("SELECT CURRENT_DATABASE()").Scan(&inspection.database).Error; err != nil {
			return nil, fmt.Errorf("inspect PostgreSQL migration database: %w", err)
		}
		if err := db.Raw("SELECT CURRENT_SCHEMA()").Scan(&inspection.schema).Error; err != nil {
			return nil, fmt.Errorf("inspect PostgreSQL migration schema: %w", err)
		}
	case "sqlite":
		return inspection, nil
	default:
		return nil, fmt.Errorf("inspect migration schema: unsupported database driver %q", db.Dialector.Name())
	}
	if inspection.schema == "" {
		return nil, fmt.Errorf("inspect migration schema: current schema is empty")
	}
	return inspection, nil
}

func (inspection *migrationInspection) validationDB() *gorm.DB {
	if inspection.driver == "sqlite" {
		return inspection.db
	}
	db := inspection.db.Session(&gorm.Session{NewDB: true})
	db.Dialector = &migrationInspectionDialector{
		Dialector:  inspection.db.Dialector,
		inspection: inspection,
	}
	return db
}

func (inspection *migrationInspection) validate(validator func(*gorm.DB) error) error {
	validateErr := validator(inspection.validationDB())
	if inspection.err != nil {
		return inspection.err
	}
	return validateErr
}

func validateMigrationAfterDDL(db *gorm.DB, validator func(*gorm.DB) error) error {
	inspection, err := newMigrationInspection(db)
	if err != nil {
		return err
	}
	return inspection.validate(validator)
}

func (inspection *migrationInspection) record(err error) bool {
	if err != nil && inspection.err == nil {
		inspection.err = err
	}
	return false
}

func (inspection *migrationInspection) tableNames() (map[string]struct{}, error) {
	if inspection.tables != nil {
		return inspection.tables, nil
	}
	var names []string
	var err error
	switch inspection.driver {
	case "mysql", "postgres", "postgresql":
		err = inspection.db.Raw(
			"SELECT table_name FROM information_schema.tables WHERE table_schema = ? AND table_type = ?",
			inspection.schema, "BASE TABLE",
		).Scan(&names).Error
	default:
		return nil, fmt.Errorf("inspect tables: unsupported driver %q", inspection.driver)
	}
	if err != nil {
		return nil, fmt.Errorf("inspect migration tables: %w", err)
	}
	inspection.tables = nameSet(names)
	return inspection.tables, nil
}

func (inspection *migrationInspection) objectNames(kind, table string) (map[string]struct{}, error) {
	var cache map[string]map[string]struct{}
	var query string
	switch kind {
	case "column":
		cache = inspection.columns
		query = "SELECT column_name FROM information_schema.columns WHERE table_schema = ? AND table_name = ?"
	case "index":
		cache = inspection.indexes
		if inspection.driver == "mysql" {
			query = "SELECT DISTINCT index_name FROM information_schema.statistics WHERE table_schema = ? AND table_name = ?"
		} else {
			query = "SELECT indexname FROM pg_indexes WHERE schemaname = ? AND tablename = ?"
		}
	case "constraint":
		cache = inspection.constraints
		query = "SELECT constraint_name FROM information_schema.table_constraints WHERE constraint_schema = ? AND table_name = ?"
	default:
		return nil, fmt.Errorf("inspect migration metadata: unsupported kind %q", kind)
	}
	if names, ok := cache[table]; ok {
		return names, nil
	}
	var names []string
	if err := inspection.db.Raw(query, inspection.schema, table).Scan(&names).Error; err != nil {
		return nil, fmt.Errorf("inspect migration %s names on %q: %w", kind, table, err)
	}
	cache[table] = nameSet(names)
	return cache[table], nil
}

func nameSet(names []string) map[string]struct{} {
	result := make(map[string]struct{}, len(names))
	for _, name := range names {
		result[name] = struct{}{}
	}
	return result
}

type migrationInspectionDialector struct {
	gorm.Dialector
	inspection *migrationInspection
}

func (dialector *migrationInspectionDialector) Translate(err error) error {
	if translator, ok := dialector.Dialector.(gorm.ErrorTranslator); ok {
		return translator.Translate(err)
	}
	return err
}

func (dialector *migrationInspectionDialector) Migrator(db *gorm.DB) gorm.Migrator {
	return &migrationInspectionMigrator{
		Migrator:   dialector.Dialector.Migrator(db),
		inspection: dialector.inspection,
	}
}

type migrationInspectionMigrator struct {
	gorm.Migrator
	inspection *migrationInspection
}

func (migrator *migrationInspectionMigrator) CurrentDatabase() string {
	return migrator.inspection.database
}

func (migrator *migrationInspectionMigrator) HasTable(value interface{}) bool {
	stmt, err := migrator.statement(value)
	if err != nil {
		return migrator.inspection.record(err)
	}
	names, err := migrator.inspection.tableNames()
	if err != nil {
		return migrator.inspection.record(err)
	}
	_, exists := names[stmt.Table]
	return exists
}

func (migrator *migrationInspectionMigrator) HasColumn(value interface{}, field string) bool {
	stmt, err := migrator.statement(value)
	if err != nil {
		return migrator.inspection.record(err)
	}
	if stmt.Schema != nil {
		if parsed := stmt.Schema.LookUpField(field); parsed != nil {
			field = parsed.DBName
		}
	}
	names, err := migrator.inspection.objectNames("column", stmt.Table)
	if err != nil {
		return migrator.inspection.record(err)
	}
	_, exists := names[field]
	return exists
}

func (migrator *migrationInspectionMigrator) HasIndex(value interface{}, name string) bool {
	stmt, err := migrator.statement(value)
	if err != nil {
		return migrator.inspection.record(err)
	}
	if stmt.Schema != nil {
		if index := stmt.Schema.LookIndex(name); index != nil {
			name = index.Name
		}
	}
	names, err := migrator.inspection.objectNames("index", stmt.Table)
	if err != nil {
		return migrator.inspection.record(err)
	}
	_, exists := names[name]
	return exists
}

func (migrator *migrationInspectionMigrator) HasConstraint(value interface{}, name string) bool {
	stmt, err := migrator.statement(value)
	if err != nil {
		return migrator.inspection.record(err)
	}
	table := stmt.Table
	if resolver, ok := migrator.Migrator.(interface {
		GuessConstraintInterfaceAndTable(*gorm.Statement, string) (schema.ConstraintInterface, string)
	}); ok {
		constraint, resolvedTable := resolver.GuessConstraintInterfaceAndTable(stmt, name)
		if resolvedTable != "" {
			table = resolvedTable
		}
		if constraint != nil {
			name = constraint.GetName()
		}
	} else {
		return migrator.Migrator.HasConstraint(value, name)
	}
	names, err := migrator.inspection.objectNames("constraint", table)
	if err != nil {
		return migrator.inspection.record(err)
	}
	_, exists := names[name]
	return exists
}

func (migrator *migrationInspectionMigrator) ColumnTypes(value interface{}) ([]gorm.ColumnType, error) {
	stmt, err := migrator.statement(value)
	if err != nil {
		migrator.inspection.record(err)
		return nil, err
	}
	if columns, ok := migrator.inspection.columnTypes[stmt.Table]; ok {
		return columns, nil
	}
	columns, err := migrator.Migrator.ColumnTypes(value)
	if err != nil {
		migrator.inspection.record(err)
		return nil, err
	}
	migrator.inspection.columnTypes[stmt.Table] = columns
	return columns, nil
}

func (migrator *migrationInspectionMigrator) statement(value interface{}) (*gorm.Statement, error) {
	runner, ok := migrator.Migrator.(interface {
		RunWithValue(interface{}, func(*gorm.Statement) error) error
	})
	if !ok {
		return nil, fmt.Errorf("inspect migration metadata: GORM migrator cannot resolve a model")
	}
	var result *gorm.Statement
	if err := runner.RunWithValue(value, func(stmt *gorm.Statement) error {
		result = stmt
		return nil
	}); err != nil {
		return nil, err
	}
	if result == nil {
		return nil, fmt.Errorf("inspect migration metadata: GORM did not resolve a model")
	}
	return result, nil
}
