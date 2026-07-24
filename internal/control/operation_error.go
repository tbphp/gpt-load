package control

import (
	"errors"

	app_errors "gpt-load/internal/platform/errors"
)

const (
	stageApplyCommittedRegistryMutation = "apply_committed_registry_mutation"
	stagePublishCommittedSnapshot       = "publish_committed_snapshot"
	stageValidateDBRegistryPair         = "validate_db_registry_pair"
)

type dbRegistryMismatchKind string

const (
	mismatchMissingRegistry = "missing_registry"
	mismatchExtraRegistry   = "extra_registry"
	mismatchGroupID         = "group_id"
	mismatchStatus          = "status"
	mismatchWeightManual    = "weight_manual"
)

type controlOperationError struct {
	stage        string
	mismatchKind string
	groupID      uint
	keyID        uint
}

func (e *controlOperationError) Error() string {
	return "control operation invariant failed"
}

func (e *controlOperationError) Unwrap() error {
	return app_errors.ErrInternalServer
}

func newControlOperationError(stage string) error {
	return &controlOperationError{stage: stage}
}

type controlResourceNotFoundError struct {
	resource string
}

func (e *controlResourceNotFoundError) Error() string {
	return e.resource + " not found"
}

func (e *controlResourceNotFoundError) Unwrap() error {
	return app_errors.ErrResourceNotFound
}

func groupNotFoundError() error {
	return &controlResourceNotFoundError{resource: "group"}
}

func keyNotFoundError() error {
	return &controlResourceNotFoundError{resource: "key"}
}

func dbRegistryMismatch(
	kind dbRegistryMismatchKind,
	groupID uint,
	keyID uint,
) error {
	return &controlOperationError{
		stage:        stageValidateDBRegistryPair,
		mismatchKind: string(kind),
		groupID:      groupID,
		keyID:        keyID,
	}
}

func withControlOperationContext(err error, groupID, keyID uint) error {
	var operationErr *controlOperationError
	if !errors.As(err, &operationErr) {
		return err
	}
	cloned := *operationErr
	cloned.groupID = groupID
	cloned.keyID = keyID
	return &cloned
}
