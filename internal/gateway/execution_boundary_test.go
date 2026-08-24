package gateway

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"gpt-load/internal/channel"
	"gpt-load/internal/execution"
	"gpt-load/internal/health"
	subscriptionproviders "gpt-load/internal/subscription/providers"
	subscriptionruntime "gpt-load/internal/subscription/runtime"
)

func TestNormalizeChannelCredentialRequiresCanonicalStoredObject(t *testing.T) {
	t.Parallel()
	channels, subscriptions := testCredentialRuntimes(t)

	credential, err := normalizeChannelCredential(
		channels,
		subscriptions,
		channel.OpenAI,
		"api_key",
		` {"api_key":"sk-typed"} `,
	)
	if err != nil {
		t.Fatalf("normalizeChannelCredential() error = %v", err)
	}
	if credential.apiKey != "sk-typed" || string(credential.payload) != `{"api_key":"sk-typed"}` {
		t.Fatalf("normalizeChannelCredential() = %q %s", credential.apiKey, credential.payload)
	}

	for _, invalid := range []string{"", " ", "sk-legacy", `"sk-legacy"`, `[]`, `{}`, `{"api_key":""}`} {
		if credential, err := normalizeChannelCredential(channels, subscriptions, channel.OpenAI, "api_key", invalid); err == nil {
			t.Fatalf("normalizeChannelCredential(%q) = %#v, nil", invalid, credential)
		}
	}
}

func TestNormalizeChannelCredentialPreservesStructuredCloudSecrets(t *testing.T) {
	t.Parallel()
	channels, subscriptions := testCredentialRuntimes(t)

	got, err := normalizeChannelCredential(
		channels,
		subscriptions,
		channel.AWSBedrock,
		"api_key",
		` {"access_key":"AKIA_TEST","secret_key":"bedrock-secret","session_token":"bedrock-session"} `,
	)
	if err != nil {
		t.Fatalf("normalizeChannelCredential() error = %v", err)
	}
	if got.apiKey != "" || string(got.payload) !=
		`{"access_key":"AKIA_TEST","secret_key":"bedrock-secret","session_token":"bedrock-session"}` {
		t.Fatalf("normalized credential = %#v / %s", got, got.payload)
	}
	if want := []string{"AKIA_TEST", "bedrock-secret", "bedrock-session"}; !reflect.DeepEqual(got.secrets, want) {
		t.Fatalf("secrets = %#v, want %#v", got.secrets, want)
	}
}

func TestNormalizeChannelCredentialUsesBoundSubscriptionDriver(t *testing.T) {
	channels, subscriptions := testCredentialRuntimes(t)
	got, err := normalizeChannelCredential(
		channels,
		subscriptions,
		channel.Codex,
		"subscription",
		`{"type":"codex","access_token":"access-secret","refresh_token":"refresh-secret","account_id":"account-one"}`,
	)
	if err != nil {
		t.Fatal(err)
	}
	if got.apiKey != "" || len(got.payload) == 0 || !reflect.DeepEqual(got.secrets[:2], []string{"access-secret", "refresh-secret"}) {
		t.Fatalf("normalized subscription credential = %#v", got)
	}
}

func testCredentialRuntimes(t *testing.T) (*channel.Registry, *subscriptionruntime.Runtime) {
	t.Helper()
	channels := channel.NewRegistry()
	subscriptions, err := subscriptionruntime.NewRuntime(channels, subscriptionproviders.Implementations()...)
	if err != nil {
		t.Fatal(err)
	}
	return channels, subscriptions
}

func TestJudgeUpstreamResultUsesNeutralExecutionEvidence(t *testing.T) {
	t.Parallel()

	now := time.Unix(1_800_000_000, 0)
	result := judgeUpstreamResult(UpstreamResult{
		DispatchState:   execution.DispatchMaybeSent,
		ResponseStarted: true,
		StatusCode:      http.StatusTooManyRequests,
		ExecutionError: &execution.ErrorEvidence{
			Kind: execution.ErrorKindHTTP, StatusCode: http.StatusTooManyRequests,
			Summary: "upstream rejected request", RetryAfter: 7 * time.Second,
		},
	}, now, health.DecisionContext{DefaultRateLimitCooldown: time.Minute})
	if result.Category != health.FailureCategoryRateLimited ||
		result.Effect != health.EffectCooldownCredential ||
		!result.CooldownUntil.Equal(now.Add(7*time.Second)) {
		t.Fatalf("JudgeExecution() = %#v", result)
	}

	result = judgeUpstreamResult(UpstreamResult{
		DispatchState: execution.DispatchNotSent,
		ExecutionError: &execution.ErrorEvidence{
			Kind: execution.ErrorKindTransport, Summary: "connection failed",
		},
	}, now, health.DecisionContext{DefaultRateLimitCooldown: time.Minute})
	if result.Category != health.FailureCategoryUpstreamHostError || result.Effect != health.EffectSkipGroup {
		t.Fatalf("JudgeExecution(not sent) = %#v", result)
	}
}

func TestJudgeUpstreamResultKeepsUpstreamTimeoutOutOfDownstreamCancellation(t *testing.T) {
	t.Parallel()

	evidence := &execution.ErrorEvidence{
		Kind: execution.ErrorKindTimeout, OriginHint: execution.ErrorOriginUpstream,
		ScopeHint: execution.ErrorScopeGroup, Summary: "upstream connection timed out",
	}
	upstream := upstreamFromExecutionResult(
		context.Background(),
		ForwardInput{},
		execution.AttemptResult{
			DispatchState: execution.DispatchNotSent,
			Error:         evidence,
		},
	)
	decision := judgeUpstreamResult(upstream, time.Now(), health.DecisionContext{})
	if decision.Category != health.FailureCategoryUpstreamHostError ||
		decision.Origin != execution.ErrorOriginUpstream ||
		decision.Scope != execution.ErrorScopeGroup ||
		decision.Retry != health.RetryNextCandidate ||
		decision.Effect != health.EffectSkipGroup ||
		decision.RuleID != "transport.not_sent" {
		t.Fatalf("timeout decision = %#v", decision)
	}
}

func TestJudgeUpstreamResultClassifiesUncommittedProtocolFailureAsUpstream(t *testing.T) {
	t.Parallel()

	decision := judgeUpstreamResult(UpstreamResult{
		Err:             fmt.Errorf("%w: invalid execution stream event", ErrUpstreamProtocol),
		DispatchState:   execution.DispatchMaybeSent,
		ResponseStarted: true,
		StatusCode:      http.StatusOK,
	}, time.Now(), health.DecisionContext{})
	if decision.Category != health.FailureCategoryAmbiguous ||
		decision.Origin != execution.ErrorOriginUpstream ||
		decision.Retry != health.RetryNone ||
		decision.Effect != health.EffectNone ||
		decision.RuleID != "fallback.ambiguous" {
		t.Fatalf("protocol decision = %#v", decision)
	}
}

func TestNormalizeUpstreamResultContractFailsClosedWithoutBodyInference(t *testing.T) {
	t.Parallel()

	result := normalizeUpstreamResultContract(UpstreamResult{
		StatusCode:         http.StatusTooManyRequests,
		Body:               []byte("private raw body"),
		ClassificationBody: []byte("private classification body"),
		Err:                errors.New("private raw error"),
	})
	if result.DispatchState != execution.DispatchMaybeSent || result.ExecutionError == nil ||
		result.ExecutionError.Kind != execution.ErrorKindInternal ||
		result.ExecutionError.Code != "attempt_result_contract_invalid" ||
		result.ExecutionError.Summary != "Attempt forwarder returned an invalid result." ||
		result.Body != nil || result.ClassificationBody != nil {
		t.Fatalf("normalized result = %#v", result)
	}
	for _, value := range []string{result.ErrorSummary, result.ExecutionError.Summary, fmt.Sprint(result.Err)} {
		if strings.Contains(value, "private") {
			t.Fatalf("invalid forwarder contract leaked private detail: %q", value)
		}
	}
}
