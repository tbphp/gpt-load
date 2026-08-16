package gateway

import (
	"net/http"
	"reflect"
	"testing"
	"time"

	"gpt-load/internal/channel"
	"gpt-load/internal/execution"
	"gpt-load/internal/health"
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
	subscriptions, err := subscriptionruntime.NewRuntime(
		channels,
		subscriptionruntime.CodexImplementations(),
		subscriptionruntime.ClaudeImplementations(),
	)
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
	}, now)
	if result.Category != health.FailureCategoryRateLimited ||
		result.Action != health.ActionCooldownCredential ||
		!result.CooldownUntil.Equal(now.Add(7*time.Second)) {
		t.Fatalf("JudgeExecution() = %#v", result)
	}

	result = judgeUpstreamResult(UpstreamResult{
		DispatchState: execution.DispatchNotSent,
		ExecutionError: &execution.ErrorEvidence{
			Kind: execution.ErrorKindTransport, Summary: "connection failed",
		},
	}, now)
	if result.Category != health.FailureCategoryUpstreamHostError || result.Action != health.ActionSkipGroup {
		t.Fatalf("JudgeExecution(not sent) = %#v", result)
	}
}
