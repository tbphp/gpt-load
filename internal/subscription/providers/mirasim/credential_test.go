package mirasim

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"strings"
	"testing"
	"time"
)

func TestParseCredentialRequiresDeviceKeyAndFillsDefaults(t *testing.T) {
	key := testDevicePEM(t)
	raw := []byte(`{"type":"mirasim","access_token":"access-secret","refresh_token":"refresh-secret","device_private_key":` + jsonString(key) + `}`)
	credential, err := ParseCredentialJSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	if credential.RelayURL != defaultRelayURL || credential.AdminURL != defaultAdminURL || credential.ClientVersion != defaultClientVersion {
		t.Fatalf("defaults = %#v", credential)
	}
	if credential.Identity() == "" || len(credential.SecretValues()) != 3 {
		t.Fatalf("identity/secrets = %q %#v", credential.Identity(), credential.SecretValues())
	}
	if _, err := ParseCredentialJSON([]byte(`{"type":"mirasim","access_token":"a","refresh_token":"b"}`)); err == nil {
		t.Fatal("missing device key was accepted")
	}
}

func TestInstallOAuthKeepsGeneratedDeviceKey(t *testing.T) {
	key := testDevicePEM(t)
	credential, err := InstallOAuth(Storage{DevicePrivateKey: key, AdminURL: defaultAdminURL}, "access-secret", "refresh-secret", time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if credential.DevicePrivateKey != strings.TrimSpace(key) || credential.AccessToken != "access-secret" || credential.Type != credentialType {
		t.Fatalf("installed = %#v", credential)
	}
}

func testDevicePEM(t *testing.T) string {
	t.Helper()
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
}

func jsonString(value string) string {
	return `"` + strings.ReplaceAll(value, "\n", `\n`) + `"`
}
