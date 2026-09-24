package requestredact

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"gpt-load/internal/platform/encryption"
)

func syntheticRedactionCipher(t *testing.T) encryption.RedactionCipher {
	t.Helper()
	service, err := encryption.NewService("synthetic-requestredact-test-master-key-2026-09-23")
	if err != nil {
		t.Fatal(err)
	}
	cipher, err := service.NewRedactionCipher(42)
	if err != nil {
		t.Fatal(err)
	}
	return cipher
}

func TestEncryptTextIsStableDistinctAndSkipsAuthenticatedTokens(t *testing.T) {
	cipher := syntheticRedactionCipher(t)
	compiled, err := Compile([]Rule{{Pattern: `[a-z]+@example\.invalid`, Replacement: "SHOULD_NOT_USE", Mode: ModeEncrypt}})
	if err != nil {
		t.Fatal(err)
	}
	first, err := compiled.TextWithCipher("alice@example.invalid bob@example.invalid alice@example.invalid", cipher)
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Fields(first)
	if len(parts) != 3 || parts[0] != parts[2] || parts[0] == parts[1] || strings.Contains(first, "SHOULD_NOT_USE") {
		t.Fatalf("deterministic encrypted text = %q", first)
	}
	if got, err := cipher.RestoreText(first); err != nil || got != "alice@example.invalid bob@example.invalid alice@example.invalid" {
		t.Fatalf("restored text = %q, error = %v", got, err)
	}
	for _, input := range []string{first, parts[0] + ",alice@example.invalid"} {
		got, err := compiled.TextWithCipher(input, cipher)
		want := first
		if input != first {
			want = parts[0] + "," + parts[0]
		}
		if err != nil || got != want {
			t.Fatalf("idempotent text = %q, error = %v, want %q", got, err, want)
		}
	}
	lookalike, err := Compile([]Rule{{Pattern: `gld1_999_bad`, Mode: ModeEncrypt}})
	if err != nil {
		t.Fatal(err)
	}
	got, err := lookalike.TextWithCipher("gld1_999_bad", cipher)
	if err != nil || got == "gld1_999_bad" {
		t.Fatalf("invalid lookalike text = %q, error = %v", got, err)
	}
	if restored, err := cipher.RestoreText(got); err != nil || restored != "gld1_999_bad" {
		t.Fatalf("invalid lookalike restored = %q, error = %v", restored, err)
	}
}

func TestAuthenticatedTokenIsSkippedButCrossingMatchFailsClosed(t *testing.T) {
	cipher := syntheticRedactionCipher(t)
	token, err := cipher.EncryptToken("private")
	if err != nil {
		t.Fatal(err)
	}
	inside, err := Compile([]Rule{{Pattern: `gld1_`, Replacement: "[MASK]"}})
	if err != nil {
		t.Fatal(err)
	}
	if got, err := inside.TextWithCipher(token, cipher); err != nil || got != token {
		t.Fatalf("protected token = %q, error = %v", got, err)
	}
	crossing, err := Compile([]Rule{{Pattern: `password:\S+`, Mode: ModeEncrypt}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := crossing.TextWithCipher("password:"+token+"suffix", cipher); !errors.Is(err, ErrContent) {
		t.Fatalf("cross-token match error = %v, want ErrContent", err)
	}
}

func TestEncryptTextPreservesRulePriorityAndFailsClosed(t *testing.T) {
	cipher := syntheticRedactionCipher(t)
	for _, test := range []struct {
		name     string
		rules    []Rule
		wantText string
	}{
		{"replace wins overlap", []Rule{{Pattern: "abc", Replacement: "[MASK]"}, {Pattern: "bcde", Mode: ModeEncrypt}}, "[MASK]"},
		{"encrypt wins overlap", []Rule{{Pattern: "abc", Mode: ModeEncrypt}, {Pattern: "bcde", Replacement: "[MASK]"}}, "abcde"},
	} {
		t.Run(test.name, func(t *testing.T) {
			compiled, err := Compile(test.rules)
			if err != nil {
				t.Fatal(err)
			}
			got, err := compiled.TextWithCipher("abcde", cipher)
			if err != nil {
				t.Fatal(err)
			}
			restored, err := cipher.RestoreText(got)
			if err != nil || restored != test.wantText {
				t.Fatalf("overlap output = %q, restored = %q, error = %v", got, restored, err)
			}
		})
	}
	compiled, err := Compile([]Rule{{Pattern: "private", Mode: ModeEncrypt}, {Pattern: "public", Replacement: "[MASK]"}})
	if err != nil {
		t.Fatal(err)
	}
	got, err := compiled.TextWithCipher("private public", cipher)
	if err != nil || !strings.HasSuffix(got, " [MASK]") || strings.Contains(got, "private") {
		t.Fatalf("mixed rules output = %q, error = %v", got, err)
	}
	if _, err := compiled.TextWithCipher("private", nil); !errors.Is(err, ErrContent) {
		t.Fatalf("missing cipher error = %v, want ErrContent", err)
	}
	if got, err := compiled.TextWithCipher("unmatched", nil); err != nil || got != "unmatched" {
		t.Fatalf("unmatched missing-cipher text = %q, error = %v", got, err)
	}
	broken := failedEncryptCipher{RedactionCipher: cipher}
	if _, err := compiled.TextWithCipher("private", broken); !errors.Is(err, ErrContent) {
		t.Fatalf("encryption failure = %v, want ErrContent", err)
	}
}

type failedEncryptCipher struct{ encryption.RedactionCipher }

func (failedEncryptCipher) EncryptToken(string) (string, error) {
	return "", errors.New("synthetic encryption failure")
}

type countingEncryptCipher struct {
	encryption.RedactionCipher
	calls int
}

func (c *countingEncryptCipher) EncryptToken(value string) (string, error) {
	c.calls++
	return c.RedactionCipher.EncryptToken(value)
}

func TestEncryptTextRejectsOversizedOutputBeforeEncryption(t *testing.T) {
	cipher := &countingEncryptCipher{RedactionCipher: syntheticRedactionCipher(t)}
	compiled, err := Compile([]Rule{
		{Pattern: "x", Replacement: strings.Repeat("y", 4096)},
		{Pattern: "private", Mode: ModeEncrypt},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := compiled.TextWithCipher(strings.Repeat("x", 32769)+"private", cipher); !errors.Is(err, ErrContent) {
		t.Fatalf("oversized output error = %v, want ErrContent", err)
	}
	if cipher.calls != 0 {
		t.Fatalf("encrypted %d values before rejecting oversized output", cipher.calls)
	}
}

func TestApplyWithCipherPreservesJSONStructureAndRejectsSignedMutation(t *testing.T) {
	cipher := syntheticRedactionCipher(t)
	compiled, err := Compile([]Rule{{Pattern: `alice@example\.invalid`, Mode: ModeEncrypt}})
	if err != nil {
		t.Fatal(err)
	}
	body := []byte(`{ "model":"public", "messages":[{"role":"assistant","tool_calls":[{"id":"call_1","function":{"name":"lookup","arguments":"{\"password\":\"alice@example.invalid\",\"n\":9007199254740993}"}}]},{"role":"user","content":"alice@example.invalid"}], "temperature":1.00 }`)
	got, err := compiled.ApplyWithCipher(body, cipher)
	if err != nil || !json.Valid(got) {
		t.Fatalf("ApplyWithCipher() = %s, error = %v", got, err)
	}
	token, err := cipher.EncryptToken("alice@example.invalid")
	if err != nil {
		t.Fatal(err)
	}
	want := []byte(strings.ReplaceAll(string(body), "alice@example.invalid", token))
	if !bytes.Equal(got, want) || !bytes.Contains(body, []byte("alice@example.invalid")) {
		t.Fatalf("JSON bytes or input changed:\n got: %s\nwant: %s", got, want)
	}
	if _, err := compiled.ApplyWithCipher([]byte(`{"messages":[{"role":"assistant","content":[{"type":"thinking","thinking":"alice@example.invalid","signature":"signed"}]}]}`), cipher); !errors.Is(err, ErrContent) {
		t.Fatalf("signed content error = %v, want ErrContent", err)
	}
}

func TestApplyDecisionsWithCipherPreservesQuestionKeys(t *testing.T) {
	cipher := syntheticRedactionCipher(t)
	compiled, err := Compile([]Rule{{Pattern: `alice@example\.invalid`, Mode: ModeEncrypt}})
	if err != nil {
		t.Fatal(err)
	}
	body := []byte(`{"state":{"email":"alice@example.invalid"},"questions":{"alice@example.invalid":{"criteria":{"note":"alice@example.invalid"}}}}`)
	got, err := compiled.ApplyDecisionsWithCipher(body, cipher)
	if err != nil {
		t.Fatal(err)
	}
	token, err := cipher.EncryptToken("alice@example.invalid")
	if err != nil {
		t.Fatal(err)
	}
	want := []byte(`{"state":{"email":"` + token + `"},"questions":{"alice@example.invalid":{"criteria":{"note":"` + token + `"}}}}`)
	if !bytes.Equal(got, want) {
		t.Fatalf("Decisions output = %s, want %s", got, want)
	}
}
