package control

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"

	"gpt-load/internal/dialect"
	"gpt-load/internal/platform/config"
	"gpt-load/internal/platform/encryption"
	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/protocol"
	"gpt-load/internal/scheduler"
	"gpt-load/internal/state"
	"gpt-load/internal/storage/models"
)

func seedManagedUpstreamKey(
	t *testing.T,
	fixture serviceFixture,
	groupID uint,
	plaintext string,
	status models.UpstreamKeyStatus,
	weight *int,
) models.UpstreamKey {
	t.Helper()
	ciphertext, err := fixture.encryption.Encrypt(plaintext)
	if err != nil {
		t.Fatal(err)
	}
	row := models.UpstreamKey{
		GroupID: groupID, KeyValue: ciphertext,
		KeyHash: fixture.encryption.Hash(plaintext),
		Status:  status, WeightManual: weight,
	}
	if err := fixture.db.Create(&row).Error; err != nil {
		t.Fatal(err)
	}
	if err := fixture.registry.ApplyImport(groupID, []state.KeyEntry{{
		ID: row.ID, GroupID: groupID, EncryptedValue: row.KeyValue,
		Status: state.KeyStatus(row.Status), WeightManual: row.WeightManual,
	}}); err != nil {
		t.Fatal(err)
	}
	return row
}

func TestUpdateGroupKeyCommittedRegistryFailureLogStage(t *testing.T) {
	initControlI18n(t)
	const (
		authKey      = "known-update-auth-secret"
		plaintext    = "known-update-upstream-secret"
		querySecret  = "known-update-query-secret"
		headerSecret = "known-update-header-secret"
	)
	fixture := newServiceFixture(t)
	group := validControlGroup("update-commit-log")
	group.UpstreamURL += "?token=" + querySecret
	group.Config = models.JSON(
		`{"header_rules":{"set":{"X-Secret":"` + headerSecret + `"}}}`,
	)
	if err := fixture.db.Create(group).Error; err != nil {
		t.Fatal(err)
	}
	other := validControlGroup("update-commit-log-other")
	if err := fixture.db.Create(other).Error; err != nil {
		t.Fatal(err)
	}
	row := seedManagedUpstreamKey(
		t, fixture, group.ID, plaintext,
		models.UpstreamKeyStatusActive, nil,
	)
	accessKey, err := fixture.service.CreateAccessKey(
		t.Context(),
		AccessKeyCreateRequest{Name: "update-commit-access-key"},
	)
	if err != nil {
		t.Fatal(err)
	}
	accessKeyRow := loadAccessKeyRow(t, fixture.db, accessKey.ID)
	if err := fixture.registry.Replace([]state.KeyEntry{{
		ID: row.ID, GroupID: other.ID,
		Status: state.KeyStatusActive, WeightAuto: state.DefaultWeight,
		EncryptedValue: row.KeyValue,
	}}); err != nil {
		t.Fatal(err)
	}
	beforeSnapshot := fixture.manager.Current()

	engine := gin.New()
	NewServer(&config.Config{AuthKey: authKey}, fixture.service).RegisterRoutes(engine)
	var logs bytes.Buffer
	logger := logrus.StandardLogger()
	previousOutput, previousFormatter := logger.Out, logger.Formatter
	logrus.SetOutput(&logs)
	logrus.SetFormatter(&logrus.JSONFormatter{DisableTimestamp: true})
	t.Cleanup(func() {
		logrus.SetOutput(previousOutput)
		logrus.SetFormatter(previousFormatter)
	})

	recorder := serveGroupKeyHTTPRequest(
		t,
		engine,
		http.MethodPut,
		fmt.Sprintf("/api/groups/%d/keys/%d", group.ID, row.ID),
		`{"status":"disabled"}`,
		authKey,
		"en-US",
	)
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("PUT response = %d %s, want 500", recorder.Code, recorder.Body.String())
	}
	if fixture.manager.Current() != beforeSnapshot {
		t.Fatal("post-commit failure published Snapshot")
	}
	var committed models.UpstreamKey
	if err := fixture.db.First(&committed, row.ID).Error; err != nil {
		t.Fatal(err)
	}
	if committed.Status != models.UpstreamKeyStatusDisabled {
		t.Fatalf("committed status = %q, want disabled", committed.Status)
	}
	view, exists := findRuntimeKey(fixture.registry.Snapshot(), row.ID)
	if !exists || view.GroupID != other.ID || view.Status != state.KeyStatusActive {
		t.Fatalf("Registry view = %#v, exists=%t", view, exists)
	}

	logText := logs.String()
	for _, required := range []string{
		`"operation":"update_group_key"`,
		`"stage":"apply_committed_registry_mutation"`,
		`"group_id":` + strconv.FormatUint(uint64(group.ID), 10),
		`"key_id":` + strconv.FormatUint(uint64(row.ID), 10),
	} {
		if !strings.Contains(logText, required) {
			t.Fatalf("logs missing %q: %s", required, logText)
		}
	}
	if strings.Contains(logText, `"mismatch_kind"`) {
		t.Fatalf("post-commit log contains mismatch kind: %s", logText)
	}
	for _, forbidden := range []string{
		authKey,
		accessKey.Key,
		accessKeyRow.KeyValue,
		accessKeyRow.KeyHash,
		plaintext,
		row.KeyValue,
		row.KeyHash,
		group.UpstreamURL,
		querySecret,
		headerSecret,
		string(group.Config),
		other.UpstreamURL,
	} {
		if strings.Contains(logText, forbidden) ||
			strings.Contains(recorder.Body.String(), forbidden) {
			t.Fatalf(
				"response/log exposes %q: response=%s logs=%s",
				forbidden,
				recorder.Body.String(),
				logText,
			)
		}
	}
}

func TestGroupKeyRoutesAreRegistered(t *testing.T) {
	initControlI18n(t)
	const authKey = "group-key-route-auth"
	for _, test := range []struct {
		name   string
		method string
		suffix string
		body   string
	}{
		{name: "list", method: http.MethodGet, suffix: "/keys"},
		{
			name: "update", method: http.MethodPut,
			suffix: "/keys/{key_id}", body: `{"status":"disabled"}`,
		},
		{name: "delete", method: http.MethodDelete, suffix: "/keys/{key_id}"},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newServiceFixture(t)
			group := validControlGroup("key-route-" + test.name)
			if err := fixture.db.Create(group).Error; err != nil {
				t.Fatal(err)
			}
			row := seedManagedUpstreamKey(
				t, fixture, group.ID, "sk-route-"+test.name,
				models.UpstreamKeyStatusActive, nil,
			)
			engine := gin.New()
			NewServer(
				&config.Config{AuthKey: authKey},
				fixture.service,
			).RegisterRoutes(engine)
			path := "/api/groups/" +
				strconv.FormatUint(uint64(group.ID), 10) +
				strings.ReplaceAll(
					test.suffix,
					"{key_id}",
					strconv.FormatUint(uint64(row.ID), 10),
				)
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(
				test.method,
				path,
				strings.NewReader(test.body),
			)
			request.Header.Set("Authorization", "Bearer "+authKey)
			engine.ServeHTTP(recorder, request)
			if recorder.Code != http.StatusOK {
				t.Fatalf(
					"%s %s status = %d, body=%s",
					test.method,
					path,
					recorder.Code,
					recorder.Body.String(),
				)
			}
		})
	}
}

const groupKeyHTTPAuth = "group-key-http-auth"

func serveGroupKeyHTTPRequest(
	t *testing.T,
	engine *gin.Engine,
	method string,
	path string,
	body string,
	auth string,
	language string,
) *httptest.ResponseRecorder {
	t.Helper()
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	if auth != "" {
		request.Header.Set("Authorization", "Bearer "+auth)
	}
	if language != "" {
		request.Header.Set("Accept-Language", language)
	}
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	engine.ServeHTTP(recorder, request)
	return recorder
}

func assertGroupKeyHTTPDoesNotExpose(
	t *testing.T,
	response string,
	forbidden ...string,
) {
	t.Helper()
	for _, value := range forbidden {
		if value != "" && strings.Contains(response, value) {
			t.Fatalf("HTTP response exposes %q: %s", value, response)
		}
	}
}

type groupKeyMutationState struct {
	row      models.UpstreamKey
	runtime  []state.KeyRuntimeView
	snapshot *state.ConfigSnapshot
}

func captureGroupKeyMutationState(
	t *testing.T,
	fixture serviceFixture,
	keyID uint,
) groupKeyMutationState {
	t.Helper()
	var row models.UpstreamKey
	if err := fixture.db.First(&row, keyID).Error; err != nil {
		t.Fatalf("load UpstreamKey %d: %v", keyID, err)
	}
	return groupKeyMutationState{
		row: row, runtime: fixture.registry.Snapshot(),
		snapshot: fixture.manager.Current(),
	}
}

func assertGroupKeyMutationStateUnchanged(
	t *testing.T,
	fixture serviceFixture,
	keyID uint,
	want groupKeyMutationState,
) {
	t.Helper()
	got := captureGroupKeyMutationState(t, fixture, keyID)
	if !reflect.DeepEqual(got.row, want.row) {
		t.Fatalf("persisted UpstreamKey changed:\ngot=%#v\nwant=%#v", got.row, want.row)
	}
	if !reflect.DeepEqual(got.runtime, want.runtime) {
		t.Fatalf("Registry runtime changed:\ngot=%#v\nwant=%#v", got.runtime, want.runtime)
	}
	if got.snapshot != want.snapshot {
		t.Fatalf(
			"Snapshot changed: got=%p/%d want=%p/%d",
			got.snapshot,
			got.snapshot.Revision,
			want.snapshot,
			want.snapshot.Revision,
		)
	}
}

func TestGroupKeyHTTPRoutesRequireAuthentication(t *testing.T) {
	initControlI18n(t)
	for _, route := range []struct {
		name   string
		method string
		suffix string
		body   string
	}{
		{name: "list", method: http.MethodGet, suffix: "/keys"},
		{
			name: "update", method: http.MethodPut,
			suffix: "/keys/{key_id}", body: `{"status":"disabled"}`,
		},
		{name: "delete", method: http.MethodDelete, suffix: "/keys/{key_id}"},
	} {
		for _, auth := range []struct {
			name  string
			value string
		}{
			{name: "missing"},
			{name: "wrong", value: groupKeyHTTPAuth + "-wrong"},
		} {
			t.Run(route.name+"/"+auth.name, func(t *testing.T) {
				fixture := newServiceFixture(t)
				group := validControlGroup("key-auth-" + route.name + "-" + auth.name)
				if err := fixture.db.Create(group).Error; err != nil {
					t.Fatal(err)
				}
				row := seedManagedUpstreamKey(
					t, fixture, group.ID, "sk-auth-"+route.name+"-"+auth.name,
					models.UpstreamKeyStatusActive, nil,
				)
				before := captureGroupKeyMutationState(t, fixture, row.ID)
				engine := gin.New()
				NewServer(
					&config.Config{AuthKey: groupKeyHTTPAuth},
					fixture.service,
				).RegisterRoutes(engine)
				path := "/api/groups/" +
					strconv.FormatUint(uint64(group.ID), 10) +
					strings.ReplaceAll(
						route.suffix,
						"{key_id}",
						strconv.FormatUint(uint64(row.ID), 10),
					)
				recorder := serveGroupKeyHTTPRequest(
					t,
					engine,
					route.method,
					path,
					route.body,
					auth.value,
					"en-US",
				)
				if recorder.Code != http.StatusUnauthorized ||
					!strings.Contains(recorder.Body.String(), `"code":"UNAUTHORIZED"`) {
					t.Fatalf(
						"%s %s response = %d %s, want 401",
						route.method,
						path,
						recorder.Code,
						recorder.Body.String(),
					)
				}
				assertGroupKeyMutationStateUnchanged(t, fixture, row.ID, before)
			})
		}
	}
}

func TestGroupKeyHTTPRejectsInvalidPathIDsWithoutMutation(t *testing.T) {
	initControlI18n(t)
	fixture := newServiceFixture(t)
	group := validControlGroup("key-invalid-path")
	if err := fixture.db.Create(group).Error; err != nil {
		t.Fatal(err)
	}
	row := seedManagedUpstreamKey(
		t, fixture, group.ID, "sk-invalid-path",
		models.UpstreamKeyStatusActive, nil,
	)
	engine := gin.New()
	NewServer(
		&config.Config{AuthKey: groupKeyHTTPAuth},
		fixture.service,
	).RegisterRoutes(engine)
	groupText := strconv.FormatUint(uint64(group.ID), 10)
	keyText := strconv.FormatUint(uint64(row.ID), 10)
	const overflow = "18446744073709551616"

	for _, test := range []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{name: "list zero Group", method: http.MethodGet, path: "/api/groups/0/keys"},
		{name: "list invalid Group", method: http.MethodGet, path: "/api/groups/not-a-number/keys"},
		{name: "list overflow Group", method: http.MethodGet, path: "/api/groups/" + overflow + "/keys"},
		{
			name: "update zero Group", method: http.MethodPut,
			path: "/api/groups/0/keys/" + keyText, body: `{"status":"disabled"}`,
		},
		{
			name: "update invalid Key", method: http.MethodPut,
			path: "/api/groups/" + groupText + "/keys/not-a-number",
			body: `{"status":"disabled"}`,
		},
		{
			name: "update overflow Key", method: http.MethodPut,
			path: "/api/groups/" + groupText + "/keys/" + overflow,
			body: `{"status":"disabled"}`,
		},
		{
			name: "delete zero Key", method: http.MethodDelete,
			path: "/api/groups/" + groupText + "/keys/0",
		},
		{
			name: "delete overflow Group", method: http.MethodDelete,
			path: "/api/groups/" + overflow + "/keys/" + keyText,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			before := captureGroupKeyMutationState(t, fixture, row.ID)
			recorder := serveGroupKeyHTTPRequest(
				t,
				engine,
				test.method,
				test.path,
				test.body,
				groupKeyHTTPAuth,
				"en-US",
			)
			if recorder.Code != http.StatusBadRequest ||
				!strings.Contains(recorder.Body.String(), `"code":"BAD_REQUEST"`) {
				t.Fatalf(
					"%s %s response = %d %s, want 400",
					test.method,
					test.path,
					recorder.Code,
					recorder.Body.String(),
				)
			}
			assertGroupKeyMutationStateUnchanged(t, fixture, row.ID, before)
		})
	}
}

func TestUpdateGroupKeyHTTPStrictJSONValidation(t *testing.T) {
	initControlI18n(t)
	fixture := newServiceFixture(t)
	group := validControlGroup("key-update-http-validation")
	if err := fixture.db.Create(group).Error; err != nil {
		t.Fatal(err)
	}
	weight := 33
	row := seedManagedUpstreamKey(
		t, fixture, group.ID, "sk-update-http-validation",
		models.UpstreamKeyStatusActive, &weight,
	)
	engine := gin.New()
	NewServer(
		&config.Config{AuthKey: groupKeyHTTPAuth},
		fixture.service,
	).RegisterRoutes(engine)
	path := fmt.Sprintf("/api/groups/%d/keys/%d", group.ID, row.ID)

	for _, test := range []struct {
		name string
		body string
		code string
	}{
		{name: "empty body", code: app_errors.ErrInvalidJSON.Code},
		{name: "top-level null", body: `null`, code: app_errors.ErrInvalidJSON.Code},
		{name: "top-level array", body: `[]`, code: app_errors.ErrInvalidJSON.Code},
		{name: "unknown field", body: `{"unknown":true}`, code: app_errors.ErrInvalidJSON.Code},
		{
			name: "multiple JSON values",
			body: `{"status":"disabled"} {"status":"active"}`,
			code: app_errors.ErrInvalidJSON.Code,
		},
		{name: "empty object", body: `{}`, code: app_errors.ErrBadRequest.Code},
		{name: "null status", body: `{"status":null}`, code: app_errors.ErrValidation.Code},
		{
			name: "invalid status", body: `{"status":"cooldown"}`,
			code: app_errors.ErrValidation.Code,
		},
		{
			name: "negative weight", body: `{"weight_manual":-1}`,
			code: app_errors.ErrValidation.Code,
		},
		{
			name: "large weight", body: `{"weight_manual":101}`,
			code: app_errors.ErrValidation.Code,
		},
		{
			name: "fraction weight", body: `{"weight_manual":1.5}`,
			code: app_errors.ErrInvalidJSON.Code,
		},
		{
			name: "string weight", body: `{"weight_manual":"1"}`,
			code: app_errors.ErrInvalidJSON.Code,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			before := captureGroupKeyMutationState(t, fixture, row.ID)
			recorder := serveGroupKeyHTTPRequest(
				t,
				engine,
				http.MethodPut,
				path,
				test.body,
				groupKeyHTTPAuth,
				"en-US",
			)
			if recorder.Code != http.StatusBadRequest ||
				!strings.Contains(
					recorder.Body.String(),
					`"code":"`+test.code+`"`,
				) {
				t.Fatalf(
					"PUT validation response = %d %s, want %s",
					recorder.Code,
					recorder.Body.String(),
					test.code,
				)
			}
			assertGroupKeyMutationStateUnchanged(t, fixture, row.ID, before)
			assertGroupKeyHTTPDoesNotExpose(
				t,
				recorder.Body.String(),
				"sk-update-http-validation",
				row.KeyValue,
				row.KeyHash,
				group.UpstreamURL,
				string(group.Config),
			)
		})
	}
}

func TestGroupKeyHTTPReturnsExactResourceNotFoundMessages(t *testing.T) {
	initControlI18n(t)
	for _, operation := range []struct {
		name   string
		method string
		body   string
	}{
		{name: "update", method: http.MethodPut, body: `{"status":"disabled"}`},
		{name: "delete", method: http.MethodDelete},
	} {
		for _, resource := range []struct {
			name      string
			groupMode string
			keyMode   string
			message   string
		}{
			{name: "missing Group", groupMode: "missing", message: "Group not found"},
			{name: "missing Key", keyMode: "missing", message: "Key not found"},
			{name: "wrong owner", keyMode: "wrong-owner", message: "Key not found"},
		} {
			t.Run(operation.name+"/"+resource.name, func(t *testing.T) {
				fixture := newServiceFixture(t)
				group := validControlGroup(
					"key-http-not-found-" + operation.name + "-" +
						strings.ReplaceAll(resource.name, " ", "-"),
				)
				if err := fixture.db.Create(group).Error; err != nil {
					t.Fatal(err)
				}
				const plaintext = "sk-key-http-not-found"
				row := seedManagedUpstreamKey(
					t, fixture, group.ID, plaintext,
					models.UpstreamKeyStatusActive, nil,
				)
				groupID, keyID := group.ID, row.ID
				var wrongOwner models.UpstreamKey
				if resource.groupMode == "missing" {
					groupID = 999999
				}
				if resource.keyMode == "missing" {
					keyID = 999999
				}
				if resource.keyMode == "wrong-owner" {
					other := validControlGroup("key-http-not-found-other")
					if err := fixture.db.Create(other).Error; err != nil {
						t.Fatal(err)
					}
					wrongOwner = seedManagedUpstreamKey(
						t, fixture, other.ID, "sk-wrong-owner-http",
						models.UpstreamKeyStatusActive, nil,
					)
					keyID = wrongOwner.ID
				}
				before := captureGroupKeyMutationState(t, fixture, row.ID)
				engine := gin.New()
				NewServer(
					&config.Config{AuthKey: groupKeyHTTPAuth},
					fixture.service,
				).RegisterRoutes(engine)
				recorder := serveGroupKeyHTTPRequest(
					t,
					engine,
					operation.method,
					fmt.Sprintf("/api/groups/%d/keys/%d", groupID, keyID),
					operation.body,
					groupKeyHTTPAuth,
					"en-US",
				)
				if recorder.Code != http.StatusNotFound {
					t.Fatalf(
						"not-found response = %d %s",
						recorder.Code,
						recorder.Body.String(),
					)
				}
				var envelope struct {
					Code    string          `json:"code"`
					Message string          `json:"message"`
					Data    json.RawMessage `json:"data"`
				}
				if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
					t.Fatal(err)
				}
				if envelope.Code != app_errors.ErrResourceNotFound.Code ||
					envelope.Message != resource.message ||
					len(envelope.Data) != 0 {
					t.Fatalf("not-found envelope = %#v", envelope)
				}
				assertGroupKeyMutationStateUnchanged(t, fixture, row.ID, before)
				assertGroupKeyHTTPDoesNotExpose(
					t,
					recorder.Body.String(),
					plaintext,
					row.KeyValue,
					row.KeyHash,
					group.UpstreamURL,
					string(group.Config),
					wrongOwner.KeyValue,
					wrongOwner.KeyHash,
				)
			})
		}
	}
}

func TestDeleteGroupKeyHTTPSuccessAllowsLastKey(t *testing.T) {
	initControlI18n(t)
	fixture := newServiceFixture(t)
	group := validControlGroup("key-delete-http")
	if err := fixture.db.Create(group).Error; err != nil {
		t.Fatal(err)
	}
	const plaintext = "sk-delete-http-last"
	row := seedManagedUpstreamKey(
		t, fixture, group.ID, plaintext,
		models.UpstreamKeyStatusActive, nil,
	)
	beforeSnapshot := fixture.manager.Current()
	engine := gin.New()
	NewServer(
		&config.Config{AuthKey: groupKeyHTTPAuth},
		fixture.service,
	).RegisterRoutes(engine)
	recorder := serveGroupKeyHTTPRequest(
		t,
		engine,
		http.MethodDelete,
		fmt.Sprintf("/api/groups/%d/keys/%d", group.ID, row.ID),
		"",
		groupKeyHTTPAuth,
		"en-US",
	)
	if recorder.Code != http.StatusOK {
		t.Fatalf("DELETE response = %d %s", recorder.Code, recorder.Body.String())
	}
	var envelope struct {
		Code int             `json:"code"`
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Code != 0 || len(envelope.Data) != 0 {
		t.Fatalf("DELETE envelope = %#v, want code 0 and omitted data", envelope)
	}
	if fixture.manager.Current() != beforeSnapshot {
		t.Fatal("HTTP delete published Snapshot")
	}
	if _, exists := fixture.registry.EncryptedValue(row.ID); exists {
		t.Fatal("deleted Key remains in Registry")
	}
	var groupCount, keyCount int64
	if err := fixture.db.Model(&models.Group{}).
		Where("id = ?", group.ID).Count(&groupCount).Error; err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.Model(&models.UpstreamKey{}).
		Where("id = ?", row.ID).Count(&keyCount).Error; err != nil {
		t.Fatal(err)
	}
	if groupCount != 1 || keyCount != 0 {
		t.Fatalf("row counts = Group:%d Key:%d", groupCount, keyCount)
	}
	assertGroupKeyHTTPDoesNotExpose(
		t,
		recorder.Body.String(),
		plaintext,
		row.KeyValue,
		row.KeyHash,
		group.UpstreamURL,
		string(group.Config),
	)
}

func TestCaptureGroupKeysSeparatesLockedCaptureFromValidation(t *testing.T) {
	fixture := newServiceFixture(t)
	group := validControlGroup("key-short-capture")
	if err := fixture.db.Create(group).Error; err != nil {
		t.Fatal(err)
	}
	row := seedManagedUpstreamKey(
		t, fixture, group.ID, "sk-short-capture",
		models.UpstreamKeyStatusActive, nil,
	)

	capture, err := fixture.service.captureGroupKeys(t.Context(), group.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(capture.rows) != 1 || capture.rows[0].ID != row.ID ||
		len(capture.views) != 1 || capture.views[0].ID != row.ID {
		t.Fatalf("raw capture = %#v", capture)
	}

	fixture.service.writeMu.Lock()
	validationDone := make(chan error, 1)
	go func() {
		_, err := validateGroupKeysCapture(capture)
		validationDone <- err
	}()
	select {
	case err := <-validationDone:
		fixture.service.writeMu.Unlock()
		if err != nil {
			t.Fatalf("validateGroupKeysCapture() error = %v", err)
		}
	case <-time.After(2 * time.Second):
		fixture.service.writeMu.Unlock()
		t.Fatal("pair validation waited for writeMu")
	}
}

type blockingDecryptService struct {
	encryption.Service
	started chan<- struct{}
	release <-chan struct{}
}

func (service blockingDecryptService) Decrypt(ciphertext string) (string, error) {
	close(service.started)
	<-service.release
	return service.Service.Decrypt(ciphertext)
}

func TestUpdateGroupKeyChangesConfigAndPreservesAutomaticRuntime(t *testing.T) {
	fixture := newServiceFixture(t)
	now := time.Date(2026, time.July, 24, 12, 0, 0, 0, time.UTC)
	fixture.service.now = func() time.Time { return now }
	group := validControlGroup("key-update")
	if err := fixture.db.Create(group).Error; err != nil {
		t.Fatal(err)
	}
	row := seedManagedUpstreamKey(
		t, fixture, group.ID, "sk-update-secret",
		models.UpstreamKeyStatusActive, nil,
	)
	if !fixture.registry.SetAutoWeight(row.ID, 41) ||
		!fixture.registry.SetCooldown(row.ID, now.Add(time.Minute)) ||
		!fixture.registry.SetBlacklisted(row.ID) {
		t.Fatal("seed runtime")
	}
	for range 3 {
		if _, ok := fixture.registry.IncrFailure(row.ID); !ok {
			t.Fatal("seed failure")
		}
	}
	beforeSnapshot := fixture.manager.Current()
	weight := 25
	got, err := fixture.service.UpdateGroupKey(
		t.Context(),
		group.ID,
		row.ID,
		UpstreamKeyUpdateRequest{
			Status: optionalField[state.KeyStatus]{
				Set: true, Value: state.KeyStatusDisabled,
			},
			WeightManual: optionalField[int]{Set: true, Value: weight},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if got.ConfiguredStatus != string(state.KeyStatusDisabled) ||
		got.EffectiveStatus != string(healthBucketDisabled) ||
		got.Weight != nil || got.Recovery.Mode != "manual" {
		t.Fatalf("updated response = %#v", got)
	}
	view, exists := findRuntimeKey(fixture.registry.Snapshot(), row.ID)
	if !exists || view.WeightManual == nil || *view.WeightManual != weight ||
		view.WeightAuto != 41 || !view.Blacklisted || view.FailureCount != 3 {
		t.Fatalf("preserved runtime = %#v, exists=%t", view, exists)
	}
	if fixture.manager.Current() != beforeSnapshot {
		t.Fatal("UpdateGroupKey published Snapshot")
	}

	got, err = fixture.service.UpdateGroupKey(
		t.Context(),
		group.ID,
		row.ID,
		UpstreamKeyUpdateRequest{
			Status: optionalField[state.KeyStatus]{
				Set: true, Value: state.KeyStatusActive,
			},
			WeightManual: optionalField[int]{Set: true, Null: true},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if got.ConfiguredStatus != string(state.KeyStatusActive) ||
		got.EffectiveStatus != string(healthBucketBlacklisted) ||
		got.Weight != nil || got.Recovery.Mode != "probe" {
		t.Fatalf("re-enabled response = %#v", got)
	}
	view, exists = findRuntimeKey(fixture.registry.Snapshot(), row.ID)
	if !exists || view.WeightManual != nil || view.WeightAuto != 41 ||
		!view.Blacklisted || view.FailureCount != 3 {
		t.Fatalf("re-enabled runtime = %#v, exists=%t", view, exists)
	}
	var persisted models.UpstreamKey
	if err := fixture.db.First(&persisted, row.ID).Error; err != nil {
		t.Fatal(err)
	}
	if persisted.Status != models.UpstreamKeyStatusActive || persisted.WeightManual != nil {
		t.Fatalf("persisted key = %#v", persisted)
	}
}

func TestUpdateGroupKeyRepairsMissingRegistryEntryFromCommittedRow(t *testing.T) {
	fixture := newServiceFixture(t)
	group := validControlGroup("key-repair")
	if err := fixture.db.Create(group).Error; err != nil {
		t.Fatal(err)
	}
	weight := 25
	row := seedManagedUpstreamKey(
		t, fixture, group.ID, "sk-repair",
		models.UpstreamKeyStatusActive, &weight,
	)
	fixture.registry.RemoveKey(row.ID)
	beforeSnapshot := fixture.manager.Current()

	got, err := fixture.service.UpdateGroupKey(
		t.Context(),
		group.ID,
		row.ID,
		UpstreamKeyUpdateRequest{
			Status: optionalField[state.KeyStatus]{
				Set: true, Value: state.KeyStatusDisabled,
			},
		},
	)
	if err != nil {
		t.Fatalf("UpdateGroupKey() error = %v", err)
	}
	if got.ConfiguredStatus != string(state.KeyStatusDisabled) ||
		got.EffectiveStatus != string(healthBucketDisabled) ||
		got.Weight != nil || got.Recovery.Mode != "manual" {
		t.Fatalf("repaired response = %#v", got)
	}
	view, exists := findRuntimeKey(fixture.registry.Snapshot(), row.ID)
	if !exists || view.WeightManual == nil || *view.WeightManual != 25 ||
		view.WeightAuto != state.DefaultWeight {
		t.Fatalf("repaired runtime = %#v, exists=%t", view, exists)
	}
	value, exists := fixture.registry.EncryptedValue(row.ID)
	if !exists || value != row.KeyValue {
		t.Fatalf("repaired credential = %q, %t", value, exists)
	}
	if fixture.manager.Current() != beforeSnapshot {
		t.Fatal("missing Registry repair published Snapshot")
	}
}

func TestUpdateGroupKeyConvergesStatusAndManualWeightMismatches(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(t *testing.T, fixture serviceFixture, row models.UpstreamKey)
	}{
		{
			name: "status",
			mutate: func(
				t *testing.T,
				fixture serviceFixture,
				row models.UpstreamKey,
			) {
				if err := fixture.registry.UpdateKeyConfig(
					row.ID,
					state.KeyStatusDisabled,
					row.WeightManual,
				); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "manual weight",
			mutate: func(
				t *testing.T,
				fixture serviceFixture,
				row models.UpstreamKey,
			) {
				wrongWeight := 77
				if err := fixture.registry.UpdateKeyConfig(
					row.ID,
					state.KeyStatusActive,
					&wrongWeight,
				); err != nil {
					t.Fatal(err)
				}
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newServiceFixture(t)
			group := validControlGroup("key-converge-" + test.name)
			if err := fixture.db.Create(group).Error; err != nil {
				t.Fatal(err)
			}
			persistedWeight := 25
			row := seedManagedUpstreamKey(
				t, fixture, group.ID, "sk-converge-"+test.name,
				models.UpstreamKeyStatusActive, &persistedWeight,
			)
			if !fixture.registry.SetAutoWeight(row.ID, 41) ||
				!fixture.registry.SetBlacklisted(row.ID) {
				t.Fatal("seed automatic runtime")
			}
			if _, ok := fixture.registry.IncrFailure(row.ID); !ok {
				t.Fatal("seed failure count")
			}
			test.mutate(t, fixture, row)
			beforeSnapshot := fixture.manager.Current()

			got, err := fixture.service.UpdateGroupKey(
				t.Context(),
				group.ID,
				row.ID,
				UpstreamKeyUpdateRequest{
					Status: optionalField[state.KeyStatus]{
						Set: true, Value: state.KeyStatusActive,
					},
				},
			)
			if err != nil {
				t.Fatalf("UpdateGroupKey() error = %v", err)
			}
			if got.ConfiguredStatus != string(state.KeyStatusActive) ||
				got.EffectiveStatus != string(healthBucketBlacklisted) ||
				got.Weight != nil || got.Recovery.Mode != "probe" {
				t.Fatalf("converged response = %#v", got)
			}
			view, exists := findRuntimeKey(
				fixture.registry.Snapshot(),
				row.ID,
			)
			if !exists ||
				view.Status != state.KeyStatusActive ||
				view.WeightManual == nil ||
				*view.WeightManual != persistedWeight ||
				view.WeightAuto != 41 ||
				!view.Blacklisted ||
				view.FailureCount != 1 {
				t.Fatalf("converged Registry view = %#v, exists=%t", view, exists)
			}
			if fixture.manager.Current() != beforeSnapshot {
				t.Fatal("mismatch convergence published Snapshot")
			}
		})
	}
}

func TestUpdateGroupKeyLeavesExtraRegistryEntryFailLoud(t *testing.T) {
	fixture := newServiceFixture(t)
	group := validControlGroup("key-update-extra")
	if err := fixture.db.Create(group).Error; err != nil {
		t.Fatal(err)
	}
	row := seedManagedUpstreamKey(
		t, fixture, group.ID, "sk-update-extra",
		models.UpstreamKeyStatusActive, nil,
	)
	extraID := row.ID + 1000
	if err := fixture.registry.ApplyImport(group.ID, []state.KeyEntry{{
		ID: extraID, GroupID: group.ID,
		Status: state.KeyStatusActive, EncryptedValue: "cipher-extra",
	}}); err != nil {
		t.Fatal(err)
	}
	beforeSnapshot := fixture.manager.Current()

	got, err := fixture.service.UpdateGroupKey(
		t.Context(),
		group.ID,
		row.ID,
		UpstreamKeyUpdateRequest{
			Status: optionalField[state.KeyStatus]{
				Set: true, Value: state.KeyStatusDisabled,
			},
		},
	)
	if got != (GroupKeyItemResponse{}) {
		t.Fatalf("UpdateGroupKey() response = %#v, want zero", got)
	}
	var operationErr *controlOperationError
	if !errors.As(err, &operationErr) ||
		operationErr.stage != stageValidateDBRegistryPair ||
		operationErr.mismatchKind != mismatchExtraRegistry ||
		operationErr.groupID != group.ID ||
		operationErr.keyID != extraID {
		t.Fatalf("UpdateGroupKey() error = %#v", err)
	}
	var committed models.UpstreamKey
	if err := fixture.db.First(&committed, row.ID).Error; err != nil {
		t.Fatal(err)
	}
	if committed.Status != models.UpstreamKeyStatusDisabled {
		t.Fatalf("committed status = %q, want disabled", committed.Status)
	}
	views := fixture.registry.Snapshot()
	updated, updatedExists := findRuntimeKey(views, row.ID)
	extra, extraExists := findRuntimeKey(views, extraID)
	if !updatedExists || updated.Status != state.KeyStatusDisabled ||
		!extraExists || extra.GroupID != group.ID {
		t.Fatalf(
			"Registry views = %#v, updated=%#v/%t extra=%#v/%t",
			views,
			updated,
			updatedExists,
			extra,
			extraExists,
		)
	}
	if fixture.manager.Current() != beforeSnapshot {
		t.Fatal("extra Registry mismatch published Snapshot")
	}
}

func TestDeleteGroupKeyAllowsLastKeyAndDoesNotPublish(t *testing.T) {
	fixture := newServiceFixture(t)
	group := validControlGroup("key-delete")
	if err := fixture.db.Create(group).Error; err != nil {
		t.Fatal(err)
	}
	row := seedManagedUpstreamKey(
		t, fixture, group.ID, "sk-last",
		models.UpstreamKeyStatusActive, nil,
	)
	beforeSnapshot := fixture.manager.Current()
	if err := fixture.service.DeleteGroupKey(t.Context(), group.ID, row.ID); err != nil {
		t.Fatal(err)
	}
	if fixture.manager.Current() != beforeSnapshot {
		t.Fatal("DeleteGroupKey published Snapshot")
	}
	if _, exists := fixture.registry.EncryptedValue(row.ID); exists {
		t.Fatal("deleted key remains in Registry")
	}
	var groupCount, keyCount int64
	if err := fixture.db.Model(&models.Group{}).
		Where("id = ?", group.ID).Count(&groupCount).Error; err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.Model(&models.UpstreamKey{}).
		Where("id = ?", row.ID).Count(&keyCount).Error; err != nil {
		t.Fatal(err)
	}
	if groupCount != 1 || keyCount != 0 {
		t.Fatalf("counts = group:%d key:%d", groupCount, keyCount)
	}
}

func TestUpdateGroupKeyValidatesThreeStateFieldsWithoutMutation(t *testing.T) {
	fixture := newServiceFixture(t)
	group := validControlGroup("key-update-validation")
	if err := fixture.db.Create(group).Error; err != nil {
		t.Fatal(err)
	}
	weight := 33
	row := seedManagedUpstreamKey(
		t, fixture, group.ID, "sk-update-validation",
		models.UpstreamKeyStatusActive, &weight,
	)
	for _, test := range []struct {
		name    string
		request UpstreamKeyUpdateRequest
		want    *app_errors.APIError
	}{
		{name: "empty", request: UpstreamKeyUpdateRequest{}, want: app_errors.ErrBadRequest},
		{
			name: "null status",
			request: UpstreamKeyUpdateRequest{
				Status: optionalField[state.KeyStatus]{Set: true, Null: true},
			},
			want: app_errors.ErrValidation,
		},
		{
			name: "invalid status",
			request: UpstreamKeyUpdateRequest{
				Status: optionalField[state.KeyStatus]{
					Set: true, Value: state.KeyStatus("cooldown"),
				},
			},
			want: app_errors.ErrValidation,
		},
		{
			name: "zero weight",
			request: UpstreamKeyUpdateRequest{
				WeightManual: optionalField[int]{Set: true, Value: 0},
			},
			want: app_errors.ErrValidation,
		},
		{
			name: "negative weight",
			request: UpstreamKeyUpdateRequest{
				WeightManual: optionalField[int]{Set: true, Value: -1},
			},
			want: app_errors.ErrValidation,
		},
		{
			name: "large weight",
			request: UpstreamKeyUpdateRequest{
				WeightManual: optionalField[int]{Set: true, Value: 101},
			},
			want: app_errors.ErrValidation,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			var beforeDB models.UpstreamKey
			if err := fixture.db.First(&beforeDB, row.ID).Error; err != nil {
				t.Fatal(err)
			}
			beforeRegistry := fixture.registry.Snapshot()
			beforeSnapshot := fixture.manager.Current()
			_, err := fixture.service.UpdateGroupKey(
				t.Context(), group.ID, row.ID, test.request,
			)
			if !errors.Is(err, test.want) {
				t.Fatalf("UpdateGroupKey() error = %v, want %v", err, test.want)
			}
			var afterDB models.UpstreamKey
			if err := fixture.db.First(&afterDB, row.ID).Error; err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(afterDB, beforeDB) ||
				!reflect.DeepEqual(fixture.registry.Snapshot(), beforeRegistry) ||
				fixture.manager.Current() != beforeSnapshot {
				t.Fatal("invalid update mutated DB/Registry/Snapshot")
			}
		})
	}
}

func TestUpdateGroupKeyAcceptsManualWeightBoundariesAndNull(t *testing.T) {
	for _, test := range []struct {
		name    string
		field   optionalField[int]
		want    *int
		initial *int
	}{
		{name: "one", field: optionalField[int]{Set: true, Value: 1}, want: keyTestIntPointer(1)},
		{name: "max", field: optionalField[int]{Set: true, Value: 100}, want: keyTestIntPointer(100)},
		{
			name: "null", field: optionalField[int]{Set: true, Null: true},
			initial: keyTestIntPointer(55),
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newServiceFixture(t)
			group := validControlGroup("key-weight-" + test.name)
			if err := fixture.db.Create(group).Error; err != nil {
				t.Fatal(err)
			}
			row := seedManagedUpstreamKey(
				t, fixture, group.ID, "sk-weight-"+test.name,
				models.UpstreamKeyStatusActive, test.initial,
			)
			got, err := fixture.service.UpdateGroupKey(
				t.Context(),
				group.ID,
				row.ID,
				UpstreamKeyUpdateRequest{WeightManual: test.field},
			)
			if err != nil {
				t.Fatal(err)
			}
			if test.want == nil {
				if got.WeightMode != "auto" || got.Weight == nil || *got.Weight != state.DefaultWeight {
					t.Fatalf("automatic weight response = %#v", got)
				}
			} else if got.WeightMode != "manual" || got.Weight == nil || *got.Weight != *test.want {
				t.Fatalf("manual weight response = %#v, want %d", got, *test.want)
			}
		})
	}
}

func keyTestIntPointer(value int) *int {
	return &value
}

func TestUpdateAndDeleteGroupKeyReturnTypedResourceNotFound(t *testing.T) {
	for _, operation := range []string{"update", "delete"} {
		for _, test := range []struct {
			name         string
			groupMissing bool
			wrongOwner   bool
			wantResource string
		}{
			{name: "missing Group", groupMissing: true, wantResource: "group"},
			{name: "missing Key", wantResource: "key"},
			{name: "wrong owner", wrongOwner: true, wantResource: "key"},
		} {
			t.Run(operation+"/"+test.name, func(t *testing.T) {
				fixture := newServiceFixture(t)
				group := validControlGroup("key-resource-" + operation + "-" +
					strings.ReplaceAll(test.name, " ", "-"))
				if err := fixture.db.Create(group).Error; err != nil {
					t.Fatal(err)
				}
				groupID := group.ID
				keyID := uint(999)
				if test.groupMissing {
					groupID += 1000
				}
				if test.wrongOwner {
					other := validControlGroup("key-resource-other-" + operation)
					if err := fixture.db.Create(other).Error; err != nil {
						t.Fatal(err)
					}
					row := seedManagedUpstreamKey(
						t, fixture, other.ID, "sk-wrong-owner-"+operation,
						models.UpstreamKeyStatusActive, nil,
					)
					keyID = row.ID
				}
				var err error
				if operation == "update" {
					_, err = fixture.service.UpdateGroupKey(
						t.Context(),
						groupID,
						keyID,
						UpstreamKeyUpdateRequest{
							Status: optionalField[state.KeyStatus]{
								Set: true, Value: state.KeyStatusDisabled,
							},
						},
					)
				} else {
					err = fixture.service.DeleteGroupKey(t.Context(), groupID, keyID)
				}
				var resourceErr *controlResourceNotFoundError
				if !errors.As(err, &resourceErr) ||
					resourceErr.resource != test.wantResource ||
					!errors.Is(err, app_errors.ErrResourceNotFound) {
					t.Fatalf("error = %#v, want %s not found", err, test.wantResource)
				}
			})
		}
	}
}

func TestDeleteGroupKeySucceedsWhenRegistryEntryIsMissing(t *testing.T) {
	fixture := newServiceFixture(t)
	group := validControlGroup("key-delete-missing-runtime")
	if err := fixture.db.Create(group).Error; err != nil {
		t.Fatal(err)
	}
	row := seedManagedUpstreamKey(
		t, fixture, group.ID, "sk-delete-missing-runtime",
		models.UpstreamKeyStatusActive, nil,
	)
	fixture.registry.RemoveKey(row.ID)
	beforeSnapshot := fixture.manager.Current()

	if err := fixture.service.DeleteGroupKey(t.Context(), group.ID, row.ID); err != nil {
		t.Fatalf("DeleteGroupKey() error = %v", err)
	}
	var count int64
	if err := fixture.db.Model(&models.UpstreamKey{}).
		Where("id = ?", row.ID).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 || fixture.manager.Current() != beforeSnapshot {
		t.Fatalf("delete missing Registry state = count:%d snapshotChanged:%t",
			count, fixture.manager.Current() != beforeSnapshot)
	}
}

func TestUpdateAndDeleteGroupKeyNeverCallDiscovery(t *testing.T) {
	fixture := newServiceFixture(t)
	group := validControlGroup("key-no-discovery")
	if err := fixture.db.Create(group).Error; err != nil {
		t.Fatal(err)
	}
	row := seedManagedUpstreamKey(
		t, fixture, group.ID, "sk-no-discovery",
		models.UpstreamKeyStatusActive, nil,
	)
	fixture.service.dialects = dialect.NewSet(&recordingDiscoveryDialect{
		value: protocol.OpenAICompletions,
		listFn: func(context.Context, string, string, state.HeaderRules) ([]string, error) {
			t.Fatal("Key mutation must not call model discovery or upstream")
			return nil, nil
		},
	})
	if _, err := fixture.service.UpdateGroupKey(
		t.Context(),
		group.ID,
		row.ID,
		UpstreamKeyUpdateRequest{
			Status: optionalField[state.KeyStatus]{
				Set: true, Value: state.KeyStatusDisabled,
			},
		},
	); err != nil {
		t.Fatal(err)
	}
	if err := fixture.service.DeleteGroupKey(t.Context(), group.ID, row.ID); err != nil {
		t.Fatal(err)
	}
}

func TestUpdateAndDeleteGroupKeyChangeSchedulerEligibility(t *testing.T) {
	fixture := newServiceFixture(t)
	group := validControlGroup("key-candidates")
	if err := fixture.db.Create(group).Error; err != nil {
		t.Fatal(err)
	}
	disabled := seedManagedUpstreamKey(
		t, fixture, group.ID, "sk-candidate-disabled",
		models.UpstreamKeyStatusActive, nil,
	)
	remaining := seedManagedUpstreamKey(
		t, fixture, group.ID, "sk-candidate-remaining",
		models.UpstreamKeyStatusActive, nil,
	)
	deleted := seedManagedUpstreamKey(
		t, fixture, group.ID, "sk-candidate-deleted",
		models.UpstreamKeyStatusActive, nil,
	)
	if got := fixture.registry.CollectCandidates(
		[]uint{group.ID}, nil, time.Time{},
	); len(got) != 3 {
		t.Fatalf("initial candidates = %#v", got)
	}
	if _, err := fixture.service.UpdateGroupKey(
		t.Context(),
		group.ID,
		disabled.ID,
		UpstreamKeyUpdateRequest{
			Status: optionalField[state.KeyStatus]{
				Set: true, Value: state.KeyStatusDisabled,
			},
		},
	); err != nil {
		t.Fatal(err)
	}
	if err := fixture.service.DeleteGroupKey(t.Context(), group.ID, deleted.ID); err != nil {
		t.Fatal(err)
	}
	rawPool := fixture.registry.CollectCandidates(
		[]uint{group.ID}, nil, time.Time{},
	)
	if len(rawPool) != 1 || rawPool[0].ID != remaining.ID {
		t.Fatalf("raw candidates = %#v, want only remaining key %d", rawPool, remaining.ID)
	}
	snapshot, err := state.Compile(state.CompileInput{
		Groups: []state.GroupConfig{{
			ID: group.ID, Name: group.Name, UpstreamURL: group.UpstreamURL,
			Protocols: []protocol.Protocol{protocol.OpenAICompletions},
			Models:    []state.ModelConfig{{ID: "gpt-4o"}},
			Enabled:   true,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	iterator := scheduler.New(
		snapshot,
		fixture.registry,
		scheduler.Query{
			Protocol: protocol.OpenAICompletions, ExternalModel: stringPointer("gpt-4o"),
		},
		rand.New(rand.NewSource(1)),
	)
	if selection, err := iterator.Next(); err != nil || selection.KeyID != remaining.ID {
		t.Fatalf("Next() = (%#v, %v), want remaining key %d", selection, err, remaining.ID)
	}
}
