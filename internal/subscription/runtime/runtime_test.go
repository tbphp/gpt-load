package subscriptionruntime

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"gpt-load/internal/channel"
	"gpt-load/internal/channel/modules"
	"gpt-load/internal/channel/spec"
)

func TestTokenEndpointFailureRetryable(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		oauthCode  string
		want       bool
	}{
		{name: "rate limited", statusCode: http.StatusTooManyRequests, want: true},
		{name: "server unavailable", statusCode: http.StatusServiceUnavailable, want: true},
		{name: "temporary OAuth code", statusCode: http.StatusBadRequest, oauthCode: "temporarily_unavailable", want: true},
		{name: "server OAuth code", statusCode: http.StatusBadRequest, oauthCode: "server_error", want: true},
		{name: "rate-limit OAuth code", statusCode: http.StatusBadRequest, oauthCode: "rate_limit_exceeded", want: true},
		{name: "invalid client", statusCode: http.StatusBadRequest, oauthCode: "invalid_client"},
		{name: "invalid client overrides server status", statusCode: http.StatusServiceUnavailable, oauthCode: "invalid_client"},
		{name: "invalid grant overrides server status", statusCode: http.StatusServiceUnavailable, oauthCode: "invalid_grant"},
		{name: "unsupported grant", statusCode: http.StatusBadRequest, oauthCode: "unsupported_grant_type"},
		{name: "invalid scope", statusCode: http.StatusBadRequest, oauthCode: "invalid_scope"},
		{name: "unknown client failure", statusCode: http.StatusBadRequest, oauthCode: "provider_specific"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := TokenEndpointFailureRetryable(test.statusCode, test.oauthCode); got != test.want {
				t.Fatalf("TokenEndpointFailureRetryable() = %t, want %t", got, test.want)
			}
		})
	}
}

func TestRuntimeRequiresExplicitImplementations(t *testing.T) {
	if _, err := NewRuntime(channel.NewRegistry()); err == nil {
		t.Fatal("NewRuntime() compiled provider implementations implicitly")
	}
}

type testDiscovery struct{ id spec.UtilityID }

func (value testDiscovery) ID() spec.UtilityID { return value.id }
func (testDiscovery) DiscoverModels(context.Context, Credential) ([]string, error) {
	return nil, nil
}

type testObservation struct{ id spec.UtilityID }

func (value testObservation) ID() spec.UtilityID { return value.id }
func (testObservation) Observe(context.Context, Credential) (Observation, error) {
	return Observation{}, nil
}

type testResetCredit struct{ id spec.ActionID }

func (value testResetCredit) ID() spec.ActionID { return value.id }
func (testResetCredit) Consume(context.Context, Credential, string) (ResetCreditResult, error) {
	return ResetCreditResult{}, nil
}

type testBrowserDriver struct{ duplicateDriver }

func (testBrowserDriver) BeginAuthorization() (Authorization, error) { return Authorization{}, nil }
func (testBrowserDriver) CompleteAuthorization(context.Context, AuthorizationCompletion) (Credential, error) {
	return Credential{}, nil
}
func (testBrowserDriver) AuthorizationFailureDefinitive(error) bool { return false }
func (testBrowserDriver) LocalCallback() (LocalCallbackSpec, bool)  { return LocalCallbackSpec{}, false }

func completeTestRuntimeImplementations() ([]Driver, []ModelDiscovery, []QuotaObservation, []ResetCreditAction) {
	return []Driver{
		testBrowserDriver{duplicateDriver{id: modules.CodexSubscriptionDriver}},
		testBrowserDriver{duplicateDriver{id: modules.ClaudeSubscriptionDriver}},
		testBrowserDriver{duplicateDriver{id: modules.AntigravitySubscriptionDriver}},
		duplicateDriver{id: modules.GrokSubscriptionDriver},
	}, []ModelDiscovery{
		testDiscovery{id: modules.CodexModelDiscovery},
		testDiscovery{id: modules.ClaudeModelDiscovery},
		testDiscovery{id: modules.AntigravityModelDiscovery},
		testDiscovery{id: modules.GrokModelDiscovery},
	}, []QuotaObservation{
		testObservation{id: modules.CodexQuotaObservation},
		testObservation{id: modules.ClaudeQuotaObservation},
		testObservation{id: modules.AntigravityQuotaObservation},
	}, []ResetCreditAction{
		testResetCredit{id: modules.CodexResetCreditAction},
	}
}

func TestRuntimeRejectsDeviceOAuthWithoutDriverSupport(t *testing.T) {
	drivers, discoveries, observations, actions := completeTestRuntimeImplementations()
	_, err := compileRuntime(channel.NewRegistry(), drivers, discoveries, observations, actions)
	if err == nil || !strings.Contains(err.Error(), "device OAuth") {
		t.Fatalf("compileRuntime() error = %v, want missing device OAuth support", err)
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
func (duplicateDriver) ClassifyRefreshFailure(error) RefreshFailureDecision {
	return RefreshFailureDecision{Kind: RefreshFailureOutcomeUnknown}
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
