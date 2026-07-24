package control

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"gpt-load/internal/platform/config"
	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/state"
	"gpt-load/internal/storage/models"
)

func TestDeleteGroupRejectsActiveAndDisabledExplicitAccessKeyReferences(t *testing.T) {
	fixture := newServiceFixture(t)
	groupID := createGroupForKeyImport(t, fixture, "sk-in-use")
	other := validControlGroup("other-reference")
	if err := fixture.db.Create(other).Error; err != nil {
		t.Fatal(err)
	}
	for _, row := range []models.AccessKey{
		{
			Name: "Active", KeyValue: "cipher-client-one", KeyHash: "client-hash-one",
			Status:  string(state.AccessKeyStatusActive),
			Filters: models.JSON(fmt.Sprintf(`{"groups":[%d],"protocols":[],"models":[]}`, groupID)),
		},
		{
			Name: "Disabled", KeyValue: "cipher-client-two", KeyHash: "client-hash-two",
			Status:  string(state.AccessKeyStatusDisabled),
			Filters: models.JSON(fmt.Sprintf(`{"groups":[%d,%d],"protocols":[],"models":[]}`, other.ID, groupID)),
		},
		{
			Name: "Unrestricted", KeyValue: "cipher-client-three", KeyHash: "client-hash-three",
			Status:  string(state.AccessKeyStatusActive),
			Filters: models.JSON(`{"groups":[],"protocols":["openai"],"models":["gpt-4o"]}`),
		},
	} {
		row := row
		if err := fixture.db.Create(&row).Error; err != nil {
			t.Fatal(err)
		}
	}
	beforeSnapshot := fixture.manager.Current()
	beforeRegistry := fixture.registry.Snapshot()

	err := fixture.service.DeleteGroup(t.Context(), groupID)
	var apiErr *app_errors.APIError
	if !errors.As(err, &apiErr) || apiErr.Code != app_errors.ErrGroupInUse.Code {
		t.Fatalf("DeleteGroup() error = %#v", err)
	}
	data := apiErr.Data.(GroupInUseData)
	if len(data.AccessKeys) != 2 ||
		data.AccessKeys[0].Name != "Active" ||
		data.AccessKeys[1].Name != "Disabled" {
		t.Fatalf("references = %#v", data)
	}
	if fixture.manager.Current() != beforeSnapshot ||
		!reflect.DeepEqual(fixture.registry.Snapshot(), beforeRegistry) {
		t.Fatal("rejected delete mutated runtime state")
	}
	var count int64
	if err := fixture.db.Model(&models.Group{}).Where("id = ?", groupID).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("group count = %d, want 1", count)
	}
	encoded, _ := json.Marshal(apiErr.Data)
	for _, forbidden := range []string{"cipher-client", "client-hash", `"status"`, `"filters"`} {
		if bytes.Contains(encoded, []byte(forbidden)) {
			t.Fatalf("GROUP_IN_USE data exposes %q: %s", forbidden, encoded)
		}
	}
}

func TestDeleteGroupCommitsCascadeThenRemovesRegistryThenPublishes(t *testing.T) {
	fixture := newServiceFixture(t)
	groupID := createGroupForKeyImport(t, fixture, "sk-delete-a\nsk-delete-b")
	beforeRevision := fixture.manager.Current().Revision
	var keyRows []models.UpstreamKey
	if err := fixture.db.Where("group_id = ?", groupID).Order("id ASC").Find(&keyRows).Error; err != nil {
		t.Fatal(err)
	}

	if err := fixture.service.DeleteGroup(t.Context(), groupID); err != nil {
		t.Fatalf("DeleteGroup() error = %v", err)
	}
	for _, row := range keyRows {
		if value, ok := fixture.registry.EncryptedValue(row.ID); ok || value != "" {
			t.Fatalf("Registry key %d remains = %q, %t", row.ID, value, ok)
		}
	}
	if got := fixture.manager.Current(); got.Revision != beforeRevision+1 {
		t.Fatalf("revision = %d, want %d", got.Revision, beforeRevision+1)
	} else if _, exists := got.GroupCatalog[groupID]; exists {
		t.Fatalf("published Snapshot retains Group %d", groupID)
	}
	var groupCount, keyCount int64
	if err := fixture.db.Model(&models.Group{}).Where("id = ?", groupID).Count(&groupCount).Error; err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.Model(&models.UpstreamKey{}).Where("group_id = ?", groupID).Count(&keyCount).Error; err != nil {
		t.Fatal(err)
	}
	if groupCount != 0 || keyCount != 0 {
		t.Fatalf("DB counts = group:%d key:%d", groupCount, keyCount)
	}
}

func TestDeleteGroupCompileFailurePreservesDatabaseRegistryAndSnapshot(t *testing.T) {
	fixture := newServiceFixture(t)
	groupID := createGroupForKeyImport(t, fixture, "sk-delete-rollback")
	corrupt := validControlGroup("delete-corrupt-other")
	if err := fixture.db.Create(corrupt).Error; err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.Exec(
		"UPDATE groups SET protocols = ? WHERE id = ?",
		`[]`,
		corrupt.ID,
	).Error; err != nil {
		t.Fatal(err)
	}
	beforeSnapshot := fixture.manager.Current()
	beforeRegistry := fixture.registry.Snapshot()

	err := fixture.service.DeleteGroup(t.Context(), groupID)
	if err == nil {
		t.Fatal("DeleteGroup() error = nil")
	}
	if fixture.manager.Current() != beforeSnapshot ||
		!reflect.DeepEqual(fixture.registry.Snapshot(), beforeRegistry) {
		t.Fatal("failed delete mutated runtime state")
	}
	var count int64
	if err := fixture.db.Model(&models.Group{}).Where("id = ?", groupID).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("target group count = %d, want 1", count)
	}
}

func TestDeleteGroupCommitFailurePreservesDatabaseRegistryAndSnapshot(t *testing.T) {
	fixture, dsn := newFileServiceFixture(t)
	groupID := createGroupForKeyImport(t, fixture, "sk-delete-commit-busy")
	var row models.UpstreamKey
	if err := fixture.db.Where("group_id = ?", groupID).Take(&row).Error; err != nil {
		t.Fatal(err)
	}
	beforeSnapshot := fixture.manager.Current()
	beforeRegistry := fixture.registry.Snapshot()
	releaseReader := holdRollbackJournalReadLock(t, fixture.db, dsn)

	err := fixture.service.DeleteGroup(t.Context(), groupID)
	var apiErr *app_errors.APIError
	if !errors.As(err, &apiErr) || apiErr.Code != app_errors.ErrDatabase.Code {
		t.Fatalf("DeleteGroup() error = %#v, want DATABASE_ERROR", err)
	}
	if fixture.manager.Current() != beforeSnapshot ||
		!reflect.DeepEqual(fixture.registry.Snapshot(), beforeRegistry) {
		t.Fatal("failed COMMIT changed runtime state")
	}

	releaseReader()
	var groupCount, keyCount int64
	if err := fixture.db.Model(&models.Group{}).
		Where("id = ?", groupID).Count(&groupCount).Error; err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.Model(&models.UpstreamKey{}).
		Where("id = ?", row.ID).Count(&keyCount).Error; err != nil {
		t.Fatal(err)
	}
	if groupCount != 1 || keyCount != 1 {
		t.Fatalf("rollback counts = group:%d key:%d", groupCount, keyCount)
	}
	if value, exists := fixture.registry.EncryptedValue(row.ID); !exists || value != row.KeyValue {
		t.Fatalf("Registry credential = %q, %t", value, exists)
	}
}

func TestDeleteGroupCorruptAccessKeyFiltersPreservesDatabaseRegistryAndSnapshot(t *testing.T) {
	fixture := newServiceFixture(t)
	groupID := createGroupForKeyImport(t, fixture, "sk-delete-corrupt-filter")
	corrupt := models.AccessKey{
		Name: "Corrupt filters", KeyValue: "cipher-corrupt-filter", KeyHash: "hash-corrupt-filter",
		Status: string(state.AccessKeyStatusDisabled), Filters: models.JSON(`{}`),
	}
	if err := fixture.db.Create(&corrupt).Error; err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.Exec("UPDATE access_keys SET filters = ? WHERE id = ?", "not-json", corrupt.ID).Error; err != nil {
		t.Fatal(err)
	}
	beforeSnapshot := fixture.manager.Current()
	beforeRegistry := fixture.registry.Snapshot()

	err := fixture.service.DeleteGroup(t.Context(), groupID)
	var apiErr *app_errors.APIError
	if !errors.As(err, &apiErr) || apiErr.Code != app_errors.ErrInternalServer.Code {
		t.Fatalf("DeleteGroup() error = %#v, want INTERNAL_SERVER_ERROR", err)
	}
	if fixture.manager.Current() != beforeSnapshot ||
		!reflect.DeepEqual(fixture.registry.Snapshot(), beforeRegistry) {
		t.Fatal("corrupt filters delete mutated runtime state")
	}
	var count int64
	if err := fixture.db.Model(&models.Group{}).Where("id = ?", groupID).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("target group count = %d, want 1", count)
	}
}

func TestDeleteGroupAcceptsRegistryAlreadyAtTargetStateAndRetainsHistory(t *testing.T) {
	fixture := newServiceFixture(t)
	groupID := createGroupForKeyImport(t, fixture, "sk-delete-target-state")
	var key models.UpstreamKey
	if err := fixture.db.Where("group_id = ?", groupID).Take(&key).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, time.July, 24, 16, 0, 0, 0, time.UTC)
	fixture.stats.Record(key.ID, false, now)
	beforeStats := fixture.stats.Snapshot(key.ID, now)
	if err := fixture.db.Create(&models.RequestLog{
		ID: "delete-history-log", CreatedAt: now, AccessKeyID: 1,
		Protocol: "openai", ClientModel: "model", UpstreamModel: "model",
		Status: "success", StatusCode: http.StatusOK,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.Create(&models.UsageStat{
		HourBucket: now, GroupID: groupID, Model: "model", RequestCount: 1,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if removed := fixture.registry.RemoveGroup(groupID); !removed {
		t.Fatal("precondition registry RemoveGroup() = false")
	}

	if err := fixture.service.DeleteGroup(t.Context(), groupID); err != nil {
		t.Fatalf("DeleteGroup() with registry already removed error = %v", err)
	}
	if got := fixture.stats.Snapshot(key.ID, now); got != beforeStats {
		t.Fatalf("StatsStore Snapshot = %#v, want %#v", got, beforeStats)
	}
	var requestLogCount, usageStatCount int64
	if err := fixture.db.Model(&models.RequestLog{}).Where("id = ?", "delete-history-log").Count(&requestLogCount).Error; err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.Model(&models.UsageStat{}).Where("group_id = ?", groupID).Count(&usageStatCount).Error; err != nil {
		t.Fatal(err)
	}
	if requestLogCount != 1 || usageStatCount != 1 {
		t.Fatalf("historical records = request_logs:%d usage_stats:%d", requestLogCount, usageStatCount)
	}
}

func TestDeleteGroupEndpointAuthenticationValidationNotFoundConflictAndSuccess(t *testing.T) {
	initControlI18n(t)
	fixture := newServiceFixture(t)
	groupID := createGroupForKeyImport(t, fixture, "sk-delete-http")
	engine := gin.New()
	NewServer(&config.Config{AuthKey: "test-auth-key"}, fixture.service).RegisterRoutes(engine)
	path := "/api/groups/" + strconv.FormatUint(uint64(groupID), 10)

	unauthorized := httptest.NewRecorder()
	engine.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodDelete, path, nil))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized response = %d, want 401", unauthorized.Code)
	}

	for _, rawID := range []string{"0", "not-a-number", "18446744073709551616"} {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodDelete, "/api/groups/"+rawID, nil)
		request.Header.Set("Authorization", "Bearer test-auth-key")
		engine.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("bad ID %q response = %d, want 400", rawID, recorder.Code)
		}
	}

	notFound := httptest.NewRecorder()
	notFoundRequest := httptest.NewRequest(http.MethodDelete, "/api/groups/999", nil)
	notFoundRequest.Header.Set("Authorization", "Bearer test-auth-key")
	notFoundRequest.Header.Set("Accept-Language", "ja-JP")
	engine.ServeHTTP(notFound, notFoundRequest)
	assertDeleteGroupEnvelope(t, notFound, http.StatusNotFound, "NOT_FOUND", "グループが存在しません")

	for _, test := range []struct {
		language string
		message  string
	}{
		{language: "zh-CN", message: "分组仍被访问密钥引用"},
		{language: "en-US", message: "The group is still referenced by access keys"},
		{language: "ja-JP", message: "グループはアクセスキーから参照されています"},
	} {
		row := models.AccessKey{
			Name:     "HTTP reference " + test.language,
			KeyValue: "cipher-http-" + test.language,
			KeyHash:  "hash-http-" + test.language,
			Status:   string(state.AccessKeyStatusActive),
			Filters:  models.JSON(fmt.Sprintf(`{"groups":[%d]}`, groupID)),
		}
		if err := fixture.db.Create(&row).Error; err != nil {
			t.Fatal(err)
		}
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodDelete, path, nil)
		request.Header.Set("Authorization", "Bearer test-auth-key")
		request.Header.Set("Accept-Language", test.language)
		engine.ServeHTTP(recorder, request)
		assertDeleteGroupEnvelope(t, recorder, http.StatusConflict, "GROUP_IN_USE", test.message)
		var cleanup models.AccessKey
		if err := fixture.db.First(&cleanup, row.ID).Error; err != nil {
			t.Fatal(err)
		}
		if err := fixture.db.Delete(&cleanup).Error; err != nil {
			t.Fatal(err)
		}
	}

	success := httptest.NewRecorder()
	successRequest := httptest.NewRequest(http.MethodDelete, path, nil)
	successRequest.Header.Set("Authorization", "Bearer test-auth-key")
	engine.ServeHTTP(success, successRequest)
	if success.Code != http.StatusOK {
		t.Fatalf("success response = %d: %s", success.Code, success.Body.String())
	}
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(success.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if string(envelope["code"]) != "0" || string(envelope["message"]) != `"操作成功"` {
		t.Fatalf("success envelope = %s", success.Body.Bytes())
	}
	if _, exists := envelope["data"]; exists {
		t.Fatalf("success envelope has unexpected data: %s", success.Body.Bytes())
	}
}

func assertDeleteGroupEnvelope(t *testing.T, recorder *httptest.ResponseRecorder, wantStatus int, wantCode, wantMessage string) {
	t.Helper()
	var envelope struct {
		Code    string `json:"code"`
		Message string `json:"message"`
		Data    struct {
			AccessKeys []AccessKeyReferenceSummary `json:"access_keys"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if recorder.Code != wantStatus || envelope.Code != wantCode || envelope.Message != wantMessage {
		t.Fatalf("response = %d %#v, want %d %q %q", recorder.Code, envelope, wantStatus, wantCode, wantMessage)
	}
	if wantCode == "GROUP_IN_USE" {
		if len(envelope.Data.AccessKeys) != 1 || envelope.Data.AccessKeys[0].ID == 0 || envelope.Data.AccessKeys[0].Name == "" {
			t.Fatalf("GROUP_IN_USE data = %#v", envelope.Data)
		}
	}
}
