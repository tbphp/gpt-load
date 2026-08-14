package control

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"gorm.io/gorm"

	"gpt-load/internal/channel"
	"gpt-load/internal/codex"
	"gpt-load/internal/execution"
	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/protocol"
	"gpt-load/internal/state"
	"gpt-load/internal/storage/models"
)

func TestDiscoverModelsUsesSystemDefaultsAndNormalizesSuccessfulResult(t *testing.T) {
	fixture := newServiceFixture(t)
	if err := fixture.db.Create(&models.SystemSetting{
		Key: "header_rules",
		Value: `{"set":{"X-System":"system","X-Override":"system"},` +
			`"remove":["X-System-Remove"]}`,
	}).Error; err != nil {
		t.Fatalf("seed system HeaderRules: %v", err)
	}

	var calls []string
	newRecorder := func(value protocol.Protocol) *recordingDiscoveryExecutorTarget {
		return &recordingDiscoveryExecutorTarget{
			value: value,
			listFn: func(
				_ context.Context,
				baseURL, apiKey string,
				rules state.HeaderRules,
			) ([]string, error) {
				calls = append(calls, string(value)+":"+apiKey)
				if baseURL != "https://api.example.com/v1" {
					t.Fatalf("base URL = %q, want normalized draft URL", baseURL)
				}
				wantRules := state.HeaderRules{
					Set: map[string]string{
						"X-System":   "system",
						"X-Override": "system",
					},
				}
				if !reflect.DeepEqual(rules, wantRules) {
					t.Fatalf("HeaderRules = %#v, want system defaults %#v", rules, wantRules)
				}
				if value == protocol.OpenAICompletions && apiKey == "key-b" {
					return []string{" claude-z ", "claude-a", "claude-z", "", "claude-a"}, nil
				}
				return nil, errors.New("try next")
			},
		}
	}
	fixture.service.executor = newRecordingDiscoveryExecutor(
		newRecorder(protocol.OpenAICompletions),
	)
	result, err := fixture.service.DiscoverModels(context.Background(), ModelDiscoveryRequest{
		ChannelID:   channel.OpenAICompatible,
		Params:      json.RawMessage(`{"base_url":" HTTPS://API.Example.COM/v1/ "}`),
		Credentials: " key-a \nkey-a\n\n key-b \nkey-b",
	})
	if err != nil {
		t.Fatalf("DiscoverModels() error = %v", err)
	}
	if !reflect.DeepEqual(result.Models, []ModelCandidate{
		{ID: "claude-z", Name: "claude-z", Sources: []string{"live"}, PricingStatus: PricingStatusPending},
		{ID: "claude-a", Name: "claude-a", Sources: []string{"live"}, PricingStatus: PricingStatusPending},
	}) {
		t.Fatalf("models = %#v, want upstream order", result.Models)
	}
	wantCalls := []string{
		"openai-completions:key-a",
		"openai-completions:key-b",
	}
	if !reflect.DeepEqual(calls, wantCalls) {
		t.Fatalf("calls = %#v, want stable normalized order %#v", calls, wantCalls)
	}
}

func TestDiscoverModelsUsesReadySubscriptionStage(t *testing.T) {
	t.Parallel()

	fixture := newServiceFixture(t)
	stage := mustImportSubscriptionStage(t, fixture, "account-models", "models@example.com")
	fixture.service.listCodexModels = func(_ context.Context, credential codex.Credential) ([]codex.Model, error) {
		if credential.AccountID != "account-models" || credential.AccessToken == "" {
			t.Fatalf("credential = %#v", credential)
		}
		return []codex.Model{{ID: " gpt-5.2 "}, {ID: "gpt-5.2"}, {ID: "gpt-5.1-codex"}}, nil
	}
	result, err := fixture.service.DiscoverModels(t.Context(), ModelDiscoveryRequest{
		ChannelID:          channel.Codex,
		StagedCredentialID: stage.StageID,
	})
	if err != nil {
		t.Fatalf("DiscoverModels() error = %v", err)
	}
	if got := result.Models; len(got) != 2 || got[0].ID != "gpt-5.2" || got[1].ID != "gpt-5.1-codex" {
		t.Fatalf("models = %#v", got)
	}
	row, err := fixture.service.loadCredentialStage(t.Context(), stage.StageID)
	if err != nil || row.Status != models.CredentialStageReady || row.EncryptedPayload == "" {
		t.Fatalf("stage was consumed = %#v, %v", row, err)
	}
}

func TestDiscoverModelsRejectsInvalidDraftBeforeHTTP(t *testing.T) {
	fixture := newServiceFixture(t)
	var calls atomic.Int64
	fixture.service.executor = newRecordingDiscoveryExecutor(&recordingDiscoveryExecutorTarget{
		value: protocol.OpenAICompletions,
		listFn: func(context.Context, string, string, state.HeaderRules) ([]string, error) {
			calls.Add(1)
			return nil, nil
		},
	})
	valid := ModelDiscoveryRequest{
		ChannelID:   channel.OpenAICompatible,
		Params:      json.RawMessage(`{"base_url":"https://api.example.com"}`),
		Credentials: "key-a",
	}
	tests := []struct {
		name   string
		mutate func(*ModelDiscoveryRequest)
	}{
		{name: "empty channel", mutate: func(value *ModelDiscoveryRequest) { value.ChannelID = "" }},
		{name: "relative URL", mutate: func(value *ModelDiscoveryRequest) {
			value.Params = json.RawMessage(`{"base_url":"/v1"}`)
		}},
		{name: "missing base URL", mutate: func(value *ModelDiscoveryRequest) { value.Params = json.RawMessage(`{}`) }},
		{name: "unknown channel", mutate: func(value *ModelDiscoveryRequest) {
			value.ChannelID = channel.ID("unknown")
		}},
		{name: "empty credentials", mutate: func(value *ModelDiscoveryRequest) { value.Credentials = " \n\t" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := valid
			test.mutate(&request)
			_, err := fixture.service.DiscoverModels(context.Background(), request)
			if !errors.Is(err, app_errors.ErrValidation) {
				t.Fatalf("DiscoverModels() error = %v, want ErrValidation", err)
			}
		})
	}
	if calls.Load() != 0 {
		t.Fatalf("ListModels calls = %d, want invalid drafts rejected first", calls.Load())
	}
}

func TestDiscoverModelsUsesChannelNativeDiscoveryProtocol(t *testing.T) {
	fixture := newServiceFixture(t)
	calls := 0
	fixture.service.executor = newRecordingDiscoveryExecutor(&recordingDiscoveryExecutorTarget{
		value: protocol.OpenAICompletions,
		listFn: func(
			_ context.Context,
			baseURL, apiKey string,
			_ state.HeaderRules,
		) ([]string, error) {
			calls++
			if baseURL != "https://api.example.com" ||
				apiKey != "key-a" {
				t.Fatalf("ListModels target = %q/%q", baseURL, apiKey)
			}
			return []string{"gpt-5"}, nil
		},
	})

	result, err := fixture.service.DiscoverModels(
		context.Background(),
		ModelDiscoveryRequest{
			ChannelID:   channel.OpenAICompatible,
			Params:      json.RawMessage(`{"base_url":"https://api.example.com"}`),
			Credentials: "key-a",
		},
	)
	if err != nil {
		t.Fatalf("DiscoverModels() error = %v", err)
	}
	if !reflect.DeepEqual(result.Models, []ModelCandidate{
		{ID: "gpt-5", Name: "gpt-5", Sources: []string{"live"}, PricingStatus: PricingStatusPending},
	}) ||
		calls != 1 {
		t.Fatalf("models/calls = %#v/%d", result.Models, calls)
	}
}

func TestDiscoverModelsPassesStructuredCloudCredentialToExecutor(t *testing.T) {
	fixture := newServiceFixture(t)
	var observed execution.AttemptSpec
	fixture.service.executor = scriptedDiscoveryExecutor{execute: func(
		_ context.Context,
		spec execution.AttemptSpec,
	) execution.AttemptResult {
		observed = spec.Clone()
		return execution.AttemptResult{
			DispatchState:   execution.DispatchMaybeSent,
			ResponseStarted: true,
			StatusCode:      http.StatusOK,
			Header:          http.Header{},
			Body:            []byte(`{"object":"list","data":[{"id":"anthropic.claude-test"}]}`),
		}
	}}

	result, err := fixture.service.DiscoverModels(context.Background(), ModelDiscoveryRequest{
		ChannelID: channel.AWSBedrock,
		Params:    json.RawMessage(`{"region":"us-east-1"}`),
		Credentials: `{"access_key":"AKIA_TEST","secret_key":"bedrock-secret",` +
			`"session_token":"bedrock-session"}`,
	})
	if err != nil {
		t.Fatalf("DiscoverModels() error = %v", err)
	}
	if got, want := string(observed.Credential.Data()),
		`{"access_key":"AKIA_TEST","secret_key":"bedrock-secret","session_token":"bedrock-session"}`; got != want {
		t.Fatalf("credential = %s, want %s", got, want)
	}
	if observed.ChannelID != string(channel.AWSBedrock) ||
		observed.Operation != execution.OperationListModels ||
		observed.ClientProtocol != protocol.OpenAICompletions {
		t.Fatalf("attempt = %#v", observed)
	}
	if got := result.Models; len(got) != 1 || got[0].ID != "anthropic.claude-test" {
		t.Fatalf("models = %#v", got)
	}
}

func TestDiscoverModelsDoesNotReadOrMutateRuntimeState(t *testing.T) {
	fixture := newServiceFixture(t)
	created, err := fixture.service.CreateGroup(context.Background(), GroupCreateRequest{
		Name: stringPointer("discover-runtime-state"), ChannelID: channel.OpenAICompatible,
		Params: json.RawMessage(`{"base_url":"https://state.example.com"}`), Credentials: "sk-state",
		Models: optionalGroupModels{
			Set: true, Values: []GroupModel{{ID: "gpt-4o"}},
		},
	})
	if err != nil {
		t.Fatalf("seed CreateGroup() error = %v", err)
	}
	beforeRows := discoveryRowCounts(t, fixture.db)
	beforeSnapshot := fixture.manager.Current()
	var keyRow models.Credential
	if err := fixture.db.First(&keyRow).Error; err != nil {
		t.Fatalf("query seeded key: %v", err)
	}
	beforeCipher, ok := fixture.registry.EncryptedCredentialData(keyRow.ID)
	if !ok {
		t.Fatal("seeded Registry key missing")
	}

	queryTables := make(map[string]int)
	var writeCount atomic.Int64
	const queryCallback = "test:draft-discovery-query-boundary"
	if err := fixture.db.Callback().Query().After("gorm:query").Register(
		queryCallback,
		func(tx *gorm.DB) { queryTables[tx.Statement.Table]++ },
	); err != nil {
		t.Fatalf("register query callback: %v", err)
	}
	const createCallback = "test:draft-discovery-create-boundary"
	if err := fixture.db.Callback().Create().After("gorm:create").Register(
		createCallback,
		func(*gorm.DB) { writeCount.Add(1) },
	); err != nil {
		t.Fatalf("register create callback: %v", err)
	}
	const updateCallback = "test:draft-discovery-update-boundary"
	if err := fixture.db.Callback().Update().After("gorm:update").Register(
		updateCallback,
		func(*gorm.DB) { writeCount.Add(1) },
	); err != nil {
		t.Fatalf("register update callback: %v", err)
	}
	const deleteCallback = "test:draft-discovery-delete-boundary"
	if err := fixture.db.Callback().Delete().After("gorm:delete").Register(
		deleteCallback,
		func(*gorm.DB) { writeCount.Add(1) },
	); err != nil {
		t.Fatalf("register delete callback: %v", err)
	}
	fixture.service.manager = nil
	fixture.service.registry = nil
	fixture.service.executor = newRecordingDiscoveryExecutor(&recordingDiscoveryExecutorTarget{
		value: protocol.OpenAICompletions,
		listFn: func(context.Context, string, string, state.HeaderRules) ([]string, error) {
			return []string{"remote-only"}, nil
		},
	})
	result, err := fixture.service.DiscoverModels(context.Background(), ModelDiscoveryRequest{
		ChannelID:   channel.OpenAICompatible,
		Params:      json.RawMessage(`{"base_url":"https://discover.example.com"}`),
		Credentials: "sk-discovery",
	})
	if err != nil || !reflect.DeepEqual(result.Models, []ModelCandidate{
		{ID: "remote-only", Name: "remote-only", Sources: []string{"live"}, PricingStatus: PricingStatusPending},
	}) {
		t.Fatalf("DiscoverModels() = %#v, %v", result, err)
	}
	if !reflect.DeepEqual(queryTables, map[string]int{"system_settings": 1, "model_prices": 1}) {
		t.Fatalf("discovery queries = %#v, want system settings and global model prices once", queryTables)
	}
	if writeCount.Load() != 0 {
		t.Fatalf("discovery DB writes = %d, want 0", writeCount.Load())
	}
	if fixture.manager.Current() != beforeSnapshot ||
		fixture.manager.Current().Revision != beforeSnapshot.Revision {
		t.Fatal("discovery replaced or revised ConfigSnapshot")
	}
	if got, ok := fixture.registry.EncryptedCredentialData(keyRow.ID); !ok || got != beforeCipher {
		t.Fatalf("Registry value = %q, %t, want unchanged", got, ok)
	}
	if afterRows := discoveryRowCounts(t, fixture.db); afterRows != beforeRows {
		t.Fatalf("row counts = %#v, want %#v", afterRows, beforeRows)
	}
	if _, exists := fixture.manager.Current().ExecutionCandidates[protocol.OpenAICompletions][execution.OperationChatCompletion]["remote-only"]; exists {
		t.Fatal("discovered model leaked into ConfigSnapshot")
	}
	if created.GroupID == 0 {
		t.Fatal("invalid seeded group")
	}
}

func TestDiscoverModelsDoesNotAcquireWriteMu(t *testing.T) {
	fixture := newServiceFixture(t)
	fixture.service.executor = newRecordingDiscoveryExecutor(&recordingDiscoveryExecutorTarget{
		value: protocol.OpenAICompletions,
		listFn: func(context.Context, string, string, state.HeaderRules) ([]string, error) {
			return []string{"gpt-4o"}, nil
		},
	})
	fixture.service.writeMu.Lock()
	defer fixture.service.writeMu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := fixture.service.DiscoverModels(ctx, ModelDiscoveryRequest{
			ChannelID:   channel.OpenAICompatible,
			Params:      json.RawMessage(`{"base_url":"https://discover.example.com"}`),
			Credentials: "sk-discovery",
		})
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("DiscoverModels() error = %v", err)
		}
	case <-ctx.Done():
		t.Fatal("DiscoverModels() waited for writeMu")
	}
}

func TestDiscoverModelsDoesNotBlockMutation(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	fixture := newServiceFixture(t)
	fixture.service.executor = newRecordingDiscoveryExecutor(&recordingDiscoveryExecutorTarget{
		value: protocol.Anthropic,
		listFn: func(
			ctx context.Context,
			_ string,
			_ string,
			_ state.HeaderRules,
		) ([]string, error) {
			close(entered)
			select {
			case <-release:
				return []string{"claude-model"}, nil
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		},
	})
	discoveryDone := make(chan error, 1)
	go func() {
		_, err := fixture.service.DiscoverModels(context.Background(), ModelDiscoveryRequest{
			ChannelID:   channel.Anthropic,
			Params:      json.RawMessage(`{"base_url":"https://discover.example.com"}`),
			Credentials: "sk-discovery",
		})
		discoveryDone <- err
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("discovery did not enter ListModels")
	}

	mutationDone := make(chan error, 1)
	go func() {
		_, err := fixture.service.CreateGroup(context.Background(), GroupCreateRequest{
			Name: stringPointer("discover-concurrent-mutation"), ChannelID: channel.OpenAICompatible,
			Params: json.RawMessage(`{"base_url":"https://mutation.example.com"}`), Credentials: "sk-mutation",
			Models: optionalGroupModels{
				Set: true, Values: []GroupModel{{ID: "gpt-4o"}},
			},
		})
		mutationDone <- err
	}()
	select {
	case err := <-mutationDone:
		if err != nil {
			t.Fatalf("CreateGroup() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("control mutation blocked behind discovery")
	}
	close(release)
	select {
	case err := <-discoveryDone:
		if err != nil {
			t.Fatalf("DiscoverModels() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("discovery did not finish after release")
	}
}

func discoveryRowCounts(t *testing.T, db *gorm.DB) [3]int64 {
	t.Helper()
	var result [3]int64
	for index, model := range []any{&models.Group{}, &models.Credential{}, &models.AccessKey{}} {
		if err := db.Model(model).Count(&result[index]).Error; err != nil {
			t.Fatalf("count %T rows: %v", model, err)
		}
	}
	return result
}
