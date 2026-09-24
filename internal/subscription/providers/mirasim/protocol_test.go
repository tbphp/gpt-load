package mirasim

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"strings"
	"testing"
)

func TestSignatureV2MatchesMirasimCryptoCoreVector(t *testing.T) {
	// All expected values were produced by the non-secret cc_canonical and
	// cc_sign test inputs in Mirasim 0.0.260's embedded crypto core.
	seed := make([]byte, ed25519.SeedSize)
	for index := range seed {
		seed[index] = byte(index)
	}
	input := signingInput{
		Method:        "POST",
		Path:          "/v1/messages",
		Timestamp:     "1788200000123",
		Nonce:         "AAECAwQFBgcICQoL",
		DeviceID:      "device-fixed",
		ClientVersion: "0.0.260",
		Credential:    "ticket-fixed",
		Metadata: map[string]string{
			headerMirasimSession: "mirasim_00000000-0000-4000-8000-000000000000",
			headerMirasimAgent:   "claude",
			headerMirasimCall:    "11111111-2222-4333-8444-555555555555",
		},
		Body: []byte(`{"model":"claude-sonnet-5","messages":[]}`),
	}
	wantCanonical := "mrs-sig-v2\n" +
		"POST\n" +
		"/v1/messages\n" +
		"1788200000123\n" +
		"AAECAwQFBgcICQoL\n" +
		"device-fixed\n" +
		"0.0.260\n" +
		"66ee005427e4f3b74ce4830f104c989613f0968f97036191a6fbaea245040170\n" +
		"91bcb885e5b045a9f55f270bb0c6d633930407b6cca792839034702ed233be6b\n" +
		"9df27ddbfc24ebaafa990cd41a7744f56c875d0dadf69e4941edc7e728aea6bd"
	canonical, errCanonical := canonicalSignaturePayload(input)
	if errCanonical != nil {
		t.Fatalf("canonicalSignaturePayload() error = %v", errCanonical)
	}
	if string(canonical) != wantCanonical {
		t.Fatalf("canonical payload mismatch\n got: %q\nwant: %q", canonical, wantCanonical)
	}
	signature := ed25519.Sign(ed25519.NewKeyFromSeed(seed), canonical)
	if got, want := base64.RawURLEncoding.EncodeToString(signature), "zUYTEKW17Gzn7TEdEzWZ2aEOpO4oW9YFFpdsyzJaUyS4A_byq3DUNYzNOL96D24MExQ0mVbot75TkvkJw3vVAQ"; got != want {
		t.Fatalf("signature = %q, want %q", got, want)
	}
}

func TestSealV1MatchesMirasimCryptoCoreVector(t *testing.T) {
	// This deterministic ciphertext was produced by cc_seal from the same
	// Mirasim 0.0.260 crypto core using only the fixed test keys below.
	recipientPublic, errPublic := base64.StdEncoding.DecodeString("NYBy1jZYgNGu6jKa35EhODhR7SGijjt16WXQ0s0WYlQ=")
	if errPublic != nil {
		t.Fatal(errPublic)
	}
	ephemeralSecret, errSecret := hex.DecodeString("404142434445464748494a4b4c4d4e4f505152535455565758595a5b5c5d5e5f")
	if errSecret != nil {
		t.Fatal(errSecret)
	}
	nonce, errNonce := hex.DecodeString("a0a1a2a3a4a5a6a7a8a9aaab")
	if errNonce != nil {
		t.Fatal(errNonce)
	}
	plaintext := []byte(`{"x-mirasim-agent":"claude","x-mirasim-call":"11111111-2222-4333-8444-555555555555","x-mirasim-device":"device-fixed","x-mirasim-nonce":"AAECAwQFBgcICQoL","x-mirasim-session":"mirasim_00000000-0000-4000-8000-000000000000","x-mirasim-sig":"zUYTEKW17Gzn7TEdEzWZ2aEOpO4oW9YFFpdsyzJaUyS4A_byq3DUNYzNOL96D24MExQ0mVbot75TkvkJw3vVAQ","x-mirasim-ts":"1788200000123"}`)
	aad := []byte("mrs-seal-v1\nPOST\n/v1/messages")
	sealed, errSeal := sealPayload(recipientPublic, ephemeralSecret, nonce, plaintext, aad)
	if errSeal != nil {
		t.Fatalf("sealPayload() error = %v", errSeal)
	}
	want := "eaYx7t4b-cmPEgMs3q3Q56B5OY_HhriMyEbsia-FpRqgoaKjpKWmp6ipqqtWlxgybxeoS1fVDaS5_1az3V-kX_XGGNPghY8g8q81tF8LkfDoIwY8W2FWXoe5_27zjH9q2jM05ZuvNfmdYnjW0x616SP-p3g96-PvzI8GDuAbPgt9-0sIkQHeCCZ35opOpopxt_tdTp55bPp8CmjCpb1OR0aWs_5UezjAlVNibbN4979hGY_BcQ7z07Bkt92DCgJiP9aP8pLSXM1gcFHvnDDAiAqfqqA1cWx2f3EIHn585U-tdtQsRZ5BJ7wJ4sZgMswGl5CxDgSFJ-MhnsQsyj6zAR_MVujCO4jUkLVsRtI38N6sN-T79EWL4w4N1ksEfzIUJDtYDNfbr83XkXpl3sB6DvYIrrrPi5Gq96WSWldJf5Pgz0IdJq_O36wS2dNYVqQmsOU-nwgigBh0NrD94K23PthUn8qbkULkp7PgyPGDXN-4MkvdjN8LVN-oEP1oaP5FMVbiH6b_7K_9NaXvJCELj59p2P_fCnJ2ULHCpwyfDGOKjg"
	if got := base64.RawURLEncoding.EncodeToString(sealed); got != want {
		t.Fatalf("sealed payload = %q, want %q", got, want)
	}
}

func TestSignatureV2UsesBlankMetadataLineWhenMetadataIsEmpty(t *testing.T) {
	payload, errCanonical := canonicalSignaturePayload(signingInput{
		Method:        "GET",
		Path:          "/v1/models",
		Timestamp:     "1",
		Nonce:         "nonce",
		DeviceID:      "device",
		ClientVersion: "0.0.260",
		Credential:    "ticket",
	})
	if errCanonical != nil {
		t.Fatal(errCanonical)
	}
	lines := strings.Split(string(payload), "\n")
	if len(lines) != 10 || lines[8] != "" {
		t.Fatalf("canonical lines = %#v", lines)
	}
}

func TestRelayAgentRecognizesCodexRoutes(t *testing.T) {
	for _, requestPath := range []string{"/v1/responses", "/v1/alpha/search"} {
		if got := relayAgent(requestPath); got != "codex" {
			t.Fatalf("relayAgent(%q) = %q, want codex", requestPath, got)
		}
	}
	if got := relayAgent("/v1/messages"); got != "claude" {
		t.Fatalf("relayAgent(/v1/messages) = %q, want claude", got)
	}
}

func TestRelaySealPublicKeyDefaultsAndFailsClosed(t *testing.T) {
	t.Setenv("MIRASIM_SEAL_PUBKEY", "")
	publicKey, errDefault := relaySealPublicKey()
	if errDefault != nil || len(publicKey) != 32 {
		t.Fatalf("default relay key length = %d, error = %v", len(publicKey), errDefault)
	}
	t.Setenv("MIRASIM_SEAL_PUBKEY", "not-base64")
	if _, errInvalid := relaySealPublicKey(); errInvalid == nil {
		t.Fatal("relaySealPublicKey() accepted an invalid override")
	}
}
