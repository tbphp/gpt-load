package control

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"gpt-load/internal/dialect"
	"gpt-load/internal/health"
	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/protocol"
	"gpt-load/internal/state"
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

func TestExecuteModelDiscoveryUsesProtocolOuterKeyInnerFallback(t *testing.T) {
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
				if baseURL != "https://api.example.com/v1" || rules.Set["X-Test"] != "draft" {
					t.Fatalf("ListModels target = %q, %#v", baseURL, rules)
				}
				if value == protocol.Anthropic && apiKey == "key-a" {
					return make([]string, 0), nil
				}
				return nil, errors.New("try next combination")
			},
		}
	}
	service := &Service{
		dialects:              dialect.NewSet(newRecorder(protocol.OpenAI), newRecorder(protocol.Anthropic)),
		modelDiscoveryTimeout: time.Second,
	}
	result, err := service.executeModelDiscovery(context.Background(), discoveryTarget{
		baseURL:     "https://api.example.com/v1",
		protocols:   []protocol.Protocol{protocol.OpenAI, protocol.Anthropic},
		keys:        []string{"key-a", "key-b"},
		headerRules: state.HeaderRules{Set: map[string]string{"X-Test": "draft"}},
	})
	if err != nil {
		t.Fatalf("executeModelDiscovery() error = %v", err)
	}
	if result.Models == nil || len(result.Models) != 0 {
		t.Fatalf("models = %#v, want non-nil empty success", result.Models)
	}
	wantCalls := []string{
		"openai:key-a",
		"openai:key-b",
		"anthropic:key-a",
	}
	if !reflect.DeepEqual(calls, wantCalls) {
		t.Fatalf("calls = %#v, want %#v", calls, wantCalls)
	}
}

func TestExecuteModelDiscoveryRejectsMissingDialectBeforeHTTP(t *testing.T) {
	calls := 0
	openAI := &recordingDiscoveryDialect{
		value: protocol.OpenAI,
		listFn: func(context.Context, string, string, state.HeaderRules) ([]string, error) {
			calls++
			return nil, nil
		},
	}
	service := &Service{
		dialects:              dialect.NewSet(openAI),
		modelDiscoveryTimeout: time.Second,
	}
	target := discoveryTarget{
		baseURL:   "https://api.example.com",
		protocols: []protocol.Protocol{protocol.OpenAI, protocol.Anthropic},
		keys:      []string{"secret-key"},
	}
	_, err := service.executeModelDiscovery(context.Background(), target)
	if err == nil || errors.Is(err, app_errors.ErrBadGateway) {
		t.Fatalf("missing Dialect error = %v, want internal invariant error", err)
	}
	if calls != 0 {
		t.Fatalf("ListModels calls = %d, want preflight rejection", calls)
	}

	service.dialects = dialect.Set{protocol.OpenAI: openAI, protocol.Anthropic: nil}
	_, err = service.executeModelDiscovery(context.Background(), target)
	if err == nil || errors.Is(err, app_errors.ErrBadGateway) || calls != 0 {
		t.Fatalf("nil Dialect result = error %v, calls %d", err, calls)
	}

	for name, invalid := range map[string]discoveryTarget{
		"base URL":  {protocols: []protocol.Protocol{protocol.OpenAI}, keys: []string{"key"}},
		"protocols": {baseURL: "https://api.example.com", keys: []string{"key"}},
		"keys":      {baseURL: "https://api.example.com", protocols: []protocol.Protocol{protocol.OpenAI}},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := service.executeModelDiscovery(context.Background(), invalid); err == nil {
				t.Fatal("empty target error = nil")
			}
		})
	}
	if calls != 0 {
		t.Fatalf("ListModels calls after empty targets = %d, want 0", calls)
	}
}

func TestExecuteModelDiscoverySharesOneTotalTimeout(t *testing.T) {
	var deadlines []time.Time
	newRecorder := func(value protocol.Protocol) *recordingDiscoveryDialect {
		return &recordingDiscoveryDialect{
			value: value,
			listFn: func(ctx context.Context, _, _ string, _ state.HeaderRules) ([]string, error) {
				deadline, ok := ctx.Deadline()
				if !ok {
					t.Fatal("ListModels context has no deadline")
				}
				deadlines = append(deadlines, deadline)
				time.Sleep(2 * time.Millisecond)
				return nil, errors.New("retry")
			},
		}
	}
	service := &Service{
		dialects:              dialect.NewSet(newRecorder(protocol.OpenAI), newRecorder(protocol.Anthropic)),
		modelDiscoveryTimeout: 200 * time.Millisecond,
	}
	_, err := service.executeModelDiscovery(context.Background(), discoveryTarget{
		baseURL:   "https://api.example.com",
		protocols: []protocol.Protocol{protocol.OpenAI, protocol.Anthropic},
		keys:      []string{"key-a", "key-b"},
	})
	if !errors.Is(err, app_errors.ErrBadGateway) {
		t.Fatalf("executeModelDiscovery() error = %v, want ErrBadGateway", err)
	}
	if len(deadlines) != 4 {
		t.Fatalf("observed deadlines = %d, want all four combinations", len(deadlines))
	}
	for index := 1; index < len(deadlines); index++ {
		if !deadlines[index].Equal(deadlines[0]) {
			t.Fatalf("deadline[%d] = %v, want shared %v", index, deadlines[index], deadlines[0])
		}
	}
}

func TestExecuteModelDiscoveryRejectsSuccessAfterInternalTimeout(t *testing.T) {
	service := &Service{
		dialects: dialect.NewSet(&recordingDiscoveryDialect{
			value: protocol.OpenAI,
			listFn: func(ctx context.Context, _, _ string, _ state.HeaderRules) ([]string, error) {
				<-ctx.Done()
				return []string{"late-model"}, nil
			},
		}),
		modelDiscoveryTimeout: 10 * time.Millisecond,
	}
	result, err := service.executeModelDiscovery(context.Background(), discoveryTarget{
		baseURL:   "https://api.example.com",
		protocols: []protocol.Protocol{protocol.OpenAI},
		keys:      []string{"key-a"},
	})
	if !errors.Is(err, app_errors.ErrBadGateway) {
		t.Fatalf("executeModelDiscovery() = %#v, %v, want zero result and ErrBadGateway", result, err)
	}
	if result.Models != nil {
		t.Fatalf("models = %#v, want nil after internal timeout", result.Models)
	}
}

func TestExecuteModelDiscoveryReturnsParentCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	service := &Service{
		dialects: dialect.NewSet(&recordingDiscoveryDialect{
			value: protocol.OpenAI,
			listFn: func(discoveryCtx context.Context, _, _ string, _ state.HeaderRules) ([]string, error) {
				calls++
				cancel()
				<-discoveryCtx.Done()
				return nil, discoveryCtx.Err()
			},
		}),
		modelDiscoveryTimeout: time.Second,
	}
	_, err := service.executeModelDiscovery(ctx, discoveryTarget{
		baseURL:   "https://api.example.com",
		protocols: []protocol.Protocol{protocol.OpenAI},
		keys:      []string{"key-a", "key-b"},
	})
	if err != context.Canceled {
		t.Fatalf("executeModelDiscovery() error = %v, want exact context.Canceled", err)
	}
	if calls != 1 {
		t.Fatalf("ListModels calls = %d, want stop after parent cancellation", calls)
	}
}

func TestExecuteModelDiscoverySanitizesAllCombinationFailures(t *testing.T) {
	const (
		baseURLSecret = "https://secret.example.com/v1?token=query-secret"
		bodySecret    = "distinctive-upstream-body"
		partialSecret = "partial-secret-model"
	)
	keys := []string{"key-secret-a", "key-secret-b"}
	newRecorder := func(value protocol.Protocol) *recordingDiscoveryDialect {
		return &recordingDiscoveryDialect{
			value: value,
			listFn: func(_ context.Context, baseURL, apiKey string, _ state.HeaderRules) ([]string, error) {
				return []string{partialSecret}, fmt.Errorf(
					"provider failure url=%s key=%s body=%s",
					baseURL,
					apiKey,
					bodySecret,
				)
			},
		}
	}
	service := &Service{
		dialects:              dialect.NewSet(newRecorder(protocol.OpenAI), newRecorder(protocol.Anthropic)),
		modelDiscoveryTimeout: time.Second,
	}
	result, err := service.executeModelDiscovery(context.Background(), discoveryTarget{
		baseURL:   baseURLSecret,
		protocols: []protocol.Protocol{protocol.OpenAI, protocol.Anthropic},
		keys:      keys,
	})
	if !errors.Is(err, app_errors.ErrBadGateway) {
		t.Fatalf("executeModelDiscovery() error = %v, want ErrBadGateway", err)
	}
	if result.Models != nil {
		t.Fatalf("partial result = %#v, want zero result", result)
	}
	for _, forbidden := range append(
		[]string{baseURLSecret, "secret.example.com", "query-secret", bodySecret, partialSecret},
		keys...,
	) {
		if strings.Contains(err.Error(), forbidden) {
			t.Fatalf("error exposes %q: %s", forbidden, err)
		}
	}
}
