package requestredact

import (
	"encoding/base64"
	"errors"
	"strings"
	"testing"

	"github.com/tidwall/gjson"

	"gpt-load/internal/platform/encryption"
)

func TestSignatureRecordRoundTrip(t *testing.T) {
	wrapped := WrapSignature("SIG123", []string{"gld1_44_a", "gld1_44_b"})
	if !strings.HasPrefix(wrapped, signatureWrapperPrefix) {
		t.Fatalf("wrapped signature %q lacks the fixed prefix", wrapped)
	}
	if _, err := base64.StdEncoding.DecodeString(wrapped); err != nil {
		t.Fatalf("wrapped signature is not valid base64: %v", err)
	}
	// 客户端可能按字节解码后用 URL 安全字母表、无填充重新编码。
	raw, _ := base64.StdEncoding.DecodeString(wrapped)
	for _, value := range []string{wrapped, base64.RawURLEncoding.EncodeToString(raw)} {
		original, tokens, ok := unwrapSignature(value)
		if !ok || original != "SIG123" || strings.Join(tokens, ",") != "gld1_44_a,gld1_44_b" {
			t.Fatalf("unwrapSignature(%q) = %q, %v, %v", value, original, tokens, ok)
		}
	}
	if WrapSignature("SIG123", nil) != "SIG123" {
		t.Fatal("signature without restored values changed")
	}
	for _, value := range []string{"SIG123", "EqQBCkgIARABGAIiQL", signatureWrapperPrefix + "!!"} {
		if _, _, ok := unwrapSignature(value); ok {
			t.Fatalf("ordinary signature %q was treated as a record", value)
		}
	}
}

func signedTestCipher(t *testing.T, accessKeyID uint) encryption.RedactionCipher {
	t.Helper()
	service, err := encryption.NewService("synthetic-signed-content-master-key-2026-09-25")
	if err != nil {
		t.Fatal(err)
	}
	cipher, err := service.NewRedactionCipher(accessKeyID)
	if err != nil {
		t.Fatal(err)
	}
	return cipher
}

func TestApplyLeavesUnrecordedSignedContentUnchanged(t *testing.T) {
	cipher := signedTestCipher(t, 3)
	signed := []string{
		`{"type":"thinking","thinking":"test with bob@example.com","signature":"SIG"}`,
		`{"functionCall":{"name":"lookup_user","args":{"email":"bob@example.com"}},"thoughtSignature":"SIG"}`,
		`{"type":"reasoning.text","text":"bob@example.com","signature":"SIG"}`,
	}
	for _, mode := range []string{ModeEncrypt, ModeReplace} {
		compiled, err := Compile([]Rule{{Pattern: `[a-z]+@example\.com`, Replacement: "[EMAIL]", Mode: mode}})
		if err != nil {
			t.Fatal(err)
		}
		for _, block := range signed {
			body := `{"messages":[{"role":"user","content":"alice@example.com"},{"role":"assistant","content":[` + block + `]}]}`
			got, err := compiled.ApplyWithCipher([]byte(body), cipher)
			if err != nil {
				t.Fatalf("%s %s: %v", mode, block, err)
			}
			if out := gjson.GetBytes(got, "messages.1.content.0").Raw; out != block {
				t.Errorf("%s: signed content changed:\n got: %s\nwant: %s", mode, out, block)
			}
			if gjson.GetBytes(got, "messages.0.content").Str == "alice@example.com" {
				t.Errorf("%s: unsigned user content was not protected", mode)
			}
		}
	}
}

func TestApplyReencryptsRecordedSignedContent(t *testing.T) {
	cipher := signedTestCipher(t, 3)
	token, err := cipher.EncryptToken("alice@example.com")
	if err != nil {
		t.Fatal(err)
	}
	upstream := `{"type":"thinking","thinking":"user ` + token + ` asked; test with bob@example.com","signature":"SIG"}`
	client := `{"type":"thinking","thinking":"user alice@example.com asked; test with bob@example.com","signature":"` + WrapSignature("SIG", []string{token}) + `"}`
	for _, rules := range [][]Rule{
		{{Pattern: `[a-z]+@example\.com`, Mode: ModeEncrypt}},
		{{Pattern: `[a-z]+@example\.com`, Replacement: "[EMAIL]"}},
		nil,
	} {
		compiled, err := Compile(rules)
		if err != nil {
			t.Fatal(err)
		}
		body := `{"messages":[{"role":"assistant","content":[` + client + `]}]}`
		got, err := compiled.ApplyWithCipher([]byte(body), cipher)
		if err != nil || gjson.GetBytes(got, "messages.0.content.0").Raw != upstream {
			t.Errorf("rules=%v: signed content = %s / %v, want %s", rules, gjson.GetBytes(got, "messages.0.content.0").Raw, err, upstream)
		}
	}
}

func TestApplyRejectsUndecryptableSignatureRecord(t *testing.T) {
	other := signedTestCipher(t, 4)
	token, err := other.EncryptToken("alice@example.com")
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := Compile([]Rule{{Pattern: `[a-z]+@example\.com`, Mode: ModeEncrypt}})
	if err != nil {
		t.Fatal(err)
	}
	body := `{"messages":[{"role":"assistant","content":[{"type":"thinking","thinking":"alice@example.com","signature":"` + WrapSignature("SIG", []string{token}) + `"}]}]}`
	if _, err := compiled.ApplyWithCipher([]byte(body), signedTestCipher(t, 3)); !errors.Is(err, ErrContent) {
		t.Fatalf("undecryptable record error = %v, want ErrContent", err)
	}
}
