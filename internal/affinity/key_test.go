package affinity

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"gpt-load/internal/protocol"
)

type testHasher struct{}

func (testHasher) Hash(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}

func TestDeriveKeyScopesStablePrefix(t *testing.T) {
	t.Parallel()

	base := DeriveKey(testHasher{}, 7, protocol.OpenAICompletions, []byte("stable-prefix"))
	if !base.Valid() {
		t.Fatal("DeriveKey() returned an empty key")
	}
	tests := []struct {
		name      string
		accessKey uint
		protocol  protocol.Protocol
		prefix    string
	}{
		{name: "access key", accessKey: 8, protocol: protocol.OpenAICompletions, prefix: "stable-prefix"},
		{name: "protocol", accessKey: 7, protocol: protocol.Anthropic, prefix: "stable-prefix"},
		{name: "prefix", accessKey: 7, protocol: protocol.OpenAICompletions, prefix: "other-prefix"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := DeriveKey(testHasher{}, test.accessKey, test.protocol, []byte(test.prefix))
			if !got.Valid() || got == base {
				t.Fatalf("DeriveKey() = %q, want non-empty key different from base", got)
			}
		})
	}
	if duplicate := DeriveKey(testHasher{}, 7, protocol.OpenAICompletions, []byte("stable-prefix")); duplicate != base {
		t.Fatalf("duplicate DeriveKey() = %q, want %q", duplicate, base)
	}
}

func TestDeriveKeyRejectsIncompleteScope(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		hasher    Hasher
		accessKey uint
		protocol  protocol.Protocol
		prefix    []byte
	}{
		{name: "nil hasher", accessKey: 1, protocol: protocol.OpenAICompletions, prefix: []byte("prefix")},
		{name: "zero access key", hasher: testHasher{}, protocol: protocol.OpenAICompletions, prefix: []byte("prefix")},
		{name: "invalid protocol", hasher: testHasher{}, accessKey: 1, protocol: protocol.Protocol("invalid"), prefix: []byte("prefix")},
		{name: "empty prefix", hasher: testHasher{}, accessKey: 1, protocol: protocol.OpenAICompletions},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := DeriveKey(test.hasher, test.accessKey, test.protocol, test.prefix); got.Valid() {
				t.Fatalf("DeriveKey() = %q, want empty key", got)
			}
		})
	}
}

func TestDeriveKeyUsesUnambiguousFieldBoundaries(t *testing.T) {
	t.Parallel()

	left := DeriveKey(testHasher{}, 1, protocol.OpenAICompletions, []byte("ab"))
	right := DeriveKey(testHasher{}, 1, protocol.OpenAICompletions, []byte("a"))
	if !left.Valid() || !right.Valid() || left == right {
		t.Fatalf("keys = %q / %q, want distinct non-empty values", left, right)
	}
}
