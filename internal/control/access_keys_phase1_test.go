package control

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"gpt-load/internal/platform/encryption"
	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/protocol"
	"gpt-load/internal/state"
	"gpt-load/internal/storage/models"
)

type decryptCountingEncryption struct {
	encryption.Service
	decryptCalls int
}

func (spy *decryptCountingEncryption) Decrypt(ciphertext string) (string, error) {
	spy.decryptCalls++
	return spy.Service.Decrypt(ciphertext)
}

func TestAccessKeyMetadataListAndUpdateNeverDecryptCiphertext(t *testing.T) {
	t.Parallel()
	fixture := newServiceFixture(t)
	fixture.service.random = bytes.NewReader(make([]byte, 16))
	created, err := fixture.service.CreateAccessKey(
		t.Context(),
		AccessKeyCreateRequest{Name: "metadata-only"},
	)
	if err != nil {
		t.Fatalf("CreateAccessKey() error = %v", err)
	}
	spy := &decryptCountingEncryption{Service: fixture.encryption}
	fixture.service.encryption = spy
	if err := fixture.db.Model(&models.AccessKey{}).
		Where("id = ?", created.ID).
		UpdateColumn("key_value", "intentionally-corrupt").Error; err != nil {
		t.Fatalf("corrupt ciphertext: %v", err)
	}

	collection, err := fixture.service.ListAccessKeyCollection(
		t.Context(),
		AccessKeyCollectionQuery{Page: 1, PageSize: 20},
	)
	if err != nil {
		t.Fatalf("ListAccessKeyCollection() error = %v", err)
	}
	if len(collection.Items) != 1 || collection.Items[0].ID != created.ID ||
		collection.Items[0].MaskedKey != "sk-gl-****0000" {
		t.Fatalf("ListAccessKeyCollection() = %#v", collection)
	}
	encoded, err := json.Marshal(collection)
	if err != nil {
		t.Fatalf("marshal metadata: %v", err)
	}
	if strings.Contains(string(encoded), `"key"`) ||
		strings.Contains(string(encoded), "intentionally-corrupt") {
		t.Fatalf("metadata response exposes secret field: %s", encoded)
	}

	updated, err := fixture.service.UpdateAccessKey(
		t.Context(),
		created.ID,
		AccessKeyUpdateRequest{Name: stringPointer("renamed")},
	)
	if err != nil {
		t.Fatalf("UpdateAccessKey() error = %v", err)
	}
	if updated.Name != "renamed" || updated.MaskedKey != collection.Items[0].MaskedKey {
		t.Fatalf("UpdateAccessKey() = %#v", updated)
	}
	if spy.decryptCalls != 0 {
		t.Fatalf("metadata list/update decrypt calls = %d, want 0", spy.decryptCalls)
	}
}

func TestCreateAccessKeyIdempotentReplaysMetadataWithoutPersistingSecret(t *testing.T) {
	t.Parallel()
	fixture := newServiceFixture(t)
	fixture.service.random = bytes.NewReader(make([]byte, 16))
	fixture.service.operationRandom = bytes.NewReader(bytes.Repeat([]byte{0x61}, 16))
	request := AccessKeyCreateRequest{Name: "idempotent-client"}
	const key = "118f47a2-9c35-4d6e-8b1a-1234567890ab"

	first, err := fixture.service.CreateAccessKeyIdempotent(t.Context(), key, request)
	if err != nil {
		t.Fatalf("first CreateAccessKeyIdempotent() error = %v", err)
	}
	if first.Key == "" || first.Replayed {
		t.Fatalf("first result = %#v", first)
	}
	replayed, err := fixture.service.CreateAccessKeyIdempotent(t.Context(), key, request)
	if err != nil {
		t.Fatalf("replay CreateAccessKeyIdempotent() error = %v", err)
	}
	if replayed.Key != "" || !replayed.Replayed ||
		!reflect.DeepEqual(replayed.AccessKeyMetadata, first.AccessKeyMetadata) {
		t.Fatalf("replay result = %#v, first = %#v", replayed, first)
	}
	var operation models.ControlOperation
	if err := fixture.db.First(&operation).Error; err != nil {
		t.Fatalf("read operation: %v", err)
	}
	if bytes.Contains(operation.CanonicalResult, []byte(first.Key)) {
		t.Fatal("operation ledger contains one-time AccessKey secret")
	}

	_, err = fixture.service.CreateAccessKeyIdempotent(
		t.Context(),
		key,
		AccessKeyCreateRequest{Name: "different-client"},
	)
	var apiErr *app_errors.APIError
	if !errors.As(err, &apiErr) ||
		apiErr.Code != app_errors.ErrIdempotencyKeyReused.Code {
		t.Fatalf("different request error = %v", err)
	}
	var count int64
	if err := fixture.db.Model(&models.AccessKey{}).Count(&count).Error; err != nil {
		t.Fatalf("count AccessKeys: %v", err)
	}
	if count != 1 {
		t.Fatalf("AccessKey count = %d, want 1", count)
	}
}

func TestAccessKeyMetadataFailsClosedForInvalidPersistedSuffix(t *testing.T) {
	t.Parallel()
	fixture := newServiceFixture(t)
	created, err := fixture.service.CreateAccessKey(
		t.Context(),
		AccessKeyCreateRequest{Name: "invalid-suffix"},
	)
	if err != nil {
		t.Fatalf("CreateAccessKey() error = %v", err)
	}
	if err := fixture.db.Exec("PRAGMA ignore_check_constraints = ON").Error; err != nil {
		t.Fatalf("disable check constraints: %v", err)
	}
	if err := fixture.db.Model(&models.AccessKey{}).
		Where("id = ?", created.ID).
		UpdateColumn("key_suffix", "ZZZZ").Error; err != nil {
		t.Fatalf("set invalid suffix: %v", err)
	}
	if err := fixture.db.Exec("PRAGMA ignore_check_constraints = OFF").Error; err != nil {
		t.Fatalf("restore check constraints: %v", err)
	}

	if collection, err := fixture.service.ListAccessKeyCollection(
		t.Context(),
		AccessKeyCollectionQuery{Page: 1, PageSize: 20},
	); err == nil || collection.Items != nil {
		t.Fatalf("ListAccessKeyCollection() = %#v, %v, want fail closed", collection, err)
	}
	if _, err := fixture.service.UpdateAccessKey(
		t.Context(),
		created.ID,
		AccessKeyUpdateRequest{Name: stringPointer("must-not-change")},
	); err == nil {
		t.Fatal("UpdateAccessKey() error = nil for invalid suffix")
	}
}

func TestRevealAccessKeyIsTheExplicitMetadataDecryptPath(t *testing.T) {
	t.Parallel()
	fixture := newServiceFixture(t)
	created, err := fixture.service.CreateAccessKey(
		t.Context(),
		AccessKeyCreateRequest{Name: "reveal"},
	)
	if err != nil {
		t.Fatalf("CreateAccessKey() error = %v", err)
	}
	spy := &decryptCountingEncryption{Service: fixture.encryption}
	fixture.service.encryption = spy

	revealed, err := fixture.service.RevealAccessKey(t.Context(), created.ID)
	if err != nil {
		t.Fatalf("RevealAccessKey() error = %v", err)
	}
	if revealed.ID != created.ID || revealed.Key != created.Key ||
		revealed.RevealedAtMS <= 0 {
		t.Fatalf("RevealAccessKey() = %#v", revealed)
	}
	if spy.decryptCalls != 1 {
		t.Fatalf("RevealAccessKey() decrypt calls = %d, want 1", spy.decryptCalls)
	}
}

func TestListAccessKeyOptionsContainsOnlySelectorMetadata(t *testing.T) {
	t.Parallel()
	fixture := newServiceFixture(t)
	first, err := fixture.service.CreateAccessKey(
		t.Context(),
		AccessKeyCreateRequest{Name: "first-option"},
	)
	if err != nil {
		t.Fatalf("create first option: %v", err)
	}
	second, err := fixture.service.CreateAccessKey(
		t.Context(),
		AccessKeyCreateRequest{Name: "second-option"},
	)
	if err != nil {
		t.Fatalf("create second option: %v", err)
	}
	if _, err := fixture.service.UpdateAccessKey(
		t.Context(),
		second.ID,
		AccessKeyUpdateRequest{Status: accessKeyStatusPointer(state.AccessKeyStatusDisabled)},
	); err != nil {
		t.Fatalf("disable second option: %v", err)
	}

	options, err := fixture.service.ListAccessKeyOptions(t.Context())
	if err != nil {
		t.Fatalf("ListAccessKeyOptions() error = %v", err)
	}
	if len(options) != 2 ||
		options[0] != (AccessKeyOption{
			ID: first.ID, Name: "first-option", Status: state.AccessKeyStatusActive,
		}) ||
		options[1] != (AccessKeyOption{
			ID: second.ID, Name: "second-option", Status: state.AccessKeyStatusDisabled,
		}) {
		t.Fatalf("options = %#v", options)
	}
}

func TestAccessKeyFilterUpdateCanPreserveOrRemoveButNotAddDanglingScope(t *testing.T) {
	t.Parallel()
	fixture := newServiceFixture(t)
	row := models.AccessKey{
		Name:      "historical-scope",
		KeyValue:  "ciphertext",
		KeyHash:   "historical-scope-hash",
		KeySuffix: "cafe",
		Status:    string(state.AccessKeyStatusActive),
		Filters:   models.JSON(`{"groups":[999],"protocols":["openai-responses"],"models":[]}`),
	}
	if err := fixture.db.Create(&row).Error; err != nil {
		t.Fatalf("create historical scope: %v", err)
	}

	preserved := AccessKeyFilters{
		Groups: []uint{999}, Protocols: []protocol.Protocol{protocol.OpenAIResponses},
	}
	if _, err := fixture.service.UpdateAccessKey(
		t.Context(),
		row.ID,
		AccessKeyUpdateRequest{Filters: &preserved},
	); err != nil {
		t.Fatalf("preserve historical scope: %v", err)
	}
	removed := AccessKeyFilters{}
	if _, err := fixture.service.UpdateAccessKey(
		t.Context(),
		row.ID,
		AccessKeyUpdateRequest{Filters: &removed},
	); err != nil {
		t.Fatalf("remove historical scope: %v", err)
	}
	if _, err := fixture.service.UpdateAccessKey(
		t.Context(),
		row.ID,
		AccessKeyUpdateRequest{Filters: &preserved},
	); err == nil {
		t.Fatal("re-add removed dangling/reserved scope error = nil")
	}
}

func accessKeyStatusPointer(value state.AccessKeyStatus) *state.AccessKeyStatus {
	return &value
}
