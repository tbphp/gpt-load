package gateway

import (
	"testing"

	"gpt-load/internal/outboundproxy"
	"gpt-load/internal/state"
	"gpt-load/internal/testutil/encryptiontest"
)

func TestResolveAttemptProxyUsesCredentialOverrideAndVerifiesFingerprint(t *testing.T) {
	t.Parallel()

	crypto := encryptiontest.Service(t, "gateway-proxy-test-key-material-2026")
	config := outboundproxy.Config{Mode: outboundproxy.ModeCustom, URL: "socks5://user:password@proxy.example.com:1080"}
	encoded, err := outboundproxy.Encode(config)
	if err != nil {
		t.Fatal(err)
	}
	ciphertext, err := crypto.Encrypt(encoded)
	if err != nil {
		t.Fatal(err)
	}
	ref := state.CredentialRef{EncryptedProxy: ciphertext, ProxyFingerprint: crypto.Hash(encoded)}
	group := outboundproxy.Effective{Config: outboundproxy.Config{Mode: outboundproxy.ModeDirect}, Source: outboundproxy.SourceGroup}

	effective, fingerprint, err := resolveAttemptProxy(crypto, group, ref)
	if err != nil {
		t.Fatalf("resolveAttemptProxy() error = %v", err)
	}
	if effective.Source != outboundproxy.SourceCredential || effective.Config.URL != config.URL {
		t.Fatalf("effective proxy = %#v", effective)
	}
	if fingerprint != ref.ProxyFingerprint {
		t.Fatalf("fingerprint = %q, want %q", fingerprint, ref.ProxyFingerprint)
	}

	ref.ProxyFingerprint = "mismatch"
	if _, _, err := resolveAttemptProxy(crypto, group, ref); err == nil {
		t.Fatal("resolveAttemptProxy accepted mismatched fingerprint")
	}
}
