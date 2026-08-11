package control

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

type settingsETagFixture struct {
	DomainHex            string `json:"domain_hex"`
	CanonicalResponseHex string `json:"canonical_response_hex"`
	FramedInputHex       string `json:"framed_input_hex"`
	ExpectedDigest       string `json:"expected_digest"`
}

func TestSettingsWireETagMatchesCheckedInFixture(t *testing.T) {
	raw, err := os.ReadFile("testdata/settings-etag-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture settingsETagFixture
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}

	representation, err := newSettingsWireRepresentation("Success", SettingsDTO{
		Values: SettingsValuesResponse{
			FirstByteTimeout:  120,
			RequestTimeout:    600,
			StreamIdleTimeout: 300,
			HeaderRules: HeaderRulesResponse{
				Set:    map[string]string{},
				Remove: []string{},
			},
			InjectUsageOptions:      true,
			ValidationInterval:      600,
			RequestLogRetentionDays: 7,
		},
		Overrides: []string{},
	})
	if err != nil {
		t.Fatal(err)
	}

	domain, err := hex.DecodeString(fixture.DomainHex)
	if err != nil {
		t.Fatal(err)
	}
	wantBody, err := hex.DecodeString(fixture.CanonicalResponseHex)
	if err != nil {
		t.Fatal(err)
	}
	wantFramed, err := hex.DecodeString(fixture.FramedInputHex)
	if err != nil {
		t.Fatal(err)
	}
	framed := append(append(append([]byte{}, domain...), 0), representation.Body...)
	if !bytes.Equal(representation.Body, wantBody) {
		t.Fatalf("canonical body = %s, want %s", representation.Body, wantBody)
	}
	if !bytes.Equal(framed, wantFramed) {
		t.Fatalf("framed input = %x, want %x", framed, wantFramed)
	}
	if len(wantFramed) <= len(domain) || wantFramed[len(domain)] != 0 ||
		bytes.Contains(wantFramed[len(domain)+1:], []byte{0}) {
		t.Fatalf("fixture separator is not exactly one NUL: %x", wantFramed)
	}
	digest := sha256.Sum256(framed)
	if got := hex.EncodeToString(digest[:]); got != fixture.ExpectedDigest {
		t.Fatalf("fixture digest = %s, want %s", got, fixture.ExpectedDigest)
	}
	if representation.ETag != "sha256-"+fixture.ExpectedDigest {
		t.Fatalf("ETag = %q", representation.ETag)
	}
	if representation.HeaderETag != `"`+representation.ETag+`"` {
		t.Fatalf("header ETag = %q", representation.HeaderETag)
	}
	if bytes.Contains(representation.Body, []byte(`"revision"`)) ||
		bytes.Contains(representation.Body, []byte(`"settings_etag"`)) {
		t.Fatalf("wire body exposes process identity: %s", representation.Body)
	}
}

func TestSettingsDTOCanonicalizesHeaderRulesAndOverrides(t *testing.T) {
	dto := canonicalizeSettingsDTO(SettingsDTO{
		Values: SettingsValuesResponse{
			HeaderRules: HeaderRulesResponse{
				Set: map[string]string{
					"x-zed":  "last",
					"A-test": "first",
				},
				Remove: []string{"x-old", "X-OLD", "a-remove"},
			},
		},
		Overrides: []string{"request_timeout", "first_byte_timeout", "request_timeout"},
	})
	if got := strings.Join(dto.Values.HeaderRules.Remove, ","); got != "A-Remove,X-Old" {
		t.Fatalf("remove = %q", got)
	}
	if _, exists := dto.Values.HeaderRules.Set["x-zed"]; exists ||
		dto.Values.HeaderRules.Set["X-Zed"] != "last" ||
		dto.Values.HeaderRules.Set["A-Test"] != "first" {
		t.Fatalf("set = %#v", dto.Values.HeaderRules.Set)
	}
	if got := strings.Join(dto.Overrides, ","); got != "first_byte_timeout,request_timeout" {
		t.Fatalf("overrides = %q", got)
	}
}
