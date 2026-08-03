package contentcoding

import "testing"

func TestIdentityAcceptable(t *testing.T) {
	tests := []struct {
		name   string
		values []string
		want   bool
	}{
		{name: "missing", want: true},
		{name: "empty", values: []string{""}, want: true},
		{name: "unrelated coding defaults to identity", values: []string{"gzip"}, want: true},
		{name: "explicit identity", values: []string{"identity"}, want: true},
		{name: "explicit identity excluded", values: []string{"identity;q=0"}, want: false},
		{name: "wildcard excludes identity", values: []string{"*;q=0"}, want: false},
		{name: "explicit identity overrides wildcard", values: []string{"identity;q=0, *;q=1"}, want: false},
		{name: "duplicate identity uses highest valid quality", values: []string{"identity;q=0.5, identity;q=0"}, want: true},
		{name: "wildcard allows identity", values: []string{"gzip;q=1, *;q=0.5"}, want: true},
		{name: "invalid identity quality does not exclude default", values: []string{"identity;q=1.001"}, want: true},
		{name: "zero with three decimal places", values: []string{"identity;q=0.000"}, want: false},
		{name: "multiple fields", values: []string{"gzip", "*;q=0"}, want: false},
		{name: "case insensitive token and parameter", values: []string{"IDENTITY;Q=0"}, want: false},
		{name: "one with zero decimals is valid", values: []string{"identity;q=1.000"}, want: true},
		{name: "too many decimals is invalid", values: []string{"identity;q=0.0000, *;q=0"}, want: false},
		{name: "one with nonzero decimal is invalid", values: []string{"identity;q=1.010, *;q=0"}, want: false},
		{name: "unknown parameter invalidates item", values: []string{"identity;level=1, *;q=0"}, want: false},
		{name: "duplicate quality parameter invalidates item", values: []string{"identity;q=1;q=0, *;q=0"}, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IdentityAcceptable(tt.values); got != tt.want {
				t.Fatalf("IdentityAcceptable(%q) = %t, want %t", tt.values, got, tt.want)
			}
		})
	}
}
