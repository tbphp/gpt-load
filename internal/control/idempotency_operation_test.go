package control

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"testing"

	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/storage/models"
)

func TestOperationRequiredStagesAreKindSpecificAndDetached(t *testing.T) {
	t.Parallel()
	tests := []struct {
		kind operationKind
		want []operationStage
	}{
		{
			kind: operationKindAccessKeyCreate,
			want: []operationStage{
				operationStageDBCommitted,
				operationStageSnapshotPublished,
				operationStageCompleted,
			},
		},
		{
			kind: operationKindGroupCreate,
			want: []operationStage{
				operationStageDBCommitted,
				operationStagePricesPublished,
				operationStageRegistryApplied,
				operationStageSnapshotPublished,
				operationStageCompleted,
			},
		},
		{
			kind: operationKindCredentialImport,
			want: []operationStage{
				operationStageDBCommitted,
				operationStageRegistryApplied,
				operationStageCompleted,
			},
		},
	}
	for _, test := range tests {
		t.Run(string(test.kind), func(t *testing.T) {
			got, err := operationRequiredStages(test.kind)
			if err != nil {
				t.Fatalf("operationRequiredStages() error = %v", err)
			}
			if !equalOperationStages(got, test.want) {
				t.Fatalf("stages = %#v, want %#v", got, test.want)
			}
			got[0] = operationStageCompleted
			again, err := operationRequiredStages(test.kind)
			if err != nil {
				t.Fatalf("second operationRequiredStages() error = %v", err)
			}
			if !equalOperationStages(again, test.want) {
				t.Fatalf("stage plan aliases caller: %#v", again)
			}
		})
	}

	if _, err := operationRequiredStages(operationKind("unknown")); err == nil {
		t.Fatal("operationRequiredStages(unknown) error = nil, want rejection")
	}
}

func TestValidateIdempotencyKeyRequiresCanonicalLowercaseUUIDV4(t *testing.T) {
	t.Parallel()
	const valid = "018f47a2-9c35-4d6e-8b1a-1234567890ab"
	if err := validateIdempotencyKey(valid); err != nil {
		t.Fatalf("validateIdempotencyKey(valid) error = %v", err)
	}
	for _, invalid := range []string{
		"",
		" 018f47a2-9c35-4d6e-8b1a-1234567890ab",
		"018F47A2-9C35-4D6E-8B1A-1234567890AB",
		"018f47a2-9c35-3d6e-8b1a-1234567890ab",
		"018f47a2-9c35-4d6e-7b1a-1234567890ab",
		"018f47a29c354d6e8b1a1234567890ab",
	} {
		t.Run(invalid, func(t *testing.T) {
			if err := validateIdempotencyKey(invalid); err == nil {
				t.Fatalf("validateIdempotencyKey(%q) error = nil, want rejection", invalid)
			}
		})
	}
}

func TestNewOperationIDUsesCanonicalUUIDV4WithoutSharingCredentialRandom(t *testing.T) {
	t.Parallel()
	random := bytes.NewReader([]byte{
		0x00, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07,
		0x08, 0x09, 0x0a, 0x0b, 0x0c, 0x0d, 0x0e, 0x0f,
	})
	got, err := newOperationID(random)
	if err != nil {
		t.Fatalf("newOperationID() error = %v", err)
	}
	if got != "00010203-0405-4607-8809-0a0b0c0d0e0f" {
		t.Fatalf("newOperationID() = %q", got)
	}
	if err := validateIdempotencyKey(got); err != nil {
		t.Fatalf("generated operation ID is not canonical UUID v4: %v", err)
	}
}

func TestExecuteIdempotentOperationCommitsResultAndReplaysWithoutMutation(t *testing.T) {
	t.Parallel()
	fixture := newServiceFixture(t)
	fixture.service.operationRandom = bytes.NewReader(bytes.Repeat([]byte{0x42}, 16))
	const key = "018f47a2-9c35-4d6e-8b1a-1234567890ab"
	digest := sha256.Sum256([]byte("same request"))
	mutations := 0
	input := idempotentOperationInput{
		IdempotencyKey: key,
		DigestVersion:  1,
		RequestDigest:  digest,
		Kind:           operationKindAccessKeyCreate,
		Mutate: func(tx operationTransaction) (idempotentMutationResult, error) {
			mutations++
			return idempotentMutationResult{
				ResourceIdentity: "access-key:7",
				CanonicalResult:  []byte(`{"id":7,"name":"client"}`),
				Ephemeral:        "one-time-secret",
			}, nil
		},
	}

	first, err := fixture.service.executeIdempotentOperation(context.Background(), input)
	if err != nil {
		t.Fatalf("first executeIdempotentOperation() error = %v", err)
	}
	if first.Replayed || first.Ephemeral != "one-time-secret" ||
		string(first.CanonicalResult) != `{"id":7,"name":"client"}` {
		t.Fatalf("first result = %#v", first)
	}

	replayInput := input
	replayInput.Mutate = func(operationTransaction) (idempotentMutationResult, error) {
		mutations++
		return idempotentMutationResult{}, errors.New("mutation reran")
	}
	replayed, err := fixture.service.executeIdempotentOperation(context.Background(), replayInput)
	if err != nil {
		t.Fatalf("replay executeIdempotentOperation() error = %v", err)
	}
	if !replayed.Replayed || replayed.Ephemeral != nil ||
		string(replayed.CanonicalResult) != string(first.CanonicalResult) {
		t.Fatalf("replay result = %#v", replayed)
	}
	if mutations != 1 {
		t.Fatalf("mutation calls = %d, want 1", mutations)
	}

	var row models.ControlOperation
	if err := fixture.db.Where("idempotency_key = ?", key).Take(&row).Error; err != nil {
		t.Fatalf("read ControlOperation: %v", err)
	}
	if row.CommitSequence == 0 || row.ResourceIdentity != "access-key:7" ||
		row.LastCompletedStage != string(operationStageCompleted) ||
		row.CompletedAtMS == nil || row.CompactedAtMS != nil {
		t.Fatalf("operation row = %#v", row)
	}
	var stages []operationStage
	if err := json.Unmarshal(row.RequiredStages, &stages); err != nil {
		t.Fatalf("decode required stages: %v", err)
	}
	wantStages, _ := operationRequiredStages(operationKindAccessKeyCreate)
	if !equalOperationStages(stages, wantStages) {
		t.Fatalf("stored stages = %#v, want %#v", stages, wantStages)
	}
	if bytes.Contains(row.CanonicalResult, []byte("one-time-secret")) {
		t.Fatal("operation row persisted ephemeral secret")
	}
}

func TestExecuteIdempotentOperationRejectsKeyReuseWithDifferentDigest(t *testing.T) {
	t.Parallel()
	fixture := newServiceFixture(t)
	fixture.service.operationRandom = bytes.NewReader(bytes.Repeat([]byte{0x24}, 16))
	const key = "018f47a2-9c35-4d6e-8b1a-1234567890ab"
	firstDigest := sha256.Sum256([]byte("first"))
	input := idempotentOperationInput{
		IdempotencyKey: key, DigestVersion: 1, RequestDigest: firstDigest,
		Kind: operationKindAccessKeyCreate,
		Mutate: func(operationTransaction) (idempotentMutationResult, error) {
			return idempotentMutationResult{
				ResourceIdentity: "access-key:7",
				CanonicalResult:  []byte(`{"id":7,"name":"client"}`),
			}, nil
		},
	}
	if _, err := fixture.service.executeIdempotentOperation(t.Context(), input); err != nil {
		t.Fatalf("seed executeIdempotentOperation() error = %v", err)
	}

	input.RequestDigest = sha256.Sum256([]byte("different"))
	_, err := fixture.service.executeIdempotentOperation(t.Context(), input)
	var apiErr *app_errors.APIError
	if !errors.As(err, &apiErr) || apiErr.Code != app_errors.ErrIdempotencyKeyReused.Code {
		t.Fatalf("different digest error = %v, want IDEMPOTENCY_KEY_REUSED", err)
	}
}

func equalOperationStages(left, right []operationStage) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
