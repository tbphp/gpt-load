package encryptiontest

import (
	"sync"
	"testing"

	"gpt-load/internal/platform/encryption"
)

var (
	mu       sync.Mutex
	services = map[string]encryption.Service{}
)

// Service returns an encryption.Service derived from keyMaterial, reusing a
// previously derived instance for the same keyMaterial within this test
// binary. PBKDF2 key derivation dominates fixture construction cost across
// packages; aesService is stateless after construction, so sharing an
// instance across fixtures (including parallel tests) is safe.
func Service(t *testing.T, keyMaterial string) encryption.Service {
	t.Helper()
	mu.Lock()
	defer mu.Unlock()
	if svc, ok := services[keyMaterial]; ok {
		return svc
	}
	svc, err := encryption.NewService(keyMaterial)
	if err != nil {
		t.Fatalf("encryption.NewService(%q) error = %v", keyMaterial, err)
	}
	services[keyMaterial] = svc
	return svc
}
