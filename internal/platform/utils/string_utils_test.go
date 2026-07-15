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

func TestRedactSecretMasksShortSecret(t *testing.T) {
	if got := RedactSecret("token=secret", "secret"); got != "token=****" {
		t.Fatalf("RedactSecret() = %q, want token=****", got)
	}
}

// TestRedactSecret asserts that every occurrence of a known secret inside an
// arbitrary text is replaced with its masked form. Regression test for the
// Gemini-channel upstream key leak via unmasked transport errors (CWE-200).
func TestRedactSecret(t *testing.T) {
	secret := "sk-secret-upstream-key-1234"
	text := `Post "http://upstream/v1beta/models/gemini-pro:generateContent?key=sk-secret-upstream-key-1234": context deadline exceeded`

	got := RedactSecret(text, secret)

	if got == text {
		t.Fatalf("RedactSecret did not modify the text: %q", got)
	}
	for i := 0; i+len(secret) <= len(got); i++ {
		if got[i:i+len(secret)] == secret {
			t.Fatalf("raw secret still present in redacted text: %q", got)
		}
	}
	want := `Post "http://upstream/v1beta/models/gemini-pro:generateContent?key=sk-s****1234": context deadline exceeded`
	if got != want {
		t.Errorf("RedactSecret() = %q, want %q", got, want)
	}
}

// TestRedactSecretMultipleOccurrences asserts all occurrences are redacted, not just the first.
func TestRedactSecretMultipleOccurrences(t *testing.T) {
	secret := "sk-secret-upstream-key-1234"
	text := secret + " appears twice: " + secret

	got := RedactSecret(text, secret)

	masked := MaskAPIKey(secret)
	want := masked + " appears twice: " + masked
	if got != want {
		t.Errorf("RedactSecret() = %q, want %q", got, want)
	}
}

// TestRedactSecretEmptySecret asserts an empty secret leaves the text untouched
// (guards against strings.ReplaceAll's pathological behavior on an empty old value).
func TestRedactSecretEmptySecret(t *testing.T) {
	text := "some upstream error with no secret"

	got := RedactSecret(text, "")

	if got != text {
		t.Errorf("RedactSecret() with empty secret = %q, want unchanged %q", got, text)
	}
}

// TestRedactSecretNotPresent asserts text without the secret is returned unchanged.
func TestRedactSecretNotPresent(t *testing.T) {
	text := "connection refused"

	got := RedactSecret(text, "sk-secret-upstream-key-1234")

	if got != text {
		t.Errorf("RedactSecret() = %q, want unchanged %q", got, text)
	}
}
