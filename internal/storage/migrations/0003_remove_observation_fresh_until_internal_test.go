package migrations

import "testing"

func TestMySQLRequiresCheckDropSyntax0003(t *testing.T) {
	for _, test := range []struct {
		serverVersion string
		want          bool
	}{
		{serverVersion: "8.0.15", want: false},
		{serverVersion: "8.0.16", want: true},
		{serverVersion: "8.0.18-commercial", want: true},
		{serverVersion: "8.0.19", want: false},
		{serverVersion: "8.4.0", want: false},
		{serverVersion: "not-a-version", want: false},
	} {
		t.Run(test.serverVersion, func(t *testing.T) {
			if got := mysqlRequiresCheckDropSyntax0003(test.serverVersion); got != test.want {
				t.Fatalf("mysqlRequiresCheckDropSyntax0003(%q) = %t, want %t", test.serverVersion, got, test.want)
			}
		})
	}
}
