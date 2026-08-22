package control

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/sirupsen/logrus"

	app_errors "gpt-load/internal/platform/errors"
)

func TestControlOperationErrorCarriesOnlyFixedContext(t *testing.T) {
	err := withControlOperationContext(
		newControlOperationError(stageApplyCommittedRegistryMutation),
		12,
		34,
	)
	if !errors.Is(err, app_errors.ErrInternalServer) {
		t.Fatalf("error = %v, want ErrInternalServer", err)
	}
	var operationErr *controlOperationError
	if !errors.As(err, &operationErr) {
		t.Fatalf("error = %T, want *controlOperationError", err)
	}
	if operationErr.stage != stageApplyCommittedRegistryMutation ||
		operationErr.groupID != 12 || operationErr.credentialID != 34 ||
		operationErr.mismatchKind != "" {
		t.Fatalf("operation error = %#v", operationErr)
	}
}

func TestServiceErrorMessageIDUsesTypedResourceNotFound(t *testing.T) {
	for _, test := range []struct {
		name      string
		operation string
		err       error
		want      string
	}{
		{
			name: "list Group credentials missing Group", operation: "list_group_credentials",
			err: groupNotFoundError(), want: "group.not_found",
		},
		{
			name: "download all Group credentials missing Group", operation: "download_all_group_credentials",
			err: groupNotFoundError(), want: "group.not_found",
		},
		{
			name: "update Group credential missing Group", operation: "update_group_credential",
			err: groupNotFoundError(), want: "group.not_found",
		},
		{
			name: "update Group settings missing Group", operation: "update_group_settings",
			err: groupNotFoundError(), want: "group.not_found",
		},
		{
			name: "update Group credential missing Credential", operation: "update_group_credential",
			err: credentialNotFoundError(), want: "credential.not_found",
		},
		{
			name: "delete wrong-owner Credential", operation: "delete_group_credential",
			err: credentialNotFoundError(), want: "credential.not_found",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := serviceErrorMessageID(
				test.operation,
				test.err,
				app_errors.ErrResourceNotFound,
			); got != test.want {
				t.Fatalf("serviceErrorMessageID() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestServiceErrorMessageIDUsesGroupNameExistsForGroupSettingsUpdate(t *testing.T) {
	if got := serviceErrorMessageID(
		"update_group_settings",
		app_errors.ErrDuplicateResource,
		app_errors.ErrDuplicateResource,
	); got != "group.name_exists" {
		t.Fatalf("serviceErrorMessageID() = %q, want group.name_exists", got)
	}
}

func TestLogServiceErrorUsesOnlyFixedOperationContext(t *testing.T) {
	const secretCause = "known-operation-secret-cause"
	err := fmt.Errorf(
		"%s: %w",
		secretCause,
		dbRegistryMismatch(mismatchWeightManual, 12, 34),
	)
	var logs bytes.Buffer
	logger := logrus.StandardLogger()
	previousOutput, previousFormatter := logger.Out, logger.Formatter
	logrus.SetOutput(&logs)
	logrus.SetFormatter(&logrus.JSONFormatter{DisableTimestamp: true})
	t.Cleanup(func() {
		logrus.SetOutput(previousOutput)
		logrus.SetFormatter(previousFormatter)
	})

	logServiceError("list_group_credentials", err, app_errors.ErrInternalServer.Code)
	logText := logs.String()
	for _, required := range []string{
		`"msg":"[CONTROL] Operation failed"`,
		`"operation":"list_group_credentials"`,
		`"stage":"validate_db_registry_pair"`,
		`"mismatch_kind":"weight_manual"`,
		`"group_id":12`,
		`"credential_id":34`,
	} {
		if !strings.Contains(logText, required) {
			t.Fatalf("log output missing %q: %s", required, logText)
		}
	}
	if strings.Contains(logText, `"plane"`) {
		t.Fatalf("log output contains redundant plane field: %s", logText)
	}
	for _, forbidden := range []string{secretCause, err.Error()} {
		if strings.Contains(logText, forbidden) {
			t.Fatalf("log output leaked %q: %s", forbidden, logText)
		}
	}
}
