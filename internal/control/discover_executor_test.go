package control

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"gpt-load/internal/channel"
	"gpt-load/internal/execution"
	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/protocol"
	"gpt-load/internal/state"
)

// recordingDiscoveryExecutorTarget acts as a provider-neutral execution target.
// This lets the discovery service tests focus
// on target freezing and credential failover rather than SDK wire details.
type recordingDiscoveryExecutorTarget struct {
	value  protocol.Protocol
	listFn func(context.Context, string, string, state.HeaderRules) ([]string, error)
}

func (recorder *recordingDiscoveryExecutorTarget) ListModels(
	ctx context.Context,
	baseURL string,
	apiKey string,
	rules state.HeaderRules,
) ([]string, error) {
	return recorder.listFn(ctx, baseURL, apiKey, rules)
}

type recordingDiscoveryExecutor struct {
	byProtocol map[protocol.Protocol]*recordingDiscoveryExecutorTarget
}

func newRecordingDiscoveryExecutor(values ...*recordingDiscoveryExecutorTarget) execution.Executor {
	byProtocol := make(map[protocol.Protocol]*recordingDiscoveryExecutorTarget, len(values))
	for _, value := range values {
		if value != nil {
			byProtocol[value.value] = value
		}
	}
	return &recordingDiscoveryExecutor{byProtocol: byProtocol}
}

func (executor *recordingDiscoveryExecutor) Execute(
	ctx context.Context,
	spec execution.AttemptSpec,
) execution.AttemptResult {
	recorder := executor.byProtocol[spec.ClientProtocol]
	if recorder == nil || recorder.listFn == nil {
		return execution.AttemptResult{
			DispatchState: execution.DispatchNotSent,
			Error: &execution.ErrorEvidence{
				Kind: execution.ErrorKindInvalidRequest, Summary: "missing test executor",
			},
		}
	}
	var target struct {
		BaseURL string `json:"base_url"`
	}
	_ = json.Unmarshal(spec.TargetConfig, &target)
	var credential struct {
		APIKey string `json:"api_key"`
	}
	_ = json.Unmarshal(spec.Credential.Data(), &credential)
	rules := state.HeaderRules{Set: make(map[string]string)}
	for name, values := range spec.Header {
		if strings.EqualFold(name, "Accept-Encoding") || len(values) == 0 {
			continue
		}
		rules.Set[name] = values[len(values)-1]
	}
	models, err := recorder.listFn(ctx, target.BaseURL, credential.APIKey, rules)
	if err != nil {
		return execution.AttemptResult{
			DispatchState: execution.DispatchMaybeSent,
			Error: &execution.ErrorEvidence{
				Kind: execution.ErrorKindTransport, Summary: "test discovery failed",
			},
		}
	}
	body := encodeDiscoveryModelsForTest(spec.ClientProtocol, models)
	return execution.AttemptResult{
		DispatchState: execution.DispatchMaybeSent, ResponseStarted: true,
		StatusCode: http.StatusOK, Header: http.Header{}, Body: body,
	}
}

func (*recordingDiscoveryExecutor) ExecuteStream(
	context.Context,
	execution.AttemptSpec,
	execution.StreamSink,
) execution.StreamResult {
	panic("unexpected stream execution")
}

func encodeDiscoveryModelsForTest(value protocol.Protocol, models []string) []byte {
	items := make([]map[string]string, 0, len(models))
	for _, model := range models {
		if value == protocol.Gemini {
			items = append(items, map[string]string{"name": "models/" + model})
		} else {
			items = append(items, map[string]string{"id": model})
		}
	}
	payload := map[string]any{"data": items}
	if value == protocol.Gemini {
		payload = map[string]any{"models": items}
	}
	body, _ := json.Marshal(payload)
	return body
}

func TestNativeOpenAIStyleProvidersUseOpenAIUtilityRequestShape(t *testing.T) {
	for _, providerKind := range []channel.ProviderKind{
		channel.ProviderDeepSeek,
		channel.ProviderOpenRouter,
		channel.ProviderGroq,
		channel.ProviderXAI,
	} {
		t.Run(string(providerKind), func(t *testing.T) {
			clientProtocol, method, path, body, err := utilityRequestShape(
				providerKind,
				execution.OperationListModels,
				"",
			)
			if err != nil || clientProtocol != protocol.OpenAICompletions ||
				method != http.MethodGet || path != "/v1/models" || body != nil {
				t.Fatalf("ListModels shape = %q %q %q %s, err=%v", clientProtocol, method, path, body, err)
			}

			if _, _, _, _, err := utilityRequestShape(
				providerKind,
				execution.OperationProbe,
				"model-one",
			); err == nil {
				t.Fatal("utilityRequestShape still owns provider Probe wire shape")
			}
		})
	}
}

func discoveryTargetForTest(
	t *testing.T,
	channelID channel.ID,
	baseURL string,
	keys []string,
	rules state.HeaderRules,
) discoveryTarget {
	t.Helper()
	registry := channel.NewRegistry()
	params := json.RawMessage(`{}`)
	if baseURL != "" {
		encoded, err := json.Marshal(map[string]string{"base_url": baseURL})
		if err != nil {
			t.Fatal(err)
		}
		params = encoded
	}
	resolved, err := registry.Resolve(channelID, params)
	if err != nil {
		t.Fatalf("Resolve(%q) error = %v", channelID, err)
	}
	credentials := make([]discoveryCredential, 0, len(keys))
	for index, key := range keys {
		data, err := json.Marshal(map[string]string{"api_key": key})
		if err != nil {
			t.Fatal(err)
		}
		credentials = append(credentials, discoveryCredential{
			snapshot: execution.NewCredentialSnapshot(uint(index+1), 1, uint64(index+1), data),
			apiKey:   key,
		})
	}
	return discoveryTarget{
		channelID: channelID, resolvedTarget: resolved, credentials: credentials,
		headerRules: rules, catalogProviderID: resolved.CatalogProviderID,
	}
}

func TestExecuteModelDiscoveryUsesNeutralExecutor(t *testing.T) {
	t.Parallel()

	var observed execution.AttemptSpec
	executor := scriptedDiscoveryExecutor{execute: func(
		_ context.Context,
		spec execution.AttemptSpec,
	) execution.AttemptResult {
		observed = spec.Clone()
		return execution.AttemptResult{
			DispatchState: execution.DispatchMaybeSent, ResponseStarted: true,
			StatusCode: http.StatusOK, Header: http.Header{},
			Body: []byte(`{"object":"list","data":[{"id":"gpt-test"}]}`),
		}
	}}
	service := &Service{executor: executor, modelDiscoveryTimeout: time.Second}
	target := discoveryTargetForTest(
		t, channel.OpenAICompatible, "https://api.example.com/v1", []string{"secret-key"}, state.HeaderRules{},
	)
	result, err := service.executeModelDiscovery(context.Background(), target)
	if err != nil {
		t.Fatalf("executeModelDiscovery() error = %v", err)
	}
	if observed.Operation != execution.OperationListModels ||
		observed.ChannelID != string(channel.OpenAICompatible) ||
		observed.Credential.ID == 0 || string(observed.TargetConfig) != `{"base_url":"https://api.example.com/v1"}` {
		t.Fatalf("observed spec = %#v", observed)
	}
	if got := result.Models; len(got) != 1 || got[0].ID != "gpt-test" {
		t.Fatalf("models = %#v", got)
	}
}

func TestExecuteModelDiscoveryPaginatesProviderModelLists(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		channelID   channel.ID
		firstBody   string
		secondBody  string
		cursorKey   string
		cursor      string
		pageSizeKey string
	}{
		{
			name: "Anthropic", channelID: channel.Anthropic,
			firstBody:  `{"data":[{"id":"model-a"},{"id":"model-shared"}],"has_more":true,"last_id":"cursor-a"}`,
			secondBody: `{"data":[{"id":"model-shared"},{"id":"model-b"}],"has_more":false}`,
			cursorKey:  "after_id", cursor: "cursor-a", pageSizeKey: "limit",
		},
		{
			name: "Gemini", channelID: channel.Gemini,
			firstBody:  `{"models":[{"name":"models/model-a"},{"name":"models/model-shared"}],"nextPageToken":"cursor+/b"}`,
			secondBody: `{"models":[{"name":"models/model-shared"},{"name":"models/model-b"}]}`,
			cursorKey:  "pageToken", cursor: "cursor+/b", pageSizeKey: "pageSize",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			calls := 0
			executor := scriptedDiscoveryExecutor{execute: func(
				_ context.Context,
				spec execution.AttemptSpec,
			) execution.AttemptResult {
				calls++
				values, err := url.ParseQuery(spec.RawQuery)
				if err != nil {
					t.Fatalf("parse page query: %v", err)
				}
				body := test.firstBody
				if calls == 1 {
					if values.Get(test.pageSizeKey) != "1000" || len(values) != 1 {
						t.Fatalf("first page query = %q", spec.RawQuery)
					}
				} else {
					if calls != 2 || values.Get(test.cursorKey) != test.cursor ||
						values.Get(test.pageSizeKey) != "1000" || len(values) != 2 {
						t.Fatalf("next page query = %q", spec.RawQuery)
					}
					body = test.secondBody
				}
				return execution.AttemptResult{
					DispatchState:   execution.DispatchMaybeSent,
					ResponseStarted: true,
					StatusCode:      http.StatusOK,
					Header:          http.Header{},
					Body:            []byte(body),
				}
			}}
			service := &Service{executor: executor, modelDiscoveryTimeout: time.Second}
			target := discoveryTargetForTest(
				t, test.channelID, "https://provider.example", []string{"secret-key"}, state.HeaderRules{},
			)

			result, err := service.executeModelDiscovery(context.Background(), target)
			if err != nil {
				t.Fatalf("executeModelDiscovery() error = %v", err)
			}
			if calls != 2 {
				t.Fatalf("model list calls = %d, want 2", calls)
			}
			got := make([]string, 0, len(result.Models))
			for _, model := range result.Models {
				got = append(got, model.ID)
			}
			want := []string{"model-a", "model-shared", "model-b"}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("models = %#v, want %#v", got, want)
			}
		})
	}
}

func TestExecuteModelDiscoveryRejectsRepeatedPaginationCursor(t *testing.T) {
	t.Parallel()

	calls := 0
	executor := scriptedDiscoveryExecutor{execute: func(
		_ context.Context,
		spec execution.AttemptSpec,
	) execution.AttemptResult {
		calls++
		return execution.AttemptResult{
			DispatchState:   execution.DispatchMaybeSent,
			ResponseStarted: true,
			StatusCode:      http.StatusOK,
			Header:          http.Header{},
			Body:            []byte(`{"data":[{"id":"model-a"}],"has_more":true,"last_id":"same-cursor"}`),
		}
	}}
	service := &Service{executor: executor, modelDiscoveryTimeout: time.Second}
	target := discoveryTargetForTest(
		t, channel.Anthropic, "https://provider.example", []string{"secret-key"}, state.HeaderRules{},
	)

	if _, err := service.executeModelDiscovery(context.Background(), target); err == nil {
		t.Fatal("executeModelDiscovery() error = nil, want repeated cursor failure")
	}
	if calls != 2 {
		t.Fatalf("model list calls = %d, want 2", calls)
	}
}

type scriptedDiscoveryExecutor struct {
	execute func(context.Context, execution.AttemptSpec) execution.AttemptResult
}

func (executor scriptedDiscoveryExecutor) Execute(ctx context.Context, spec execution.AttemptSpec) execution.AttemptResult {
	return executor.execute(ctx, spec)
}

func (scriptedDiscoveryExecutor) ExecuteStream(context.Context, execution.AttemptSpec, execution.StreamSink) execution.StreamResult {
	panic("unexpected stream execution")
}

func TestExecuteModelDiscoveryFallsBackAcrossCredentialsInStableOrder(t *testing.T) {
	var calls []string
	recorder := &recordingDiscoveryExecutorTarget{
		value: protocol.OpenAICompletions,
		listFn: func(_ context.Context, baseURL, apiKey string, rules state.HeaderRules) ([]string, error) {
			calls = append(calls, apiKey)
			if baseURL != "https://api.example.com/v1" || rules.Set["X-Test"] != "draft" {
				t.Fatalf("target = %q/%#v", baseURL, rules)
			}
			if apiKey == "key-b" {
				return []string{" z-model ", "a-model", "z-model"}, nil
			}
			return nil, errors.New("try next credential")
		},
	}
	service := &Service{executor: newRecordingDiscoveryExecutor(recorder), modelDiscoveryTimeout: time.Second}
	target := discoveryTargetForTest(t, channel.OpenAICompatible, "https://api.example.com/v1",
		[]string{"key-a", "key-b"}, state.HeaderRules{Set: map[string]string{"X-Test": "draft"}})
	result, err := service.executeModelDiscovery(context.Background(), target)
	if err != nil {
		t.Fatalf("executeModelDiscovery() error = %v", err)
	}
	if !reflect.DeepEqual(calls, []string{"key-a", "key-b"}) {
		t.Fatalf("calls = %#v", calls)
	}
	if got := []string{result.Models[0].ID, result.Models[1].ID}; !reflect.DeepEqual(got, []string{"z-model", "a-model"}) {
		t.Fatalf("models = %#v", result.Models)
	}
}

func TestExecuteModelDiscoverySharesOneTotalTimeout(t *testing.T) {
	var mu sync.Mutex
	var deadlines []time.Time
	recorder := &recordingDiscoveryExecutorTarget{
		value: protocol.Anthropic,
		listFn: func(ctx context.Context, _, _ string, _ state.HeaderRules) ([]string, error) {
			deadline, ok := ctx.Deadline()
			if !ok {
				t.Fatal("discovery context has no deadline")
			}
			mu.Lock()
			deadlines = append(deadlines, deadline)
			mu.Unlock()
			return nil, errors.New("retry")
		},
	}
	service := &Service{executor: newRecordingDiscoveryExecutor(recorder), modelDiscoveryTimeout: time.Second}
	target := discoveryTargetForTest(t, channel.Anthropic, "https://api.example.com",
		[]string{"key-a", "key-b"}, state.HeaderRules{})
	_, err := service.executeModelDiscovery(context.Background(), target)
	if !errors.Is(err, app_errors.ErrBadGateway) {
		t.Fatalf("executeModelDiscovery() error = %v", err)
	}
	if len(deadlines) != 2 || !deadlines[0].Equal(deadlines[1]) {
		t.Fatalf("deadlines = %#v", deadlines)
	}
}

func TestExecuteModelDiscoveryReturnsParentCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	recorder := &recordingDiscoveryExecutorTarget{
		value: protocol.Gemini,
		listFn: func(discoveryCtx context.Context, _, _ string, _ state.HeaderRules) ([]string, error) {
			calls++
			cancel()
			<-discoveryCtx.Done()
			return nil, discoveryCtx.Err()
		},
	}
	service := &Service{executor: newRecordingDiscoveryExecutor(recorder), modelDiscoveryTimeout: time.Second}
	target := discoveryTargetForTest(t, channel.Gemini, "https://api.example.com",
		[]string{"key-a", "key-b"}, state.HeaderRules{})
	_, err := service.executeModelDiscovery(ctx, target)
	if err != context.Canceled || calls != 1 {
		t.Fatalf("error/calls = %v/%d", err, calls)
	}
}

func TestExecuteModelDiscoveryRejectsInvalidTargetBeforeDispatch(t *testing.T) {
	calls := 0
	service := &Service{
		executor: scriptedDiscoveryExecutor{execute: func(context.Context, execution.AttemptSpec) execution.AttemptResult {
			calls++
			return execution.AttemptResult{}
		}},
		modelDiscoveryTimeout: time.Second,
	}
	for name, target := range map[string]discoveryTarget{
		"empty channel": {},
		"empty credentials": func() discoveryTarget {
			value := discoveryTargetForTest(t, channel.OpenAICompatible, "https://api.example.com", []string{"key"}, state.HeaderRules{})
			value.credentials = nil
			return value
		}(),
		"mismatched target": func() discoveryTarget {
			value := discoveryTargetForTest(t, channel.OpenAICompatible, "https://api.example.com", []string{"key"}, state.HeaderRules{})
			value.channelID = channel.Anthropic
			return value
		}(),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := service.executeModelDiscovery(context.Background(), target); err == nil {
				t.Fatal("invalid target error = nil")
			}
		})
	}
	if calls != 0 {
		t.Fatalf("dispatch calls = %d", calls)
	}
}

func TestNormalizeDiscoveredModels(t *testing.T) {
	got := normalizeDiscoveredModels([]string{" model-b ", "", "model-a", "model-b", "\t"})
	if !reflect.DeepEqual(got, []string{"model-b", "model-a"}) {
		t.Fatalf("normalizeDiscoveredModels() = %#v", got)
	}
}
