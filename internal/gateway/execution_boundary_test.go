package gateway

import (
	"net/http"
	"reflect"
	"testing"
	"time"

	"gpt-load/internal/channel"
	"gpt-load/internal/execution"
	"gpt-load/internal/health"
)

func TestNormalizeChannelCredentialRequiresCanonicalStoredObject(t *testing.T) {
	t.Parallel()

	credential, err := normalizeChannelCredential(
		channel.NewRegistry(),
		channel.OpenAI,
		` {"api_key":"sk-typed"} `,
	)
	if err != nil {
		t.Fatalf("normalizeChannelCredential() error = %v", err)
	}
	if credential.apiKey != "sk-typed" || string(credential.payload) != `{"api_key":"sk-typed"}` {
		t.Fatalf("normalizeChannelCredential() = %q %s", credential.apiKey, credential.payload)
	}

	for _, invalid := range []string{"", " ", "sk-legacy", `"sk-legacy"`, `[]`, `{}`, `{"api_key":""}`} {
		if credential, err := normalizeChannelCredential(channel.NewRegistry(), channel.OpenAI, invalid); err == nil {
			t.Fatalf("normalizeChannelCredential(%q) = %#v, nil", invalid, credential)
		}
	}
}

func TestNormalizeChannelCredentialPreservesStructuredCloudSecrets(t *testing.T) {
	t.Parallel()

	got, err := normalizeChannelCredential(
		channel.NewRegistry(),
		channel.AWSBedrock,
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
