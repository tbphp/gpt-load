package subscriptionruntime

import (
	"context"
	"testing"

	"gpt-load/internal/channel"
	"gpt-load/internal/channel/modules"
	"gpt-load/internal/channel/spec"
)

func TestRuntimeRequiresExplicitImplementations(t *testing.T) {
	if _, err := NewRuntime(channel.NewRegistry()); err == nil {
		t.Fatal("NewRuntime() compiled provider implementations implicitly")
	}
}

func TestRuntimeFailsClosedWhenBoundImplementationIsMissing(t *testing.T) {
	_, err := compileRuntime(channel.NewRegistry(), nil, nil, nil, nil)
	if err == nil {
		t.Fatal("compileRuntime() succeeded without the Codex driver")
	}
}

type duplicateDriver struct{ id spec.SubscriptionDriverID }

func (driver duplicateDriver) ID() spec.SubscriptionDriverID { return driver.id }
func (duplicateDriver) Parse([]byte) (Credential, error)     { return Credential{}, nil }
func (duplicateDriver) Refresh(context.Context, Credential) (Credential, error) {
	return Credential{}, nil
}
func (duplicateDriver) ClassifyRefreshFailure(error) RefreshFailure {
	return RefreshFailureOutcomeUnknown
}

func TestRuntimeRejectsDuplicateImplementationIDs(t *testing.T) {
	_, err := compileRuntime(
		channel.NewRegistry(),
		[]Driver{
			duplicateDriver{id: modules.CodexSubscriptionDriver},
			duplicateDriver{id: modules.CodexSubscriptionDriver},
		},
		nil,
		nil,
		nil,
	)
	if err == nil {
		t.Fatal("compileRuntime() accepted duplicate driver IDs")
	}
}
