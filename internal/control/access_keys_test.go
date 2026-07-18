package control

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"gpt-load/internal/platform/config"
	"gpt-load/internal/protocol"
	"gpt-load/internal/state"
	stateloader "gpt-load/internal/state/loader"
	"gpt-load/internal/storage/models"
)

func TestCreateAccessKeyGeneratesEncryptedGLToken(t *testing.T) {
	fixture := newServiceFixture(t)
	fixture.service.random = bytes.NewReader([]byte{
		0x00, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07,
		0x08, 0x09, 0x0a, 0x0b, 0x0c, 0x0d, 0x0e, 0x0f,
	})
	if err := fixture.registry.ApplyImport(77, []state.KeyEntry{{
		ID: 88, GroupID: 77, Status: state.KeyStatusActive, EncryptedValue: "existing-upstream-cipher",
	}}); err != nil {
		t.Fatalf("seed Registry: %v", err)
	}
	if count, ok := fixture.registry.IncrFailure(88); !ok || count != 1 {
		t.Fatalf("seed failure count = %d, %t", count, ok)
	}
	before := fixture.manager.Current().Revision

	result, err := fixture.service.CreateAccessKey(context.Background(), AccessKeyCreateRequest{Name: " client "})
	if err != nil {
		t.Fatalf("CreateAccessKey() error = %v", err)
	}
	const plaintext = "gl-000102030405060708090a0b0c0d0e0f"
	if result.Key != plaintext || result.Name != "client" || result.Status != state.AccessKeyStatusActive {
		t.Fatalf("CreateAccessKey() = %#v", result)
	}
	if got := fixture.manager.Current().Revision; got != before+1 {
		t.Fatalf("revision = %d, want %d", got, before+1)
	}

	var row models.AccessKey
	if err := fixture.db.First(&row, result.ID).Error; err != nil {
		t.Fatalf("query AccessKey: %v", err)
	}
	if row.KeyValue == "" || row.KeyHash == "" || row.KeyValue == plaintext || strings.Contains(row.KeyValue, plaintext) {
		t.Fatalf("stored AccessKey exposes plaintext: %#v", row)
	}
	decrypted, err := fixture.encryption.Decrypt(row.KeyValue)
	if err != nil || decrypted != plaintext {
		t.Fatalf("Decrypt() = %q, %v, want plaintext", decrypted, err)
	}
	if row.KeyHash != fixture.encryption.Hash(plaintext) {
		t.Fatalf("KeyHash = %q, want stable HMAC", row.KeyHash)
	}
	if _, ok := fixture.manager.Current().AccessKeysByHash[row.KeyHash]; !ok {
		t.Fatalf("published snapshot lacks hash %q", row.KeyHash)
	}
	if got, ok := fixture.registry.EncryptedValue(88); !ok || got != "existing-upstream-cipher" {
		t.Fatalf("Registry value = %q, %t, want unchanged", got, ok)
	}
	if count, ok := fixture.registry.IncrFailure(88); !ok || count != 2 {
		t.Fatalf("Registry failure count = %d, %t, want preserved count 2", count, ok)
	}
}

func TestAccessKeyFiltersNormalizeAndAcceptExistingDisabledGroups(t *testing.T) {
	fixture := newServiceFixture(t)
	enabled := validControlGroup("filter-enabled")
	if err := fixture.db.Create(enabled).Error; err != nil {
		t.Fatalf("create enabled group: %v", err)
	}
	disabled := validControlGroup("filter-disabled")
	if err := fixture.db.Create(disabled).Error; err != nil {
		t.Fatalf("create disabled group: %v", err)
	}
	if err := fixture.db.Model(disabled).Update("enabled", false).Error; err != nil {
		t.Fatalf("disable group: %v", err)
	}
	fixture.service.random = bytes.NewReader(make([]byte, 16))

	result, err := fixture.service.CreateAccessKey(context.Background(), AccessKeyCreateRequest{
		Name: "filtered",
		Filters: &AccessKeyFilters{
			Groups:    []uint{enabled.ID, disabled.ID, enabled.ID},
			Protocols: []protocol.Protocol{protocol.OpenAI, protocol.OpenAIResponse, protocol.OpenAI},
			Models:    []string{" gpt-4o ", "gpt-4o", "claude"},
		},
	})
	if err != nil {
		t.Fatalf("CreateAccessKey() error = %v", err)
	}
	if len(result.Filters.Groups) != 2 || result.Filters.Groups[0] != enabled.ID || result.Filters.Groups[1] != disabled.ID {
		t.Fatalf("normalized groups = %#v", result.Filters.Groups)
	}
	if len(result.Filters.Protocols) != 2 || result.Filters.Protocols[1] != protocol.OpenAIResponse {
		t.Fatalf("normalized protocols = %#v", result.Filters.Protocols)
	}
	if len(result.Filters.Models) != 2 || result.Filters.Models[0] != "gpt-4o" || result.Filters.Models[1] != "claude" {
		t.Fatalf("normalized models = %#v", result.Filters.Models)
	}

	var row models.AccessKey
	if err := fixture.db.First(&row, result.ID).Error; err != nil {
		t.Fatalf("query AccessKey: %v", err)
	}
	var document map[string]json.RawMessage
	if err := json.Unmarshal(row.Filters, &document); err != nil {
		t.Fatalf("decode stored filters: %v", err)
	}
	if len(document) != 3 || document["groups"] == nil || document["protocols"] == nil || document["models"] == nil {
		t.Fatalf("stored filter document = %#v", document)
	}
	view := fixture.manager.Current().AccessKeysByHash[row.KeyHash]
	if _, ok := view.Filters.Groups[enabled.ID]; !ok {
		t.Fatalf("published filters = %#v, want enabled group", view.Filters)
	}
	if _, ok := view.Filters.Groups[disabled.ID]; !ok {
		t.Fatalf("published filters = %#v, want disabled group reference retained", view.Filters)
	}
}

func TestAccessKeyFiltersRejectInvalidCurrentInputWithoutPublishing(t *testing.T) {
	blank := "   "
	tooLong := strings.Repeat("名", 86)
	controlName := "bad\nname"
	tests := []struct {
		name    string
		request AccessKeyCreateRequest
	}{
		{name: "blank name", request: AccessKeyCreateRequest{Name: blank}},
		{name: "long name", request: AccessKeyCreateRequest{Name: tooLong}},
		{name: "control name", request: AccessKeyCreateRequest{Name: controlName}},
		{name: "zero group", request: AccessKeyCreateRequest{Name: "client", Filters: &AccessKeyFilters{Groups: []uint{0}}}},
		{name: "missing group", request: AccessKeyCreateRequest{Name: "client", Filters: &AccessKeyFilters{Groups: []uint{999}}}},
		{name: "unknown protocol", request: AccessKeyCreateRequest{Name: "client", Filters: &AccessKeyFilters{Protocols: []protocol.Protocol{"unknown"}}}},
		{name: "blank model", request: AccessKeyCreateRequest{Name: "client", Filters: &AccessKeyFilters{Models: []string{" "}}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newServiceFixture(t)
			fixture.service.random = bytes.NewReader(make([]byte, 16))
			before := fixture.manager.Current().Revision
			if _, err := fixture.service.CreateAccessKey(context.Background(), test.request); err == nil {
				t.Fatal("CreateAccessKey() error = nil")
			}
			var count int64
			if err := fixture.db.Model(&models.AccessKey{}).Count(&count).Error; err != nil {
				t.Fatalf("count AccessKeys: %v", err)
			}
			if count != 0 || fixture.manager.Current().Revision != before {
				t.Fatalf("count/revision = %d/%d, want 0/%d", count, fixture.manager.Current().Revision, before)
			}
		})
	}
}

func TestListAccessKeysReturnsPlaintextInIDOrderAndFailsClosed(t *testing.T) {
	initControlI18n(t)
	fixture := newServiceFixture(t)
	randomBytes := make([]byte, 32)
	for index := range randomBytes {
		randomBytes[index] = byte(index)
	}
	fixture.service.random = bytes.NewReader(randomBytes)
	first, err := fixture.service.CreateAccessKey(context.Background(), AccessKeyCreateRequest{Name: "first"})
	if err != nil {
		t.Fatalf("create first: %v", err)
	}
	second, err := fixture.service.CreateAccessKey(context.Background(), AccessKeyCreateRequest{Name: "second"})
	if err != nil {
		t.Fatalf("create second: %v", err)
	}

	listed, err := fixture.service.ListAccessKeys(context.Background())
	if err != nil {
		t.Fatalf("ListAccessKeys() error = %v", err)
	}
	if len(listed) != 2 || listed[0].ID != first.ID || listed[0].Key != first.Key || listed[1].ID != second.ID || listed[1].Key != second.Key {
		t.Fatalf("ListAccessKeys() = %#v", listed)
	}

	const corruptCiphertext = "known-corrupt-ciphertext"
	if err := fixture.db.Model(&models.AccessKey{}).Where("id = ?", second.ID).UpdateColumn("key_value", corruptCiphertext).Error; err != nil {
		t.Fatalf("corrupt second ciphertext: %v", err)
	}
	if partial, err := fixture.service.ListAccessKeys(context.Background()); err == nil || partial != nil {
		t.Fatalf("ListAccessKeys() = %#v, %v, want nil/error", partial, err)
	}

	engine := gin.New()
	NewServer(&config.Config{AuthKey: "test-auth-key"}, fixture.service).RegisterRoutes(engine)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/access-keys", nil)
	request.Header.Set("Authorization", "Bearer test-auth-key")
	engine.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusInternalServerError || !strings.Contains(recorder.Body.String(), "INTERNAL_SERVER_ERROR") {
		t.Fatalf("GET access keys = %d %s", recorder.Code, recorder.Body.String())
	}
	for _, forbidden := range []string{first.Key, corruptCiphertext} {
		if strings.Contains(recorder.Body.String(), forbidden) {
			t.Fatalf("fail-closed response exposes %q: %s", forbidden, recorder.Body.String())
		}
	}
}

func TestUpdateAccessKeyPreservesCredentialAcrossPointerPatches(t *testing.T) {
	fixture := newServiceFixture(t)
	group := validControlGroup("access-key-update")
	if err := fixture.db.Create(group).Error; err != nil {
		t.Fatalf("create group: %v", err)
	}
	fixture.service.random = bytes.NewReader(make([]byte, 16))
	created, err := fixture.service.CreateAccessKey(context.Background(), AccessKeyCreateRequest{
		Name: "before",
		Filters: &AccessKeyFilters{
			Groups: []uint{group.ID}, Protocols: []protocol.Protocol{protocol.OpenAI}, Models: []string{"gpt-4o"},
		},
	})
	if err != nil {
		t.Fatalf("CreateAccessKey() error = %v", err)
	}
	original := loadAccessKeyRow(t, fixture.db, created.ID)

	name := " after "
	updated, err := fixture.service.UpdateAccessKey(context.Background(), created.ID, AccessKeyUpdateRequest{Name: &name})
	if err != nil {
		t.Fatalf("name-only UpdateAccessKey() error = %v", err)
	}
	if updated.Name != "after" || updated.Status != state.AccessKeyStatusActive || updated.Key != created.Key {
		t.Fatalf("name-only update = %#v", updated)
	}
	assertAccessKeyCredentialUnchanged(t, fixture.db, created.ID, original)

	status := state.AccessKeyStatusDisabled
	updated, err = fixture.service.UpdateAccessKey(context.Background(), created.ID, AccessKeyUpdateRequest{Status: &status})
	if err != nil {
		t.Fatalf("status-only UpdateAccessKey() error = %v", err)
	}
	if updated.Name != "after" || updated.Status != status || updated.Filters.Models[0] != "gpt-4o" {
		t.Fatalf("status-only update = %#v", updated)
	}
	assertAccessKeyCredentialUnchanged(t, fixture.db, created.ID, original)

	emptyFilters := AccessKeyFilters{}
	updated, err = fixture.service.UpdateAccessKey(context.Background(), created.ID, AccessKeyUpdateRequest{Filters: &emptyFilters})
	if err != nil {
		t.Fatalf("clear-filters UpdateAccessKey() error = %v", err)
	}
	if len(updated.Filters.Groups) != 0 || len(updated.Filters.Protocols) != 0 || len(updated.Filters.Models) != 0 {
		t.Fatalf("cleared filters = %#v", updated.Filters)
	}
	assertAccessKeyCredentialUnchanged(t, fixture.db, created.ID, original)

	finalName := "final"
	active := state.AccessKeyStatusActive
	finalFilters := AccessKeyFilters{
		Groups: []uint{group.ID}, Protocols: []protocol.Protocol{protocol.Anthropic}, Models: []string{"claude-3-5"},
	}
	updated, err = fixture.service.UpdateAccessKey(context.Background(), created.ID, AccessKeyUpdateRequest{
		Name: &finalName, Status: &active, Filters: &finalFilters,
	})
	if err != nil {
		t.Fatalf("all-fields UpdateAccessKey() error = %v", err)
	}
	if updated.Name != finalName || updated.Status != active || len(updated.Filters.Groups) != 1 || updated.Filters.Protocols[0] != protocol.Anthropic {
		t.Fatalf("all-fields update = %#v", updated)
	}
	assertAccessKeyCredentialUnchanged(t, fixture.db, created.ID, original)

	before := fixture.manager.Current().Revision
	if _, err := fixture.service.UpdateAccessKey(context.Background(), created.ID, AccessKeyUpdateRequest{}); err == nil {
		t.Fatal("empty UpdateAccessKey() error = nil")
	}
	if got := fixture.manager.Current().Revision; got != before {
		t.Fatalf("revision after empty update = %d, want %d", got, before)
	}
}

func TestUpdateAccessKeyStatusAndDeletePublishWithoutMutatingRegistry(t *testing.T) {
	fixture := newServiceFixture(t)
	fixture.service.random = bytes.NewReader(make([]byte, 16))
	created, err := fixture.service.CreateAccessKey(context.Background(), AccessKeyCreateRequest{Name: "toggle"})
	if err != nil {
		t.Fatalf("CreateAccessKey() error = %v", err)
	}
	row := loadAccessKeyRow(t, fixture.db, created.ID)
	if err := fixture.registry.ApplyImport(77, []state.KeyEntry{{
		ID: 88, GroupID: 77, Status: state.KeyStatusActive, EncryptedValue: "registry-cipher",
	}}); err != nil {
		t.Fatalf("seed Registry: %v", err)
	}
	if count, ok := fixture.registry.IncrFailure(88); !ok || count != 1 {
		t.Fatalf("seed failure count = %d, %t", count, ok)
	}

	before := fixture.manager.Current().Revision
	disabled := state.AccessKeyStatusDisabled
	if _, err := fixture.service.UpdateAccessKey(context.Background(), created.ID, AccessKeyUpdateRequest{Status: &disabled}); err != nil {
		t.Fatalf("disable UpdateAccessKey() error = %v", err)
	}
	if got := fixture.manager.Current().Revision; got != before+1 {
		t.Fatalf("revision after disable = %d, want %d", got, before+1)
	}
	if _, ok := fixture.manager.Current().AccessKeysByHash[row.KeyHash]; ok {
		t.Fatal("disabled AccessKey remains in authentication snapshot")
	}

	active := state.AccessKeyStatusActive
	if _, err := fixture.service.UpdateAccessKey(context.Background(), created.ID, AccessKeyUpdateRequest{Status: &active}); err != nil {
		t.Fatalf("enable UpdateAccessKey() error = %v", err)
	}
	if _, ok := fixture.manager.Current().AccessKeysByHash[row.KeyHash]; !ok {
		t.Fatal("active AccessKey missing from authentication snapshot")
	}

	invalid := state.AccessKeyStatus("paused")
	before = fixture.manager.Current().Revision
	if _, err := fixture.service.UpdateAccessKey(context.Background(), created.ID, AccessKeyUpdateRequest{Status: &invalid}); err == nil {
		t.Fatal("invalid status UpdateAccessKey() error = nil")
	}
	if got := fixture.manager.Current().Revision; got != before {
		t.Fatalf("revision after invalid status = %d, want %d", got, before)
	}

	if err := fixture.service.DeleteAccessKey(context.Background(), created.ID); err != nil {
		t.Fatalf("DeleteAccessKey() error = %v", err)
	}
	if got := fixture.manager.Current().Revision; got != before+1 {
		t.Fatalf("revision after delete = %d, want %d", got, before+1)
	}
	if _, ok := fixture.manager.Current().AccessKeysByHash[row.KeyHash]; ok {
		t.Fatal("deleted AccessKey remains in authentication snapshot")
	}
	var count int64
	if err := fixture.db.Model(&models.AccessKey{}).Where("id = ?", created.ID).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("deleted DB row count = %d, error=%v", count, err)
	}

	before = fixture.manager.Current().Revision
	if err := fixture.service.DeleteAccessKey(context.Background(), created.ID); err == nil {
		t.Fatal("repeated DeleteAccessKey() error = nil")
	}
	if _, err := fixture.service.UpdateAccessKey(context.Background(), created.ID, AccessKeyUpdateRequest{Name: stringPointer("missing")}); err == nil {
		t.Fatal("unknown UpdateAccessKey() error = nil")
	}
	if got := fixture.manager.Current().Revision; got != before {
		t.Fatalf("revision after missing mutations = %d, want %d", got, before)
	}
	if value, ok := fixture.registry.EncryptedValue(88); !ok || value != "registry-cipher" {
		t.Fatalf("Registry value = %q, %t, want unchanged", value, ok)
	}
	if failures, ok := fixture.registry.IncrFailure(88); !ok || failures != 2 {
		t.Fatalf("Registry failure count = %d, %t, want preserved count 2", failures, ok)
	}
}

func TestUpdateAccessKeyRollsBackWhenCredentialCannotDecrypt(t *testing.T) {
	fixture := newServiceFixture(t)
	fixture.service.random = bytes.NewReader(make([]byte, 16))
	created, err := fixture.service.CreateAccessKey(context.Background(), AccessKeyCreateRequest{Name: "before"})
	if err != nil {
		t.Fatalf("CreateAccessKey() error = %v", err)
	}
	if err := fixture.db.Model(&models.AccessKey{}).Where("id = ?", created.ID).UpdateColumn("key_value", "corrupt").Error; err != nil {
		t.Fatalf("corrupt AccessKey: %v", err)
	}
	before := fixture.manager.Current().Revision
	if _, err := fixture.service.UpdateAccessKey(context.Background(), created.ID, AccessKeyUpdateRequest{Name: stringPointer("after")}); err == nil {
		t.Fatal("UpdateAccessKey() error = nil")
	}
	row := loadAccessKeyRow(t, fixture.db, created.ID)
	if row.Name != "before" || fixture.manager.Current().Revision != before {
		t.Fatalf("failed update changed row/revision: name=%q revision=%d", row.Name, fixture.manager.Current().Revision)
	}
}

func TestAccessKeyDanglingFiltersDoNotBlockUnrelatedUpdate(t *testing.T) {
	fixture := newServiceFixture(t)
	danglingPlaintext := "gl-dangling-test-value"
	danglingCiphertext, err := fixture.encryption.Encrypt(danglingPlaintext)
	if err != nil {
		t.Fatalf("encrypt dangling key: %v", err)
	}
	if err := fixture.db.Create(&models.AccessKey{
		Name: "legacy", KeyValue: danglingCiphertext, KeyHash: fixture.encryption.Hash(danglingPlaintext),
		Status: string(state.AccessKeyStatusActive), Filters: models.JSON(`{"groups":[999],"protocols":[],"models":[]}`),
	}).Error; err != nil {
		t.Fatalf("create dangling AccessKey: %v", err)
	}
	fixture.service.random = bytes.NewReader(make([]byte, 16))
	current, err := fixture.service.CreateAccessKey(context.Background(), AccessKeyCreateRequest{Name: "current"})
	if err != nil {
		t.Fatalf("create current AccessKey: %v", err)
	}

	if _, err := fixture.service.UpdateAccessKey(context.Background(), current.ID, AccessKeyUpdateRequest{Name: stringPointer("renamed")}); err != nil {
		t.Fatalf("unrelated UpdateAccessKey() error = %v", err)
	}
	before := fixture.manager.Current().Revision
	invalidFilters := AccessKeyFilters{Groups: []uint{999}}
	if _, err := fixture.service.UpdateAccessKey(context.Background(), current.ID, AccessKeyUpdateRequest{Filters: &invalidFilters}); err == nil {
		t.Fatal("current dangling filter UpdateAccessKey() error = nil")
	}
	if got := fixture.manager.Current().Revision; got != before {
		t.Fatalf("revision after rejected dangling filter = %d, want %d", got, before)
	}
}

func TestConcurrentAccessKeyCRUDPublishesDatabaseTruth(t *testing.T) {
	fixture := newServiceFixture(t)
	rows := make([]AccessKeyResponse, 4)
	for index := range rows {
		created, err := fixture.service.CreateAccessKey(context.Background(), AccessKeyCreateRequest{Name: "seed-" + string(rune('a'+index))})
		if err != nil {
			t.Fatalf("seed AccessKey %d: %v", index, err)
		}
		rows[index] = created
	}
	before := fixture.manager.Current().Revision
	start := make(chan struct{})
	errors := make(chan error, 6)
	var ready sync.WaitGroup
	ready.Add(6)
	operations := []func() error{
		func() error {
			_, err := fixture.service.CreateAccessKey(context.Background(), AccessKeyCreateRequest{Name: "concurrent-a"})
			return err
		},
		func() error {
			_, err := fixture.service.CreateAccessKey(context.Background(), AccessKeyCreateRequest{Name: "concurrent-b"})
			return err
		},
		func() error {
			_, err := fixture.service.UpdateAccessKey(context.Background(), rows[0].ID, AccessKeyUpdateRequest{Name: stringPointer("updated-a")})
			return err
		},
		func() error {
			status := state.AccessKeyStatusDisabled
			_, err := fixture.service.UpdateAccessKey(context.Background(), rows[1].ID, AccessKeyUpdateRequest{Status: &status})
			return err
		},
		func() error { return fixture.service.DeleteAccessKey(context.Background(), rows[2].ID) },
		func() error { return fixture.service.DeleteAccessKey(context.Background(), rows[3].ID) },
	}
	for _, operation := range operations {
		operation := operation
		go func() {
			ready.Done()
			<-start
			errors <- operation()
		}()
	}
	ready.Wait()
	close(start)
	for range operations {
		if err := <-errors; err != nil {
			t.Fatalf("concurrent AccessKey mutation error = %v", err)
		}
	}

	input, err := stateloader.BuildCompileInput(context.Background(), fixture.db)
	if err != nil {
		t.Fatalf("BuildCompileInput() error = %v", err)
	}
	want, err := state.Compile(input)
	if err != nil {
		t.Fatalf("Compile(DB input) error = %v", err)
	}
	got := fixture.manager.Current()
	want.Revision = got.Revision
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("published snapshot differs from DB compile\ngot=%#v\nwant=%#v", got, want)
	}
	if got.Revision != before+uint64(len(operations)) {
		t.Fatalf("revision = %d, want %d", got.Revision, before+uint64(len(operations)))
	}
}

func loadAccessKeyRow(t *testing.T, db *gorm.DB, id uint) models.AccessKey {
	t.Helper()
	var row models.AccessKey
	if err := db.First(&row, id).Error; err != nil {
		t.Fatalf("query AccessKey %d: %v", id, err)
	}
	return row
}

func assertAccessKeyCredentialUnchanged(t *testing.T, db *gorm.DB, id uint, original models.AccessKey) {
	t.Helper()
	current := loadAccessKeyRow(t, db, id)
	if current.KeyValue != original.KeyValue || current.KeyHash != original.KeyHash {
		t.Fatalf("credential changed: before=%q/%q after=%q/%q", original.KeyValue, original.KeyHash, current.KeyValue, current.KeyHash)
	}
}

func stringPointer(value string) *string {
	return &value
}
