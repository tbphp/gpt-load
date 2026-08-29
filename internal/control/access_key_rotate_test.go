package control

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/state"
	"gpt-load/internal/storage/models"
)

func TestAccessKeyRotateOperationUsesExistingSnapshotStagePlan(t *testing.T) {
	t.Parallel()
	kind := operationKind("access_key_rotate")
	if !kind.valid() {
		t.Fatal("access_key_rotate operation kind is not registered")
	}
	got, err := operationRequiredStages(kind)
	if err != nil {
		t.Fatalf("operationRequiredStages(access_key_rotate) error = %v", err)
	}
	want := []operationStage{
		operationStageDBCommitted,
		operationStageSnapshotPublished,
		operationStageCompleted,
	}
	if !equalOperationStages(got, want) {
		t.Fatalf("rotation stages = %#v, want %#v", got, want)
	}
	if err := validateOperationResourceIdentity(kind, "access-key:7"); err != nil {
		t.Fatalf("rotation resource identity rejected: %v", err)
	}
}

func TestAccessKeyRotateHTTPReplacesCredentialAndReplaysStableResult(t *testing.T) {
	t.Parallel()
	initControlI18n(t)
	fixture := newServiceFixture(t)
	fixture.service.random = bytes.NewReader(bytes.Join([][]byte{
		bytes.Repeat([]byte{0x10}, 16),
		bytes.Repeat([]byte{0x20}, 16),
		bytes.Repeat([]byte{0x30}, 16),
	}, nil))
	fixture.service.operationRandom = bytes.NewReader(bytes.Join([][]byte{
		bytes.Repeat([]byte{0x71}, 16),
		bytes.Repeat([]byte{0x72}, 16),
	}, nil))
	created, err := fixture.service.CreateAccessKey(t.Context(), AccessKeyCreateRequest{
		Name: "rotate-stable",
		Filters: &AccessKeyFilters{
			Models: []string{"gpt-5.6"}, AllowedCIDRs: []string{"192.0.2.0/24"},
		},
		RPMLimit: OptionalRPMLimit{Set: true, Value: 12},
	})
	if err != nil {
		t.Fatalf("seed AccessKey: %v", err)
	}
	originalRow := loadAccessKeyRow(t, fixture.db, created.ID)
	engine := newAccessKeyLifecycleEngine(t, fixture)
	path := fmt.Sprintf("/api/access-keys/%d/rotate", created.ID)
	const operationA = "00000000-0000-4000-8000-000000007101"

	first := serveAccessKeyLifecycleRequest(t, engine, http.MethodPost, path, `{}`, operationA)
	if first.Code != http.StatusOK || first.Header().Get("Cache-Control") != "no-store" ||
		first.Header().Get("Pragma") != "no-cache" {
		t.Fatalf("first rotate = %d headers=%v body=%s", first.Code, first.Header(), first.Body.String())
	}
	firstData := decodeAccessKeyLifecycleData(t, first)
	var firstKey, firstMasked string
	if err := json.Unmarshal(firstData["key"], &firstKey); err != nil {
		t.Fatalf("decode first rotated key: %v", err)
	}
	if err := json.Unmarshal(firstData["masked_key"], &firstMasked); err != nil {
		t.Fatalf("decode first rotated mask: %v", err)
	}
	if firstKey == "" || firstKey == created.Key || firstMasked == created.MaskedKey {
		t.Fatalf("first rotation did not replace credential: key=%q mask=%q", firstKey, firstMasked)
	}
	assertJSONRawEqual(t, firstData["replayed"], "false")
	assertRotationPreservedMetadata(t, firstData, created)
	firstRow := loadAccessKeyRow(t, fixture.db, created.ID)
	if firstRow.KeyHash == originalRow.KeyHash || firstRow.KeyValue == originalRow.KeyValue ||
		firstRow.KeySuffix == originalRow.KeySuffix {
		t.Fatalf("rotation row unchanged: before=%#v after=%#v", originalRow, firstRow)
	}
	if _, exists := fixture.manager.Current().AccessKeysByHash[originalRow.KeyHash]; exists {
		t.Fatal("old hash remains in published Snapshot")
	}
	if _, exists := fixture.manager.Current().AccessKeysByHash[firstRow.KeyHash]; !exists {
		t.Fatal("new hash missing from published Snapshot")
	}

	replayedA := serveAccessKeyLifecycleRequest(t, engine, http.MethodPost, path, "", operationA)
	if replayedA.Code != http.StatusOK {
		t.Fatalf("replay A = %d %s", replayedA.Code, replayedA.Body.String())
	}
	replayedAData := decodeAccessKeyLifecycleData(t, replayedA)
	if replayedAData["key"] != nil {
		t.Fatalf("replay A returned secret: %s", replayedAData["key"])
	}
	assertJSONRawEqual(t, replayedAData["replayed"], "true")
	assertJSONRawEqual(t, replayedAData["masked_key"], string(firstData["masked_key"]))
	if current := loadAccessKeyRow(t, fixture.db, created.ID); current.KeyHash != firstRow.KeyHash {
		t.Fatal("replay A rotated the key again")
	}

	const operationB = "00000000-0000-4000-8000-000000007102"
	second := serveAccessKeyLifecycleRequest(t, engine, http.MethodPost, path, `{}`, operationB)
	if second.Code != http.StatusOK {
		t.Fatalf("second rotation = %d %s", second.Code, second.Body.String())
	}
	secondData := decodeAccessKeyLifecycleData(t, second)
	var secondKey string
	if err := json.Unmarshal(secondData["key"], &secondKey); err != nil || secondKey == "" || secondKey == firstKey {
		t.Fatalf("second rotated key = %q, err=%v", secondKey, err)
	}
	secondRow := loadAccessKeyRow(t, fixture.db, created.ID)
	if secondRow.KeyHash == firstRow.KeyHash {
		t.Fatal("second rotation preserved first rotated hash")
	}

	replayedA = serveAccessKeyLifecycleRequest(t, engine, http.MethodPost, path, `{}`, operationA)
	if replayedA.Code != http.StatusOK {
		t.Fatalf("replay A after B = %d %s", replayedA.Code, replayedA.Body.String())
	}
	replayedAData = decodeAccessKeyLifecycleData(t, replayedA)
	assertJSONRawEqual(t, replayedAData["masked_key"], string(firstData["masked_key"]))
	if replayedAData["key"] != nil {
		t.Fatalf("replay A after B returned secret: %s", replayedAData["key"])
	}
	revealed, err := fixture.service.RevealAccessKey(t.Context(), created.ID)
	if err != nil || revealed.Key != secondKey {
		t.Fatalf("Reveal after B = %#v, %v; want current second key", revealed, err)
	}
}

func TestAccessKeyRotateAllowsDisabledExpiredKeyWithoutChangingPolicy(t *testing.T) {
	t.Parallel()
	initControlI18n(t)
	fixture := newServiceFixture(t)
	fixture.service.random = bytes.NewReader(bytes.Join([][]byte{
		bytes.Repeat([]byte{0x40}, 16),
		bytes.Repeat([]byte{0x41}, 16),
	}, nil))
	operationNow := time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)
	fixture.service.now = func() time.Time { return operationNow }
	expiresAtMS := operationNow.Add(time.Hour).UnixMilli()
	disabled := state.AccessKeyStatusDisabled
	created, err := fixture.service.CreateAccessKey(t.Context(), AccessKeyCreateRequest{
		Name:        "disabled-expired",
		Status:      &disabled,
		ExpiresAtMS: &expiresAtMS,
		Filters: &AccessKeyFilters{
			Groups: []uint{}, Models: []string{"gpt-5.6"},
			AllowedCIDRs: []string{"198.51.100.7"},
		},
		RPMLimit: OptionalRPMLimit{Set: true, Value: 7},
	})
	if err != nil {
		t.Fatalf("seed disabled AccessKey: %v", err)
	}
	operationNow = operationNow.Add(2 * time.Hour)
	engine := newAccessKeyLifecycleEngine(t, fixture)
	recorder := serveAccessKeyLifecycleRequest(
		t,
		engine,
		http.MethodPost,
		fmt.Sprintf("/api/access-keys/%d/rotate", created.ID),
		`{}`,
		"00000000-0000-4000-8000-000000007110",
	)
	if recorder.Code != http.StatusOK {
		t.Fatalf("rotate disabled expired = %d %s", recorder.Code, recorder.Body.String())
	}
	data := decodeAccessKeyLifecycleData(t, recorder)
	assertRotationPreservedMetadata(t, data, created)
	assertJSONRawEqual(t, data["status"], `"disabled"`)
	assertJSONRawEqual(t, data["expires_at_ms"], strconv.FormatInt(expiresAtMS, 10))
}

func TestAccessKeyRotateReplayRecoversSnapshotWithoutSecondMutation(t *testing.T) {
	t.Parallel()
	initControlI18n(t)
	fixture := newServiceFixture(t)
	fixture.service.random = bytes.NewReader(bytes.Join([][]byte{
		bytes.Repeat([]byte{0x50}, 16),
		bytes.Repeat([]byte{0x51}, 16),
	}, nil))
	created, err := fixture.service.CreateAccessKey(t.Context(), AccessKeyCreateRequest{Name: "recover-rotation"})
	if err != nil {
		t.Fatal(err)
	}
	originalRow := loadAccessKeyRow(t, fixture.db, created.ID)
	failSnapshotPublication := true
	publishSnapshot := fixture.service.publishSnapshot
	fixture.service.publishSnapshot = func(input state.CompileInput) (*state.ConfigSnapshot, error) {
		if failSnapshotPublication {
			return nil, errors.New("injected snapshot stage failure")
		}
		return publishSnapshot(input)
	}
	engine := newAccessKeyLifecycleEngine(t, fixture)
	path := fmt.Sprintf("/api/access-keys/%d/rotate", created.ID)
	const key = "00000000-0000-4000-8000-000000007120"

	first := serveAccessKeyLifecycleRequest(t, engine, http.MethodPost, path, `{}`, key)
	if first.Code < http.StatusInternalServerError || !strings.Contains(first.Body.String(), app_errors.ErrControlOperationIncomplete.Code) {
		t.Fatalf("incomplete rotate = %d %s", first.Code, first.Body.String())
	}
	rotatedRow := loadAccessKeyRow(t, fixture.db, created.ID)
	if rotatedRow.KeyHash == originalRow.KeyHash {
		t.Fatal("rotation transaction did not commit before publication failure")
	}
	if _, exists := fixture.manager.Current().AccessKeysByHash[originalRow.KeyHash]; !exists {
		t.Fatal("failed publication unexpectedly replaced current Snapshot")
	}
	var operation models.ControlOperation
	if err := fixture.db.Where("idempotency_key = ?", key).Take(&operation).Error; err != nil {
		t.Fatal(err)
	}
	if operation.OperationKind != "access_key_rotate" ||
		operation.LastCompletedStage != string(operationStageDBCommitted) ||
		operation.FailedStage != string(operationStageSnapshotPublished) {
		t.Fatalf("incomplete rotation operation = %#v", operation)
	}

	failSnapshotPublication = false
	replayed := serveAccessKeyLifecycleRequest(t, engine, http.MethodPost, path, "", key)
	if replayed.Code != http.StatusOK {
		t.Fatalf("recovered replay = %d %s", replayed.Code, replayed.Body.String())
	}
	replayedData := decodeAccessKeyLifecycleData(t, replayed)
	assertJSONRawEqual(t, replayedData["replayed"], "true")
	if replayedData["key"] != nil {
		t.Fatalf("recovery replay returned secret: %s", replayedData["key"])
	}
	if current := loadAccessKeyRow(t, fixture.db, created.ID); current.KeyHash != rotatedRow.KeyHash {
		t.Fatal("recovery replay generated a second credential")
	}
	if _, exists := fixture.manager.Current().AccessKeysByHash[rotatedRow.KeyHash]; !exists {
		t.Fatal("recovery replay did not publish rotated hash")
	}
}

func TestAccessKeyRotateHTTPRejectsInvalidBodyIDAndIdempotencyKey(t *testing.T) {
	t.Parallel()
	initControlI18n(t)
	fixture := newServiceFixture(t)
	fixture.service.random = bytes.NewReader(bytes.Repeat([]byte{0x60}, 16))
	created, err := fixture.service.CreateAccessKey(t.Context(), AccessKeyCreateRequest{Name: "rotate-validation"})
	if err != nil {
		t.Fatal(err)
	}
	engine := newAccessKeyLifecycleEngine(t, fixture)
	validPath := fmt.Sprintf("/api/access-keys/%d/rotate", created.ID)

	for _, test := range []struct {
		name           string
		path           string
		body           string
		idempotencyKey string
		wantStatus     int
		wantCode       string
	}{
		{name: "missing idempotency key", path: validPath, body: `{}`, wantStatus: http.StatusPreconditionRequired, wantCode: app_errors.ErrIdempotencyKeyRequired.Code},
		{name: "invalid idempotency key", path: validPath, body: `{}`, idempotencyKey: "not-a-uuid", wantStatus: http.StatusBadRequest, wantCode: app_errors.ErrInvalidIdempotencyKey.Code},
		{name: "nonempty object", path: validPath, body: `{"unexpected":true}`, idempotencyKey: "00000000-0000-4000-8000-000000007131", wantStatus: http.StatusBadRequest, wantCode: app_errors.ErrInvalidJSON.Code},
		{name: "null body", path: validPath, body: `null`, idempotencyKey: "00000000-0000-4000-8000-000000007132", wantStatus: http.StatusBadRequest, wantCode: app_errors.ErrInvalidJSON.Code},
		{name: "array body", path: validPath, body: `[]`, idempotencyKey: "00000000-0000-4000-8000-000000007133", wantStatus: http.StatusBadRequest, wantCode: app_errors.ErrInvalidJSON.Code},
		{name: "zero id", path: "/api/access-keys/0/rotate", body: `{}`, idempotencyKey: "00000000-0000-4000-8000-000000007134", wantStatus: http.StatusBadRequest, wantCode: app_errors.ErrBadRequest.Code},
	} {
		t.Run(test.name, func(t *testing.T) {
			recorder := serveAccessKeyLifecycleRequest(
				t,
				engine,
				http.MethodPost,
				test.path,
				test.body,
				test.idempotencyKey,
			)
			if recorder.Code != test.wantStatus || !strings.Contains(recorder.Body.String(), test.wantCode) {
				t.Fatalf("invalid rotate = %d %s, want %s", recorder.Code, recorder.Body.String(), test.wantCode)
			}
		})
	}
}

func TestAccessKeyRotateMutationAuditUsesExistingAccessKeyLocator(t *testing.T) {
	t.Parallel()
	fixture := newServiceFixture(t)
	fixture.service.random = bytes.NewReader(bytes.Join([][]byte{
		bytes.Repeat([]byte{0x70}, 16),
		bytes.Repeat([]byte{0x71}, 16),
	}, nil))
	created := seedAuditAccessKey(t, fixture)
	fixture.service.random = bytes.NewReader(bytes.Repeat([]byte{0x71}, 16))
	var logs bytes.Buffer
	_, engine := newMutationAuditRouteServer(t, fixture, &logs)
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, newMutationAuditHTTPRequest(mutationAuditRequest{
		method:         http.MethodPost,
		path:           fmt.Sprintf("/api/access-keys/%d/rotate", created.ID),
		body:           `{}`,
		idempotencyKey: "00000000-0000-4000-8000-000000007140",
	}))
	if recorder.Code != http.StatusOK {
		t.Fatalf("audited rotate = %d %s", recorder.Code, recorder.Body.String())
	}
	assertMutationEvent(
		t,
		oneMutationAuditEvent(t, logs.Bytes()),
		"access_key_rotate",
		"access_key",
		fmt.Sprintf("access-key:%d", created.ID),
		"192.0.2.1",
		"succeeded",
		http.StatusOK,
		"",
		"info",
	)
}

func assertRotationPreservedMetadata(
	t *testing.T,
	data map[string]json.RawMessage,
	want AccessKeyCreateResult,
) {
	t.Helper()
	assertJSONRawEqual(t, data["id"], strconv.FormatUint(uint64(want.ID), 10))
	assertJSONRawEqual(t, data["name"], strconv.Quote(want.Name))
	assertJSONRawEqual(t, data["status"], strconv.Quote(string(want.Status)))
	assertJSONRawEqual(t, data["rpm_limit"], strconv.FormatInt(want.RPMLimit, 10))
	if string(data["filters"]) != mustCanonicalJSON(t, want.Filters) {
		t.Fatalf("rotated filters = %s, want %#v", data["filters"], want.Filters)
	}
	if string(data["expires_at_ms"]) != mustCanonicalJSON(t, want.ExpiresAtMS) {
		t.Fatalf("rotated expiry = %s, want %#v", data["expires_at_ms"], want.ExpiresAtMS)
	}
}

func mustCanonicalJSON(t *testing.T, value any) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}
