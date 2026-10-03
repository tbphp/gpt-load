package migrations

import "testing"

func TestPolicyRevisionStorageRequiresSigned64BitInteger(t *testing.T) {
	for _, tc := range []struct {
		dialect  string
		typeName string
		rawType  string
		valid    bool
	}{
		{"mysql", "bigint", "bigint", true},
		{"mysql", "bigint", "bigint(20)", true},
		{"mysql", "bigint", "bigint unsigned", false},
		{"mysql", "int", "int", false},
		{"postgres", "int8", "int8", true},
		{"postgres", "bigint", "bigint", true},
		{"postgres", "int4", "int4", false},
		{"postgres", "numeric", "numeric(20,2)", false},
		{"sqlite", "INTEGER", "INTEGER", true},
		{"sqlite", "BIGINT", "BIGINT", true},
		{"sqlite", "REAL", "REAL", false},
	} {
		if err := validateRevisionStorage0029(tc.dialect, tc.typeName, tc.rawType); (err == nil) != tc.valid {
			t.Errorf("%s %s: error=%v valid=%v", tc.dialect, tc.rawType, err, tc.valid)
		}
	}
}
