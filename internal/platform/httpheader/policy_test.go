package httpheader

import "testing"

func TestCredentialAndForbiddenHeaderPolicy(t *testing.T) {
	t.Run("credential names are case insensitive", func(t *testing.T) {
		tests := []struct {
			name string
			want bool
		}{
			{name: "Authorization", want: true},
			{name: "proxy-authorization", want: true},
			{name: "API-KEY", want: true},
			{name: "x-api-key", want: true},
			{name: "X-Goog-Api-Key", want: true},
			{name: "X-Custom"},
			{name: ""},
			{name: " "},
		}
		for _, test := range tests {
			if got := IsCredentialName(test.name); got != test.want {
				t.Errorf("IsCredentialName(%q) = %t, want %t", test.name, got, test.want)
			}
		}
	})

	t.Run("forbidden request set names are case insensitive", func(t *testing.T) {
		tests := []struct {
			name string
			want bool
		}{
			{name: "Connection", want: true},
			{name: "proxy-connection", want: true},
			{name: "KEEP-ALIVE", want: true},
			{name: "te", want: true},
			{name: "Trailer", want: true},
			{name: "transfer-encoding", want: true},
			{name: "Upgrade", want: true},
			{name: "cookie", want: true},
			{name: "Cookie2", want: true},
			{name: "Proxy-Authorization", want: true},
			{name: "pRoXy-Custom", want: true},
			{name: "Accept-Encoding", want: true},
			{name: "Content-Encoding", want: true},
			{name: "Content-Length", want: true},
			{name: "Authorization"},
			{name: "Proxy"},
			{name: ""},
			{name: " "},
		}
		for _, test := range tests {
			if got := IsForbiddenRequestRuleSetName(test.name); got != test.want {
				t.Errorf(
					"IsForbiddenRequestRuleSetName(%q) = %t, want %t",
					got,
					test.name,
					test.want,
				)
			}
		}
	})
}
