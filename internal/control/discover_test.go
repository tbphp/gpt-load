package control

import (
	"context"
	"errors"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"gorm.io/gorm"

	"gpt-load/internal/dialect"
	"gpt-load/internal/platform/config"
	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/protocol"
	"gpt-load/internal/state"
	"gpt-load/internal/storage/models"
)

func TestDiscoverModelsNormalizesDraftAndMergesHeaderRules(t *testing.T) {
	fixture := newServiceFixture(t)
	if err := fixture.db.Create(&models.SystemSetting{
		Key: "header_rules",
		Value: `{"set":{"X-System":"system","X-Override":"system"},` +
			`"remove":["X-System-Remove"]}`,
	}).Error; err != nil {
		t.Fatalf("seed system HeaderRules: %v", err)
	}

	var calls []string
	newRecorder := func(value protocol.Protocol) *recordingDiscoveryDialect {
		return &recordingDiscoveryDialect{
			value: value,
			listFn: func(
				_ context.Context,
				baseURL, apiKey string,
				rules state.HeaderRules,
			) ([]string, error) {
				calls = append(calls, string(value)+":"+apiKey)
				if baseURL != "https://api.example.com/v1?fixed=1" {
					t.Fatalf("base URL = %q, want normalized draft URL", baseURL)
				}
				wantRules := state.HeaderRules{
					Set:    map[string]string{"X-Draft": "draft", "X-Override": "draft"},
					Remove: []string{"X-Draft-Remove"},
				}
				if !reflect.DeepEqual(rules, wantRules) {
					t.Fatalf("HeaderRules = %#v, want top-level Group override %#v", rules, wantRules)
				}
				if value == protocol.Anthropic && apiKey == "key-a" {
					return []string{"claude-z", "claude-a"}, nil
				}
				return nil, errors.New("try next")
			},
		}
	}
	fixture.service.dialects = dialect.NewSet(
		newRecorder(protocol.OpenAI),
		newRecorder(protocol.Anthropic),
	)
	result, err := fixture.service.DiscoverModels(context.Background(), ModelDiscoveryRequest{
		UpstreamURL: " HTTPS://API.Example.COM/v1/?fixed=1 ",
		Protocols: []protocol.Protocol{
			protocol.OpenAI,
			protocol.OpenAI,
			protocol.Anthropic,
			protocol.OpenAI,
		},
		Keys: " key-a \nkey-a\n\n key-b \nkey-b",
		Config: config.Settings{"header_rules": map[string]any{
			"set": map[string]any{
				"X-Draft":    "draft",
				"X-Override": "draft",
			},
			"remove": []any{"X-Draft-Remove"},
		}},
	})
	if err != nil {
		t.Fatalf("DiscoverModels() error = %v", err)
	}
	if !reflect.DeepEqual(result.Models, []string{"claude-z", "claude-a"}) {
		t.Fatalf("models = %#v, want upstream order", result.Models)
	}
	wantCalls := []string{"openai:key-a", "openai:key-b", "anthropic:key-a"}
	if !reflect.DeepEqual(calls, wantCalls) {
		t.Fatalf("calls = %#v, want stable normalized order %#v", calls, wantCalls)
	}
}

func TestDiscoverModelsRejectsInvalidDraftBeforeHTTP(t *testing.T) {
	fixture := newServiceFixture(t)
	var calls atomic.Int64
	fixture.service.dialects = dialect.NewSet(&recordingDiscoveryDialect{
		value: protocol.OpenAI,
		listFn: func(context.Context, string, string, state.HeaderRules) ([]string, error) {
			calls.Add(1)
			return nil, nil
		},
	})
	valid := ModelDiscoveryRequest{
		UpstreamURL: "https://api.example.com",
		Protocols:   []protocol.Protocol{protocol.OpenAI},
		Keys:        "key-a",
		Config:      config.Settings{},
	}
	tests := []struct {
		name   string
		mutate func(*ModelDiscoveryRequest)
	}{
		{name: "empty URL", mutate: func(value *ModelDiscoveryRequest) { value.UpstreamURL = "" }},
		{name: "relative URL", mutate: func(value *ModelDiscoveryRequest) { value.UpstreamURL = "/v1" }},
		{name: "empty protocols", mutate: func(value *ModelDiscoveryRequest) { value.Protocols = nil }},
		{name: "unknown protocol", mutate: func(value *ModelDiscoveryRequest) {
			value.Protocols = []protocol.Protocol{"unknown"}
		}},
		{name: "response protocol", mutate: func(value *ModelDiscoveryRequest) {
			value.Protocols = []protocol.Protocol{protocol.OpenAIResponse}
		}},
		{name: "empty keys", mutate: func(value *ModelDiscoveryRequest) { value.Keys = " \n\t" }},
		{name: "unknown config", mutate: func(value *ModelDiscoveryRequest) {
			value.Config = config.Settings{"unknown": true}
		}},
		{name: "invalid HeaderRules", mutate: func(value *ModelDiscoveryRequest) {
			value.Config = config.Settings{"header_rules": []any{"invalid"}}
		}},
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

func TestDiscoverModelsDoesNotReadOrMutateRuntimeState(t *testing.T) {
	fixture := newServiceFixture(t)
	created, err := fixture.service.CreateGroup(context.Background(), GroupCreateRequest{
		UpstreamURL: "https://state.example.com",
		Protocols:   []protocol.Protocol{protocol.OpenAI},
		Keys:        "sk-state",
		Models: optionalGroupModels{
			Set: true, Values: []GroupModel{{ID: "gpt-4o"}},
		},
	})
	if err != nil {
		t.Fatalf("seed CreateGroup() error = %v", err)
	}
	beforeRows := discoveryRowCounts(t, fixture.db)
	beforeSnapshot := fixture.manager.Current()
	var keyRow models.UpstreamKey
	if err := fixture.db.First(&keyRow).Error; err != nil {
		t.Fatalf("query seeded key: %v", err)
	}
	beforeCipher, ok := fixture.registry.EncryptedValue(keyRow.ID)
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
	fixture.service.dialects = dialect.NewSet(&recordingDiscoveryDialect{
		value: protocol.OpenAI,
		listFn: func(context.Context, string, string, state.HeaderRules) ([]string, error) {
			return []string{"remote-only"}, nil
		},
	})
	result, err := fixture.service.DiscoverModels(context.Background(), ModelDiscoveryRequest{
		UpstreamURL: "https://discover.example.com",
		Protocols:   []protocol.Protocol{protocol.OpenAI},
		Keys:        "sk-discovery",
		Config:      config.Settings{},
	})
	if err != nil || !reflect.DeepEqual(result.Models, []string{"remote-only"}) {
		t.Fatalf("DiscoverModels() = %#v, %v", result, err)
	}
	if !reflect.DeepEqual(queryTables, map[string]int{"system_settings": 1}) {
		t.Fatalf("discovery queries = %#v, want only system_settings once", queryTables)
	}
	if writeCount.Load() != 0 {
		t.Fatalf("discovery DB writes = %d, want 0", writeCount.Load())
	}
	if fixture.manager.Current() != beforeSnapshot ||
		fixture.manager.Current().Revision != beforeSnapshot.Revision {
		t.Fatal("discovery replaced or revised ConfigSnapshot")
	}
	if got, ok := fixture.registry.EncryptedValue(keyRow.ID); !ok || got != beforeCipher {
		t.Fatalf("Registry value = %q, %t, want unchanged", got, ok)
	}
	if afterRows := discoveryRowCounts(t, fixture.db); afterRows != beforeRows {
		t.Fatalf("row counts = %#v, want %#v", afterRows, beforeRows)
	}
	if _, exists := fixture.manager.Current().Candidates[protocol.OpenAI]["remote-only"]; exists {
		t.Fatal("discovered model leaked into ConfigSnapshot")
	}
	if created.GroupID == 0 {
		t.Fatal("invalid seeded group")
	}
}

func TestDiscoverModelsDoesNotAcquireWriteMu(t *testing.T) {
	fixture := newServiceFixture(t)
	fixture.service.dialects = dialect.NewSet(&recordingDiscoveryDialect{
		value: protocol.OpenAI,
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
			UpstreamURL: "https://discover.example.com",
			Protocols:   []protocol.Protocol{protocol.OpenAI},
			Keys:        "sk-discovery",
			Config:      config.Settings{},
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
	fixture.service.dialects = dialect.NewSet(&recordingDiscoveryDialect{
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
			UpstreamURL: "https://discover.example.com",
			Protocols:   []protocol.Protocol{protocol.Anthropic},
			Keys:        "sk-discovery",
			Config:      config.Settings{},
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
			UpstreamURL: "https://mutation.example.com",
			Protocols:   []protocol.Protocol{protocol.OpenAI},
			Keys:        "sk-mutation",
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
	for index, model := range []any{&models.Group{}, &models.UpstreamKey{}, &models.AccessKey{}} {
		if err := db.Model(model).Count(&result[index]).Error; err != nil {
			t.Fatalf("count %T rows: %v", model, err)
		}
	}
	return result
}
