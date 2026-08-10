package errors

import (
	"errors"
	"net/http"
	"testing"

	"gorm.io/gorm"
)

func TestParseDBErrorUsesDatabaseIndependentGORMErrors(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want *APIError
	}{
		{name: "record not found", err: gorm.ErrRecordNotFound, want: ErrResourceNotFound},
		{name: "duplicate key", err: gorm.ErrDuplicatedKey, want: ErrDuplicateResource},
		{name: "wrapped duplicate key", err: errors.Join(errors.New("create group"), gorm.ErrDuplicatedKey), want: ErrDuplicateResource},
		{name: "other", err: errors.New("database unavailable"), want: ErrDatabase},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ParseDBError(tt.err); got != tt.want {
				t.Fatalf("ParseDBError(%v) = %#v, want %#v", tt.err, got, tt.want)
			}
		})
	}
}

func TestChannelTargetConflictContract(t *testing.T) {
	err := ErrChannelTargetConflict
	if err.HTTPStatus != http.StatusConflict || err.Code != "CHANNEL_TARGET_CONFLICT" {
		t.Fatalf("ErrChannelTargetConflict = %#v", err)
	}
}

func TestGroupInUseContract(t *testing.T) {
	err := ErrGroupInUse
	if err.HTTPStatus != http.StatusConflict || err.Code != "GROUP_IN_USE" ||
		err.Message != "Group is referenced by access keys" {
		t.Fatalf("ErrGroupInUse = %#v", err)
	}
}

func TestParseDBErrorRecognizesSQLiteUniqueConstraint(t *testing.T) {
	err := errors.New("constraint failed: UNIQUE constraint failed: groups.name (2067)")
	if got := ParseDBError(err); got != ErrDuplicateResource {
		t.Fatalf("ParseDBError() = %#v, want duplicate resource", got)
	}
}

func TestParseDBErrorRecognizesNativeMySQLAndPostgreSQLUniqueConstraints(t *testing.T) {
	for _, err := range []error{
		errors.New("Error 1062: Duplicate entry 'group' for key 'groups.name'"),
		errors.New("duplicate key value violates unique constraint groups_name_key"),
	} {
		if got := ParseDBError(err); got != ErrDuplicateResource {
			t.Fatalf("ParseDBError(%v) = %#v, want duplicate resource", err, got)
		}
	}
}

func TestNewAPIErrorWithDataDoesNotMutateBase(t *testing.T) {
	data := map[string]any{"id": 12}
	got := NewAPIErrorWithData(ErrChannelTargetConflict, data)
	if got == ErrChannelTargetConflict || got.Data == nil {
		t.Fatalf("NewAPIErrorWithData() = %#v", got)
	}
	if ErrChannelTargetConflict.Data != nil {
		t.Fatalf("base Data = %#v, want nil", ErrChannelTargetConflict.Data)
	}
}
