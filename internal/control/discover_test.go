package control

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"gorm.io/gorm"

	"gpt-load/internal/dialect"
	"gpt-load/internal/health"
	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/protocol"
	"gpt-load/internal/state"
	"gpt-load/internal/storage/models"
)

type recordingDiscoveryDialect struct {
	value  protocol.Protocol
	listFn func(context.Context, string, string, state.HeaderRules) ([]string, error)
}

func (d *recordingDiscoveryDialect) Protocol() protocol.Protocol {
	return d.value
}

func (*recordingDiscoveryDialect) ExtractModel(*dialect.ParsedRequest) (string, bool, error) {
	return "", false, nil
}

func (*recordingDiscoveryDialect) BuildUpstreamURL(string, *dialect.ParsedRequest) (string, error) {
	return "", nil
}

func (*recordingDiscoveryDialect) InjectCredential(http.Header, string) {}

func (d *recordingDiscoveryDialect) ListModels(
	ctx context.Context,
	baseURL, apiKey string,
	rules state.HeaderRules,
) ([]string, error) {
	return d.listFn(ctx, baseURL, apiKey, rules)
}

func (*recordingDiscoveryDialect) ClassifyStatus(int, []byte) health.ErrorClass {
	return health.ErrorClassNonRetryable
}

func TestDiscoverModelsValidatesAndPreservesOrder(t *testing.T) {
	fixture := newServiceFixture(t)
	tests := []ModelDiscoveryRequest{
		{UpstreamURL: "", Protocol: protocol.OpenAI, Key: "sk-key"},
		{UpstreamURL: "/relative", Protocol: protocol.OpenAI, Key: "sk-key"},
		{UpstreamURL: "https://api.example.com", Protocol: "", Key: "sk-key"},
		{UpstreamURL: "https://api.example.com", Protocol: "unknown", Key: "sk-key"},
		{UpstreamURL: "https://api.example.com", Protocol: protocol.OpenAIResponse, Key: "sk-key"},
		{UpstreamURL: "https://api.example.com", Protocol: protocol.OpenAI, Key: "   "},
	}
	for _, request := range tests {
		if _, err := fixture.service.DiscoverModels(context.Background(), request); err == nil {
			t.Fatalf("DiscoverModels(%#v) error = nil", request)
		}
	}

	var gotURL, gotKey string
	var gotRules state.HeaderRules
	recorder := &recordingDiscoveryDialect{
		value: protocol.Anthropic,
		listFn: func(
			_ context.Context,
			baseURL, apiKey string,
			rules state.HeaderRules,
		) ([]string, error) {
			gotURL, gotKey, gotRules = baseURL, apiKey, rules
			return []string{"z-model", "a-model"}, nil
		},
	}
	fixture.service.dialects = dialect.NewSet(recorder)
	result, err := fixture.service.DiscoverModels(context.Background(), ModelDiscoveryRequest{
		UpstreamURL: " HTTPS://API.Example.COM/v1/ ",
		Protocol:    protocol.Anthropic,
		Key:         " upstream-key ",
	})
	if err != nil {
		t.Fatalf("DiscoverModels() error = %v", err)
	}
	if gotURL != "https://api.example.com/v1" || gotKey != "upstream-key" {
		t.Fatalf("ListModels inputs = %q, %q", gotURL, gotKey)
	}
	if len(gotRules.Set) != 0 || len(gotRules.Remove) != 0 {
		t.Fatalf("HeaderRules = %#v, want empty", gotRules)
	}
	if !reflect.DeepEqual(result.Models, []string{"z-model", "a-model"}) {
		t.Fatalf("Models = %#v, want upstream order", result.Models)
	}

	recorder.listFn = func(
		context.Context,
		string,
		string,
		state.HeaderRules,
	) ([]string, error) {
		return nil, nil
	}
	empty, err := fixture.service.DiscoverModels(context.Background(), ModelDiscoveryRequest{
		UpstreamURL: "https://api.example.com",
		Protocol:    protocol.Anthropic,
		Key:         "upstream-key",
	})
	if err != nil || empty.Models == nil || len(empty.Models) != 0 {
		t.Fatalf("empty DiscoverModels() = %#v, %v, want non-nil empty", empty, err)
	}

	fixture.service.dialects = dialect.NewSet()
	_, err = fixture.service.DiscoverModels(context.Background(), ModelDiscoveryRequest{
		UpstreamURL: "https://api.example.com",
		Protocol:    protocol.Gemini,
		Key:         "upstream-key",
	})
	if err == nil || errors.Is(err, app_errors.ErrBadGateway) {
		t.Fatalf("missing Dialect error = %v, want internal invariant error", err)
	}

	fixture.service.dialects = dialect.Set{protocol.Gemini: nil}
	_, err = fixture.service.DiscoverModels(context.Background(), ModelDiscoveryRequest{
		UpstreamURL: "https://api.example.com",
		Protocol:    protocol.Gemini,
		Key:         "upstream-key",
	})
	if err == nil || errors.Is(err, app_errors.ErrBadGateway) {
		t.Fatalf("nil Dialect error = %v, want internal invariant error", err)
	}
}

func TestDiscoverModelsUsesOpenAIListModels(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet || request.URL.Path != "/v1/models" {
			t.Errorf("request = %s %s", request.Method, request.URL.Path)
		}
		if got := request.Header.Get("Authorization"); got != "Bearer upstream-key" {
			t.Errorf("Authorization = %q", got)
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"object":"list","data":[{"id":"gpt-4.1"},{"id":"gpt-4o"}]}`))
	}))
	t.Cleanup(upstream.Close)

	fixture := newServiceFixture(t)
	fixture.service.dialects = dialect.NewSet(dialect.NewOpenAI(upstream.Client()))
	result, err := fixture.service.DiscoverModels(context.Background(), ModelDiscoveryRequest{
		UpstreamURL: upstream.URL,
		Protocol:    protocol.OpenAI,
		Key:         "upstream-key",
	})
	if err != nil {
		t.Fatalf("DiscoverModels() error = %v", err)
	}
	if !reflect.DeepEqual(result.Models, []string{"gpt-4.1", "gpt-4o"}) {
		t.Fatalf("Models = %#v", result.Models)
	}
}

func TestDiscoverModelsDoesNotReadOrMutateRuntimeState(t *testing.T) {
	fixture := newServiceFixture(t)
	imported, err := fixture.service.Import(context.Background(), ImportRequest{
		UpstreamURL: "https://state.example.com",
		Protocols:   []protocol.Protocol{protocol.OpenAI},
		Keys:        "sk-state",
		Models: optionalGroupModels{
			Set: true, Values: []GroupModel{{ID: "gpt-4o"}},
		},
	})
	if err != nil {
		t.Fatalf("seed Import() error = %v", err)
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

	var queryCount atomic.Int64
	callbackName := "test:discover-models-query-count"
	if err := fixture.db.Callback().Query().Before("gorm:query").Register(
		callbackName,
		func(*gorm.DB) { queryCount.Add(1) },
	); err != nil {
		t.Fatalf("register query callback: %v", err)
	}
	t.Cleanup(func() {
		_ = fixture.db.Callback().Query().Remove(callbackName)
	})
	recorder := &recordingDiscoveryDialect{
		value: protocol.OpenAI,
		listFn: func(
			context.Context,
			string,
			string,
			state.HeaderRules,
		) ([]string, error) {
			return []string{"remote-only"}, nil
		},
	}
	fixture.service.dialects = dialect.NewSet(recorder)
	result, err := fixture.service.DiscoverModels(context.Background(), ModelDiscoveryRequest{
		UpstreamURL: "https://discover.example.com",
		Protocol:    protocol.OpenAI,
		Key:         "sk-discovery",
	})
	if err != nil || !reflect.DeepEqual(result.Models, []string{"remote-only"}) {
		t.Fatalf("DiscoverModels() = %#v, %v", result, err)
	}
	if got := queryCount.Load(); got != 0 {
		t.Fatalf("discovery DB query count = %d, want 0", got)
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
	if imported.GroupID == 0 {
		t.Fatal("invalid seeded group")
	}
}

func TestDiscoverModelsDoesNotAcquireWriteMu(t *testing.T) {
	fixture := newServiceFixture(t)
	fixture.service.dialects = dialect.NewSet(&recordingDiscoveryDialect{
		value: protocol.OpenAI,
		listFn: func(
			context.Context,
			string,
			string,
			state.HeaderRules,
		) ([]string, error) {
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
			Protocol:    protocol.OpenAI,
			Key:         "sk-discovery",
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
			Protocol:    protocol.Anthropic,
			Key:         "sk-discovery",
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
		_, err := fixture.service.Import(context.Background(), ImportRequest{
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
			t.Fatalf("Import() error = %v", err)
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

func TestDiscoverModelsMapsTimeoutAndUpstreamErrors(t *testing.T) {
	t.Run("service timeout", func(t *testing.T) {
		fixture := newServiceFixture(t)
		fixture.service.modelDiscoveryTimeout = 20 * time.Millisecond
		fixture.service.dialects = dialect.NewSet(&recordingDiscoveryDialect{
			value: protocol.OpenAI,
			listFn: func(
				ctx context.Context,
				_ string,
				_ string,
				_ state.HeaderRules,
			) ([]string, error) {
				<-ctx.Done()
				return nil, ctx.Err()
			},
		})
		_, err := fixture.service.DiscoverModels(context.Background(), ModelDiscoveryRequest{
			UpstreamURL: "https://timeout.example.com",
			Protocol:    protocol.OpenAI,
			Key:         "sk-timeout",
		})
		if !errors.Is(err, app_errors.ErrBadGateway) {
			t.Fatalf("DiscoverModels() error = %v, want ErrBadGateway", err)
		}
	})

	t.Run("upstream error is detached from secrets", func(t *testing.T) {
		const (
			keySecret   = "sk-discovery-distinctive-secret"
			querySecret = "query-distinctive-secret"
		)
		fixture := newServiceFixture(t)
		fixture.service.dialects = dialect.NewSet(&recordingDiscoveryDialect{
			value: protocol.OpenAI,
			listFn: func(
				context.Context,
				string,
				string,
				state.HeaderRules,
			) ([]string, error) {
				return nil, fmt.Errorf("provider failure containing %s and %s", keySecret, querySecret)
			},
		})
		_, err := fixture.service.DiscoverModels(context.Background(), ModelDiscoveryRequest{
			UpstreamURL: "https://error.example.com?token=" + querySecret,
			Protocol:    protocol.OpenAI,
			Key:         keySecret,
		})
		if !errors.Is(err, app_errors.ErrBadGateway) {
			t.Fatalf("DiscoverModels() error = %v, want ErrBadGateway", err)
		}
		for _, secret := range []string{keySecret, querySecret} {
			if errorText := err.Error(); strings.Contains(errorText, secret) {
				t.Fatalf("error exposes %q: %s", secret, errorText)
			}
		}
	})
}

func TestDiscoverModelsStopsOnParentCancellation(t *testing.T) {
	var calls atomic.Int64
	fixture := newServiceFixture(t)
	fixture.service.dialects = dialect.NewSet(&recordingDiscoveryDialect{
		value: protocol.OpenAI,
		listFn: func(
			context.Context,
			string,
			string,
			state.HeaderRules,
		) ([]string, error) {
			calls.Add(1)
			return nil, nil
		},
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := fixture.service.DiscoverModels(ctx, ModelDiscoveryRequest{
		UpstreamURL: "https://cancel.example.com",
		Protocol:    protocol.OpenAI,
		Key:         "sk-cancel",
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("DiscoverModels() error = %v, want context.Canceled", err)
	}
	if calls.Load() != 0 {
		t.Fatalf("ListModels calls = %d, want 0", calls.Load())
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
