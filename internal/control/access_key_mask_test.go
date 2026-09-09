package control

import (
	"bytes"
	"testing"
)

func TestAccessKeyMasksPreserveDefaultFormatAndUseLengthBands(t *testing.T) {
	t.Parallel()
	for _, test := range []struct{ name, key, mask string }{
		{"generated", "", "sk-gl-****0000"},
		{"one", "a", "********"},
		{"eight", "12345678", "********"},
		{"nine", "123456789", "****6789"},
		{"sixteen", "1234567890123456", "****3456"},
		{"seventeen", "12345678901234567", "123456****4567"},
		{"custom prefix", "client-abcdefghijklmnop", "client****mnop"},
		{"default prefix", "sk-gl-0123456789abcdef0123456789abcdef", "sk-gl-****cdef"},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newServiceFixture(t)
			fixture.service.random = bytes.NewReader(make([]byte, 16))
			created, err := fixture.service.CreateAccessKey(t.Context(), AccessKeyCreateRequest{Name: test.name, Key: test.key})
			if err != nil {
				t.Fatal(err)
			}
			if created.MaskedKey != test.mask {
				t.Fatalf("created mask = %q, want %q", created.MaskedKey, test.mask)
			}
			spy := &decryptCountingEncryption{Service: fixture.encryption}
			fixture.service.encryption = spy
			listed, err := fixture.service.ListAccessKeyCollection(t.Context(), AccessKeyCollectionQuery{Page: 1, PageSize: 20})
			if err != nil {
				t.Fatal(err)
			}
			if len(listed.Items) != 1 || listed.Items[0].MaskedKey != test.mask {
				t.Fatal("collection mask differs from creation")
			}
			name := "updated"
			updated, err := fixture.service.UpdateAccessKey(t.Context(), created.ID, AccessKeyUpdateRequest{Name: &name})
			if err != nil {
				t.Fatal(err)
			}
			if updated.MaskedKey != test.mask {
				t.Fatal("editing changed the mask")
			}
			home, err := fixture.service.ReadHomeBase(t.Context(), fixture.service.now().UnixMilli())
			if err != nil {
				t.Fatal(err)
			}
			if len(home.AccessKeys) != 1 || home.AccessKeys[0].MaskedKey != test.mask {
				t.Fatal("home mask differs from creation")
			}
			scoped, err := fixture.service.ReadAccessKeyHomeBase(t.Context(), fixture.service.now().UnixMilli(), created.ID)
			if err != nil {
				t.Fatal(err)
			}
			if scoped.CurrentAccessKey == nil || scoped.CurrentAccessKey.MaskedKey != test.mask {
				t.Fatal("access key home mask differs from creation")
			}
			if spy.decryptCalls != 0 {
				t.Fatal("metadata read or update decrypted a key")
			}
			fixture.service.random = bytes.NewReader(bytes.Repeat([]byte{1}, 16))
			rotated, err := fixture.service.RotateAccessKeyIdempotent(t.Context(), "00000000-0000-4000-8000-000000008301", created.ID)
			if err != nil {
				t.Fatal(err)
			}
			if rotated.MaskedKey != "sk-gl-****0101" {
				t.Fatal("rotation did not restore the generated key mask")
			}
			view := fixture.manager.Current().AccessKeysByID[created.ID]
			if view.KeyPrefix != "sk-gl-" || view.KeySuffix != "0101" {
				t.Fatal("runtime mask metadata differs from rotation")
			}
		})
	}
}
