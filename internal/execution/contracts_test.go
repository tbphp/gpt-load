package execution

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"gpt-load/internal/protocol"
	"gpt-load/internal/reasoning"
	"gpt-load/internal/usage"
)

func TestOperationAndDispatchEnums(t *testing.T) {
	operations := []Operation{
		OperationChatCompletion,
		OperationResponsesCreate,
		OperationResponsesRetrieve,
		OperationResponsesDelete,
		OperationResponsesCancel,
		OperationResponsesInputItems,
		OperationResponsesCompact,
		OperationResponsesInputTokens,
		OperationResponsesPassthrough,
		OperationListModels,
		OperationProbe,
	}
	for _, operation := range operations {
		if !operation.Valid() {
			t.Fatalf("expected operation %q to be valid", operation)
		}
	}
	if Operation("unknown").Valid() {
		t.Fatal("expected unknown operation to be invalid")
	}
	for _, mode := range []RouteMode{RouteNative, RouteConverted} {
		if !mode.Valid() {
			t.Fatalf("expected route mode %q to be valid", mode)
		}
	}
	if RouteMode("fallback").Valid() {
		t.Fatal("expected unknown route mode to be invalid")
	}
	for _, upstreamAPI := range []UpstreamAPI{
		UpstreamAPIOpenAIChatCompletions,
		UpstreamAPIOpenAIResponses,
		UpstreamAPIAnthropicMessages,
		UpstreamAPIGeminiGenerateContent,
		UpstreamAPIOpenAIModels,
		UpstreamAPIAnthropicModels,
		UpstreamAPIGeminiModels,
		UpstreamAPIAzureOpenAI,
		UpstreamAPIAWSBedrock,
		UpstreamAPIGoogleVertex,
	} {
		if !upstreamAPI.Valid() {
			t.Fatalf("expected upstream API %q to be valid", upstreamAPI)
		}
	}
	if UpstreamAPI("unknown").Valid() {
		t.Fatal("expected unknown upstream API to be invalid")
	}
	for _, feature := range []Feature{
		FeatureStreaming,
		FeatureTools,
		FeatureReasoning,
		FeatureMultimodal,
		FeatureStructuredOutput,
		FeatureNativeResourceSemantics,
	} {
		if !feature.Valid() {
			t.Fatalf("expected feature %q to be valid", feature)
		}
	}
	if Feature("unknown").Valid() {
		t.Fatal("expected unknown feature to be invalid")
	}

	for _, state := range []DispatchState{DispatchNotSent, DispatchMaybeSent} {
		if !state.Valid() {
			t.Fatalf("expected dispatch state %q to be valid", state)
		}
	}
	if DispatchState("sent").Valid() {
		t.Fatal("expected unknown dispatch state to be invalid")
	}
	for _, kind := range []ErrorKind{
		ErrorKindTransport,
		ErrorKindTimeout,
		ErrorKindCanceled,
		ErrorKindHTTP,
		ErrorKindProvider,
		ErrorKindInvalidRequest,
		ErrorKindInternal,
	} {
		if !kind.Valid() {
			t.Fatalf("expected error kind %q to be valid", kind)
		}
	}
	if ErrorKind("unknown").Valid() {
		t.Fatal("expected unknown error kind to be invalid")
	}
}

func TestCapabilitySetIsValidatedAndDefensivelyCopied(t *testing.T) {
	chatFeatures, err := NewFeatureSet(FeatureStreaming, FeatureTools, FeatureStreaming)
	if err != nil {
		t.Fatalf("NewFeatureSet() error = %v", err)
	}
	set, err := NewCapabilitySet(
		Capability{Operation: OperationResponsesCreate},
		Capability{Operation: OperationChatCompletion, Features: chatFeatures},
		Capability{Operation: OperationResponsesCreate},
	)
	if err != nil {
		t.Fatalf("NewCapabilitySet() error = %v", err)
	}
	if err := set.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if !set.Has(OperationChatCompletion) || !set.Has(OperationResponsesCreate) {
		t.Fatal("expected configured operations to be present")
	}
	if set.Has(OperationProbe) {
		t.Fatal("expected an unconfigured operation to be absent")
	}
	requiredStreaming, err := NewFeatureSet(FeatureStreaming)
	if err != nil {
		t.Fatalf("NewFeatureSet() error = %v", err)
	}
	requiredReasoning, err := NewFeatureSet(FeatureReasoning)
	if err != nil {
		t.Fatalf("NewFeatureSet() error = %v", err)
	}
	if !set.Supports(OperationChatCompletion, requiredStreaming) {
		t.Fatal("expected chat completion streaming to be supported")
	}
	if set.Supports(OperationChatCompletion, requiredReasoning) {
		t.Fatal("expected unsupported reasoning feature to be rejected")
	}
	if !set.Features(OperationChatCompletion).Has(FeatureTools) {
		t.Fatal("expected operation features to be returned")
	}

	want := []Operation{OperationChatCompletion, OperationResponsesCreate}
	got := set.Operations()
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("Operations() = %v, want %v", got, want)
	}
	got[0] = OperationProbe
	if !set.Has(OperationChatCompletion) || set.Has(OperationProbe) {
		t.Fatal("mutating Operations() result changed the set")
	}

	clone := set.Clone()
	clone.capabilities[OperationProbe] = FeatureSet{}
	clone.capabilities[OperationChatCompletion].features[FeatureReasoning] = struct{}{}
	if set.Has(OperationProbe) {
		t.Fatal("mutating clone changed the original set")
	}
	if set.Features(OperationChatCompletion).Has(FeatureReasoning) {
		t.Fatal("mutating cloned features changed the original set")
	}

	if _, err := NewCapabilitySet(Capability{Operation: Operation("unknown")}); err == nil {
		t.Fatal("expected an invalid operation to be rejected")
	}
	if _, err := NewFeatureSet(Feature("unknown")); err == nil {
		t.Fatal("expected an invalid feature to be rejected")
	}
}

func TestCredentialSnapshotProtectsSecretDataAndJSON(t *testing.T) {
	secret := []byte(`{"api_key":"sk-secret-value"}`)
	credential := NewCredentialSnapshot(41, 7, 3, secret)
	secret[0] = 'X'
	if got := string(credential.Data()); got != `{"api_key":"sk-secret-value"}` {
		t.Fatalf("Data() = %q after input mutation", got)
	}

	copyOfData := credential.Data()
	copyOfData[0] = 'Y'
	if got := string(credential.Data()); got != `{"api_key":"sk-secret-value"}` {
		t.Fatalf("Data() = %q after returned copy mutation", got)
	}

	clone := credential.Clone()
	clone.data[0] = 'Z'
	if got := string(credential.Data()); got != `{"api_key":"sk-secret-value"}` {
		t.Fatalf("Data() = %q after clone mutation", got)
	}

	encoded, err := json.Marshal(credential)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	if strings.Contains(string(encoded), "sk-secret-value") || strings.Contains(string(encoded), "api_key") {
		t.Fatalf("credential JSON leaked secret data: %s", encoded)
	}
	if !strings.Contains(string(encoded), `"id":41`) || !strings.Contains(string(encoded), `"version":7`) || !strings.Contains(string(encoded), `"identity_generation":3`) {
		t.Fatalf("credential JSON omitted identity metadata: %s", encoded)
	}
}

func TestAttemptSpecOwnsReferenceBackedValues(t *testing.T) {
	credentialData := []byte(`{"api_key":"sk-secret-value"}`)
	raw := validAttemptSpec(credentialData)
	owned := NewAttemptSpec(raw)

	raw.Header.Set("X-Test", "mutated")
	raw.Query.Set("api-version", "mutated")
	raw.Body[0] = 'X'
	raw.TargetConfig[0] = 'Y'
	credentialData[0] = 'Y'
	if got := owned.Header.Get("X-Test"); got != "original" {
		t.Fatalf("owned header = %q, want original", got)
	}
	if got := owned.Query.Get("api-version"); got != "2026-01-01" {
		t.Fatalf("owned query = %q, want original", got)
	}
	if got := string(owned.Body); got != `{"model":"client-model"}` {
		t.Fatalf("owned body = %q", got)
	}
	if got := string(owned.TargetConfig); got != `{"base_url":"https://upstream.example"}` {
		t.Fatalf("owned target config = %q", got)
	}
	if got := string(owned.Credential.Data()); got != `{"api_key":"sk-secret-value"}` {
		t.Fatalf("owned credential = %q", got)
	}

	clone := owned.Clone()
	clone.Header.Set("X-Test", "clone")
	clone.Query.Set("api-version", "clone")
	clone.Body[0] = 'Z'
	clone.TargetConfig[0] = 'Q'
	clone.RequiredFeatures.features[FeatureReasoning] = struct{}{}
	clone.Credential.data[0] = 'Q'
	if owned.Header.Get("X-Test") != "original" || owned.Query.Get("api-version") != "2026-01-01" {
		t.Fatal("mutating clone changed original maps")
	}
	if string(owned.Body) != `{"model":"client-model"}` || string(owned.Credential.Data()) != `{"api_key":"sk-secret-value"}` {
		t.Fatal("mutating clone changed original byte slices")
	}
	if string(owned.TargetConfig) != `{"base_url":"https://upstream.example"}` {
		t.Fatal("mutating clone changed original raw messages")
	}
	if owned.RequiredFeatures.Has(FeatureReasoning) {
		t.Fatal("mutating clone changed original required features")
	}

	encoded, err := json.Marshal(owned)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	if strings.Contains(string(encoded), "sk-secret-value") || strings.Contains(string(encoded), "api_key") {
		t.Fatalf("attempt JSON leaked credential data: %s", encoded)
	}
}

func TestAttemptSpecRawQueryPreservesOpaqueBytesAndRejectsUnsafeShapes(t *testing.T) {
	t.Parallel()

	spec := validAttemptSpec([]byte(`{"api_key":"sk-secret-value"}`))
	spec.Query = nil
	spec.RawQuery = "first=%2F&broken=%ZZ&first=%2f&&empty="
	owned := NewAttemptSpec(spec)
	if owned.RawQuery != spec.RawQuery || owned.Clone().RawQuery != spec.RawQuery {
		t.Fatalf("RawQuery was changed: owned=%q clone=%q", owned.RawQuery, owned.Clone().RawQuery)
	}
	if err := owned.Validate(); err != nil {
		t.Fatalf("opaque RawQuery Validate() error = %v", err)
	}

	for _, test := range []struct {
		name     string
		rawQuery string
	}{
		{name: "fragment", rawQuery: "trace=kept#fragment"},
		{name: "newline", rawQuery: "trace=kept\nInjected=true"},
		{name: "nul", rawQuery: "trace=kept\x00"},
	} {
		t.Run(test.name, func(t *testing.T) {
			invalid := spec.Clone()
			invalid.RawQuery = test.rawQuery
			if err := invalid.Validate(); err == nil {
				t.Fatalf("RawQuery %q was accepted", test.rawQuery)
			} else if validationErr, ok := err.(*ValidationError); !ok || validationErr.Field != "raw_query" {
				t.Fatalf("Validate() error = %v, want raw_query validation error", err)
			}
		})
	}

	conflict := spec.Clone()
	conflict.Query = url.Values{"trace": {"kept"}}
	if err := conflict.Validate(); err == nil {
		t.Fatal("RawQuery and Query were accepted together")
	} else if validationErr, ok := err.(*ValidationError); !ok || validationErr.Field != "raw_query" {
		t.Fatalf("Validate() error = %v, want raw_query validation error", err)
	}
}

func TestAttemptAndStreamResultsAreDefensivelyCopied(t *testing.T) {
	reasoningBudget := int64(4096)
	result := AttemptResult{
		DispatchState:    DispatchMaybeSent,
		UpstreamAPI:      UpstreamAPIAnthropicMessages,
		AppliedReasoning: &reasoning.Config{Mode: "enabled", BudgetTokens: &reasoningBudget},
		StatusCode:       http.StatusOK,
		Header:           http.Header{"X-Request-Id": {"request-1"}},
		Body:             []byte("response"),
		Usage: &UsageEvidence{
			Normalized: usage.Result{State: usage.StateComplete},
			Raw:        []byte(`{"input_tokens":1}`),
		},
		Error: &ErrorEvidence{
			Kind:       ErrorKindProvider,
			StatusCode: http.StatusTooManyRequests,
			Type:       "rate_limit",
			Summary:    "request rate limited",
			Header:     http.Header{"Retry-After": {"1"}},
		},
	}
	clone := result.Clone()
	clone.Header.Set("X-Request-ID", "changed")
	clone.Body[0] = 'X'
	clone.Usage.Raw[0] = 'Y'
	clone.Error.Header.Set("Retry-After", "2")
	*clone.AppliedReasoning.BudgetTokens = 2048
	if result.Header.Get("X-Request-ID") != "request-1" || string(result.Body) != "response" {
		t.Fatal("mutating attempt result clone changed original response data")
	}
	if string(result.Usage.Raw) != `{"input_tokens":1}` || result.Error.Header.Get("Retry-After") != "1" {
		t.Fatal("mutating attempt result clone changed original evidence")
	}
	if result.AppliedReasoning == nil || result.AppliedReasoning.BudgetTokens == nil ||
		*result.AppliedReasoning.BudgetTokens != reasoningBudget {
		t.Fatal("mutating attempt result clone changed original reasoning")
	}

	event := StreamEvent{
		Sequence: 1,
		Kind:     StreamEventUsage,
		Data:     []byte("event"),
		Usage:    &UsageEvidence{Raw: []byte("usage")},
	}
	eventClone := event.Clone()
	eventClone.Data[0] = 'X'
	eventClone.Usage.Raw[0] = 'Y'
	if string(event.Data) != "event" || string(event.Usage.Raw) != "usage" {
		t.Fatal("mutating stream event clone changed original")
	}

	streamResult := StreamResult{
		DispatchState:     DispatchMaybeSent,
		ResponseStarted:   true,
		UpstreamAPI:       UpstreamAPIOpenAIResponses,
		AppliedReasoning:  &reasoning.Config{Effort: "high", BudgetTokens: &reasoningBudget},
		StatusCode:        http.StatusOK,
		Header:            http.Header{"X-Request-Id": {"request-1"}},
		UpstreamRequestID: "upstream-1",
		Usage:             &UsageEvidence{Raw: []byte("usage")},
		Error:             &ErrorEvidence{Kind: ErrorKindProvider, Type: "stream", Summary: "stream ended", Header: http.Header{"Retry-After": {"1"}}},
	}
	streamClone := streamResult.Clone()
	streamClone.Header.Set("X-Request-ID", "changed")
	streamClone.Usage.Raw[0] = 'X'
	streamClone.Error.Header.Set("Retry-After", "2")
	*streamClone.AppliedReasoning.BudgetTokens = 1024
	if streamResult.Header.Get("X-Request-ID") != "request-1" || string(streamResult.Usage.Raw) != "usage" || streamResult.Error.Header.Get("Retry-After") != "1" {
		t.Fatal("mutating stream result clone changed original")
	}
	if streamResult.AppliedReasoning == nil || streamResult.AppliedReasoning.BudgetTokens == nil ||
		*streamResult.AppliedReasoning.BudgetTokens != reasoningBudget {
		t.Fatal("mutating stream result clone changed original reasoning")
	}
}

func TestValidationAcceptsValidContractsAndRejectsInvalidFields(t *testing.T) {
	spec := validAttemptSpec([]byte(`{"api_key":"sk-secret-value"}`))
	if err := spec.Validate(); err != nil {
		t.Fatalf("valid AttemptSpec.Validate() error = %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*AttemptSpec)
		field  string
	}{
		{name: "request id", mutate: func(s *AttemptSpec) { s.RequestID = "" }, field: "request_id"},
		{name: "attempt id", mutate: func(s *AttemptSpec) { s.AttemptID = "" }, field: "attempt_id"},
		{name: "sequence", mutate: func(s *AttemptSpec) { s.Sequence = 0 }, field: "sequence"},
		{name: "channel", mutate: func(s *AttemptSpec) { s.ChannelID = "" }, field: "channel_id"},
		{name: "target kind", mutate: func(s *AttemptSpec) { s.TargetKind = "" }, field: "target_kind"},
		{name: "route mode", mutate: func(s *AttemptSpec) { s.RouteMode = RouteMode("fallback") }, field: "route_mode"},
		{name: "required features", mutate: func(s *AttemptSpec) {
			s.RequiredFeatures.features[Feature("unknown")] = struct{}{}
		}, field: "required_features"},
		{name: "protocol", mutate: func(s *AttemptSpec) { s.ClientProtocol = protocol.Protocol("unknown") }, field: "client_protocol"},
		{name: "operation", mutate: func(s *AttemptSpec) { s.Operation = Operation("unknown") }, field: "operation"},
		{name: "model", mutate: func(s *AttemptSpec) { s.UpstreamModel = "" }, field: "upstream_model"},
		{name: "method", mutate: func(s *AttemptSpec) { s.Method = "" }, field: "method"},
		{name: "path", mutate: func(s *AttemptSpec) { s.Path = "https://upstream.example/v1/chat/completions" }, field: "path"},
		{name: "header", mutate: func(s *AttemptSpec) { s.Header.Set("X-Test", "bad\nvalue") }, field: "header"},
		{name: "first byte timeout", mutate: func(s *AttemptSpec) { s.Timeouts.FirstByte = -time.Second }, field: "timeouts.first_byte"},
		{name: "timeout", mutate: func(s *AttemptSpec) { s.Timeouts.Request = -time.Second }, field: "timeouts.request"},
		{name: "stream idle timeout", mutate: func(s *AttemptSpec) { s.Timeouts.StreamIdle = -time.Second }, field: "timeouts.stream_idle"},
		{name: "target config", mutate: func(s *AttemptSpec) { s.TargetConfig = json.RawMessage("not-json") }, field: "target_config"},
		{name: "credential", mutate: func(s *AttemptSpec) { s.Credential = NewCredentialSnapshot(0, 7, 3, []byte("secret")) }, field: "credential.id"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			invalid := spec.Clone()
			tt.mutate(&invalid)
			err := invalid.Validate()
			if err == nil {
				t.Fatalf("Validate() error = nil, want field %q", tt.field)
			}
			var validationErr *ValidationError
			if !errors.As(err, &validationErr) {
				t.Fatalf("Validate() error type = %T, want *ValidationError", err)
			}
			if validationErr.Field != tt.field {
				t.Fatalf("ValidationError.Field = %q, want %q", validationErr.Field, tt.field)
			}
			if strings.Contains(err.Error(), "sk-secret-value") {
				t.Fatalf("validation error leaked credential data: %v", err)
			}
		})
	}

	for _, operation := range []Operation{
		OperationResponsesRetrieve,
		OperationResponsesDelete,
		OperationResponsesCancel,
		OperationResponsesInputItems,
		OperationResponsesPassthrough,
		OperationListModels,
		OperationProbe,
	} {
		modelOptional := spec.Clone()
		modelOptional.Operation = operation
		modelOptional.ClientModel = ""
		modelOptional.UpstreamModel = ""
		modelOptional.Method = http.MethodGet
		modelOptional.Path = "/v1/resources"
		if err := modelOptional.Validate(); err != nil {
			t.Fatalf("model-optional operation %q Validate() error = %v", operation, err)
		}
	}
	responsesCreate := spec.Clone()
	responsesCreate.Operation = OperationResponsesCreate
	responsesCreate.UpstreamModel = ""
	if err := responsesCreate.Validate(); err == nil {
		t.Fatal("expected responses create without a model to be rejected")
	}
	for _, operation := range []Operation{OperationResponsesCompact, OperationResponsesInputTokens} {
		modelRequired := spec.Clone()
		modelRequired.Operation = operation
		modelRequired.UpstreamModel = ""
		if err := modelRequired.Validate(); err == nil {
			t.Fatalf("expected %q without a model to be rejected", operation)
		}
	}

	if err := (AttemptResult{DispatchState: DispatchMaybeSent, ResponseStarted: true, StatusCode: http.StatusOK}).Validate(); err != nil {
		t.Fatalf("valid AttemptResult.Validate() error = %v", err)
	}
	if err := (StreamResult{DispatchState: DispatchNotSent, Error: &ErrorEvidence{Kind: ErrorKindTransport, Type: "transport", Summary: "dial failed"}}).Validate(); err != nil {
		t.Fatalf("valid StreamResult.Validate() error = %v", err)
	}
	if err := (AttemptResult{DispatchState: DispatchState("unknown")}).Validate(); err == nil {
		t.Fatal("expected invalid result dispatch state to be rejected")
	}
	if err := (AttemptResult{DispatchState: DispatchNotSent, UpstreamAPI: UpstreamAPI("unknown")}).Validate(); err == nil {
		t.Fatal("expected invalid result upstream API to be rejected")
	}
	if err := (StreamResult{DispatchState: DispatchNotSent, UpstreamAPI: UpstreamAPI("unknown")}).Validate(); err == nil {
		t.Fatal("expected invalid stream result upstream API to be rejected")
	}
	invalidResults := []AttemptResult{
		{DispatchState: DispatchMaybeSent, StatusCode: http.StatusOK},
		{DispatchState: DispatchMaybeSent, ResponseStarted: true},
		{DispatchState: DispatchNotSent, ResponseStarted: true, StatusCode: http.StatusOK},
		{DispatchState: DispatchNotSent, Body: []byte("body"), Error: &ErrorEvidence{Kind: ErrorKindTransport, Summary: "not sent"}},
		{DispatchState: DispatchMaybeSent, ResponseStarted: true, StatusCode: http.StatusInternalServerError},
	}
	for i, result := range invalidResults {
		if err := result.Validate(); err == nil {
			t.Fatalf("invalid result %d was accepted: %+v", i, result)
		}
	}
	failedResult := AttemptResult{
		DispatchState:   DispatchMaybeSent,
		ResponseStarted: true,
		StatusCode:      http.StatusTooManyRequests,
		Error:           &ErrorEvidence{Kind: ErrorKindHTTP, StatusCode: http.StatusTooManyRequests, Summary: "rate limited"},
	}
	if err := failedResult.Validate(); err != nil {
		t.Fatalf("failed result with evidence Validate() error = %v", err)
	}
	if err := (ErrorEvidence{Kind: ErrorKindHTTP, RetryAfter: -time.Second, Summary: "retry"}).Validate(); err == nil {
		t.Fatal("expected negative retry-after to be rejected")
	}
	if err := (ErrorEvidence{Kind: ErrorKindProvider, Summary: strings.Repeat("x", MaxErrorSummaryLength+1)}).Validate(); err == nil {
		t.Fatal("expected overlong error summary to be rejected")
	}
	if err := (ErrorEvidence{Kind: ErrorKindHTTP, Summary: "safe", Header: http.Header{"Authorization": {"Bearer secret"}}}).Validate(); err == nil {
		t.Fatal("expected unsafe error evidence header to be rejected")
	}
	safeEvidence := ErrorEvidence{
		Kind:    ErrorKindHTTP,
		Hint:    FailureHintInvalidCredential,
		Summary: "safe summary",
		Header:  http.Header{"Retry-After": {"1"}},
	}
	encodedEvidence, err := json.Marshal(safeEvidence)
	if err != nil {
		t.Fatalf("json.Marshal(error evidence) error = %v", err)
	}
	if strings.Contains(string(encodedEvidence), "Retry-After") {
		t.Fatalf("error evidence JSON exposed headers: %s", encodedEvidence)
	}
	if err := (ErrorEvidence{Kind: ErrorKindHTTP, Hint: FailureHint("unknown"), Summary: "safe"}).Validate(); err == nil {
		t.Fatal("expected unknown failure hint to be rejected")
	}
}

func TestStreamEventValidation(t *testing.T) {
	ready := StreamEvent{
		Sequence:          1,
		Kind:              StreamEventReady,
		StatusCode:        http.StatusOK,
		Header:            http.Header{"Content-Type": {"text/event-stream"}},
		UpstreamRequestID: "upstream-1",
	}
	if err := ready.Validate(); err != nil {
		t.Fatalf("valid ready event Validate() error = %v", err)
	}
	readyClone := ready.Clone()
	readyClone.Header.Set("Content-Type", "changed")
	if ready.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatal("mutating ready event clone changed original header")
	}
	if err := (StreamEvent{Sequence: 2, Kind: StreamEventData, Data: []byte("data")}).Validate(); err != nil {
		t.Fatalf("valid data event Validate() error = %v", err)
	}
	if err := (StreamEvent{Sequence: 3, Kind: StreamEventUsage, Usage: &UsageEvidence{Normalized: usage.Result{State: usage.StateComplete}}}).Validate(); err != nil {
		t.Fatalf("valid usage event Validate() error = %v", err)
	}
	if err := (StreamEvent{Sequence: 0, Kind: StreamEventData, Data: []byte("data")}).Validate(); err == nil {
		t.Fatal("expected zero sequence to be rejected")
	}
	if err := (StreamEvent{Sequence: 1, Kind: StreamEventKind("unknown")}).Validate(); err == nil {
		t.Fatal("expected unknown event kind to be rejected")
	}
	if err := (StreamEvent{Sequence: 2, Kind: StreamEventData, StatusCode: http.StatusOK, Data: []byte("data")}).Validate(); err == nil {
		t.Fatal("expected data event with ready metadata to be rejected")
	}
	if err := (StreamEvent{Sequence: 1, Kind: StreamEventReady, StatusCode: http.StatusOK, Header: http.Header{}, Data: []byte("data")}).Validate(); err == nil {
		t.Fatal("expected ready event with data to be rejected")
	}
}

func validAttemptSpec(credentialData []byte) AttemptSpec {
	return AttemptSpec{
		RequestID:      "request-1",
		AttemptID:      "attempt-1",
		Sequence:       1,
		ChannelID:      "openai",
		TargetKind:     "openai",
		RouteMode:      RouteNative,
		ClientProtocol: protocol.OpenAICompletions,
		Operation:      OperationChatCompletion,
		RequiredFeatures: FeatureSet{features: map[Feature]struct{}{
			FeatureStreaming: {},
		}},
		ClientModel:   "client-model",
		UpstreamModel: "upstream-model",
		Method:        http.MethodPost,
		Path:          "/v1/chat/completions",
		Query:         url.Values{"api-version": {"2026-01-01"}},
		Header:        http.Header{"X-Test": {"original"}},
		Body:          []byte(`{"model":"client-model"}`),
		TargetConfig:  json.RawMessage(`{"base_url":"https://upstream.example"}`),
		Timeouts: AttemptTimeouts{
			FirstByte:  10 * time.Second,
			Request:    30 * time.Second,
			StreamIdle: 15 * time.Second,
		},
		Credential: NewCredentialSnapshot(41, 7, 3, credentialData),
	}
}

type contractExecutor struct{}

func (contractExecutor) Capabilities() CapabilitySet {
	capabilities, _ := NewCapabilitySet(Capability{Operation: OperationChatCompletion})
	return capabilities
}

func (contractExecutor) Execute(context.Context, AttemptSpec) AttemptResult {
	return AttemptResult{}
}

func (contractExecutor) ExecuteStream(context.Context, AttemptSpec, StreamSink) StreamResult {
	return StreamResult{}
}

var _ Executor = contractExecutor{}
