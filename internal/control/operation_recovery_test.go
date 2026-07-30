package control

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"testing"
	"time"

	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/protocol"
	"gpt-load/internal/state"
	"gpt-load/internal/storage/models"
)

func TestOperationReplayRepairsPostCommitRegistryFailureWithoutRerunningMutation(t *testing.T) {
	fixture := newServiceFixture(t)
	fixture.service.operationRandom = bytes.NewReader(bytes.Repeat([]byte{0x31}, 16))
	reconcileCalls := 0
	fixture.service.reconcileRegistryGroup = func(uint, []state.KeyEntry) (bool, error) {
		reconcileCalls++
		return false, errors.New("injected registry failure")
	}
	mutations := 0
	input := newDurableGroupOperationInput(
		t,
		fixture,
		"018f47a2-9c35-4d6e-8b1a-1234567890ab",
		&mutations,
	)

	_, err := fixture.service.executeIdempotentOperation(t.Context(), input)
	assertAPIErrorCode(t, err, app_errors.ErrControlOperationIncomplete.Code)
	if mutations != 1 || reconcileCalls != 1 {
		t.Fatalf("mutation/reconcile calls = %d/%d, want 1/1", mutations, reconcileCalls)
	}

	var operation models.ControlOperation
	if err := fixture.db.First(&operation).Error; err != nil {
		t.Fatalf("read operation: %v", err)
	}
	if operation.LastCompletedStage != string(operationStageDBCommitted) ||
		operation.FailedStage != string(operationStageRegistryApplied) {
		t.Fatalf("failed operation = %#v", operation)
	}
	var groupCount int64
	if err := fixture.db.Model(&models.Group{}).Count(&groupCount).Error; err != nil {
		t.Fatalf("count groups: %v", err)
	}
	if groupCount != 1 {
		t.Fatalf("committed group count = %d, want 1", groupCount)
	}

	fixture.service.reconcileRegistryGroup = fixture.registry.ReconcileGroup
	replayed, err := fixture.service.executeIdempotentOperation(t.Context(), input)
	if err != nil {
		t.Fatalf("replay executeIdempotentOperation() error = %v", err)
	}
	if !replayed.Replayed || mutations != 1 {
		t.Fatalf("replay/mutation = %t/%d, want true/1", replayed.Replayed, mutations)
	}
	groupID := mustResourceGroupID(t, replayed.ResourceIdentity)
	if value, ok := fixture.registry.EncryptedValue(1); !ok || value != "cipher-one" {
		t.Fatalf("recovered registry key = %q, %t", value, ok)
	}
	if current := fixture.manager.Current(); current == nil || current.Groups[groupID].ID != groupID {
		t.Fatalf("recovered snapshot = %#v", current)
	}
}

func TestOperationReplayOnlyRecordsStageWhenRegistrySideEffectAlreadySatisfied(t *testing.T) {
	fixture := newServiceFixture(t)
	fixture.service.operationRandom = bytes.NewReader(bytes.Repeat([]byte{0x32}, 16))
	reconcileCalls := 0
	fixture.service.reconcileRegistryGroup = func(
		groupID uint,
		entries []state.KeyEntry,
	) (bool, error) {
		reconcileCalls++
		return fixture.registry.ReconcileGroup(groupID, entries)
	}
	failedStageWrite := false
	fixture.service.beforeAdvanceOperationStage = func(
		_ context.Context,
		_ *models.ControlOperation,
		stage operationStage,
	) error {
		if stage == operationStageRegistryApplied && !failedStageWrite {
			failedStageWrite = true
			return errors.New("injected stage write failure")
		}
		return nil
	}
	mutations := 0
	input := newDurableGroupOperationInput(
		t,
		fixture,
		"028f47a2-9c35-4d6e-8b1a-1234567890ab",
		&mutations,
	)

	_, err := fixture.service.executeIdempotentOperation(t.Context(), input)
	assertAPIErrorCode(t, err, app_errors.ErrControlOperationIncomplete.Code)
	if reconcileCalls != 1 {
		t.Fatalf("initial reconcile calls = %d, want 1", reconcileCalls)
	}
	if value, ok := fixture.registry.EncryptedValue(1); !ok || value != "cipher-one" {
		t.Fatalf("side effect did not complete before stage failure: %q, %t", value, ok)
	}

	fixture.service.beforeAdvanceOperationStage = nil
	replayed, err := fixture.service.executeIdempotentOperation(t.Context(), input)
	if err != nil {
		t.Fatalf("replay executeIdempotentOperation() error = %v", err)
	}
	if !replayed.Replayed || mutations != 1 {
		t.Fatalf("replay/mutation = %t/%d, want true/1", replayed.Replayed, mutations)
	}
	if reconcileCalls != 1 {
		t.Fatalf("replay reapplied satisfied registry side effect, calls = %d", reconcileCalls)
	}
}

func TestOperationBarrierBlocksNewMutationBehindOlderRecovery(t *testing.T) {
	fixture := newServiceFixture(t)
	fixture.service.operationRandom = bytes.NewReader(bytes.Repeat([]byte{0x33}, 32))
	fixture.service.reconcileRegistryGroup = func(uint, []state.KeyEntry) (bool, error) {
		return false, errors.New("registry remains unavailable")
	}
	firstMutations := 0
	first := newDurableGroupOperationInput(
		t,
		fixture,
		"038f47a2-9c35-4d6e-8b1a-1234567890ab",
		&firstMutations,
	)
	_, err := fixture.service.executeIdempotentOperation(t.Context(), first)
	assertAPIErrorCode(t, err, app_errors.ErrControlOperationIncomplete.Code)

	secondMutations := 0
	second := idempotentOperationInput{
		IdempotencyKey: "048f47a2-9c35-4d6e-8b1a-1234567890ab",
		DigestVersion:  1,
		RequestDigest:  [32]byte{4},
		Kind:           operationKindAccessKeyCreate,
		Mutate: func(operationTransaction) (idempotentMutationResult, error) {
			secondMutations++
			return idempotentMutationResult{
				ResourceIdentity: "access-key:9",
				CanonicalResult:  []byte(`{"id":9}`),
			}, nil
		},
	}
	_, err = fixture.service.executeIdempotentOperation(t.Context(), second)
	assertAPIErrorCode(t, err, app_errors.ErrControlRecoveryPending.Code)
	if secondMutations != 0 {
		t.Fatalf("new mutation ran behind failed barrier %d times", secondMutations)
	}
	var operationCount int64
	if err := fixture.db.Model(&models.ControlOperation{}).Count(&operationCount).Error; err != nil {
		t.Fatalf("count operations: %v", err)
	}
	if operationCount != 1 {
		t.Fatalf("operation count = %d, want only older operation", operationCount)
	}
	assertRecoveryPendingDataShape(t, err)

	ordinaryMutations := 0
	_, err = fixture.service.writeConfig(t.Context(), func(operationTransaction) error {
		ordinaryMutations++
		return nil
	}, nil)
	assertAPIErrorCode(t, err, app_errors.ErrControlRecoveryPending.Code)
	if ordinaryMutations != 0 {
		t.Fatalf("ordinary control mutation ran behind failed barrier %d times", ordinaryMutations)
	}
}

func TestOperationBarrierReturnsDatabaseErrorWhenRecoveryQueryFails(t *testing.T) {
	fixture := newServiceFixture(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	ordinaryMutations := 0
	_, err := fixture.service.writeConfig(ctx, func(operationTransaction) error {
		ordinaryMutations++
		return nil
	}, nil)
	if !errors.Is(err, app_errors.ErrDatabase) {
		t.Fatalf("writeConfig() error = %v, want database error", err)
	}
	if ordinaryMutations != 0 {
		t.Fatalf("writeConfig() ran mutation %d times", ordinaryMutations)
	}

	idempotentMutations := 0
	_, err = fixture.service.executeIdempotentOperation(ctx, idempotentOperationInput{
		IdempotencyKey: "078f47a2-9c35-4d6e-8b1a-1234567890ab",
		DigestVersion:  1,
		RequestDigest:  [32]byte{7},
		Kind:           operationKindAccessKeyCreate,
		Mutate: func(operationTransaction) (idempotentMutationResult, error) {
			idempotentMutations++
			return idempotentMutationResult{}, nil
		},
	})
	if !errors.Is(err, app_errors.ErrDatabase) {
		t.Fatalf("executeIdempotentOperation() error = %v, want database error", err)
	}
	if idempotentMutations != 0 {
		t.Fatalf("executeIdempotentOperation() ran mutation %d times", idempotentMutations)
	}
}

func TestDrainCommittedOperationsFailsClosedOnUnsupportedDigestVersion(t *testing.T) {
	fixture := newServiceFixture(t)
	stages, err := json.Marshal([]operationStage{
		operationStageDBCommitted,
		operationStageSnapshotPublished,
		operationStageCompleted,
	})
	if err != nil {
		t.Fatalf("marshal stages: %v", err)
	}
	now := time.Date(2026, time.July, 28, 12, 0, 0, 0, time.UTC)
	row := models.ControlOperation{
		OperationID:        "058f47a2-9c35-4d6e-8b1a-1234567890ab",
		IdempotencyKey:     "068f47a2-9c35-4d6e-8b1a-1234567890ab",
		DigestVersion:      99,
		RequestDigest:      bytes.Repeat([]byte{0x6}, 32),
		OperationKind:      string(operationKindAccessKeyCreate),
		ResourceIdentity:   "access-key:1",
		CanonicalResult:    []byte(`{"id":1}`),
		RequiredStages:     models.JSON(stages),
		LastCompletedStage: string(operationStageDBCommitted),
		CreatedAt:          now,
		UpdatedAt:          now,
	}
	if err := fixture.db.Create(&row).Error; err != nil {
		t.Fatalf("seed unsupported operation: %v", err)
	}

	err = fixture.service.DrainCommittedOperations(t.Context())
	if err == nil || !errors.Is(err, app_errors.ErrInternalServer) {
		t.Fatalf("DrainCommittedOperations() error = %v, want fail closed", err)
	}
	var stored models.ControlOperation
	if err := fixture.db.First(&stored).Error; err != nil {
		t.Fatalf("read operation: %v", err)
	}
	if stored.LastCompletedStage != string(operationStageDBCommitted) {
		t.Fatalf("unsupported operation advanced to %q", stored.LastCompletedStage)
	}
}

func TestOperationRecoveryBackoffIsExponentialAndBounded(t *testing.T) {
	backoff := operationRecoveryInitialBackoff
	want := []time.Duration{
		500 * time.Millisecond,
		time.Second,
		2 * time.Second,
		4 * time.Second,
		8 * time.Second,
		16 * time.Second,
		30 * time.Second,
		30 * time.Second,
	}
	for index, expected := range want {
		backoff = nextOperationRecoveryBackoff(backoff)
		if backoff != expected {
			t.Fatalf("backoff step %d = %s, want %s", index, backoff, expected)
		}
	}
}

func TestCompactCompletedOperationsKeepsPermanentComparatorTombstone(t *testing.T) {
	fixture := newServiceFixture(t)
	fixture.service.operationRandom = bytes.NewReader(bytes.Repeat([]byte{0x34}, 16))
	fixture.service.now = func() time.Time {
		return time.Date(2026, time.July, 20, 12, 0, 0, 0, time.UTC)
	}
	mutations := 0
	input := idempotentOperationInput{
		IdempotencyKey: "078f47a2-9c35-4d6e-8b1a-1234567890ab",
		DigestVersion:  1,
		RequestDigest:  [32]byte{7},
		Kind:           operationKindAccessKeyCreate,
		Mutate: func(operationTransaction) (idempotentMutationResult, error) {
			mutations++
			return idempotentMutationResult{
				ResourceIdentity: "access-key:7",
				CanonicalResult:  []byte(`{"id":7}`),
			}, nil
		},
	}
	if _, err := fixture.service.executeIdempotentOperation(t.Context(), input); err != nil {
		t.Fatalf("execute operation: %v", err)
	}

	compacted, err := fixture.service.CompactCompletedOperations(
		t.Context(),
		time.Date(2026, time.July, 28, 12, 0, 0, 0, time.UTC),
	)
	if err != nil {
		t.Fatalf("CompactCompletedOperations() error = %v", err)
	}
	if compacted != 1 {
		t.Fatalf("compacted rows = %d, want 1", compacted)
	}
	var row models.ControlOperation
	if err := fixture.db.First(&row).Error; err != nil {
		t.Fatalf("read compacted operation: %v", err)
	}
	if row.CompactedAt == nil || len(row.CanonicalResult) != 0 ||
		len(row.RequiredStages) != 0 || row.LastCompletedStage != "" ||
		row.OperationID == "" || row.ResourceIdentity != "access-key:7" ||
		len(row.RequestDigest) != 32 {
		t.Fatalf("compacted row = %#v", row)
	}

	_, err = fixture.service.executeIdempotentOperation(t.Context(), input)
	assertAPIErrorCode(t, err, app_errors.ErrIdempotencyResultExpired.Code)
	if mutations != 1 {
		t.Fatalf("expired replay reran mutation %d times", mutations)
	}
	input.RequestDigest = [32]byte{8}
	_, err = fixture.service.executeIdempotentOperation(t.Context(), input)
	assertAPIErrorCode(t, err, app_errors.ErrIdempotencyKeyReused.Code)
}

func newDurableGroupOperationInput(
	t *testing.T,
	fixture serviceFixture,
	idempotencyKey string,
	mutations *int,
) idempotentOperationInput {
	t.Helper()
	return idempotentOperationInput{
		IdempotencyKey: idempotencyKey,
		DigestVersion:  1,
		RequestDigest:  [32]byte{byte(idempotencyKey[0])},
		Kind:           operationKindGroupCreate,
		Mutate: func(tx operationTransaction) (idempotentMutationResult, error) {
			*mutations++
			group := models.Group{
				Name:        "group-" + idempotencyKey[:4],
				UpstreamURL: "https://upstream.example.com",
				Protocols:   models.JSON(`["` + string(protocol.OpenAICompletions) + `"]`),
				Models:      models.JSON(`[]`),
				Config:      models.JSON(`{}`),
				Enabled:     true,
			}
			if err := tx.Create(&group).Error; err != nil {
				return idempotentMutationResult{}, err
			}
			key := models.UpstreamKey{
				GroupID: group.ID, KeyValue: "cipher-one", KeyHash: "hash-one",
				Status: models.UpstreamKeyStatusActive,
			}
			if err := tx.Create(&key).Error; err != nil {
				return idempotentMutationResult{}, err
			}
			result := []byte(fmt.Sprintf(`{"group_id":%d}`, group.ID))
			return idempotentMutationResult{
				ResourceIdentity: "group:" + strconv.FormatUint(uint64(group.ID), 10),
				CanonicalResult:  result,
			}, nil
		},
	}
}

func mustResourceGroupID(t *testing.T, identity string) uint {
	t.Helper()
	const prefix = "group:"
	if len(identity) <= len(prefix) || identity[:len(prefix)] != prefix {
		t.Fatalf("resource identity = %q", identity)
	}
	value, err := strconv.ParseUint(identity[len(prefix):], 10, 64)
	if err != nil {
		t.Fatalf("parse resource identity: %v", err)
	}
	return uint(value)
}

func assertAPIErrorCode(t *testing.T, err error, want string) {
	t.Helper()
	var apiErr *app_errors.APIError
	if !errors.As(err, &apiErr) || apiErr.Code != want {
		t.Fatalf("error = %v, want API code %s", err, want)
	}
}

func assertRecoveryPendingDataShape(t *testing.T, err error) {
	t.Helper()
	var apiErr *app_errors.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error = %v, want APIError", err)
	}
	raw, marshalErr := json.Marshal(apiErr.Data)
	if marshalErr != nil {
		t.Fatalf("marshal recovery data: %v", marshalErr)
	}
	var fields map[string]any
	if unmarshalErr := json.Unmarshal(raw, &fields); unmarshalErr != nil {
		t.Fatalf("unmarshal recovery data: %v", unmarshalErr)
	}
	for _, name := range []string{
		"operation_id", "operation_kind", "failed_stage", "retry_after_ms",
	} {
		if _, ok := fields[name]; !ok {
			t.Errorf("recovery data missing %q: %s", name, raw)
		}
	}
	if len(fields) != 4 {
		t.Fatalf("recovery data fields = %v, want exact frozen shape", fields)
	}
}
