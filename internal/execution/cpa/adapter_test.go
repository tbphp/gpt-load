package cpa

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"gpt-load/internal/channel"
	"gpt-load/internal/dialect"
	"gpt-load/internal/execution"
	"gpt-load/internal/gateway"
	"gpt-load/internal/health"
	"gpt-load/internal/outboundproxy"
	"gpt-load/internal/platform/encryption"
	"gpt-load/internal/platform/httproute"
	"gpt-load/internal/protocol"
	"gpt-load/internal/state"
	stateloader "gpt-load/internal/state/loader"
	"gpt-load/internal/storage/models"
	"gpt-load/internal/subscription"
	subscriptionproviders "gpt-load/internal/subscription/providers"
	"gpt-load/internal/subscription/providers/antigravity"
	"gpt-load/internal/subscription/providers/codex"
	providerobservation "gpt-load/internal/subscription/providers/observation"
	subscriptionruntime "gpt-load/internal/subscription/runtime"
	"gpt-load/internal/testutil/encryptiontest"
)

type fakeExecutor struct {
	mu          sync.Mutex
	calls       int
	countCalls  int
	last        codex.Credential
	request     codex.ExecuteRequest
	result      codex.ExecuteResponse
	countResult codex.ExecuteResponse
	err         error
	stream      *codex.ExecuteStreamResponse
}

type fakeCredentialPreparer struct {
	credential subscriptionruntime.Credential
	evidence   *execution.ErrorEvidence
	delegate   credentialPreparer
	calls      int
	force      bool
}

func (f *fakeCredentialPreparer) Prepare(
	ctx context.Context,
	channelID channel.ID,
	credential execution.CredentialSnapshot,
	force bool,
) (subscriptionruntime.Credential, *execution.ErrorEvidence) {
	f.calls++
	f.force = force
	if f.delegate != nil {
		return f.delegate.Prepare(ctx, channelID, credential, force)
	}
	return f.credential, f.evidence
}

func (f *fakeCredentialPreparer) RecordPassiveQuotaObservation(
	credentialID uint,
	identityGeneration uint64,
	observedAtMS int64,
	windows []providerobservation.QuotaWindow,
) {
	if f.delegate != nil {
		f.delegate.RecordPassiveQuotaObservation(credentialID, identityGeneration, observedAtMS, windows)
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (fn roundTripperFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

func (f *fakeExecutor) Execute(_ context.Context, _ string, credential codex.Credential, request codex.ExecuteRequest) (codex.ExecuteResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	f.last = credential
	f.request = request
	return f.result, f.err
}

func (f *fakeExecutor) ExecuteStream(_ context.Context, _ string, credential codex.Credential, request codex.ExecuteRequest) (*codex.ExecuteStreamResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	f.last = credential
	f.request = request
	return f.stream, f.err
}

func (f *fakeExecutor) CountTokens(_ context.Context, _ string, credential codex.Credential, request codex.ExecuteRequest) (codex.ExecuteResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.countCalls++
	f.last = credential
	f.request = request
	return f.countResult, f.err
}

func setCodexExecutor(t *testing.T, adapter *Adapter, executor codex.Executor) {
	t.Helper()
	bridge, ok := adapter.providers[channel.ProviderCodex].(*codexProviderBridge)
	if !ok {
		t.Fatal("Codex provider bridge is unavailable")
	}
	bridge.executor = executor
}

func TestAdapterExecuteRecordsPassiveQuotaObservationOnSuccessAndHTTPError(t *testing.T) {
	tests := []struct {
		name string
		err  error
	}{
		{name: "success"},
		{name: "HTTP error", err: &codex.UpstreamHTTPError{Operation: "responses", StatusCode: 429}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			adapter, _, _, keyService, row := newAdapterFixture(t, credentialJSON("access", "refresh", time.Now().Add(time.Hour)))
			manager, ok := adapter.credentials.(*subscription.CredentialManager)
			if !ok {
				t.Fatal("adapter credentials is not a *subscription.CredentialManager")
			}
			observedAt := time.Date(2026, 8, 28, 9, 0, 0, 0, time.UTC)
			setCodexExecutor(t, adapter, &fakeExecutor{
				result: codex.ExecuteResponse{
					Payload:         []byte(`{"id":"resp_1","model":"gpt-5","output":[]}`),
					QuotaObservedAt: observedAt,
					QuotaSignals:    map[string]string{"X-Codex-Primary-Used-Percent": "55"},
				},
				err: test.err,
			})
			spec := validSpec(t, row, keyService)

			adapter.Execute(t.Context(), spec)

			dirty := manager.DirtyPassiveQuotaObservations(1)
			if len(dirty) != 1 || dirty[0].CredentialID != spec.Credential.ID ||
				dirty[0].ObservedAtMS != observedAt.UnixMilli() || len(dirty[0].Windows) != 1 ||
				dirty[0].Windows[0].ID != "primary" {
				t.Fatalf("dirty observations = %#v", dirty)
			}
		})
	}
}

func TestAdapterExecuteStreamRecordsPassiveQuotaObservationOnHandshake(t *testing.T) {
	adapter, _, _, keyService, row := newAdapterFixture(t, credentialJSON("access", "refresh", time.Now().Add(time.Hour)))
	manager, ok := adapter.credentials.(*subscription.CredentialManager)
	if !ok {
		t.Fatal("adapter credentials is not a *subscription.CredentialManager")
	}
	chunks := make(chan codex.ExecuteStreamChunk, 1)
	chunks <- codex.ExecuteStreamChunk{Payload: []byte(`data: {"type":"response.completed","response":{"id":"resp_1","model":"gpt-5"}}`)}
	close(chunks)
	observedAt := time.Date(2026, 8, 28, 9, 0, 0, 0, time.UTC)
	setCodexExecutor(t, adapter, &fakeExecutor{stream: &codex.ExecuteStreamResponse{
		Headers:         http.Header{"X-Oai-Request-Id": {"oai-request-1"}},
		Chunks:          chunks,
		QuotaObservedAt: observedAt,
		QuotaSignals:    map[string]string{"X-Codex-Primary-Used-Percent": "55"},
	}})

	adapter.ExecuteStream(t.Context(), validSpec(t, row, keyService), func(execution.StreamEvent) error { return nil })

	dirty := manager.DirtyPassiveQuotaObservations(1)
	if len(dirty) != 1 || dirty[0].ObservedAtMS != observedAt.UnixMilli() || len(dirty[0].Windows) != 1 {
		t.Fatalf("dirty observations = %#v", dirty)
	}
}

func TestAdapterStopsBeforeDispatchWhenCredentialPreparationFails(t *testing.T) {
	adapter, _, _, keyService, row := newAdapterFixture(t, credentialJSON("access", "refresh", time.Now().Add(time.Hour)))
	preparer := &fakeCredentialPreparer{evidence: &execution.ErrorEvidence{
		Kind: execution.ErrorKindProvider, Hint: execution.FailureHintReauthorizationRequired,
		Code: "refresh_rejected", Summary: "subscription account requires reauthorization",
	}}
	executor := &fakeExecutor{}
	adapter.credentials = preparer
	setCodexExecutor(t, adapter, executor)
	spec := validSpec(t, row, keyService)
	spec.ForceCredentialRefresh = true

	result := adapter.Execute(t.Context(), spec)
	if result.DispatchState != execution.DispatchNotSent || result.Error != preparer.evidence {
		t.Fatalf("result = %#v", result)
	}
	if preparer.calls != 1 || !preparer.force || executor.calls != 0 {
		t.Fatalf("prepare calls = %d, force = %t, execute calls = %d", preparer.calls, preparer.force, executor.calls)
	}
}

func TestAdapterPassesEnvironmentProxyPolicyToCPAExecutor(t *testing.T) {
	adapter, _, _, keyService, row := newAdapterFixture(
		t,
		credentialJSON("access", "refresh", time.Now().Add(time.Hour)),
	)
	executor := &fakeExecutor{result: codex.ExecuteResponse{
		Payload: []byte(`{"model":"gpt-5"}`),
	}}
	setCodexExecutor(t, adapter, executor)
	spec := validSpec(t, row, keyService)
	spec.Proxy = outboundproxy.Effective{
		Config: outboundproxy.Config{Mode: outboundproxy.ModeEnvironment},
		Source: outboundproxy.SourceEnvironment,
	}
	result := adapter.Execute(t.Context(), spec)
	if result.Error != nil || executor.calls != 1 {
		t.Fatalf("Execute() result/calls = %#v/%d", result, executor.calls)
	}
	if !executor.request.ProxyFromEnvironment || executor.request.ProxyURL != "" {
		t.Fatalf("CPA proxy request = %#v", executor.request)
	}
}

func TestAdapterReturnsStableProxyPreparationEvidence(t *testing.T) {
	adapter, _, _, keyService, row := newAdapterFixture(
		t,
		credentialJSON("access", "refresh", time.Now().Add(time.Hour)),
	)
	executor := &fakeExecutor{}
	setCodexExecutor(t, adapter, executor)
	spec := validSpec(t, row, keyService)
	spec.Proxy = outboundproxy.Effective{
		Config: outboundproxy.Config{Mode: outboundproxy.ModeCustom, URL: "ftp://proxy.example.com"},
		Source: outboundproxy.SourceGroup,
	}

	result := adapter.Execute(t.Context(), spec)
	if result.DispatchState != execution.DispatchNotSent || result.Error == nil ||
		result.Error.Kind != execution.ErrorKindInternal ||
		result.Error.OriginHint != execution.ErrorOriginInternal ||
		result.Error.ScopeHint != "" ||
		result.Error.Code != "subscription_proxy_prepare_failed" || executor.calls != 0 {
		t.Fatalf("result/calls = %#v/%d", result, executor.calls)
	}
}

func TestAdapterMapsAnySubscription401ToSafeRefresh(t *testing.T) {
	adapter, _, _, keyService, row := newAdapterFixture(t, credentialJSON("access-secret", "refresh-secret", time.Now().Add(time.Hour)))
	setCodexExecutor(t, adapter, &fakeExecutor{err: statusError{status: http.StatusUnauthorized, message: `{"error":{"type":"authentication_error","code":"auth_unavailable","message":"access-secret expired for a@example.com (account-1)"}}`}})
	result := adapter.Execute(t.Context(), validSpec(t, row, keyService))
	if result.Error == nil || result.Error.Hint != execution.FailureHintRefreshRequired ||
		result.Error.ReplaySafety != execution.ReplaySafetyRejectedBeforeProcessing {
		t.Fatalf("error evidence = %#v", result.Error)
	}
	if result.Error.Summary == "" || strings.Contains(result.Error.Summary, "access-secret") ||
		strings.Contains(result.Error.Summary, "a@example.com") || strings.Contains(result.Error.Summary, "account-1") {
		t.Fatalf("unsafe summary = %q", result.Error.Summary)
	}
}

func TestAdapterMapsCodexQuotaFailureToRateLimit(t *testing.T) {
	adapter, _, _, keyService, row := newAdapterFixture(t, credentialJSON("access", "refresh", time.Now().Add(time.Hour)))
	setCodexExecutor(t, adapter, &fakeExecutor{err: statusError{
		status:  http.StatusTooManyRequests,
		message: `{"error":{"type":"rate_limit_error","code":"quota_exceeded"}}`,
	}})
	result := adapter.Execute(t.Context(), validSpec(t, row, keyService))
	if result.Error == nil || result.Error.Hint != execution.FailureHintRateLimited {
		t.Fatalf("error evidence = %#v", result.Error)
	}
}

func TestAdapterMapsExplicitExpiredTokenToSafeRefresh(t *testing.T) {
	adapter, _, _, keyService, row := newAdapterFixture(t, credentialJSON("access-secret", "refresh-secret", time.Now().Add(time.Hour)))
	setCodexExecutor(t, adapter, &fakeExecutor{err: statusError{status: http.StatusUnauthorized, message: `{"error":{"type":"authentication_error","code":"token_expired","message":"access token expired"}}`}})
	result := adapter.Execute(t.Context(), validSpec(t, row, keyService))
	if result.Error == nil || result.Error.Hint != execution.FailureHintRefreshRequired || result.Error.ReplaySafety != execution.ReplaySafetyRejectedBeforeProcessing {
		t.Fatalf("error evidence = %#v", result.Error)
	}
}

func TestAdapterExecutesEverySupportedClientProtocolThroughCPA(t *testing.T) {
	tests := []struct {
		name       string
		protocol   protocol.Protocol
		operation  execution.Operation
		wantFormat string
		wantAPI    protocol.Protocol
	}{
		{name: "OpenAI Chat", protocol: protocol.OpenAICompletions, operation: execution.OperationChatCompletion, wantFormat: "openai", wantAPI: protocol.OpenAIResponses},
		{name: "OpenAI Responses", protocol: protocol.OpenAIResponses, operation: execution.OperationResponsesCreate, wantFormat: "openai-response", wantAPI: protocol.OpenAIResponses},
		{name: "Anthropic Messages", protocol: protocol.Anthropic, operation: execution.OperationChatCompletion, wantFormat: "claude", wantAPI: protocol.OpenAIResponses},
		{name: "Gemini GenerateContent", protocol: protocol.Gemini, operation: execution.OperationChatCompletion, wantFormat: "gemini", wantAPI: protocol.OpenAIResponses},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			adapter, _, _, keyService, row := newAdapterFixture(t, credentialJSON("access", "refresh", time.Now().Add(time.Hour)))
			fake := &fakeExecutor{result: codex.ExecuteResponse{
				Payload: []byte(`{"model":"gpt-5"}`),
				Headers: http.Header{
					"Content-Type":     {"text/event-stream"},
					"Content-Encoding": {"gzip"},
					"Content-Length":   {"999"},
					"ETag":             {`"upstream"`},
					"Digest":           {"sha-256=upstream"},
					"Content-MD5":      {"upstream"},
					"Content-Range":    {"bytes 0-998/999"},
					"Content-Digest":   {"sha-256=:dXBzdHJlYW0=:"},
					"Repr-Digest":      {"sha-256=:dXBzdHJlYW0=:"},
					"Signature":        {"sig1=:dXBzdHJlYW0=:"},
					"Signature-Input":  {`sig1=("content-digest")`},
					"X-Request-Id":     {"upstream-1"},
				},
			}}
			setCodexExecutor(t, adapter, fake)
			spec := validSpec(t, row, keyService)
			spec.ClientProtocol = test.protocol
			spec.Operation = test.operation
			if test.protocol != protocol.OpenAIResponses {
				spec.RouteMode = execution.RouteConverted
			}

			result := adapter.Execute(t.Context(), spec)
			if result.Error != nil || fake.calls != 1 || fake.request.Format != test.wantFormat || result.UpstreamProtocol != test.wantAPI {
				t.Fatalf("result=%#v calls=%d request=%#v", result, fake.calls, fake.request)
			}
			if got := result.Header.Get("Content-Type"); got != "application/json" {
				t.Fatalf("Content-Type = %q, want application/json", got)
			}
			for _, name := range []string{"Content-Encoding", "Content-Length", "ETag", "Digest", "Content-MD5", "Content-Range", "Content-Digest", "Repr-Digest", "Signature", "Signature-Input"} {
				if value := result.Header.Get(name); value != "" {
					t.Fatalf("stale %s = %q", name, value)
				}
			}
			if result.Header.Get("X-Request-Id") != "upstream-1" {
				t.Fatalf("request ID header = %q", result.Header.Get("X-Request-Id"))
			}
		})
	}
}

func TestAdapterExecutesCodexImagesWithFixedCanonicalPath(t *testing.T) {
	t.Parallel()

	adapter, _, _, keyService, row := newAdapterFixture(t, credentialJSON("access", "refresh", time.Now().Add(time.Hour)))
	fake := &fakeExecutor{result: codex.ExecuteResponse{
		Payload: []byte(`{"created":1,"data":[{"b64_json":"AA=="}],"usage":{"input_tokens":9}}`),
	}}
	setCodexExecutor(t, adapter, fake)
	spec := validSpec(t, row, keyService)
	spec.ClientProtocol = protocol.OpenAIImages
	spec.Operation = execution.OperationImagesGenerate
	spec.RouteMode = execution.RouteNative
	spec.RouteRequirement = execution.RouteRequirementNative
	spec.ClientModel = "public-image"
	spec.UpstreamModel = "provider-image"
	spec.Path = "/v1/images/generations"
	spec.Header = http.Header{"Content-Type": {"application/json"}}
	spec.Body = []byte(`{"model":"public-image","stream":true,"prompt":"draw","provider":"client","api_key":"client","future":{"keep":true}}`)

	result := adapter.Execute(t.Context(), spec)
	if err := result.Validate(); err != nil || result.Error != nil || fake.calls != 1 {
		t.Fatalf("result/calls = %+v/%d, validation=%v", result, fake.calls, err)
	}
	if fake.request.Format != "openai-image" || fake.request.RequestPath != "/v1/images/generations" {
		t.Fatalf("CPA Images request = %#v", fake.request)
	}
	var body map[string]json.RawMessage
	if err := json.Unmarshal(fake.request.Payload, &body); err != nil {
		t.Fatal(err)
	}
	if string(body["model"]) != `"provider-image"` || string(body["stream"]) != "false" ||
		string(body["future"]) != `{"keep":true}` {
		t.Fatalf("sanitized payload = %s", fake.request.Payload)
	}
	for _, field := range []string{"provider", "api_key"} {
		if _, exists := body[field]; exists {
			t.Fatalf("control field %q reached CPA: %s", field, fake.request.Payload)
		}
	}
	if !bytes.Equal(fake.request.OriginalRequest, fake.request.Payload) {
		t.Fatalf("CPA original request retained unsanitized body: %s / %s", fake.request.OriginalRequest, fake.request.Payload)
	}
	if result.Usage != nil {
		t.Fatalf("Images usage = %#v, want nil", result.Usage)
	}
}

func TestNormalizeCPAImagesResultsKeepOnlyObservedModel(t *testing.T) {
	spec := execution.AttemptSpec{ClientProtocol: protocol.OpenAIImages}

	missing := execution.AttemptResult{
		Model: "provider-image",
		Body:  []byte(`{"created":1,"data":[{"b64_json":"AA=="}]}`),
	}
	normalizeCPAImagesAttemptResult(spec, &missing)
	if missing.Model != "" {
		t.Fatalf("missing response model = %q, want empty", missing.Model)
	}

	observed := execution.AttemptResult{
		Model: "provider-image",
		Body:  []byte(`{"model":"public-image","data":[{"b64_json":"AA=="}]}`),
	}
	normalizeCPAImagesAttemptResult(spec, &observed)
	if observed.Model != "provider-image" {
		t.Fatalf("observed response model = %q, want provider-image", observed.Model)
	}

	stream := execution.StreamResult{Model: "provider-image"}
	normalizeCPAImagesStreamResult(spec, &stream)
	if stream.Model != "" {
		t.Fatalf("unobserved stream model = %q, want empty", stream.Model)
	}
}

func TestAdapterReportsDirectCodexImagesUpstreamProtocol(t *testing.T) {
	t.Parallel()

	adapter, _, _, keyService, row := newAdapterFixture(
		t,
		credentialJSON("access", "refresh", time.Now().Add(time.Hour)),
	)
	var upstreamPath string
	transport := roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		upstreamPath = request.URL.Path
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Header:     http.Header{"Content-Type": {"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"created":1,"data":[{"b64_json":"AA=="}]}`)),
			Request:    request,
		}, nil
	})
	ctx := context.WithValue(t.Context(), "cliproxy.roundtripper", http.RoundTripper(transport))
	spec := validSpec(t, row, keyService)
	spec.ClientProtocol = protocol.OpenAIImages
	spec.Operation = execution.OperationImagesGenerate
	spec.RouteMode = execution.RouteNative
	spec.RouteRequirement = execution.RouteRequirementNative
	spec.ClientModel = "gpt-image-2"
	spec.UpstreamModel = "gpt-image-2"
	spec.Path = "/v1/images/generations"
	spec.Header = http.Header{"Content-Type": {"application/json"}}
	spec.Body = []byte(`{"model":"gpt-image-2","prompt":"draw"}`)

	result := adapter.Execute(ctx, spec)
	if err := result.Validate(); err != nil || result.Error != nil {
		t.Fatalf("result = %+v, validation=%v", result, err)
	}
	if upstreamPath != "/backend-api/codex/images/generations" {
		t.Fatalf("upstream path = %q, want Codex Images endpoint", upstreamPath)
	}
	if result.UpstreamProtocol != protocol.OpenAIImages {
		t.Fatalf("upstream protocol = %q, want %q", result.UpstreamProtocol, protocol.OpenAIImages)
	}
}

func TestAdapterExecutesCodexImagesMultipartAndStream(t *testing.T) {
	t.Parallel()

	t.Run("multipart edit", func(t *testing.T) {
		adapter, _, _, keyService, row := newAdapterFixture(t, credentialJSON("access", "refresh", time.Now().Add(time.Hour)))
		fake := &fakeExecutor{result: codex.ExecuteResponse{Payload: []byte(`{"created":1,"data":[{"b64_json":"AA=="}]}`)}}
		setCodexExecutor(t, adapter, fake)
		body, contentType := cpaImagesMultipart(t)
		spec := validSpec(t, row, keyService)
		spec.ClientProtocol = protocol.OpenAIImages
		spec.Operation = execution.OperationImagesEdit
		spec.RouteRequirement = execution.RouteRequirementNative
		spec.ClientModel = "public-image"
		spec.UpstreamModel = "provider-image"
		spec.Path = "/v1/images/edits"
		spec.Header = http.Header{"Content-Type": {contentType}}
		spec.Body = body

		result := adapter.Execute(t.Context(), spec)
		if err := result.Validate(); err != nil || result.Error != nil || fake.calls != 1 {
			t.Fatalf("result/calls = %+v/%d, validation=%v", result, fake.calls, err)
		}
		if fake.request.Format != "openai-image" || fake.request.RequestPath != "/v1/images/edits" {
			t.Fatalf("CPA multipart request = %#v", fake.request)
		}
		mediaType, params, err := mime.ParseMediaType(fake.request.Headers.Get("Content-Type"))
		if err != nil || mediaType != "multipart/form-data" {
			t.Fatalf("Content-Type = %q: %v", fake.request.Headers.Get("Content-Type"), err)
		}
		reader := multipart.NewReader(bytes.NewReader(fake.request.Payload), params["boundary"])
		seen := map[string][]byte{}
		for {
			part, err := reader.NextRawPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatal(err)
			}
			_, disposition, _ := mime.ParseMediaType(part.Header.Get("Content-Disposition"))
			seen[disposition["name"]], _ = io.ReadAll(part)
		}
		if string(seen["model"]) != "provider-image" || string(seen["stream"]) != "false" ||
			string(seen["image[]"]) != "png-data" || seen["api_key"] != nil {
			t.Fatalf("multipart fields = %#v", seen)
		}
	})

	t.Run("stream generation", func(t *testing.T) {
		adapter, _, _, keyService, row := newAdapterFixture(t, credentialJSON("access", "refresh", time.Now().Add(time.Hour)))
		chunks := make(chan codex.ExecuteStreamChunk, 2)
		chunks <- codex.ExecuteStreamChunk{Payload: []byte("event: image_generation.partial_image\ndata: {\"type\":\"image_generation.partial_image\",\"b64_json\":\"AA==\"}\n")}
		chunks <- codex.ExecuteStreamChunk{Payload: []byte("event: image_generation.completed\ndata: {\"type\":\"image_generation.completed\",\"b64_json\":\"BB==\"}\n")}
		close(chunks)
		fake := &fakeExecutor{stream: &codex.ExecuteStreamResponse{
			Chunks:              chunks,
			UpstreamRequestPath: "/backend-api/codex/images/generations",
		}}
		setCodexExecutor(t, adapter, fake)
		spec := validSpec(t, row, keyService)
		spec.ClientProtocol = protocol.OpenAIImages
		spec.Operation = execution.OperationImagesGenerate
		spec.RouteRequirement = execution.RouteRequirementNative
		spec.ClientModel = "public-image"
		spec.UpstreamModel = "provider-image"
		spec.Path = "/v1/images/generations"
		spec.Header = http.Header{"Content-Type": {"application/json"}}
		spec.Body = []byte(`{"model":"public-image","prompt":"draw"}`)
		var events []execution.StreamEvent
		result := adapter.ExecuteStream(t.Context(), spec, func(event execution.StreamEvent) error {
			events = append(events, event.Clone())
			return nil
		})
		if err := result.Validate(); err != nil || result.Error != nil || fake.calls != 1 {
			t.Fatalf("result/calls = %+v/%d, validation=%v", result, fake.calls, err)
		}
		if result.UpstreamProtocol != protocol.OpenAIImages {
			t.Fatalf("upstream protocol = %q, want %q", result.UpstreamProtocol, protocol.OpenAIImages)
		}
		if fake.request.RequestPath != "/v1/images/generations" || fake.request.Format != "openai-image" ||
			!bytes.Contains(fake.request.Payload, []byte(`"stream":true`)) {
			t.Fatalf("stream CPA request = %#v", fake.request)
		}
		for _, event := range events {
			if event.Kind == execution.StreamEventUsage {
				t.Fatalf("unexpected Images usage event: %+v", event)
			}
		}
	})
}

func TestAdapterRejectsInvalidCodexImagesTuplesBeforeDispatch(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(*execution.AttemptSpec)
	}{
		{name: "wrong method", mutate: func(spec *execution.AttemptSpec) { spec.Method = http.MethodGet }},
		{name: "arbitrary path", mutate: func(spec *execution.AttemptSpec) { spec.Path = "/v1/images/variations" }},
		{name: "operation path mismatch", mutate: func(spec *execution.AttemptSpec) { spec.Operation = execution.OperationImagesEdit }},
		{name: "converted mode", mutate: func(spec *execution.AttemptSpec) { spec.RouteMode = execution.RouteConverted }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			adapter, _, _, keyService, row := newAdapterFixture(t, credentialJSON("access", "refresh", time.Now().Add(time.Hour)))
			fake := &fakeExecutor{}
			setCodexExecutor(t, adapter, fake)
			spec := validSpec(t, row, keyService)
			spec.ClientProtocol = protocol.OpenAIImages
			spec.Operation = execution.OperationImagesGenerate
			spec.RouteRequirement = execution.RouteRequirementNative
			spec.Path = "/v1/images/generations"
			spec.Header = http.Header{"Content-Type": {"application/json"}}
			spec.Body = []byte(`{"model":"gpt-5","prompt":"draw"}`)
			test.mutate(&spec)
			result := adapter.Execute(t.Context(), spec)
			if result.DispatchState != execution.DispatchNotSent || result.Error == nil || fake.calls != 0 {
				t.Fatalf("result/calls = %+v/%d", result, fake.calls)
			}
		})
	}
}

func TestCodexClientImageGenerationReachesSubscriptionExecutor(t *testing.T) {
	gin.SetMode(gin.TestMode)
	adapter, _, credentialRegistry, keyService, row := newAdapterFixture(
		t,
		credentialJSON("access", "refresh", time.Now().Add(time.Hour)),
	)
	fake := &fakeExecutor{result: codex.ExecuteResponse{
		Payload: []byte(`{"created":1713833628,"data":[{"b64_json":"VEVTVA=="}]}`),
	}}
	setCodexExecutor(t, adapter, fake)
	credentialRef, ok := credentialRegistry.CredentialRef(row.ID)
	if !ok {
		t.Fatal("credential ref is unavailable")
	}
	manager := state.NewManager()
	if _, err := manager.Publish(state.CompileInput{
		ChannelRegistry: channel.NewRegistry(),
		Groups: []state.GroupConfig{{
			ID: row.GroupID, Name: "codex", ChannelID: channel.Codex,
			ConnectionType: "subscription", Params: json.RawMessage(`{}`),
			Models: []state.ModelConfig{{ID: "gpt-image-2"}}, Enabled: true,
		}},
		Credentials: []state.CredentialConfig{{
			ID: row.ID, GroupID: row.GroupID, Status: state.CredentialStatusActive,
			Version: credentialRef.Version, IdentityGeneration: credentialRef.IdentityGeneration,
			Fingerprint: credentialRef.Fingerprint,
		}},
		AccessKeys: []state.AccessKeyConfig{{
			ID: 1, Name: "codex-client", KeyHash: keyService.Hash("gl-codex-client"),
			Status: state.AccessKeyStatusActive,
		}},
	}); err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	handler := gateway.NewHandler(
		manager,
		credentialRegistry,
		keyService,
		gateway.NewExecutionForwarder(adapter),
		dialect.NewSet(dialect.NewOpenAIImages()),
		health.NewStatsStore(),
		health.NewMutationCoordinator(),
		nil,
		nil,
		nil,
	)
	engine := gin.New()
	routes, err := httproute.NewRegistry(handler.HTTPModule())
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}
	if err := routes.Bind(engine); err != nil {
		t.Fatalf("Bind() error = %v", err)
	}

	request := httptest.NewRequest(
		http.MethodPost,
		"/v1/images/generations",
		strings.NewReader(`{"model":"gpt-image-2","prompt":"生成一张 T 字母图片","background":"auto","quality":"auto","size":"auto"}`),
	)
	request.Header.Set("Authorization", "Bearer gl-codex-client")
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK || recorder.Body.String() != `{"created":1713833628,"data":[{"b64_json":"VEVTVA=="}]}` {
		t.Fatalf("response = %d headers=%v body=%s", recorder.Code, recorder.Header(), recorder.Body.String())
	}
	if fake.calls != 1 || fake.request.Format != "openai-image" ||
		fake.request.RequestPath != "/v1/images/generations" ||
		!bytes.Contains(fake.request.Payload, []byte(`"model":"gpt-image-2"`)) {
		t.Fatalf("Codex executor request = calls=%d %#v", fake.calls, fake.request)
	}
}

func cpaImagesMultipart(t *testing.T) ([]byte, string) {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for name, value := range map[string]string{
		"model": "public-image", "prompt": "edit", "stream": "true", "api_key": "client",
	} {
		if err := writer.WriteField(name, value); err != nil {
			t.Fatal(err)
		}
	}
	part, err := writer.CreateFormFile("image[]", "source.png")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.WriteString(part, "png-data")
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return bytes.Clone(body.Bytes()), writer.FormDataContentType()
}

func TestSubscriptionResponseHeadersUseAllowlist(t *testing.T) {
	source := http.Header{
		"Content-Type":                         {"application/upstream"},
		"X-Oai-Request-Id":                     {"oai-request-1"},
		"Retry-After":                          {"3"},
		"X-Codex-Turn-State":                   {"private-state"},
		"X-Codex-Plan-Type":                    {"pro"},
		"X-Codex-Primary-Used-Percent":         {"11"},
		"X-Models-Etag":                        {"private-model-cache"},
		"X-Openai-Proxy-Wasm":                  {"v0.1"},
		"Cf-Ray":                               {"edge-trace"},
		"Nel":                                  {`{"success_fraction":0.01}`},
		"Report-To":                            {`{"group":"cf-nel"}`},
		"Cross-Origin-Opener-Policy":           {"same-origin-allow-popups"},
		"Referrer-Policy":                      {"strict-origin-when-cross-origin"},
		"Strict-Transport-Security":            {"max-age=31536000"},
		"Access-Control-Allow-Origin":          {"*"},
		"Access-Control-Allow-Credentials":     {"true"},
		"Access-Control-Expose-Headers":        {"X-Oai-Request-Id"},
		"Access-Control-Allow-Private-Network": {"true"},
	}
	original := source.Clone()

	headers := subscriptionResponseHeaders(source, "text/event-stream")
	if !reflect.DeepEqual(headers, http.Header{
		"Content-Type":     {"text/event-stream"},
		"Cache-Control":    {"no-cache"},
		"X-Oai-Request-Id": {"oai-request-1"},
		"X-Request-Id":     {"oai-request-1"},
		"Retry-After":      {"3"},
	}) {
		t.Fatalf("subscription response headers = %#v", headers)
	}
	if !reflect.DeepEqual(source, original) {
		t.Fatalf("subscriptionResponseHeaders() mutated source: got %#v, want %#v", source, original)
	}
}

func TestAdapterCountsCodexTokensForEverySupportedProtocol(t *testing.T) {
	tests := []struct {
		name       string
		protocol   protocol.Protocol
		operation  execution.Operation
		routeMode  execution.RouteMode
		wantFormat string
		body       string
		response   string
	}{
		{
			name: "OpenAI Responses", protocol: protocol.OpenAIResponses,
			operation: execution.OperationResponsesInputTokens, routeMode: execution.RouteNative,
			wantFormat: "openai-response", body: `{"model":"gpt-5","input":"hello"}`,
			response: `{"object":"response.input_tokens","input_tokens":7}`,
		},
		{
			name: "Anthropic", protocol: protocol.Anthropic,
			operation: execution.OperationCountTokens, routeMode: execution.RouteConverted,
			wantFormat: "claude", body: `{"model":"gpt-5","messages":[{"role":"user","content":"hello"}]}`,
			response: `{"input_tokens":7}`,
		},
		{
			name: "Gemini", protocol: protocol.Gemini,
			operation: execution.OperationCountTokens, routeMode: execution.RouteConverted,
			wantFormat: "gemini", body: `{"contents":[{"role":"user","parts":[{"text":"hello"}]}]}`,
			response: `{"totalTokens":7}`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			adapter, _, _, keyService, row := newAdapterFixture(t, credentialJSON("access", "refresh", time.Now().Add(time.Hour)))
			preparer := &fakeCredentialPreparer{delegate: adapter.credentials}
			adapter.credentials = preparer
			fake := &fakeExecutor{countResult: codex.ExecuteResponse{Payload: []byte(test.response)}}
			setCodexExecutor(t, adapter, fake)
			spec := validSpec(t, row, keyService)
			spec.ClientProtocol = test.protocol
			spec.Operation = test.operation
			spec.RouteMode = test.routeMode
			spec.Body = []byte(test.body)

			result := adapter.Execute(t.Context(), spec)
			if result.Error != nil || result.DispatchState != execution.DispatchLocal || !result.ResponseStarted ||
				result.UpstreamProtocol != "" || result.Model != "" || result.UpstreamRequestID != "" ||
				result.Header.Get(localTokenCountHeader) != "local-estimate" ||
				preparer.calls != 0 || fake.countCalls != 1 || fake.calls != 0 ||
				fake.request.Format != test.wantFormat || string(result.Body) != test.response {
				t.Fatalf("result=%#v prepareCalls=%d calls=%d countCalls=%d request=%#v", result, preparer.calls, fake.calls, fake.countCalls, fake.request)
			}
		})
	}
}

func TestAdapterRejectsUnsupportedLocalCodexTokenCountInput(t *testing.T) {
	tests := []struct {
		name      string
		protocol  protocol.Protocol
		operation execution.Operation
		routeMode execution.RouteMode
		model     string
		body      string
	}{
		{
			name: "responses previous response", protocol: protocol.OpenAIResponses,
			operation: execution.OperationResponsesInputTokens, routeMode: execution.RouteNative,
			body: `{"model":"gpt-5","previous_response_id":"resp_123","input":"continue"}`,
		},
		{
			name: "responses image", protocol: protocol.OpenAIResponses,
			operation: execution.OperationResponsesInputTokens, routeMode: execution.RouteNative,
			body: `{"model":"gpt-5","input":[{"type":"message","role":"user","content":[{"type":"input_image","image_url":"https://example.test/image.png"}]}]}`,
		},
		{
			name: "anthropic document", protocol: protocol.Anthropic,
			operation: execution.OperationCountTokens, routeMode: execution.RouteConverted,
			body: `{"model":"gpt-5","messages":[{"role":"user","content":[{"type":"document","source":{"type":"base64","data":"AA=="}}]}]}`,
		},
		{
			name: "gemini inline data", protocol: protocol.Gemini,
			operation: execution.OperationCountTokens, routeMode: execution.RouteConverted,
			body: `{"contents":[{"role":"user","parts":[{"inlineData":{"mimeType":"image/png","data":"AA=="}}]}]}`,
		},
		{
			name: "unknown tokenizer model", protocol: protocol.OpenAIResponses,
			operation: execution.OperationResponsesInputTokens, routeMode: execution.RouteNative,
			model: "codex-unknown", body: `{"model":"codex-unknown","input":"hello"}`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			adapter, _, _, keyService, row := newAdapterFixture(t, credentialJSON("access", "refresh", time.Now().Add(time.Hour)))
			preparer := &fakeCredentialPreparer{delegate: adapter.credentials}
			adapter.credentials = preparer
			fake := &fakeExecutor{countResult: codex.ExecuteResponse{Payload: []byte(`{"input_tokens":7}`)}}
			setCodexExecutor(t, adapter, fake)
			spec := validSpec(t, row, keyService)
			spec.ClientProtocol = test.protocol
			spec.Operation = test.operation
			spec.RouteMode = test.routeMode
			spec.Body = []byte(test.body)
			if test.model != "" {
				spec.ClientModel, spec.UpstreamModel = test.model, test.model
			}

			result := adapter.Execute(t.Context(), spec)
			if result.DispatchState != execution.DispatchNotSent || result.ResponseStarted || result.Error == nil ||
				result.Error.Kind != execution.ErrorKindInvalidRequest || result.Error.Code != "local_token_count_unsupported_input" ||
				preparer.calls != 0 || fake.countCalls != 0 {
				t.Fatalf("result=%#v prepareCalls=%d countCalls=%d", result, preparer.calls, fake.countCalls)
			}
		})
	}
}

func TestAdapterRejectsUnsupportedAntigravityInputBeforeCredentialPreparation(t *testing.T) {
	canonical, err := antigravity.MarshalCredential(antigravity.Credential{
		Type: "antigravity", AccessToken: "access", RefreshToken: "refresh", AccountID: "google-account-one",
		Email: "owner@example.com", ProjectID: "project-one", Expire: time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
	})
	if err != nil {
		t.Fatal(err)
	}
	adapter, _, _, keyService, row := newSubscriptionAdapterFixture(t, string(channel.Antigravity), canonical, "google-account-one")
	preparer := &fakeCredentialPreparer{delegate: adapter.credentials}
	adapter.credentials = preparer
	spec := validSpec(t, row, keyService)
	spec.ChannelID = string(channel.Antigravity)
	spec.ClientProtocol = protocol.OpenAIResponses
	spec.Operation = execution.OperationResponsesCreate
	spec.RouteMode = execution.RouteConverted
	spec.ClientModel, spec.UpstreamModel = "gemini-live", "gemini-live"
	spec.Body = []byte(`{"input":[{"role":"user","content":[{"type":"input_image","image_url":"https://example.test/image.png"}]}]}`)

	result := adapter.Execute(t.Context(), spec)
	if result.DispatchState != execution.DispatchNotSent || result.ResponseStarted || result.Error == nil ||
		result.Error.Kind != execution.ErrorKindInvalidRequest || result.Error.Code != "unsupported_subscription_input" ||
		preparer.calls != 0 {
		t.Fatalf("result=%#v prepareCalls=%d", result, preparer.calls)
	}
}

func TestAdapterMarksLocalCodexTokenCountFailureNotSent(t *testing.T) {
	adapter, _, _, keyService, row := newAdapterFixture(t, credentialJSON("access", "refresh", time.Now().Add(time.Hour)))
	preparer := &fakeCredentialPreparer{delegate: adapter.credentials}
	adapter.credentials = preparer
	fake := &fakeExecutor{err: errors.New("tokenizer failed")}
	setCodexExecutor(t, adapter, fake)
	spec := validSpec(t, row, keyService)
	spec.Operation = execution.OperationResponsesInputTokens
	spec.Path = "/v1/responses/input_tokens"

	result := adapter.Execute(t.Context(), spec)
	if result.DispatchState != execution.DispatchNotSent || result.ResponseStarted || result.Error == nil ||
		result.Error.Kind != execution.ErrorKindInternal || result.Error.Code != "local_token_count_failed" ||
		preparer.calls != 0 || fake.countCalls != 1 {
		t.Fatalf("result=%#v prepareCalls=%d countCalls=%d", result, preparer.calls, fake.countCalls)
	}
}

func TestAdapterStreamsEverySupportedClientProtocolThroughCPA(t *testing.T) {
	tests := []struct {
		name       string
		protocol   protocol.Protocol
		operation  execution.Operation
		wantFormat string
		wantAPI    protocol.Protocol
		payload    string
		wantData   []string
	}{
		{
			name: "OpenAI Chat", protocol: protocol.OpenAICompletions, operation: execution.OperationChatCompletion,
			wantFormat: "openai", wantAPI: protocol.OpenAIResponses,
			payload:  `{"id":"chatcmpl_1","object":"chat.completion.chunk","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
			wantData: []string{`data: {"id":"chatcmpl_1","object":"chat.completion.chunk","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}` + "\n\n", "data: [DONE]\n\n"},
		},
		{
			name: "OpenAI Responses", protocol: protocol.OpenAIResponses, operation: execution.OperationResponsesCreate,
			wantFormat: "openai-response", wantAPI: protocol.OpenAIResponses,
			payload:  `data: {"type":"response.completed","response":{"id":"resp_1"}}` + "\n\n",
			wantData: []string{`data: {"type":"response.completed","response":{"id":"resp_1"}}` + "\n\n"},
		},
		{
			name: "Anthropic Messages", protocol: protocol.Anthropic, operation: execution.OperationChatCompletion,
			wantFormat: "claude", wantAPI: protocol.OpenAIResponses,
			payload:  "event: message_stop\ndata: {\"type\":\"message_stop\"}",
			wantData: []string{"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"},
		},
		{
			name: "Gemini GenerateContent", protocol: protocol.Gemini, operation: execution.OperationChatCompletion,
			wantFormat: "gemini", wantAPI: protocol.OpenAIResponses,
			payload: `{"candidates":[{"content":{"role":"model","parts":[]}}],"usageMetadata":{"promptTokenCount":1,"candidatesTokenCount":1,"totalTokenCount":2}}`,
			wantData: []string{
				`data: {"candidates":[{"content":{"role":"model","parts":[]}}],"usageMetadata":{"promptTokenCount":1,"candidatesTokenCount":1,"totalTokenCount":2}}` + "\n\n",
				`data: {"candidates":[{"finishReason":"STOP"}]}` + "\n\n",
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			adapter, _, _, keyService, row := newAdapterFixture(t, credentialJSON("access", "refresh", time.Now().Add(time.Hour)))
			chunks := make(chan codex.ExecuteStreamChunk, 1)
			chunks <- codex.ExecuteStreamChunk{Payload: []byte(test.payload)}
			close(chunks)
			fake := &fakeExecutor{stream: &codex.ExecuteStreamResponse{Chunks: chunks}}
			setCodexExecutor(t, adapter, fake)
			spec := validSpec(t, row, keyService)
			spec.ClientProtocol = test.protocol
			spec.Operation = test.operation
			if test.protocol != protocol.OpenAIResponses {
				spec.RouteMode = execution.RouteConverted
			}

			var data []string
			result := adapter.ExecuteStream(t.Context(), spec, func(event execution.StreamEvent) error {
				if event.Kind == execution.StreamEventData {
					data = append(data, string(event.Data))
				}
				return nil
			})
			if result.Error != nil || fake.calls != 1 || fake.request.Format != test.wantFormat || result.UpstreamProtocol != test.wantAPI {
				t.Fatalf("result=%#v calls=%d request=%#v", result, fake.calls, fake.request)
			}
			if strings.Join(data, "") != strings.Join(test.wantData, "") {
				t.Fatalf("stream data = %q, want %q", data, test.wantData)
			}
		})
	}
}

func TestAdapterGroupsNativeResponsesSSELinesIntoCompleteEvents(t *testing.T) {
	adapter, _, _, keyService, row := newAdapterFixture(t, credentialJSON("access", "refresh", time.Now().Add(time.Hour)))
	chunks := make(chan codex.ExecuteStreamChunk, 5)
	for _, payload := range []string{
		"event: response.created",
		`data: {"type":"response.created","response":{"id":"resp_1","model":"gpt-5"}}`,
		"",
		"event: response.completed",
		`data: {"type":"response.completed","response":{"id":"resp_1","model":"gpt-5"}}`,
	} {
		chunks <- codex.ExecuteStreamChunk{Payload: []byte(payload)}
	}
	close(chunks)
	setCodexExecutor(t, adapter, &fakeExecutor{stream: &codex.ExecuteStreamResponse{
		Headers: http.Header{
			"X-Oai-Request-Id":   {"oai-request-1"},
			"X-Codex-Turn-State": {"private-state"},
		},
		Chunks: chunks,
	}})

	var data []string
	var readyHeader http.Header
	result := adapter.ExecuteStream(t.Context(), validSpec(t, row, keyService), func(event execution.StreamEvent) error {
		if event.Kind == execution.StreamEventReady {
			readyHeader = event.Header.Clone()
		}
		if event.Kind == execution.StreamEventData {
			data = append(data, string(event.Data))
		}
		return nil
	})
	if result.Error != nil {
		t.Fatalf("ExecuteStream() error = %#v", result.Error)
	}
	if readyHeader.Get("X-Request-Id") != "oai-request-1" ||
		readyHeader.Get("Cache-Control") != "no-cache" ||
		readyHeader.Values("X-Codex-Turn-State") != nil {
		t.Fatalf("ready headers = %#v", readyHeader)
	}
	want := []string{
		"event: response.created\ndata: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_1\",\"model\":\"gpt-5\"}}\n\n",
		"event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_1\",\"model\":\"gpt-5\"}}\n\n",
	}
	if len(data) != len(want) {
		t.Fatalf("stream data events = %q, want %q", data, want)
	}
	for index := range want {
		if data[index] != want[index] {
			t.Fatalf("stream data event %d = %q, want %q", index, data[index], want[index])
		}
	}
}

func TestAdapterSkipsEmptyChunksOutsideNativeResponsesFraming(t *testing.T) {
	adapter, _, _, keyService, row := newAdapterFixture(t, credentialJSON("access", "refresh", time.Now().Add(time.Hour)))
	chunks := make(chan codex.ExecuteStreamChunk, 2)
	chunks <- codex.ExecuteStreamChunk{}
	chunks <- codex.ExecuteStreamChunk{Payload: []byte(`{"id":"chat_1","choices":[{"finish_reason":"stop"}]}`)}
	close(chunks)
	setCodexExecutor(t, adapter, &fakeExecutor{stream: &codex.ExecuteStreamResponse{Chunks: chunks}})
	spec := validSpec(t, row, keyService)
	spec.ClientProtocol = protocol.OpenAICompletions
	spec.Operation = execution.OperationChatCompletion
	spec.RouteMode = execution.RouteConverted

	var data []string
	result := adapter.ExecuteStream(t.Context(), spec, func(event execution.StreamEvent) error {
		if event.Kind == execution.StreamEventData {
			data = append(data, string(event.Data))
		}
		return nil
	})
	if result.Error != nil {
		t.Fatalf("ExecuteStream() error = %#v", result.Error)
	}
	want := []string{
		"data: {\"id\":\"chat_1\",\"choices\":[{\"finish_reason\":\"stop\"}]}\n\n",
		"data: [DONE]\n\n",
	}
	if !reflect.DeepEqual(data, want) {
		t.Fatalf("stream data = %q, want %q", data, want)
	}
}

func TestAdapterDoesNotCompleteStreamAfterContextCancellation(t *testing.T) {
	adapter, _, _, keyService, row := newAdapterFixture(t, credentialJSON("access", "refresh", time.Now().Add(time.Hour)))
	chunks := make(chan codex.ExecuteStreamChunk)
	close(chunks)
	setCodexExecutor(t, adapter, &fakeExecutor{stream: &codex.ExecuteStreamResponse{Chunks: chunks}})
	spec := validSpec(t, row, keyService)
	spec.ClientProtocol = protocol.OpenAICompletions
	spec.Operation = execution.OperationChatCompletion
	spec.RouteMode = execution.RouteConverted
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	var dataEvents int
	result := adapter.ExecuteStream(ctx, spec, func(event execution.StreamEvent) error {
		if event.Kind == execution.StreamEventData {
			dataEvents++
		}
		return nil
	})
	if result.Error == nil || dataEvents != 0 {
		t.Fatalf("result = %#v, data events = %d", result, dataEvents)
	}
}

func TestAdapterUsesFirstByteTimeoutBeforeFirstData(t *testing.T) {
	adapter, _, _, keyService, row := newAdapterFixture(t, credentialJSON("access", "refresh", time.Now().Add(time.Hour)))
	chunks := make(chan codex.ExecuteStreamChunk, 1)
	setCodexExecutor(t, adapter, &fakeExecutor{stream: &codex.ExecuteStreamResponse{Chunks: chunks}})
	spec := validSpec(t, row, keyService)
	spec.Timeouts.FirstByte = 20 * time.Millisecond
	spec.Timeouts.StreamIdle = 250 * time.Millisecond
	spec.Timeouts.Request = time.Second

	started := time.Now()
	var events []execution.StreamEvent
	result := adapter.ExecuteStream(t.Context(), spec, func(event execution.StreamEvent) error {
		events = append(events, event.Clone())
		return nil
	})
	if elapsed := time.Since(started); elapsed >= 150*time.Millisecond {
		t.Fatalf("first-byte timeout took %s", elapsed)
	}
	if result.Error == nil || result.Error.Kind != execution.ErrorKindTimeout || result.ResponseStarted || result.StatusCode != 0 || len(events) != 0 {
		t.Fatalf("result = %#v, events = %#v", result, events)
	}
}

func TestAdapterSwitchesToStreamIdleTimeoutAfterFirstData(t *testing.T) {
	adapter, _, _, keyService, row := newAdapterFixture(t, credentialJSON("access", "refresh", time.Now().Add(time.Hour)))
	chunks := make(chan codex.ExecuteStreamChunk, 1)
	chunks <- codex.ExecuteStreamChunk{Payload: []byte("data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_1\"}}\n\n")}
	setCodexExecutor(t, adapter, &fakeExecutor{stream: &codex.ExecuteStreamResponse{Chunks: chunks}})
	spec := validSpec(t, row, keyService)
	spec.Timeouts.FirstByte = 200 * time.Millisecond
	spec.Timeouts.StreamIdle = 20 * time.Millisecond
	spec.Timeouts.Request = time.Second

	var events []execution.StreamEvent
	result := adapter.ExecuteStream(t.Context(), spec, func(event execution.StreamEvent) error {
		events = append(events, event.Clone())
		return nil
	})
	if result.Error == nil || result.Error.Kind != execution.ErrorKindTimeout || !result.ResponseStarted || result.StatusCode != http.StatusOK {
		t.Fatalf("result = %#v", result)
	}
	if len(events) != 2 || events[0].Kind != execution.StreamEventReady || events[1].Kind != execution.StreamEventData {
		t.Fatalf("events = %#v", events)
	}
}

func TestAdapterUsesStreamIdleTimeoutBetweenNativeResponsesLines(t *testing.T) {
	adapter, _, _, keyService, row := newAdapterFixture(t, credentialJSON("access", "refresh", time.Now().Add(time.Hour)))
	chunks := make(chan codex.ExecuteStreamChunk, 1)
	chunks <- codex.ExecuteStreamChunk{Payload: []byte("event: response.created")}
	setCodexExecutor(t, adapter, &fakeExecutor{stream: &codex.ExecuteStreamResponse{Chunks: chunks}})
	spec := validSpec(t, row, keyService)
	spec.Timeouts.FirstByte = 200 * time.Millisecond
	spec.Timeouts.StreamIdle = 20 * time.Millisecond
	spec.Timeouts.Request = time.Second

	started := time.Now()
	var events []execution.StreamEvent
	result := adapter.ExecuteStream(t.Context(), spec, func(event execution.StreamEvent) error {
		events = append(events, event.Clone())
		return nil
	})
	if elapsed := time.Since(started); elapsed >= 150*time.Millisecond {
		t.Fatalf("stream idle timeout between SSE lines took %s", elapsed)
	}
	if result.Error == nil || result.Error.Kind != execution.ErrorKindTimeout || result.ResponseStarted || len(events) != 0 {
		t.Fatalf("result = %#v, events = %#v", result, events)
	}
}

func TestAdapterReturnsFirstStreamErrorBeforeReady(t *testing.T) {
	adapter, _, _, keyService, row := newAdapterFixture(t, credentialJSON("access", "refresh", time.Now().Add(time.Hour)))
	chunks := make(chan codex.ExecuteStreamChunk, 1)
	chunks <- codex.ExecuteStreamChunk{Err: statusError{status: http.StatusBadRequest, message: `{"error":{"type":"invalid_request_error","code":"bad_request"}}`}}
	close(chunks)
	setCodexExecutor(t, adapter, &fakeExecutor{stream: &codex.ExecuteStreamResponse{
		Headers: http.Header{"Content-Type": {"text/event-stream"}}, Chunks: chunks,
	}})

	var events []execution.StreamEvent
	result := adapter.ExecuteStream(t.Context(), validSpec(t, row, keyService), func(event execution.StreamEvent) error {
		events = append(events, event.Clone())
		return nil
	})
	if result.Error == nil || result.StatusCode != http.StatusBadRequest ||
		result.Error.ReplaySafety != "" || len(events) != 0 {
		t.Fatalf("result = %#v, events = %#v", result, events)
	}
}

func TestAdapterRewritesStreamModelAliasForEveryProtocol(t *testing.T) {
	tests := []struct {
		name      string
		protocol  protocol.Protocol
		operation execution.Operation
		payload   string
	}{
		{name: "OpenAI Chat", protocol: protocol.OpenAICompletions, operation: execution.OperationChatCompletion, payload: `{"model":"upstream-model","choices":[{"finish_reason":"stop"}]}`},
		{name: "OpenAI Responses", protocol: protocol.OpenAIResponses, operation: execution.OperationResponsesCreate, payload: "data: {\"type\":\"response.completed\",\"response\":{\"model\":\"upstream-model\"}}\n\n"},
		{name: "Anthropic", protocol: protocol.Anthropic, operation: execution.OperationChatCompletion, payload: "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"model\":\"upstream-model\"}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}"},
		{name: "Gemini", protocol: protocol.Gemini, operation: execution.OperationChatCompletion, payload: `{"modelVersion":"upstream-model","candidates":[{"finishReason":"STOP"}]}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			adapter, _, _, keyService, row := newAdapterFixture(t, credentialJSON("access", "refresh", time.Now().Add(time.Hour)))
			chunks := make(chan codex.ExecuteStreamChunk, 1)
			chunks <- codex.ExecuteStreamChunk{Payload: []byte(test.payload)}
			close(chunks)
			setCodexExecutor(t, adapter, &fakeExecutor{stream: &codex.ExecuteStreamResponse{Chunks: chunks}})
			spec := validSpec(t, row, keyService)
			spec.ClientProtocol = test.protocol
			spec.Operation = test.operation
			spec.ClientModel = "public-model"
			spec.UpstreamModel = "upstream-model"
			if test.protocol != protocol.OpenAIResponses {
				spec.RouteMode = execution.RouteConverted
			}

			var wire strings.Builder
			result := adapter.ExecuteStream(t.Context(), spec, func(event execution.StreamEvent) error {
				if event.Kind == execution.StreamEventData {
					_, _ = wire.Write(event.Data)
				}
				return nil
			})
			if result.Error != nil || !strings.Contains(wire.String(), "public-model") || strings.Contains(wire.String(), "upstream-model") {
				t.Fatalf("result = %#v, wire = %q", result, wire.String())
			}
		})
	}
}

func TestAdapterReportsFinalCPAResponsesReasoning(t *testing.T) {
	tests := []struct {
		name       string
		stream     bool
		model      string
		body       string
		wantEffort string
	}{
		{
			name:       "adaptive effort",
			model:      "gpt-5.6-sol",
			body:       `{"model":"gpt-5.6-sol","max_tokens":64,"messages":[{"role":"user","content":"hello"}],"thinking":{"type":"adaptive"},"output_config":{"effort":"max"}}`,
			wantEffort: "max",
		},
		{
			name:       "budget effort stream",
			stream:     true,
			model:      "gpt-5.6-luna",
			body:       `{"model":"gpt-5.6-luna","max_tokens":16000,"messages":[{"role":"user","content":"hello"}],"thinking":{"type":"enabled","budget_tokens":14848},"stream":true}`,
			wantEffort: "high",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			adapter, _, _, keyService, row := newAdapterFixture(t, credentialJSON("access", "refresh", time.Now().Add(time.Hour)))
			wireEffort := make(chan string, 1)
			transport := roundTripperFunc(func(request *http.Request) (*http.Response, error) {
				if got := request.URL.String(); got != "https://chatgpt.com/backend-api/codex/responses" {
					t.Errorf("upstream URL = %q", got)
				}
				var payload struct {
					Reasoning struct {
						Effort string `json:"effort"`
					} `json:"reasoning"`
				}
				if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
					t.Errorf("decode upstream request: %v", err)
				}
				wireEffort <- payload.Reasoning.Effort
				return &http.Response{
					StatusCode: http.StatusOK,
					Status:     "200 OK",
					Header:     http.Header{"Content-Type": {"text/event-stream"}},
					Body: io.NopCloser(strings.NewReader(
						`data: {"type":"response.completed","response":{"id":"resp_1","object":"response","status":"completed","model":"` + test.model + `","output":[],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}}` + "\n\n",
					)),
					Request: request,
				}, nil
			})
			ctx := context.WithValue(t.Context(), "cliproxy.roundtripper", http.RoundTripper(transport))
			spec := validSpec(t, row, keyService)
			spec.ClientProtocol = protocol.Anthropic
			spec.Operation = execution.OperationChatCompletion
			spec.RouteMode = execution.RouteConverted
			spec.UpstreamModel = test.model
			spec.Body = []byte(test.body)

			var resultReasoning string
			var upstreamProtocol protocol.Protocol
			if test.stream {
				result := adapter.ExecuteStream(ctx, spec, func(execution.StreamEvent) error { return nil })
				if result.Error != nil {
					t.Fatalf("ExecuteStream() error = %#v", result.Error)
				}
				upstreamProtocol = result.UpstreamProtocol
				if result.AppliedReasoning != nil {
					resultReasoning = result.AppliedReasoning.Effort
				}
			} else {
				result := adapter.Execute(ctx, spec)
				if result.Error != nil {
					t.Fatalf("Execute() error = %#v", result.Error)
				}
				upstreamProtocol = result.UpstreamProtocol
				if result.AppliedReasoning != nil {
					resultReasoning = result.AppliedReasoning.Effort
				}
			}
			if got := <-wireEffort; got != test.wantEffort {
				t.Fatalf("wire reasoning effort = %q, want %q", got, test.wantEffort)
			}
			if upstreamProtocol != protocol.OpenAIResponses {
				t.Fatalf("upstream API = %q, want %q", upstreamProtocol, protocol.OpenAIResponses)
			}
			if resultReasoning != test.wantEffort {
				t.Fatalf("applied reasoning effort = %q, want %q", resultReasoning, test.wantEffort)
			}
		})
	}
}

func TestAdapterStreamsRealCPAResponsesAsCompleteClientSSE(t *testing.T) {
	tests := []struct {
		name         string
		protocol     protocol.Protocol
		body         string
		wantFragment string
		wantTerminal string
	}{
		{
			name: "OpenAI Responses", protocol: protocol.OpenAIResponses,
			body:         `{"model":"gpt-5.6-luna","input":"hello","stream":true}`,
			wantFragment: "event: response.output_text.delta\ndata: ",
			wantTerminal: "\n\n",
		},
		{
			name: "OpenAI Chat", protocol: protocol.OpenAICompletions,
			body:         `{"model":"gpt-5.6-luna","messages":[{"role":"user","content":"hello"}],"stream":true}`,
			wantFragment: `"object":"chat.completion.chunk"`,
			wantTerminal: "data: [DONE]\n\n",
		},
		{
			name: "Gemini", protocol: protocol.Gemini,
			body:         `{"contents":[{"role":"user","parts":[{"text":"hello"}]}]}`,
			wantFragment: `"text":"hello"`,
			wantTerminal: `data: {"candidates":[{"finishReason":"STOP"}]}` + "\n\n",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			adapter, _, _, keyService, row := newAdapterFixture(t, credentialJSON("access", "refresh", time.Now().Add(time.Hour)))
			transport := roundTripperFunc(func(request *http.Request) (*http.Response, error) {
				if got := request.URL.String(); got != "https://chatgpt.com/backend-api/codex/responses" {
					t.Errorf("upstream URL = %q", got)
				}
				return &http.Response{
					StatusCode: http.StatusOK,
					Status:     "200 OK",
					Header:     http.Header{"Content-Type": {"text/event-stream"}},
					Body: io.NopCloser(strings.NewReader(strings.Join([]string{
						"event: response.created\n" + `data: {"type":"response.created","response":{"id":"resp_1","object":"response","status":"in_progress","created_at":1,"model":"gpt-5.6-luna","output":[]}}`,
						"event: response.output_text.delta\n" + `data: {"type":"response.output_text.delta","response_id":"resp_1","output_index":0,"item_id":"msg_1","content_index":0,"delta":"hello"}`,
						"event: response.completed\n" + `data: {"type":"response.completed","response":{"id":"resp_1","object":"response","status":"completed","model":"gpt-5.6-luna","output":[],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}}`,
					}, "\n\n") + "\n\n")),
					Request: request,
				}, nil
			})
			ctx := context.WithValue(t.Context(), "cliproxy.roundtripper", http.RoundTripper(transport))
			spec := validSpec(t, row, keyService)
			spec.ClientProtocol = test.protocol
			if test.protocol == protocol.OpenAIResponses {
				spec.Operation = execution.OperationResponsesCreate
				spec.RouteMode = execution.RouteNative
			} else {
				spec.Operation = execution.OperationChatCompletion
				spec.RouteMode = execution.RouteConverted
			}
			spec.UpstreamModel = "gpt-5.6-luna"
			spec.ClientModel = "gpt-5.6-luna"
			spec.Body = []byte(test.body)

			var wire strings.Builder
			result := adapter.ExecuteStream(ctx, spec, func(event execution.StreamEvent) error {
				if event.Kind == execution.StreamEventData {
					wantPrefix := []byte("data:")
					if test.protocol == protocol.OpenAIResponses {
						wantPrefix = []byte("event:")
					}
					if !bytes.HasPrefix(event.Data, wantPrefix) {
						t.Errorf("unframed client event = %q", event.Data)
					}
					_, _ = wire.Write(event.Data)
				}
				return nil
			})
			if result.Error != nil || result.UpstreamProtocol != protocol.OpenAIResponses {
				t.Fatalf("result = %#v", result)
			}
			if !strings.Contains(wire.String(), test.wantFragment) || !strings.HasSuffix(wire.String(), test.wantTerminal) {
				t.Fatalf("client wire = %q", wire.String())
			}
			if test.protocol == protocol.OpenAIResponses &&
				!strings.Contains(wire.String(), "event: response.completed\ndata: ") {
				t.Fatalf("Responses terminal event is incomplete: %q", wire.String())
			}
		})
	}
}

func TestAdapterReturnsCodexBootstrapRejectionBeforeReady(t *testing.T) {
	adapter, _, _, keyService, row := newAdapterFixture(t, credentialJSON("access", "refresh", time.Now().Add(time.Hour)))
	setCodexExecutor(t, adapter, &fakeExecutor{
		stream: &codex.ExecuteStreamResponse{UpstreamRequestPath: "/backend-api/codex/responses"},
		err: statusError{
			status:  http.StatusServiceUnavailable,
			message: `{"error":{"type":"service_unavailable_error","code":"server_is_overloaded","message":"try another credential"}}`,
		},
	})

	var events []execution.StreamEvent
	result := adapter.ExecuteStream(t.Context(), validSpec(t, row, keyService), func(event execution.StreamEvent) error {
		events = append(events, event.Clone())
		return nil
	})
	if result.Error == nil || result.StatusCode != http.StatusServiceUnavailable ||
		result.Error.Type != "service_unavailable_error" || result.Error.Code != "server_is_overloaded" ||
		result.Error.ReplaySafety != execution.ReplaySafetyRejectedBeforeProcessing || len(events) != 0 {
		t.Fatalf("result = %#v, events = %#v", result, events)
	}
}

func TestAdapterStreamsReadyThenFramedData(t *testing.T) {
	adapter, _, _, keyService, row := newAdapterFixture(t, credentialJSON("access", "refresh", time.Now().Add(time.Hour)))
	chunks := make(chan codex.ExecuteStreamChunk, 1)
	chunks <- codex.ExecuteStreamChunk{Payload: []byte("data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_1\"}}\n\n")}
	close(chunks)
	setCodexExecutor(t, adapter, &fakeExecutor{stream: &codex.ExecuteStreamResponse{Headers: http.Header{"Content-Type": {"text/event-stream"}}, Chunks: chunks}})
	var events []execution.StreamEvent
	result := adapter.ExecuteStream(t.Context(), validSpec(t, row, keyService), func(event execution.StreamEvent) error {
		events = append(events, event.Clone())
		return nil
	})
	if result.Error != nil || len(events) != 2 || events[0].Kind != execution.StreamEventReady || events[1].Kind != execution.StreamEventData || string(events[1].Data[len(events[1].Data)-2:]) != "\n\n" {
		t.Fatalf("result=%#v events=%#v", result, events)
	}
}

type statusError struct {
	status  int
	message string
}

func (e statusError) Error() string   { return e.message }
func (e statusError) StatusCode() int { return e.status }

func TestUnaryExecutionErrorDistinguishesDefinitelyUnsentNetworkFailures(t *testing.T) {
	t.Parallel()

	bridge := newCodexProviderBridge()
	credential := codexProviderCredential{}
	tests := []struct {
		name string
		err  error
		want execution.DispatchState
	}{
		{
			name: "DNS lookup failed",
			err:  &net.DNSError{Err: "no such host", Name: "upstream.invalid"},
			want: execution.DispatchNotSent,
		},
		{
			name: "dial failed",
			err:  &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")},
			want: execution.DispatchNotSent,
		},
		{
			name: "read failed",
			err:  &net.OpError{Op: "read", Net: "tcp", Err: io.ErrUnexpectedEOF},
			want: execution.DispatchMaybeSent,
		},
		{
			name: "read wrapped DNS failure",
			err: &net.OpError{Op: "read", Net: "tcp", Err: &net.DNSError{
				Err: "temporary lookup failure", Name: "upstream.invalid",
			}},
			want: execution.DispatchMaybeSent,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := unaryExecutionError(t.Context(), bridge, test.err, credential)
			if result.DispatchState != test.want {
				t.Fatalf("unaryExecutionError() dispatch = %q, want %q", result.DispatchState, test.want)
			}
		})
	}

	responseEvidence := &recordingProviderBridge{
		classificationEvidence: &execution.ErrorEvidence{
			Kind: execution.ErrorKindHTTP, StatusCode: http.StatusBadGateway,
			Summary: "upstream response started",
		},
	}
	result := unaryExecutionError(t.Context(), responseEvidence, &net.DNSError{
		Err: "no such host", Name: "upstream.invalid",
	}, nil)
	if result.DispatchState != execution.DispatchMaybeSent {
		t.Fatalf("evidence status dispatch = %q, want %q", result.DispatchState, execution.DispatchMaybeSent)
	}
}

func newAdapterFixture(t *testing.T, canonical []byte) (*Adapter, *gorm.DB, *state.CredentialRegistry, encryption.Service, models.Credential) {
	t.Helper()
	credential, err := codex.ParseCredentialJSON(canonical)
	if err != nil {
		t.Fatal(err)
	}
	return newSubscriptionAdapterFixture(t, string(channel.Codex), canonical, credential.AccountID)
}

func newSubscriptionAdapterFixture(
	t *testing.T,
	channelID string,
	canonical []byte,
	identity string,
) (*Adapter, *gorm.DB, *state.CredentialRegistry, encryption.Service, models.Credential) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{TranslateError: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&models.Group{}, &models.Credential{}); err != nil {
		t.Fatal(err)
	}
	keyService := encryptiontest.Service(t, "cpa-adapter-test-encryption-key-material")
	ciphertext, err := keyService.Encrypt(string(canonical))
	if err != nil {
		t.Fatal(err)
	}
	group := models.Group{Name: "subscription", ChannelID: channelID, ConnectionType: models.ConnectionTypeSubscription, Params: models.JSON(`{}`), Models: models.JSON(`[]`), Overrides: models.JSON(`{}`), Enabled: true}
	if err := db.Create(&group).Error; err != nil {
		t.Fatal(err)
	}
	row := models.Credential{GroupID: group.ID, Data: ciphertext, Fingerprint: keyService.Hash(string(canonical)), IdentityFingerprint: keyService.Hash("identity|" + identity), SecretVersion: 1, AuthState: models.CredentialAuthStateReady, Status: models.CredentialStatusActive}
	if err := db.Create(&row).Error; err != nil {
		t.Fatal(err)
	}
	registry := state.NewCredentialRegistry()
	identityGeneration := stateloader.CredentialIdentityGeneration(row.IdentityFingerprint, group.ChannelID, string(group.ConnectionType), json.RawMessage(group.Params))
	if err := registry.ReplaceCredentials([]state.CredentialEntry{{ID: row.ID, GroupID: group.ID, Version: 1, IdentityGeneration: identityGeneration, Fingerprint: row.Fingerprint, Status: state.CredentialStatusActive, WeightAuto: state.DefaultWeight, EncryptedValue: row.Data}}); err != nil {
		t.Fatal(err)
	}
	channels := channel.NewRegistry()
	subscriptions, err := subscriptionruntime.NewRuntime(channels, subscriptionproviders.Implementations()...)
	if err != nil {
		t.Fatal(err)
	}
	credentials := subscription.NewCredentialManager(db, keyService, registry, health.NewMutationCoordinator(), subscriptions)
	return NewAdapter(credentials, channels), db, registry, keyService, row
}

func credentialJSON(access, refresh string, expires time.Time) []byte {
	value, _ := codex.MarshalCredential(codex.Credential{Type: codex.Provider, AccessToken: access, RefreshToken: refresh, AccountID: "account-1", Email: "a@example.com", Expire: expires.UTC().Format(time.RFC3339)})
	return value
}

func validSpec(t *testing.T, row models.Credential, keyService encryption.Service) execution.AttemptSpec {
	t.Helper()
	plaintext, err := keyService.Decrypt(row.Data)
	if err != nil {
		t.Fatal(err)
	}
	return execution.NewAttemptSpec(execution.AttemptSpec{RequestID: "request-1", AttemptID: "attempt-1", Sequence: 1, ChannelID: "codex", RouteMode: execution.RouteNative, ClientProtocol: protocol.OpenAIResponses, Operation: execution.OperationResponsesCreate, ClientModel: "gpt-5", UpstreamModel: "gpt-5", Method: http.MethodPost, Path: "/v1/responses", Body: []byte(`{"model":"gpt-5","input":"hi"}`), Credential: execution.NewCredentialSnapshot(row.ID, row.SecretVersion, 1, []byte(plaintext))})
}
