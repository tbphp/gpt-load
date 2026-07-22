package control

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sirupsen/logrus"

	"gpt-load/internal/dialect"
	"gpt-load/internal/health"
	"gpt-load/internal/protocol"
	"gpt-load/internal/state"
)

func TestValidationWorkerUsesExplicitModelAndFirstProtocol(t *testing.T) {
	probes := &validationProbeRecorder{}
	worker := newValidationWorkerForTest(
		validationSnapshot(map[uint]state.GroupView{
			1: validationGroup([]protocol.Protocol{protocol.Anthropic, protocol.OpenAI}, " explicit-model ", nil),
		}),
		[]state.KeyRef{{ID: 7, GroupID: 1, EncryptedValue: "key-7"}},
		probes,
	)

	worker.Validate(context.Background())

	if got, want := probes.calls(), []validationProbeCall{{protocol: protocol.Anthropic, model: "explicit-model", apiKey: "plain-key-7"}}; !sameValidationProbeCalls(got, want) {
		t.Fatalf("Probe calls = %#v, want %#v", got, want)
	}
}

func TestValidationWorkerFallsBackToFirstRealModelID(t *testing.T) {
	probes := &validationProbeRecorder{}
	worker := newValidationWorkerForTest(
		validationSnapshot(map[uint]state.GroupView{
			1: validationGroup([]protocol.Protocol{protocol.OpenAI}, " \t", []state.ModelConfig{{ID: "  real-model  ", Alias: "external-model"}}),
		}),
		[]state.KeyRef{{ID: 7, GroupID: 1, EncryptedValue: "key-7"}},
		probes,
	)

	worker.Validate(context.Background())

	if got, want := probes.calls(), []validationProbeCall{{protocol: protocol.OpenAI, model: "real-model", apiKey: "plain-key-7"}}; !sameValidationProbeCalls(got, want) {
		t.Fatalf("Probe calls = %#v, want %#v", got, want)
	}
}

func TestValidationWorkerSkipsMissingGroupProtocolModelAndDialect(t *testing.T) {
	probes := &validationProbeRecorder{}
	snapshot := validationSnapshot(map[uint]state.GroupView{
		1: validationGroup(nil, "", nil),
		2: validationGroup([]protocol.Protocol{protocol.OpenAI}, " \t", nil),
		3: validationGroup([]protocol.Protocol{protocol.Gemini}, "model", nil),
	})
	worker := newValidationWorkerForTest(snapshot, []state.KeyRef{
		{ID: 1, GroupID: 9, EncryptedValue: "key-1"},
		{ID: 2, GroupID: 1, EncryptedValue: "key-2"},
		{ID: 3, GroupID: 2, EncryptedValue: "key-3"},
		{ID: 4, GroupID: 3, EncryptedValue: "key-4"},
	}, probes)

	worker.Validate(context.Background())

	if got := probes.calls(); len(got) != 0 {
		t.Fatalf("Probe calls = %#v, want none", got)
	}
	if got := worker.registry.(*validationRegistryRecorder).events(); len(got) != 0 {
		t.Fatalf("recovery events = %#v, want none", got)
	}
}

func TestValidationWorkerKeepsKeyBlacklistedOnDecryptOrProbeFailure(t *testing.T) {
	probes := &validationProbeRecorder{errByKey: map[string]error{"plain-key-2": errors.New("probe failed")}}
	worker := newValidationWorkerForTest(
		validationSnapshot(map[uint]state.GroupView{1: validationGroup([]protocol.Protocol{protocol.OpenAI}, "model", nil)}),
		[]state.KeyRef{
			{ID: 1, GroupID: 1, EncryptedValue: "decrypt-fails"},
			{ID: 2, GroupID: 1, EncryptedValue: "key-2"},
		},
		probes,
	)
	worker.decryptor = validationDecryptor{errors: map[string]error{"decrypt-fails": errors.New("decrypt failed")}}

	worker.Validate(context.Background())

	if got, want := probes.calls(), []validationProbeCall{{protocol: protocol.OpenAI, model: "model", apiKey: "plain-key-2"}}; !sameValidationProbeCalls(got, want) {
		t.Fatalf("Probe calls = %#v, want %#v", got, want)
	}
	if got := worker.registry.(*validationRegistryRecorder).events(); len(got) != 0 {
		t.Fatalf("recovery events = %#v, want none", got)
	}
}

func TestValidationWorkerRecoversInRequiredOrder(t *testing.T) {
	probes := &validationProbeRecorder{}
	worker := newValidationWorkerForTest(
		validationSnapshot(map[uint]state.GroupView{1: validationGroup([]protocol.Protocol{protocol.OpenAI}, "model", nil)}),
		[]state.KeyRef{{ID: 7, GroupID: 1, EncryptedValue: "key-7"}},
		probes,
	)

	worker.Validate(context.Background())

	if got, want := worker.recorder.events(), []string{"stats.reset:7", "registry.weight:7:50", "registry.recover:7"}; !sameValidationEvents(got, want) {
		t.Fatalf("recovery events = %#v, want %#v", got, want)
	}
	if got := worker.snapshots.(*validationSnapshotRecorder).calls(); got != 1 {
		t.Fatalf("snapshot reads = %d, want 1", got)
	}
	if got := worker.registry.(*validationRegistryRecorder).blacklistedCalls(); got != 1 {
		t.Fatalf("BlacklistedKeys calls = %d, want 1", got)
	}
}

func TestValidationWorkerDoesNotReportRecoveredWhenConditionalRecoveryFails(t *testing.T) {
	probes := &validationProbeRecorder{}
	worker := newValidationWorkerForTest(
		validationSnapshot(map[uint]state.GroupView{1: validationGroup([]protocol.Protocol{protocol.OpenAI}, "model", nil)}),
		[]state.KeyRef{{ID: 7, GroupID: 1, EncryptedValue: "key-7"}},
		probes,
	)
	worker.registry.(*validationRegistryRecorder).recoveryOK = false

	worker.Validate(context.Background())

	if got, want := worker.recorder.events(), []string{"stats.reset:7"}; !sameValidationEvents(got, want) {
		t.Fatalf("recovery events = %#v, want %#v", got, want)
	}
}

func TestValidationWorkerDoesNotRecoverDisabledOrReplacedKeyRef(t *testing.T) {
	tests := []struct {
		name            string
		mutate          func(t *testing.T, registry *state.KeyRegistry)
		expectedCipher  string
		expectedEnabled bool
	}{
		{
			name: "disabled after sweep", expectedCipher: "cipher-original", mutate: func(t *testing.T, registry *state.KeyRegistry) {
				t.Helper()
				if err := registry.SetKeyStatus(7, state.KeyStatusDisabled); err != nil {
					t.Fatalf("SetKeyStatus() error = %v", err)
				}
			},
		},
		{
			name: "replaced after sweep", expectedCipher: "cipher-replaced", expectedEnabled: true, mutate: func(t *testing.T, registry *state.KeyRegistry) {
				t.Helper()
				if err := registry.Replace([]state.KeyEntry{{
					ID: 7, GroupID: 1, Status: state.KeyStatusActive, Blacklisted: true,
					FailureCount: 5, WeightAuto: 17, EncryptedValue: "cipher-replaced",
				}}); err != nil {
					t.Fatalf("Replace() error = %v", err)
				}
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			registry := state.NewKeyRegistry()
			if err := registry.Replace([]state.KeyEntry{{
				ID: 7, GroupID: 1, Status: state.KeyStatusActive, Blacklisted: true,
				FailureCount: 3, WeightAuto: 17, EncryptedValue: "cipher-original",
			}}); err != nil {
				t.Fatalf("Replace() error = %v", err)
			}
			probeStarted := make(chan struct{})
			releaseProbe := make(chan struct{})
			worker := newRealRegistryValidationWorker(registry, &validationProbeRecorder{probe: func(ctx context.Context, _ protocol.Protocol, _ string, _ string) error {
				close(probeStarted)
				select {
				case <-releaseProbe:
					return nil
				case <-ctx.Done():
					return ctx.Err()
				}
			}})
			done := make(chan struct{})
			go func() {
				worker.Validate(context.Background())
				close(done)
			}()
			awaitSignal(t, probeStarted)
			test.mutate(t, registry)
			close(releaseProbe)
			awaitValidationDone(t, done)

			if got := len(registry.ActiveKeyIDs()); test.expectedEnabled && got != 1 {
				t.Fatalf("active key count = %d, want 1", got)
			} else if !test.expectedEnabled && got != 0 {
				t.Fatalf("active key count = %d, want 0", got)
			}
			if !test.expectedEnabled {
				if err := registry.SetKeyStatus(7, state.KeyStatusActive); err != nil {
					t.Fatalf("SetKeyStatus() error = %v", err)
				}
			}
			if got, want := registry.BlacklistedKeys(), []state.KeyRef{{ID: 7, GroupID: 1, EncryptedValue: test.expectedCipher}}; !reflect.DeepEqual(got, want) {
				t.Fatalf("blacklisted keys after stale recovery = %#v, want %#v", got, want)
			}
			if got := registry.CollectCandidates([]uint{1}, nil, time.Time{}); len(got) != 0 {
				t.Fatalf("candidates after stale recovery = %#v, want none", got)
			}
		})
	}
}

func TestValidationWorkerFailureLogUsesSafeStructuredFields(t *testing.T) {
	var logs bytes.Buffer
	logger := logrus.StandardLogger()
	previousOutput, previousFormatter, previousLevel := logger.Out, logger.Formatter, logger.GetLevel()
	logrus.SetOutput(&logs)
	logrus.SetFormatter(&logrus.JSONFormatter{DisableTimestamp: true})
	logrus.SetLevel(logrus.WarnLevel)
	t.Cleanup(func() {
		logrus.SetOutput(previousOutput)
		logrus.SetFormatter(previousFormatter)
		logrus.SetLevel(previousLevel)
	})

	worker := newValidationWorkerForTest(
		validationSnapshot(map[uint]state.GroupView{1: {
			UpstreamURL:     "https://sensitive.example.com/path",
			Protocols:       []protocol.Protocol{protocol.OpenAI},
			ValidationModel: "model",
		}}),
		[]state.KeyRef{{ID: 7, GroupID: 1, EncryptedValue: "cipher-secret"}},
		&validationProbeRecorder{},
	)
	worker.decryptor = validationDecryptor{errors: map[string]error{"cipher-secret": errors.New("plain-secret underlying failure")}}

	worker.Validate(context.Background())

	output := logs.String()
	if !strings.Contains(output, `"stage":"decrypt"`) {
		t.Fatalf("log output = %q, want decrypt stage", output)
	}
	for _, forbidden := range []string{"cipher-secret", "plain-secret", "https://sensitive.example.com", "underlying failure"} {
		if strings.Contains(output, forbidden) {
			t.Fatalf("log output leaked %q: %q", forbidden, output)
		}
	}
}

func TestValidationWorkerLimitsGlobalConcurrencyToEight(t *testing.T) {
	started := make(chan uint, 9)
	release := make(chan struct{})
	probes := &validationProbeRecorder{
		probe: func(ctx context.Context, _ protocol.Protocol, _ string, apiKey string) error {
			keyID := validationKeyID(apiKey)
			started <- keyID
			select {
			case <-release:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		},
	}
	worker := newValidationWorkerForTest(
		validationSnapshot(map[uint]state.GroupView{1: validationGroup([]protocol.Protocol{protocol.OpenAI}, "model", nil)}),
		validationRefs(9),
		probes,
	)
	done := make(chan struct{})
	go func() {
		worker.Validate(context.Background())
		close(done)
	}()

	for range validationConcurrency {
		awaitValidationStart(t, started)
	}
	if got := probes.maxActive(); got != validationConcurrency {
		t.Fatalf("maximum active probes = %d, want %d", got, validationConcurrency)
	}
	select {
	case keyID := <-started:
		t.Fatalf("key %d started before a worker was released", keyID)
	default:
	}

	close(release)
	awaitValidationDone(t, done)
	if got, want := len(probes.calls()), 9; got != want {
		t.Fatalf("Probe calls = %d, want %d", got, want)
	}
}

func TestValidationWorkerCancellationStopsDispatchAndInFlightProbes(t *testing.T) {
	started := make(chan uint, 9)
	probes := &validationProbeRecorder{
		probe: func(ctx context.Context, _ protocol.Protocol, _ string, apiKey string) error {
			started <- validationKeyID(apiKey)
			<-ctx.Done()
			return ctx.Err()
		},
	}
	worker := newValidationWorkerForTest(
		validationSnapshot(map[uint]state.GroupView{1: validationGroup([]protocol.Protocol{protocol.OpenAI}, "model", nil)}),
		validationRefs(9),
		probes,
	)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		worker.Validate(ctx)
		close(done)
	}()

	for range validationConcurrency {
		awaitValidationStart(t, started)
	}
	cancel()
	awaitValidationDone(t, done)
	select {
	case keyID := <-started:
		t.Fatalf("key %d started after cancellation", keyID)
	default:
	}
	if got, want := len(probes.calls()), validationConcurrency; got != want {
		t.Fatalf("Probe calls = %d, want %d", got, want)
	}
}

func TestValidationWorkerDoesNotProbeQueuedJobAfterCancellation(t *testing.T) {
	probes := &validationProbeRecorder{}
	worker := newValidationWorkerForTest(
		validationSnapshot(map[uint]state.GroupView{1: validationGroup([]protocol.Protocol{protocol.OpenAI}, "model", nil)}),
		nil,
		probes,
	)
	jobs := make(chan state.KeyRef, 1)
	jobs <- state.KeyRef{ID: 7, GroupID: 1, EncryptedValue: "key-7"}
	close(jobs)
	ctx := &validationCancelAfterJobContext{
		done:    make(chan struct{}),
		checked: make(chan uint, 1),
		release: make(chan struct{}),
	}
	finished := make(chan struct{})
	go func() {
		worker.consumeValidationJobs(ctx, worker.snapshots.Current(), jobs)
		close(finished)
	}()
	awaitValidationStart(t, ctx.checked)
	close(ctx.done)
	close(ctx.release)
	awaitValidationDone(t, finished)

	if got := probes.calls(); len(got) != 0 {
		t.Fatalf("Probe calls = %#v, want none after cancellation", got)
	}
	if got := worker.recorder.events(); len(got) != 0 {
		t.Fatalf("recovery events = %#v, want none after cancellation", got)
	}
}

type validationCancelAfterJobContext struct {
	done    chan struct{}
	checked chan uint
	release chan struct{}
}

func (*validationCancelAfterJobContext) Deadline() (time.Time, bool) {
	return time.Time{}, false
}

func (ctx *validationCancelAfterJobContext) Done() <-chan struct{} {
	return ctx.done
}

func (ctx *validationCancelAfterJobContext) Err() error {
	ctx.checked <- 1
	<-ctx.release
	return context.Canceled
}

func (*validationCancelAfterJobContext) Value(any) any {
	return nil
}

type validationSnapshotRecorder struct {
	mu       sync.Mutex
	snapshot *state.ConfigSnapshot
	read     int
}

func (source *validationSnapshotRecorder) Current() *state.ConfigSnapshot {
	source.mu.Lock()
	defer source.mu.Unlock()
	source.read++
	return source.snapshot
}

func (source *validationSnapshotRecorder) calls() int {
	source.mu.Lock()
	defer source.mu.Unlock()
	return source.read
}

type validationEventRecorder struct {
	mu    sync.Mutex
	items []string
}

func (recorder *validationEventRecorder) add(event string) {
	recorder.mu.Lock()
	recorder.items = append(recorder.items, event)
	recorder.mu.Unlock()
}

func (recorder *validationEventRecorder) events() []string {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	return append([]string(nil), recorder.items...)
}

type validationRegistryRecorder struct {
	mu              sync.Mutex
	refs            []state.KeyRef
	blacklistedRead int
	recoveryOK      bool
	recorder        *validationEventRecorder
}

func (registry *validationRegistryRecorder) BlacklistedKeys() []state.KeyRef {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	registry.blacklistedRead++
	return append([]state.KeyRef(nil), registry.refs...)
}

func (registry *validationRegistryRecorder) RecoverIfMatch(ref state.KeyRef, weight int) bool {
	registry.mu.Lock()
	recoveryOK := registry.recoveryOK
	registry.mu.Unlock()
	if !recoveryOK {
		return false
	}
	registry.recorder.add(fmt.Sprintf("registry.weight:%d:%d", ref.ID, weight))
	registry.recorder.add(fmt.Sprintf("registry.recover:%d", ref.ID))
	return true
}

func (registry *validationRegistryRecorder) events() []string {
	return registry.recorder.events()
}

func (registry *validationRegistryRecorder) blacklistedCalls() int {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	return registry.blacklistedRead
}

type validationStatsRecorder struct {
	recorder *validationEventRecorder
}

func (stats *validationStatsRecorder) Reset(keyID uint) {
	stats.recorder.add(fmt.Sprintf("stats.reset:%d", keyID))
}

type validationDecryptor struct {
	errors map[string]error
}

func (decryptor validationDecryptor) Decrypt(value string) (string, error) {
	if err := decryptor.errors[value]; err != nil {
		return "", err
	}
	return "plain-" + value, nil
}

type validationProbeCall struct {
	protocol protocol.Protocol
	model    string
	apiKey   string
}

type validationProbeRecorder struct {
	mu       sync.Mutex
	items    []validationProbeCall
	active   int
	maximum  int
	errByKey map[string]error
	probe    func(context.Context, protocol.Protocol, string, string) error
}

func (recorder *validationProbeRecorder) calls() []validationProbeCall {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	return append([]validationProbeCall(nil), recorder.items...)
}

func (recorder *validationProbeRecorder) maxActive() int {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	return recorder.maximum
}

func (recorder *validationProbeRecorder) invoke(ctx context.Context, p protocol.Protocol, model, apiKey string) error {
	recorder.mu.Lock()
	recorder.items = append(recorder.items, validationProbeCall{protocol: p, model: model, apiKey: apiKey})
	recorder.active++
	if recorder.active > recorder.maximum {
		recorder.maximum = recorder.active
	}
	recorder.mu.Unlock()
	defer func() {
		recorder.mu.Lock()
		recorder.active--
		recorder.mu.Unlock()
	}()
	if recorder.probe != nil {
		return recorder.probe(ctx, p, model, apiKey)
	}
	return recorder.errByKey[apiKey]
}

type validationTestDialect struct {
	protocol protocol.Protocol
	probes   *validationProbeRecorder
}

func (dialect *validationTestDialect) Protocol() protocol.Protocol {
	return dialect.protocol
}

func (*validationTestDialect) ExtractModel(*dialect.ParsedRequest) (string, bool, error) {
	return "", false, nil
}

func (*validationTestDialect) BuildUpstreamURL(string, *dialect.ParsedRequest) (string, error) {
	return "", nil
}

func (*validationTestDialect) InjectCredential(http.Header, string) {}

func (*validationTestDialect) ListModels(context.Context, string, string, state.HeaderRules) ([]string, error) {
	return nil, nil
}

func (dialect *validationTestDialect) Probe(ctx context.Context, _ string, apiKey string, _ state.HeaderRules, model string) error {
	return dialect.probes.invoke(ctx, dialect.protocol, model, apiKey)
}

func (*validationTestDialect) ClassifyStatus(int, []byte) health.FailureCategory {
	return health.FailureCategoryAmbiguous
}

type validationTestWorker struct {
	*validationWorker
	recorder *validationEventRecorder
}

func newValidationWorkerForTest(snapshot *state.ConfigSnapshot, refs []state.KeyRef, probes *validationProbeRecorder) *validationTestWorker {
	recorder := &validationEventRecorder{}
	registry := &validationRegistryRecorder{refs: refs, recoveryOK: true, recorder: recorder}
	dialects := dialect.Set{}
	for _, p := range []protocol.Protocol{protocol.OpenAI, protocol.Anthropic} {
		dialects[p] = &validationTestDialect{protocol: p, probes: probes}
	}
	return &validationTestWorker{
		validationWorker: &validationWorker{
			snapshots:   &validationSnapshotRecorder{snapshot: snapshot},
			registry:    registry,
			stats:       &validationStatsRecorder{recorder: recorder},
			decryptor:   validationDecryptor{},
			dialects:    dialects,
			maintenance: &sync.Mutex{},
		},
		recorder: recorder,
	}
}

func newRealRegistryValidationWorker(registry *state.KeyRegistry, probes *validationProbeRecorder) *validationWorker {
	return &validationWorker{
		snapshots: &validationSnapshotRecorder{snapshot: validationSnapshot(map[uint]state.GroupView{
			1: validationGroup([]protocol.Protocol{protocol.OpenAI}, "model", nil),
		})},
		registry:    registry,
		stats:       health.NewStatsStore(),
		decryptor:   validationDecryptor{},
		dialects:    dialect.Set{protocol.OpenAI: &validationTestDialect{protocol: protocol.OpenAI, probes: probes}},
		maintenance: &sync.Mutex{},
	}
}

func validationSnapshot(groups map[uint]state.GroupView) *state.ConfigSnapshot {
	return &state.ConfigSnapshot{Groups: groups}
}

func validationGroup(protocols []protocol.Protocol, validationModel string, models []state.ModelConfig) state.GroupView {
	return state.GroupView{
		UpstreamURL:     "https://upstream.example.com",
		Protocols:       protocols,
		ValidationModel: validationModel,
		Models:          models,
	}
}

func validationRefs(count int) []state.KeyRef {
	refs := make([]state.KeyRef, count)
	for index := range refs {
		keyID := uint(index + 1)
		refs[index] = state.KeyRef{ID: keyID, GroupID: 1, EncryptedValue: fmt.Sprintf("key-%d", keyID)}
	}
	return refs
}

func validationKeyID(apiKey string) uint {
	var keyID uint
	if _, err := fmt.Sscanf(apiKey, "plain-key-%d", &keyID); err != nil {
		panic(fmt.Sprintf("parse key ID from %q: %v", apiKey, err))
	}
	return keyID
}

func awaitValidationStart(t *testing.T, started <-chan uint) uint {
	t.Helper()
	select {
	case keyID := <-started:
		return keyID
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for a probe to start")
		return 0
	}
}

func awaitValidationDone(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for validation worker to return")
	}
}

func sameValidationProbeCalls(got, want []validationProbeCall) bool {
	if len(got) != len(want) {
		return false
	}
	for index := range got {
		if got[index] != want[index] {
			return false
		}
	}
	return true
}

func sameValidationEvents(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for index := range got {
		if got[index] != want[index] {
			return false
		}
	}
	return true
}
