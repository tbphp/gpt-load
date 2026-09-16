package control

import (
	"encoding/json"
	"errors"
	"fmt"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"gpt-load/internal/concurrency"
	"gpt-load/internal/platform/config"
	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/state"
	stateloader "gpt-load/internal/state/loader"
	"gpt-load/internal/storage/models"
)

func TestSettingsCombinedConcurrencySaveIsAtomic(t *testing.T) {
	fixture := newServiceFixture(t)
	before := fixture.manager.Current()
	setting := map[string]json.RawMessage{state.SettingRetryCount: json.RawMessage(`3`)}
	invalid := SettingsUpdateRequest{
		Settings: setting,
		Concurrency: &SettingsConcurrencyUpdates{
			Global: optionalField[int64]{Set: true, Value: concurrency.MaximumLimit + 1},
		},
	}
	if _, err := fixture.service.UpdateSettings(t.Context(), invalid); !errors.Is(err, app_errors.ErrValidation) {
		t.Fatalf("invalid combined update = %v", err)
	}
	if fixture.manager.Current() != before {
		t.Fatal("invalid update published snapshot")
	}
	var count int64
	if err := fixture.db.Model(&models.SystemSetting{}).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("persisted setting count = %d, %v", count, err)
	}
	valid := SettingsUpdateRequest{
		Settings: setting,
		Concurrency: &SettingsConcurrencyUpdates{
			Global:       optionalField[int64]{Set: true, Value: 5},
			DefaultGroup: optionalField[int64]{Set: true, Value: 0},
		},
	}
	updated, err := fixture.service.UpdateSettings(t.Context(), valid)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Values.RetryCount != 3 || fixture.manager.Current().Revision != before.Revision+1 ||
		fixture.manager.Current().ConcurrencyPolicies[concurrency.Global] != 5 ||
		fixture.manager.Current().ConcurrencyPolicies[concurrency.DefaultGroup] != 0 {
		t.Fatalf("combined update did not publish once: %+v", fixture.manager.Current())
	}
	if _, err := fixture.service.UpdateSettings(t.Context(), SettingsUpdateRequest{
		Concurrency: &SettingsConcurrencyUpdates{Global: optionalField[int64]{Set: true, Null: true}},
	}); err != nil {
		t.Fatal(err)
	}
	if _, exists := fixture.manager.Current().ConcurrencyPolicies[concurrency.Global]; exists {
		t.Fatal("null did not remove global override")
	}

	if err := fixture.db.Exec(`CREATE TRIGGER reject_concurrency_write
		BEFORE INSERT ON concurrency_policies BEGIN SELECT RAISE(ABORT, 'forced rollback'); END`).Error; err != nil {
		t.Fatal(err)
	}
	fixture.service.db = fixture.db.Session(&gorm.Session{Logger: logger.Default.LogMode(logger.Silent)})
	before = fixture.manager.Current()
	if _, err := fixture.service.UpdateSettings(t.Context(), SettingsUpdateRequest{
		Settings:    map[string]json.RawMessage{state.SettingRetryCount: json.RawMessage(`4`)},
		Concurrency: &SettingsConcurrencyUpdates{Global: optionalField[int64]{Set: true, Value: 6}},
	}); !errors.Is(err, app_errors.ErrDatabase) {
		t.Fatalf("failed DB transaction = %v", err)
	}
	if fixture.manager.Current() != before {
		t.Fatal("failed DB transaction published snapshot")
	}
	settings, err := fixture.service.GetSettings(t.Context())
	if err != nil || settings.Values.RetryCount != 3 {
		t.Fatalf("setting rollback = %+v, %v", settings.Values, err)
	}
	assertConcurrencyPoliciesAbsent(t, fixture, concurrency.Global)
}

func TestGroupAndAccessKeyCombinedConcurrencySave(t *testing.T) {
	fixture := newServiceFixture(t)
	groupID := createGroupWithCredentials(t, fixture, "sk-combined")
	key, err := fixture.service.CreateAccessKey(t.Context(), AccessKeyCreateRequest{Name: "before"})
	if err != nil {
		t.Fatal(err)
	}
	before := fixture.manager.Current()
	name := "combined-group"
	groupResult, err := fixture.service.UpdateGroupSettings(t.Context(), groupID, GroupSettingsUpdateRequest{
		Name:           optionalField[string]{Set: true, Value: name},
		MaxConcurrency: optionalField[int64]{Set: true, Value: 7},
	})
	if err != nil || groupResult.Name != name || fixture.manager.Current().Revision != before.Revision+1 ||
		fixture.manager.Current().ConcurrencyPolicies[concurrency.Group(groupID)] != 7 {
		t.Fatalf("group combined update = %+v, %v", groupResult, err)
	}
	before = fixture.manager.Current()
	if _, err := fixture.service.UpdateGroupSettings(t.Context(), groupID, GroupSettingsUpdateRequest{
		Name:           optionalField[string]{Set: true, Value: "invalid"},
		MaxConcurrency: optionalField[int64]{Set: true, Value: -1},
	}); !errors.Is(err, app_errors.ErrValidation) {
		t.Fatalf("invalid group update = %v", err)
	}
	if fixture.manager.Current() != before {
		t.Fatal("invalid group update published snapshot")
	}
	groupResult, err = fixture.service.GetGroupSettings(t.Context(), groupID)
	if err != nil || groupResult.Name != name {
		t.Fatalf("invalid group update changed name: %+v, %v", groupResult, err)
	}
	if _, err := fixture.service.UpdateGroupSettings(t.Context(), groupID, GroupSettingsUpdateRequest{
		MaxConcurrency: optionalField[int64]{Set: true, Null: true},
	}); err != nil {
		t.Fatal(err)
	}
	assertConcurrencyPoliciesAbsent(t, fixture, concurrency.Group(groupID))

	before = fixture.manager.Current()
	newName := "combined-key"
	updated, err := fixture.service.UpdateAccessKey(t.Context(), key.ID, AccessKeyUpdateRequest{
		Name: &newName, MaxConcurrency: optionalField[int64]{Set: true, Value: 9},
	})
	if err != nil || updated.Name != newName || fixture.manager.Current().Revision != before.Revision+1 ||
		fixture.manager.Current().ConcurrencyPolicies[concurrency.AccessKey(key.ID)] != 9 {
		t.Fatalf("key combined update = %+v, %v", updated, err)
	}
	before = fixture.manager.Current()
	badName := "invalid-key"
	if _, err := fixture.service.UpdateAccessKey(t.Context(), key.ID, AccessKeyUpdateRequest{
		Name: &badName, MaxConcurrency: optionalField[int64]{Set: true, Value: concurrency.MaximumLimit + 1},
	}); !errors.Is(err, app_errors.ErrValidation) {
		t.Fatalf("invalid key update = %v", err)
	}
	if fixture.manager.Current() != before || loadAccessKeyRow(t, fixture.db, key.ID).Name != newName {
		t.Fatal("invalid key update changed metadata or snapshot")
	}
	if _, err := fixture.service.UpdateAccessKey(t.Context(), key.ID, AccessKeyUpdateRequest{
		MaxConcurrency: optionalField[int64]{Set: true, Null: true},
	}); err != nil {
		t.Fatal(err)
	}
	assertConcurrencyPoliciesAbsent(t, fixture, concurrency.AccessKey(key.ID))
}

func TestCombinedConcurrencySaveRollsBackResourceWhenPolicyWriteFails(t *testing.T) {
	fixture := newServiceFixture(t)
	groupID := createGroupWithCredentials(t, fixture, "sk-policy-rollback")
	key, err := fixture.service.CreateAccessKey(t.Context(), AccessKeyCreateRequest{Name: "original-key"})
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.Exec(`CREATE TRIGGER reject_concurrency_write
		BEFORE INSERT ON concurrency_policies BEGIN SELECT RAISE(ABORT, 'forced rollback'); END`).Error; err != nil {
		t.Fatal(err)
	}
	fixture.service.db = fixture.db.Session(&gorm.Session{Logger: logger.Default.LogMode(logger.Silent)})
	before := fixture.manager.Current()
	if _, err := fixture.service.UpdateGroupSettings(t.Context(), groupID, GroupSettingsUpdateRequest{
		Name:           optionalField[string]{Set: true, Value: "changed-group"},
		MaxConcurrency: optionalField[int64]{Set: true, Value: 3},
	}); !errors.Is(err, app_errors.ErrDatabase) {
		t.Fatalf("failed group transaction = %v", err)
	}
	group, err := fixture.service.GetGroupSettings(t.Context(), groupID)
	if err != nil || group.Name == "changed-group" || fixture.manager.Current() != before {
		t.Fatalf("group transaction partially applied: %+v, %v", group, err)
	}
	name := "changed-key"
	if _, err := fixture.service.UpdateAccessKey(t.Context(), key.ID, AccessKeyUpdateRequest{
		Name: &name, MaxConcurrency: optionalField[int64]{Set: true, Value: 3},
	}); !errors.Is(err, app_errors.ErrDatabase) {
		t.Fatalf("failed key transaction = %v", err)
	}
	if loadAccessKeyRow(t, fixture.db, key.ID).Name != "original-key" || fixture.manager.Current() != before {
		t.Fatal("key transaction partially applied")
	}
	assertConcurrencyPoliciesAbsent(t, fixture, concurrency.Group(groupID), concurrency.AccessKey(key.ID))
}

func TestIdempotentAccessKeyEditIncludesConcurrencyInSameOperation(t *testing.T) {
	fixture := newServiceFixture(t)
	key, err := fixture.service.CreateAccessKey(t.Context(), AccessKeyCreateRequest{Name: "before"})
	if err != nil {
		t.Fatal(err)
	}
	const operationID = "00000000-0000-4000-8000-000000006554"
	name := "after"
	request := AccessKeyUpdateRequest{
		Key: "replacement-key-value", Name: &name,
		MaxConcurrency: optionalField[int64]{Set: true, Value: 8},
	}
	before := fixture.manager.Current().Revision
	first, err := fixture.service.UpdateAccessKeyIdempotent(t.Context(), operationID, key.ID, request)
	if err != nil || first.Name != name || fixture.manager.Current().Revision != before+1 ||
		fixture.manager.Current().ConcurrencyPolicies[concurrency.AccessKey(key.ID)] != 8 {
		t.Fatalf("combined idempotent update = %+v, %v", first, err)
	}
	if _, err := fixture.service.UpdateAccessKeyIdempotent(t.Context(), operationID, key.ID, request); err != nil ||
		fixture.manager.Current().Revision != before+1 {
		t.Fatalf("replay published again: %v", err)
	}
	request.MaxConcurrency.Value = 9
	if _, err := fixture.service.UpdateAccessKeyIdempotent(t.Context(), operationID, key.ID, request); err == nil {
		t.Fatal("reused key accepted different concurrency limit")
	}
}

func TestSettingsConcurrencyRequestStrictJSON(t *testing.T) {
	for _, body := range []string{
		`{"concurrency":null}`, `{"concurrency":[]}`,
		`{"settings":{},"concurrency":{"extra":2}}`,
		`{"concurrency":{"global":1,"global":2}}`,
		`{"concurrency":{"global":"2"}}`,
	} {
		var request SettingsUpdateRequest
		if err := decodeStrictControlJSONObject([]byte(body), &request); err == nil {
			t.Fatalf("accepted invalid request %s", body)
		}
	}
	var empty SettingsUpdateRequest
	if err := decodeStrictControlJSONObject([]byte(`{}`), &empty); err == nil {
		t.Fatal("accepted empty request")
	}
	for _, body := range []string{
		`{"concurrency":{"global":-1}}`,
		`{"concurrency":{"default_group":1000001}}`,
	} {
		var request SettingsUpdateRequest
		if err := decodeStrictControlJSONObject([]byte(body), &request); err != nil {
			t.Fatalf("decode request %s: %v", body, err)
		}
		if err := validateSettingsConcurrencyUpdates(request.Concurrency); !errors.Is(err, app_errors.ErrValidation) {
			t.Fatalf("validate request %s: %v", body, err)
		}
	}
}

func setConcurrencyForTest(t *testing.T, fixture serviceFixture, scope string, id uint, limit *int64) {
	t.Helper()
	request := ConcurrencyUpdateRequest{MaxConcurrency: optionalField[int64]{Set: true, Null: limit == nil}}
	if limit != nil {
		request.MaxConcurrency.Value = *limit
	}
	if err := fixture.service.updateConcurrency(t.Context(), concurrencyTarget{scope, id}, request); err != nil {
		t.Fatal(err)
	}
}

func TestConcurrencySharedAccountInheritanceAndHotUpdate(t *testing.T) {
	fixture := newServiceFixture(t)
	_, one := createHomeSubscriptionCredential(t, fixture, "one", "shared-account", "shared@example.com")
	_, two := createHomeSubscriptionCredential(t, fixture, "two", "shared-account", "shared@example.com")
	maximum := int64(3)
	setConcurrencyForTest(t, fixture, "default_credential", 0, &maximum)
	oneRef, _ := fixture.registry.CredentialRef(one.ID)
	twoRef, _ := fixture.registry.CredentialRef(two.ID)
	first, allowed := fixture.manager.AdmitUpstreamConcurrency(oneRef)
	if !allowed {
		t.Fatal("first rejected")
	}
	defer first.Release()
	second, allowed := fixture.manager.AdmitUpstreamConcurrency(twoRef)
	if !allowed {
		t.Fatal("second rejected")
	}
	defer second.Release()
	views, err := fixture.service.readConcurrency([]concurrencyTarget{{"credential", one.ID}, {"credential", two.ID}})
	if err != nil {
		t.Fatal(err)
	}
	for _, view := range views.Items {
		if view.CurrentConcurrency != 2 || view.EffectiveMaxConcurrency != 3 || view.MaxConcurrency != nil || !view.Shared {
			t.Fatalf("view=%+v", view)
		}
	}
	maximum = 1
	setConcurrencyForTest(t, fixture, "credential", one.ID, &maximum)
	if lease, allowed := fixture.manager.AdmitUpstreamConcurrency(twoRef); allowed {
		lease.Release()
		t.Fatal("cross-group account exceeded lowered limit")
	}
	// An unrelated publication and token rotation preserve the stable account counter.
	if err := fixture.db.Model(&models.Credential{}).Where("id = ?", two.ID).Updates(map[string]any{"secret_version": two.SecretVersion + 1, "fingerprint": "rotated-secret"}).Error; err != nil {
		t.Fatal(err)
	}
	input, err := stateloader.BuildCompileInput(t.Context(), fixture.db, fixture.channelRegistry)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.manager.Publish(input); err != nil {
		t.Fatal(err)
	}
	if lease, allowed := fixture.manager.AdmitUpstreamConcurrency(twoRef); allowed {
		lease.Release()
		t.Fatal("token rotation reset concurrency")
	}
	first.Release()
	if lease, allowed := fixture.manager.AdmitUpstreamConcurrency(twoRef); allowed {
		lease.Release()
		t.Fatal("admitted at capacity")
	}
	second.Release()
	lease, allowed := fixture.manager.AdmitUpstreamConcurrency(twoRef)
	if !allowed {
		t.Fatal("slot not released")
	}
	lease.Release()
	maximum = 0
	setConcurrencyForTest(t, fixture, "credential", two.ID, &maximum)
	views, err = fixture.service.readConcurrency([]concurrencyTarget{{"credential", one.ID}})
	if err != nil || views.Items[0].EffectiveMaxConcurrency != 0 || views.Items[0].MaxConcurrency == nil {
		t.Fatalf("unlimited override=%+v %v", views, err)
	}
	setConcurrencyForTest(t, fixture, "credential", one.ID, nil)
	views, err = fixture.service.readConcurrency([]concurrencyTarget{{"credential", two.ID}})
	if err != nil || views.Items[0].EffectiveMaxConcurrency != 3 || views.Items[0].MaxConcurrency != nil {
		t.Fatalf("inherit=%+v %v", views, err)
	}
	// Policies are durable; counts are deliberately not restored on restart.
	manager := state.NewManager()
	input, err = stateloader.BuildCompileInput(t.Context(), fixture.db, fixture.channelRegistry)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := manager.Publish(input)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.ConcurrencyPolicies[concurrency.DefaultCredential] != 3 {
		t.Fatal("default policy not persisted")
	}
	if got := manager.ObserveConcurrency([]concurrency.Subject{concurrency.Upstream})[concurrency.Upstream]; got != 0 {
		t.Fatal("restored in-flight count")
	}
}

func TestConcurrencyHTTPAuthorizationAndValidation(t *testing.T) {
	initControlI18n(t)
	fixture := newServiceFixture(t)
	key, err := fixture.service.CreateAccessKey(t.Context(), AccessKeyCreateRequest{Name: "limited-client"})
	if err != nil {
		t.Fatal(err)
	}
	other, err := fixture.service.CreateAccessKey(t.Context(), AccessKeyCreateRequest{Name: "other-client"})
	if err != nil {
		t.Fatal(err)
	}
	engine := gin.New()
	NewServer(&config.Config{AuthKey: authTestKey}, fixture.service).RegisterRoutes(engine)
	request := func(method, path, body, auth string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+auth)
		req.Header.Set("Content-Type", "application/json")
		result := httptest.NewRecorder()
		engine.ServeHTTP(result, req)
		return result
	}
	for _, tc := range []struct {
		method, path, body, auth string
		status                   int
	}{
		{"GET", fmt.Sprintf("/api/concurrency?targets=access_key:%d", key.ID), "", key.Key, 200},
		{"GET", "/api/concurrency?targets=global:0", "", key.Key, 403},
		{"GET", fmt.Sprintf("/api/concurrency?targets=access_key:%d,access_key:%d", key.ID, other.ID), "", key.Key, 403},
		{"PUT", fmt.Sprintf("/api/concurrency/access_key/%d", key.ID), `{"max_concurrency":2}`, key.Key, 403},
		{"PUT", "/api/concurrency/global/0", `{}`, authTestKey, 400},
		{"PUT", "/api/concurrency/global/0", `{"max_concurrency":-1}`, authTestKey, 400},
		{"PUT", "/api/concurrency/global/0", `{"max_concurrency":1.5}`, authTestKey, 400},
		{"PUT", "/api/concurrency/global/0", `{"max_concurrency":1000001}`, authTestKey, 400},
		{"PUT", "/api/concurrency/global/0", `{"max_concurrency":1,"max_concurrency":2}`, authTestKey, 400},
		{"PUT", "/api/concurrency/upstream/0", `{"max_concurrency":2}`, authTestKey, 400},
		{"PUT", "/api/concurrency/global/0", `{"max_concurrency":2}`, authTestKey, 200},
		{"GET", "/api/concurrency?targets=global:0,upstream:0", "", authTestKey, 200},
	} {
		t.Run(tc.method+tc.path+tc.body, func(t *testing.T) {
			result := request(tc.method, tc.path, tc.body, tc.auth)
			if result.Code != tc.status {
				t.Fatalf("status=%d want=%d body=%s", result.Code, tc.status, result.Body.String())
			}
		})
	}
	snapshot := fixture.manager.Current()
	lease, blocked, _ := fixture.manager.AdmitConcurrency(snapshot, snapshot.RequestConcurrencyLimits(key.ID))
	if blocked != "" {
		t.Fatal(blocked)
	}
	defer lease.Release()
	result := request("GET", fmt.Sprintf("/api/concurrency?targets=access_key:%d", key.ID), "", key.Key)
	var envelope struct {
		Data ConcurrencyResponse `json:"data"`
	}
	if err := json.Unmarshal(result.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if len(envelope.Data.Items) != 1 || envelope.Data.Items[0].CurrentConcurrency != 1 {
		t.Fatalf("self scoped=%s", result.Body.String())
	}
	if strings.Contains(result.Body.String(), "fingerprint") || strings.Contains(result.Body.String(), "subject") {
		t.Fatal("internal identity exposed")
	}
}

func TestConcurrencyBusyProbeDoesNotReachUpstream(t *testing.T) {
	fixture := newServiceFixture(t)
	groupID := createGroupWithCredentials(t, fixture, "sk-probe")
	var row models.Credential
	if err := fixture.db.Where("group_id = ?", groupID).Take(&row).Error; err != nil {
		t.Fatal(err)
	}
	limit := int64(1)
	setConcurrencyForTest(t, fixture, "credential", row.ID, &limit)
	ref, _ := fixture.registry.CredentialRef(row.ID)
	lease, allowed := fixture.manager.AdmitUpstreamConcurrency(ref)
	if !allowed {
		t.Fatal("cannot fill probe slot")
	}
	defer lease.Release()
	if _, err := fixture.service.TestGroupCredential(t.Context(), groupID, row.ID); err == nil {
		t.Fatal("busy probe was admitted")
	}
}

func TestCredentialMutationsPublishConcurrencySnapshot(t *testing.T) {
	fixture := newServiceFixture(t)
	groupID := createGroupWithCredentials(t, fixture, "sk-existing")
	beforeImport := fixture.manager.Current()
	if _, err := fixture.service.ImportGroupCredentials(t.Context(), groupID, CredentialImportRequest{
		Credentials: "sk-imported",
	}); err != nil {
		t.Fatalf("ImportGroupCredentials() error = %v", err)
	}
	var imported models.Credential
	if err := fixture.db.Where("group_id = ?", groupID).Order("id DESC").Take(&imported).Error; err != nil {
		t.Fatal(err)
	}
	afterImport := fixture.manager.Current()
	if afterImport == beforeImport || afterImport.Revision <= beforeImport.Revision {
		t.Fatal("credential import did not publish a new snapshot")
	}
	if _, err := fixture.service.readConcurrency([]concurrencyTarget{{"credential", imported.ID}}); err != nil {
		t.Fatalf("read imported credential concurrency: %v", err)
	}
	ref, exists := fixture.registry.CredentialRef(imported.ID)
	if !exists {
		t.Fatal("imported credential is missing from registry")
	}
	lease, admitted := fixture.manager.AdmitUpstreamConcurrency(ref)
	if !admitted {
		t.Fatal("imported credential was rejected by concurrency admission")
	}
	lease.Release()

	maximum := int64(2)
	setConcurrencyForTest(t, fixture, "credential", imported.ID, &maximum)
	beforeDelete := fixture.manager.Current()
	if err := fixture.service.DeleteGroupCredential(t.Context(), groupID, imported.ID); err != nil {
		t.Fatalf("DeleteGroupCredential() error = %v", err)
	}
	if afterDelete := fixture.manager.Current(); afterDelete == beforeDelete ||
		afterDelete.Revision <= beforeDelete.Revision {
		t.Fatal("credential delete did not publish a new snapshot")
	}
	if _, err := fixture.service.readConcurrency([]concurrencyTarget{{"credential", imported.ID}}); !errors.Is(err, app_errors.ErrResourceNotFound) {
		t.Fatalf("deleted credential concurrency error = %v, want resource not found", err)
	}
	assertConcurrencyPoliciesAbsent(t, fixture, concurrency.Credential(imported.ID))
}

func TestResourceDeletesRemoveConcurrencyPolicies(t *testing.T) {
	fixture := newServiceFixture(t)
	groupID := createGroupWithCredentials(t, fixture, "sk-delete-group")
	var credential models.Credential
	if err := fixture.db.Where("group_id = ?", groupID).Take(&credential).Error; err != nil {
		t.Fatal(err)
	}
	key, err := fixture.service.CreateAccessKey(t.Context(), AccessKeyCreateRequest{Name: "delete-key"})
	if err != nil {
		t.Fatal(err)
	}
	maximum := int64(4)
	setConcurrencyForTest(t, fixture, "group", groupID, &maximum)
	setConcurrencyForTest(t, fixture, "credential", credential.ID, &maximum)
	setConcurrencyForTest(t, fixture, "access_key", key.ID, &maximum)

	if err := fixture.service.DeleteAccessKey(t.Context(), key.ID); err != nil {
		t.Fatalf("DeleteAccessKey() error = %v", err)
	}
	assertConcurrencyPoliciesAbsent(t, fixture, concurrency.AccessKey(key.ID))
	if err := fixture.service.DeleteGroup(t.Context(), groupID); err != nil {
		t.Fatalf("DeleteGroup() error = %v", err)
	}
	assertConcurrencyPoliciesAbsent(
		t, fixture, concurrency.Group(groupID), concurrency.Credential(credential.ID),
	)
}

func TestBatchCredentialDeletePublishesSnapshotAndRemovesPolicies(t *testing.T) {
	fixture := newServiceFixture(t)
	groupID := createGroupWithCredentials(t, fixture, "sk-batch-one\nsk-batch-two")
	var rows []models.Credential
	if err := fixture.db.Where("group_id = ?", groupID).Order("id ASC").Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	ids := make([]uint, len(rows))
	subjects := make([]concurrency.Subject, len(rows))
	maximum := int64(2)
	for index, row := range rows {
		ids[index] = row.ID
		subjects[index] = concurrency.Credential(row.ID)
		setConcurrencyForTest(t, fixture, "credential", row.ID, &maximum)
	}
	beforeDelete := fixture.manager.Current()
	if _, err := fixture.service.BatchGroupCredentials(t.Context(), groupID, CredentialBatchRequest{
		Action: CredentialBatchDelete, CredentialIDs: ids,
	}); err != nil {
		t.Fatalf("BatchGroupCredentials(delete) error = %v", err)
	}
	if afterDelete := fixture.manager.Current(); afterDelete == beforeDelete ||
		afterDelete.Revision <= beforeDelete.Revision {
		t.Fatal("batch credential delete did not publish a new snapshot")
	}
	for _, id := range ids {
		if _, err := fixture.service.readConcurrency([]concurrencyTarget{{"credential", id}}); !errors.Is(err, app_errors.ErrResourceNotFound) {
			t.Fatalf("deleted credential %d concurrency error = %v, want resource not found", id, err)
		}
	}
	assertConcurrencyPoliciesAbsent(t, fixture, subjects...)
}

func TestParseConcurrencyTargetAcceptsSafePlatformUint(t *testing.T) {
	if strconv.IntSize < 64 {
		t.Skip("platform uint is 32-bit")
	}
	const raw = "group:4294967296"
	target, err := parseConcurrencyTarget(raw)
	if err != nil || target.Scope != "group" || uint64(target.ID) != 4294967296 {
		t.Fatalf("parseConcurrencyTarget(%q) = %#v, %v", raw, target, err)
	}
	for _, invalid := range []string{"group:01", "group:9007199254740992"} {
		if _, err := parseConcurrencyTarget(invalid); !errors.Is(err, app_errors.ErrBadRequest) {
			t.Fatalf("parseConcurrencyTarget(%q) error = %v, want bad request", invalid, err)
		}
	}
}

func assertConcurrencyPoliciesAbsent(
	t *testing.T,
	fixture serviceFixture,
	subjects ...concurrency.Subject,
) {
	t.Helper()
	values := make([]string, len(subjects))
	for index, subject := range subjects {
		values[index] = string(subject)
	}
	var count int64
	if err := fixture.db.Model(&models.ConcurrencyPolicy{}).
		Where("subject IN ?", values).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("found %d concurrency policies for deleted subjects %v", count, values)
	}
}
