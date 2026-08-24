package control

import (
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestIdempotencyDigestFixtures(t *testing.T) {
	t.Parallel()
	type fixture struct {
		Name            string        `json:"name"`
		OperationKind   operationKind `json:"operation_kind"`
		PathTemplate    string        `json:"path_template"`
		ResourceLocator string        `json:"resource_locator"`
		CanonicalBody   string        `json:"canonical_body"`
		DomainHex       string        `json:"domain_hex"`
		SeparatorHex    string        `json:"separator_hex"`
		FramedInputHex  string        `json:"framed_input_hex"`
		ExpectedSHA256  string        `json:"expected_sha256"`
	}
	paths, err := filepath.Glob("testdata/idempotency/*.json")
	if err != nil {
		t.Fatalf("glob fixtures: %v", err)
	}
	if len(paths) != 3 {
		t.Fatalf("fixture count = %d, want 3", len(paths))
	}
	digests := make(map[string]string, len(paths))
	for _, path := range paths {
		path := path
		t.Run(filepath.Base(path), func(t *testing.T) {
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read fixture: %v", err)
			}
			var value fixture
			if err := json.Unmarshal(raw, &value); err != nil {
				t.Fatalf("decode fixture: %v", err)
			}
			result, err := buildIdempotencyDigest(idempotencyDigestInput{
				Version:         1,
				Method:          "POST",
				OperationKind:   value.OperationKind,
				PathTemplate:    value.PathTemplate,
				ResourceLocator: value.ResourceLocator,
				AuthScopeID:     idempotencyAuthScopeID,
				CanonicalBody:   []byte(value.CanonicalBody),
			})
			if err != nil {
				t.Fatalf("buildIdempotencyDigest() error = %v", err)
			}
			if got := hex.EncodeToString([]byte(idempotencyDigestDomain)); got != value.DomainHex {
				t.Fatalf("domain hex = %s, want %s", got, value.DomainHex)
			}
			if value.SeparatorHex != "00" ||
				result.FramedInput[len(idempotencyDigestDomain)] != 0 {
				t.Fatalf("separator fixture/runtime = %q/%02x", value.SeparatorHex, result.FramedInput[len(idempotencyDigestDomain)])
			}
			if got := hex.EncodeToString(result.FramedInput); got != value.FramedInputHex {
				t.Fatalf("framed input hex = %s, want %s", got, value.FramedInputHex)
			}
			gotDigest := hex.EncodeToString(result.Digest[:])
			if gotDigest != value.ExpectedSHA256 {
				t.Fatalf("digest = %s, want %s", gotDigest, value.ExpectedSHA256)
			}
			digests[value.Name] = gotDigest
		})
	}
	for _, pair := range [][2]string{
		{"group-create-k", "group-create-k-twice"},
	} {
		if digests[pair[0]] == digests[pair[1]] {
			t.Fatalf("%s and %s digests are equal", pair[0], pair[1])
		}
	}
}

func TestIdempotencyDigestV1MatchesAccessKeyCreateFixture(t *testing.T) {
	t.Parallel()
	body := []byte(
		`{"filters":{"groups":[],"models":[],"protocols":[]},"name":"CI client","rpm_limit":0}`,
	)
	result, err := buildIdempotencyDigest(idempotencyDigestInput{
		Version:         1,
		Method:          "POST",
		OperationKind:   operationKindAccessKeyCreate,
		PathTemplate:    "/api/access-keys",
		ResourceLocator: "new",
		AuthScopeID:     "control-admin-v1",
		CanonicalBody:   body,
	})
	if err != nil {
		t.Fatalf("buildIdempotencyDigest() error = %v", err)
	}

	const wantDomainHex = "6770742d6c6f61642f636f6e74726f6c2d6964656d706f74656e6379"
	const wantInputHex = "6770742d6c6f61642f636f6e74726f6c2d6964656d706f74656e637900000000013100000004504f5354000000116163636573735f6b65795f637265617465000000102f6170692f6163636573732d6b657973000000036e657700000010636f6e74726f6c2d61646d696e2d7631000000557b2266696c74657273223a7b2267726f757073223a5b5d2c226d6f64656c73223a5b5d2c2270726f746f636f6c73223a5b5d7d2c226e616d65223a22434920636c69656e74222c2272706d5f6c696d6974223a307d"
	const wantDigestHex = "7b22877fc5ce68bde59170f4906463bbbf873abff4cda536dd36c30da3a62b81"

	if got := hex.EncodeToString([]byte(idempotencyDigestDomain)); got != wantDomainHex {
		t.Fatalf("domain hex = %s, want %s", got, wantDomainHex)
	}
	if got := hex.EncodeToString(result.FramedInput); got != wantInputHex {
		t.Fatalf("framed input hex = %s, want %s", got, wantInputHex)
	}
	if got := hex.EncodeToString(result.Digest[:]); got != wantDigestHex {
		t.Fatalf("digest hex = %s, want %s", got, wantDigestHex)
	}
	if result.FramedInput[len(idempotencyDigestDomain)] != 0 {
		t.Fatalf(
			"separator = %02x, want exactly one NUL after domain",
			result.FramedInput[len(idempotencyDigestDomain)],
		)
	}
}

func TestNormalizeIdempotencyKeyLinesPreservesSortedMultiplicity(t *testing.T) {
	t.Parallel()
	one, err := normalizeIdempotencyKeyLines(" K \r\n")
	if err != nil {
		t.Fatalf("normalizeIdempotencyKeyLines(K) error = %v", err)
	}
	two, err := normalizeIdempotencyKeyLines("K\r\n K\n")
	if err != nil {
		t.Fatalf("normalizeIdempotencyKeyLines(K twice) error = %v", err)
	}
	if len(one) != 1 || one[0] != "K" {
		t.Fatalf("one = %#v, want [K]", one)
	}
	if len(two) != 2 || two[0] != "K" || two[1] != "K" {
		t.Fatalf("two = %#v, want [K K]", two)
	}

	oneBody, err := canonicalIdempotencyBody(map[string]any{"credentials": one})
	if err != nil {
		t.Fatalf("canonicalIdempotencyBody(one) error = %v", err)
	}
	twoBody, err := canonicalIdempotencyBody(map[string]any{"credentials": two})
	if err != nil {
		t.Fatalf("canonicalIdempotencyBody(two) error = %v", err)
	}
	oneDigest, err := buildIdempotencyDigest(idempotencyDigestInput{
		Version: 1, Method: "POST", OperationKind: operationKindCredentialImport,
		PathTemplate: "/api/groups/:group_id/credentials/import", ResourceLocator: "group:7",
		AuthScopeID: "control-admin-v1", CanonicalBody: oneBody,
	})
	if err != nil {
		t.Fatalf("buildIdempotencyDigest(one) error = %v", err)
	}
	twoDigest, err := buildIdempotencyDigest(idempotencyDigestInput{
		Version: 1, Method: "POST", OperationKind: operationKindCredentialImport,
		PathTemplate: "/api/groups/:group_id/credentials/import", ResourceLocator: "group:7",
		AuthScopeID: "control-admin-v1", CanonicalBody: twoBody,
	})
	if err != nil {
		t.Fatalf("buildIdempotencyDigest(two) error = %v", err)
	}
	if oneDigest.Digest == twoDigest.Digest {
		t.Fatal("K and K\\nK produced the same digest")
	}
}

func TestIdempotencyDigestRejectsUnsupportedVersionAndInvalidFields(t *testing.T) {
	t.Parallel()
	valid := idempotencyDigestInput{
		Version: 1, Method: "POST", OperationKind: operationKindAccessKeyCreate,
		PathTemplate: "/api/access-keys", ResourceLocator: "new",
		AuthScopeID: "control-admin-v1", CanonicalBody: []byte(`{}`),
	}

	unsupported := valid
	unsupported.Version = 2
	if _, err := buildIdempotencyDigest(unsupported); err == nil {
		t.Fatal("buildIdempotencyDigest(version 2) error = nil, want rejection")
	}

	lowerMethod := valid
	lowerMethod.Method = "post"
	if _, err := buildIdempotencyDigest(lowerMethod); err == nil {
		t.Fatal("buildIdempotencyDigest(lowercase method) error = nil, want rejection")
	}

	implicitLocator := valid
	implicitLocator.ResourceLocator = ""
	if _, err := buildIdempotencyDigest(implicitLocator); err == nil {
		t.Fatal("buildIdempotencyDigest(empty locator) error = nil, want rejection")
	}
}
