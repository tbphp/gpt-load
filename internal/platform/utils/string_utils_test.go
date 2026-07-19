package utils

import "testing"

func TestMaskAPIKeyNeverReturnsShortSecret(t *testing.T) {
	tests := []struct {
		name   string
		secret string
		want   string
	}{
		{name: "empty", secret: "", want: ""},
		{name: "one byte", secret: "x", want: "****"},
		{name: "eight bytes", secret: "12345678", want: "****"},
		{name: "nine bytes", secret: "123456789", want: "1234****6789"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := MaskAPIKey(tt.secret); got != tt.want {
				t.Fatalf("MaskAPIKey(%q) = %q, want %q", tt.secret, got, tt.want)
			}
		})
	}
}
