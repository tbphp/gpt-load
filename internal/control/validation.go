package control

import (
	"context"
	"strings"
	"sync"

	"github.com/sirupsen/logrus"

	"gpt-load/internal/dialect"
	"gpt-load/internal/health"
	"gpt-load/internal/platform/encryption"
	"gpt-load/internal/state"
)

const validationConcurrency = 8

type validationSweep interface {
	Validate(context.Context)
}

type validationRegistry interface {
	BlacklistedKeys() []state.KeyRef
	RecoverIfMatch(ref state.KeyRef, weight int) bool
}

type statsResetter interface {
	Reset(uint)
}

type snapshotSource interface {
	Current() *state.ConfigSnapshot
}

type credentialDecryptor interface {
	Decrypt(string) (string, error)
}

type validationWorker struct {
	snapshots   snapshotSource
	registry    validationRegistry
	stats       statsResetter
	decryptor   credentialDecryptor
	dialects    dialect.Set
	maintenance *sync.Mutex
}

var _ validationSweep = (*validationWorker)(nil)

func newValidationWorker(
	manager *state.Manager,
	registry *state.KeyRegistry,
	stats *health.StatsStore,
	decryptor encryption.Service,
	dialects dialect.Set,
	maintenance *sync.Mutex,
) *validationWorker {
	return &validationWorker{
		snapshots:   manager,
		registry:    registry,
		stats:       stats,
		decryptor:   decryptor,
		dialects:    dialects,
		maintenance: maintenance,
	}
}

func (worker *validationWorker) Validate(ctx context.Context) {
	if worker == nil || worker.snapshots == nil || worker.registry == nil {
		return
	}
	snapshot := worker.snapshots.Current()
	if snapshot == nil {
		return
	}
	refs := worker.registry.BlacklistedKeys()
	if len(refs) == 0 {
		return
	}

	concurrency := min(validationConcurrency, len(refs))

	jobs := make(chan state.KeyRef)
	var workers sync.WaitGroup
	workers.Add(concurrency)
	for range concurrency {
		go func() {
			defer workers.Done()
			worker.consumeValidationJobs(ctx, snapshot, jobs)
		}()
	}

dispatch:
	for _, ref := range refs {
		if ctx.Err() != nil {
			break
		}
		select {
		case <-ctx.Done():
			break dispatch
		case jobs <- ref:
		}
	}
	close(jobs)
	workers.Wait()
}

func (worker *validationWorker) consumeValidationJobs(
	ctx context.Context,
	snapshot *state.ConfigSnapshot,
	jobs <-chan state.KeyRef,
) {
	for {
		select {
		case <-ctx.Done():
			return
		case ref, ok := <-jobs:
			if !ok || ctx.Err() != nil {
				return
			}
			worker.validateRef(ctx, snapshot, ref)
		}
	}
}

func (worker *validationWorker) validateRef(ctx context.Context, snapshot *state.ConfigSnapshot, ref state.KeyRef) {
	if ctx.Err() != nil {
		return
	}
	group, ok := snapshot.Groups[ref.GroupID]
	if !ok {
		logValidationFailure(ref, "", "missing_group")
		return
	}
	if len(group.Protocols) == 0 {
		logValidationFailure(ref, "", "missing_protocol")
		return
	}

	protocol := group.Protocols[0]
	model := strings.TrimSpace(group.ValidationModel)
	if model == "" && len(group.Models) > 0 {
		model = strings.TrimSpace(group.Models[0].ID)
	}
	if model == "" {
		logValidationFailure(ref, string(protocol), "missing_model")
		return
	}

	dialect, ok := worker.dialects[protocol]
	if !ok || dialect == nil {
		logValidationFailure(ref, string(protocol), "missing_dialect")
		return
	}
	if worker.decryptor == nil {
		logValidationFailure(ref, string(protocol), "decrypt")
		return
	}
	if worker.stats == nil {
		logValidationFailure(ref, string(protocol), "conditional_recover")
		return
	}
	apiKey, err := worker.decryptor.Decrypt(ref.EncryptedValue)
	if err != nil {
		if ctx.Err() == nil {
			logValidationFailure(ref, string(protocol), "decrypt")
		}
		return
	}
	if err := dialect.Probe(ctx, group.UpstreamURL, apiKey, group.HeaderRules, model); err != nil {
		if ctx.Err() == nil {
			logValidationFailure(ref, string(protocol), "probe")
		}
		return
	}

	if worker.maintenance == nil {
		if ctx.Err() == nil {
			logValidationFailure(ref, string(protocol), "conditional_recover")
		}
		return
	}
	var recovered bool
	func() {
		worker.maintenance.Lock()
		defer worker.maintenance.Unlock()
		worker.stats.Reset(ref.ID)
		recovered = worker.registry.RecoverIfMatch(ref, state.DefaultWeight)
	}()
	if !recovered && ctx.Err() == nil {
		logValidationFailure(ref, string(protocol), "conditional_recover")
	}
}

func logValidationFailure(ref state.KeyRef, protocol, stage string) {
	logrus.WithFields(logrus.Fields{
		"key_id":   ref.ID,
		"group_id": ref.GroupID,
		"protocol": protocol,
		"stage":    stage,
	}).Warn("validation failed")
}
