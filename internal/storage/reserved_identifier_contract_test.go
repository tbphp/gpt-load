package storage_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"gorm.io/gorm"

	"gpt-load/internal/storage"
)

// These lists mirror the reserved-keyword catalogs exposed by the MySQL 8.4
// and PostgreSQL 18 images in the CI database matrix. Update them with the
// matrix versions so new schema identifiers cannot silently become reserved.
const mysqlReservedWords = `accessible add all alter analyze and as asc asensitive before between bigint binary blob both by call cascade case change char character check collate column condition constraint continue convert create cross cube cume_dist current_date current_time current_timestamp current_user cursor database databases day_hour day_microsecond day_minute day_second dec decimal declare default delayed delete dense_rank desc describe deterministic distinct distinctrow div double drop dual each else elseif empty enclosed escaped except exists exit explain false fetch first_value float float4 float8 for force foreign from fulltext function generated get grant group grouping groups having high_priority hour_microsecond hour_minute hour_second if ignore in index infile inner inout insensitive insert int int1 int2 int3 int4 int8 integer intersect interval into io_after_gtids io_before_gtids is iterate join json_table key keys kill lag last_value lateral lead leading leave left like limit linear lines load localtime localtimestamp lock long longblob longtext loop low_priority match maxvalue mediumblob mediumint mediumtext middleint minute_microsecond minute_second mod modifies natural no_write_to_binlog not nth_value ntile null numeric of on optimize optimizer_costs option optionally or order out outer outfile over partition percent_rank precision primary procedure purge qualify range rank read read_write reads real recursive references regexp release rename repeat replace require resignal restrict return revoke right rlike row row_number rows schema schemas second_microsecond select sensitive separator set show signal smallint spatial specific sql sql_big_result sql_calc_found_rows sql_small_result sqlexception sqlstate sqlwarning ssl starting stored straight_join system table tablesample terminated then tinyblob tinyint tinytext to trailing trigger true undo union unique unlock unsigned update usage use using utc_date utc_time utc_timestamp values varbinary varchar varcharacter varying virtual when where while window with write xor year_month zerofill`

const postgresReservedWords = `all analyse analyze and any array as asc asymmetric both case cast check collate column constraint create current_catalog current_date current_role current_time current_timestamp current_user default deferrable desc distinct do else end except false fetch for foreign from grant group having in initially intersect into lateral leading limit localtime localtimestamp not null offset on only or order placing primary references returning select session_user some symmetric system_user table then to trailing true union unique user using variadic when where window with`

var rawSQLArgumentIndexes = map[string]int{
	"Delete":     1,
	"Distinct":   0,
	"Exec":       0,
	"Find":       1,
	"First":      1,
	"Group":      0,
	"Having":     0,
	"InnerJoins": 0,
	"Joins":      0,
	"Not":        0,
	"Or":         0,
	"Order":      0,
	"Raw":        0,
	"Select":     0,
	"Table":      0,
	"Take":       1,
	"Where":      0,
}

func TestProductionRawSQLDoesNotUseReservedSchemaIdentifiers(t *testing.T) {
	db, err := storage.Open(":memory:")
	if err != nil {
		t.Fatalf("open SQLite schema database: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get SQLite connection pool: %v", err)
	}
	t.Cleanup(func() {
		if err := sqlDB.Close(); err != nil {
			t.Errorf("close SQLite schema database: %v", err)
		}
	})
	if err := storage.AutoMigrate(db); err != nil {
		t.Fatalf("migrate SQLite schema database: %v", err)
	}

	reservedIdentifiers := reservedSchemaIdentifiers(t, db)
	if got := strings.Join(sortedKeys(reservedIdentifiers), ","); got != "groups,key" {
		t.Fatalf("reserved schema identifiers = %q, want %q", got, "groups,key")
	}

	repositoryRoot := filepath.Clean(filepath.Join("..", ".."))
	var violations []string
	err = filepath.WalkDir(filepath.Join(repositoryRoot, "internal"), func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || filepath.Ext(path) != ".go" || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		fileSet := token.NewFileSet()
		file, err := parser.ParseFile(fileSet, path, nil, 0)
		if err != nil {
			return err
		}
		ast.Inspect(file, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			selector, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			argumentIndex, ok := rawSQLArgumentIndexes[selector.Sel.Name]
			if !ok || argumentIndex >= len(call.Args) {
				return true
			}
			literal, ok := call.Args[argumentIndex].(*ast.BasicLit)
			if !ok || literal.Kind != token.STRING {
				return true
			}
			query, err := strconv.Unquote(literal.Value)
			if err != nil {
				return true
			}
			if gormQuotesPlainIdentifier(selector.Sel.Name) && isPlainSQLIdentifier(query) {
				return true
			}
			for identifier := range reservedIdentifiers {
				if containsUnquotedSQLIdentifier(query, identifier) {
					position := fileSet.Position(literal.Pos())
					violations = append(violations, position.String()+": raw "+selector.Sel.Name+" references reserved identifier "+strconv.Quote(identifier))
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatalf("scan production Go source: %v", err)
	}
	if len(violations) > 0 {
		sort.Strings(violations)
		t.Fatalf("raw SQL must use GORM clauses for reserved schema identifiers:\n%s", strings.Join(violations, "\n"))
	}
}

func gormQuotesPlainIdentifier(method string) bool {
	return method == "Distinct" || method == "Select" || method == "Table"
}

func isPlainSQLIdentifier(value string) bool {
	if value == "" || value[0] >= '0' && value[0] <= '9' {
		return false
	}
	for index := range len(value) {
		if !isSQLIdentifierByte(value[index]) {
			return false
		}
	}
	return true
}

func reservedSchemaIdentifiers(t *testing.T, db *gorm.DB) map[string]struct{} {
	t.Helper()
	type nameRow struct {
		Name string
	}
	var tables []nameRow
	if err := db.Raw(`
		SELECT name
		FROM sqlite_master
		WHERE type = 'table' AND name NOT LIKE 'sqlite_%'
	`).Scan(&tables).Error; err != nil {
		t.Fatalf("list migrated SQLite tables: %v", err)
	}

	schemaIdentifiers := make(map[string]struct{})
	for _, table := range tables {
		schemaIdentifiers[strings.ToLower(table.Name)] = struct{}{}
		var columns []nameRow
		if err := db.Raw("SELECT name FROM pragma_table_info(?)", table.Name).Scan(&columns).Error; err != nil {
			t.Fatalf("list columns for %q: %v", table.Name, err)
		}
		for _, column := range columns {
			schemaIdentifiers[strings.ToLower(column.Name)] = struct{}{}
		}
	}

	reservedWords := make(map[string]struct{})
	for _, word := range strings.Fields(mysqlReservedWords + " " + postgresReservedWords) {
		reservedWords[word] = struct{}{}
	}
	result := make(map[string]struct{})
	for identifier := range schemaIdentifiers {
		if _, reserved := reservedWords[identifier]; reserved {
			result[identifier] = struct{}{}
		}
	}
	return result
}

func sortedKeys(values map[string]struct{}) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func containsUnquotedSQLIdentifier(query, identifier string) bool {
	for index := 0; index < len(query); {
		switch query[index] {
		case '\'', '"', '`':
			quote := query[index]
			index++
			for index < len(query) {
				if query[index] != quote {
					index++
					continue
				}
				index++
				if index < len(query) && query[index] == quote {
					index++
					continue
				}
				break
			}
		case '[':
			if end := strings.IndexByte(query[index+1:], ']'); end >= 0 {
				index += end + 2
			} else {
				index++
			}
		default:
			if isSQLIdentifierByte(query[index]) {
				start := index
				for index < len(query) && isSQLIdentifierByte(query[index]) {
					index++
				}
				if strings.EqualFold(query[start:index], identifier) {
					return true
				}
				continue
			}
			index++
		}
	}
	return false
}

func isSQLIdentifierByte(value byte) bool {
	return value == '_' || value >= 0x80 || value >= 'a' && value <= 'z' ||
		value >= 'A' && value <= 'Z' || value >= '0' && value <= '9'
}
