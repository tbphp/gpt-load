package encryption

import (
	"strings"
	"testing"
)

func newTestRedactionCipher(t *testing.T, master string, accessKeyID uint) RedactionCipher {
	t.Helper()
	service, err := NewService(master)
	if err != nil {
		t.Fatal(err)
	}
	cipher, err := service.NewRedactionCipher(accessKeyID)
	if err != nil {
		t.Fatal(err)
	}
	return cipher
}

func TestRedactionCipherStableAndIsolated(t *testing.T) {
	first := newTestRedactionCipher(t, "stable-master-key", 42)
	restarted := newTestRedactionCipher(t, "stable-master-key", 42)
	otherKey := newTestRedactionCipher(t, "stable-master-key", 43)
	otherMaster := newTestRedactionCipher(t, "different-master-key", 42)

	token, err := first.EncryptToken("alice@example.com")
	if err != nil {
		t.Fatal(err)
	}
	for _, cipher := range []RedactionCipher{first, restarted} {
		again, err := cipher.EncryptToken("alice@example.com")
		if err != nil || again != token {
			t.Fatalf("same master and AccessKey generated a different token: err=%v", err)
		}
		if end, valid := cipher.ValidTokenAt("before "+token+" after", len("before ")); !valid || end != len("before ")+len(token) {
			t.Fatalf("ValidTokenAt() = (%d, %t), want (%d, true)", end, valid, len("before ")+len(token))
		}
		if got, err := cipher.RestoreText("before " + token + " after"); err != nil || got != "before alice@example.com after" {
			t.Fatalf("RestoreText() = %q, %v", got, err)
		}
	}
	if otherToken, err := first.EncryptToken("bob@example.com"); err != nil || otherToken == token {
		t.Fatalf("different plaintext returned the same token: err=%v", err)
	}
	for _, cipher := range []RedactionCipher{otherKey, otherMaster} {
		otherToken, err := cipher.EncryptToken("alice@example.com")
		if err != nil || otherToken == token {
			t.Fatalf("AccessKey or master key isolation failed: err=%v", err)
		}
		if _, valid := cipher.ValidTokenAt(token, 0); valid {
			t.Fatal("token authenticated under another AccessKey or master key")
		}
		if _, err := cipher.RestoreText(token); err == nil {
			t.Fatal("RestoreText() accepted a token from another AccessKey or master key")
		}
	}
}

func TestRedactionCipherRestoresOnceAndPreservesBytes(t *testing.T) {
	cipher := newTestRedactionCipher(t, "utf8-master-key", 9)
	inner, err := cipher.EncryptToken("密码😀\n\"\\")
	if err != nil {
		t.Fatal(err)
	}
	outer, err := cipher.EncryptToken(inner)
	if err != nil {
		t.Fatal(err)
	}
	got, err := cipher.RestoreText("普通文本:" + outer + "|" + inner + "|")
	if err != nil {
		t.Fatal(err)
	}
	if want := "普通文本:" + inner + "|密码😀\n\"\\|"; got != want {
		t.Fatalf("RestoreText() = %q, want %q", got, want)
	}
	if got, err := cipher.RestoreText("gld1_not_a_token"); err != nil || got != "gld1_not_a_token" {
		t.Fatalf("ordinary similar prefix changed: %q, %v", got, err)
	}
	if got, err := cipher.RestoreText("gld1_999999999999999999999999"); err != nil || got != "gld1_999999999999999999999999" {
		t.Fatalf("unfinished ordinary prefix changed: %q, %v", got, err)
	}
}

func TestRedactionCipherRejectsDamagedCandidates(t *testing.T) {
	cipher := newTestRedactionCipher(t, "tamper-master-key", 8)
	token, err := cipher.EncryptToken("alice@example.com")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.SplitN(strings.TrimPrefix(token, "gld1_"), "_", 2)
	if len(parts) != 2 {
		t.Fatalf("invalid token format: %q", token)
	}
	changed := []byte(token)
	changed[len(changed)-1] = 'A'
	if changed[len(changed)-1] == token[len(token)-1] {
		changed[len(changed)-1] = 'B'
	}
	cases := []string{
		string(changed),                                       // authentication failure
		"gld1_0_" + parts[1],                                  // noncanonical/invalid length
		"gld1_0" + parts[0] + "_" + parts[1],                  // noncanonical decimal length
		"gld1_" + parts[0] + "_" + parts[1][:len(parts[1])-1], // truncated body
		"gld1_" + parts[0] + "_" + "!" + parts[1][1:],         // invalid alphabet
		"gld1_" + parts[0] + "_" + "\n" + parts[1][1:],        // decoder otherwise ignores newlines
		"gld1_" + parts[0] + "_" + "=" + parts[1][1:],         // v1 has no padding
		"gld1_999999999999999999999999_abc",                   // overlong numeric header
	}
	for _, damaged := range cases {
		if _, valid := cipher.ValidTokenAt(damaged, 0); valid {
			t.Fatalf("damaged token accepted: %q", damaged)
		}
		if _, err := cipher.RestoreText("prefix " + damaged + " suffix"); err == nil {
			t.Fatalf("damaged token passed through: %q", damaged)
		}
	}
}

func TestNewRedactionCipherRejectsZeroAccessKeyID(t *testing.T) {
	service, err := NewService("master-key")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.NewRedactionCipher(0); err == nil {
		t.Fatal("zero AccessKey ID accepted")
	}
}

func TestRedactionCipherRejectsOversizedSingleValue(t *testing.T) {
	cipher := newTestRedactionCipher(t, "size-limit-master-key", 7)
	if _, err := cipher.EncryptToken(strings.Repeat("x", 16<<20+1)); err == nil {
		t.Fatal("single redaction value larger than the output-safe limit was accepted")
	}
}

func TestRedactionCipherV1WireVector(t *testing.T) {
	// Freeze the HKDF context, AccessKey ID byte order, authenticated header,
	// AES-SIV output and unpadded Base64url encoding as one wire contract.
	cipher := newTestRedactionCipher(t, "wire-vector-master-key", 0x01020304)
	got, err := cipher.EncryptToken("alice@example.com")
	if err != nil {
		t.Fatal(err)
	}
	const want = "gld1_44_E2Caq6Dvt3Kn4iGKkBDO3jR6k36EuX1DDAJ-_XmS24sc"
	if got != want {
		t.Fatalf("v1 wire token = %q, want %q", got, want)
	}
}
