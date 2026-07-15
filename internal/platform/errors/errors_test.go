package errors

import (
	"errors"
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

func TestParseDBErrorRecognizesSQLiteUniqueConstraint(t *testing.T) {
	err := errors.New("constraint failed: UNIQUE constraint failed: groups.signature (2067)")
	if got := ParseDBError(err); got != ErrDuplicateResource {
		t.Fatalf("ParseDBError() = %#v, want duplicate resource", got)
	}
}
