package control

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"hash"
	"net/textproto"
	"sort"
	"strings"
	"sync"

	"github.com/sirupsen/logrus"

	"gpt-load/internal/dialect"
	"gpt-load/internal/health"
	"gpt-load/internal/platform/encryption"
	"gpt-load/internal/protocol"
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
	WithCurrentSnapshot(func(*state.ConfigSnapshot) bool) bool
}

type credentialDecryptor interface {
	Decrypt(string) (string, error)
}

type validationWorker struct {
	snapshots snapshotSource
	registry  validationRegistry
	stats     statsResetter
	mutations keyMutationCoordinator
	decryptor credentialDecryptor
	dialects  dialect.Set
}

type groupValidationSignature [sha256.Size]byte

type groupValidationTarget struct {
	protocol  protocol.Protocol
	model     string
	signature groupValidationSignature
}

var _ validationSweep = (*validationWorker)(nil)

func newValidationWorker(
	manager *state.Manager,
	registry *state.KeyRegistry,
	stats *health.StatsStore,
	mutations *health.MutationCoordinator,
	decryptor encryption.Service,
	dialects dialect.Set,
) *validationWorker {
	return &validationWorker{
		snapshots: manager,
		registry:  registry,
		stats:     stats,
		mutations: mutations,
		decryptor: decryptor,
		dialects:  dialects,
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
	target, ok := buildGroupValidationTarget(group)
	if !ok && len(group.Protocols) == 0 {
		logValidationFailure(ref, "", "missing_protocol")
		return
	}
	if !ok {
		logValidationFailure(ref, string(group.Protocols[0]), "missing_model")
		return
	}

	selectedDialect, ok := worker.dialects[target.protocol]
	if !ok || selectedDialect == nil {
		logValidationFailure(ref, string(target.protocol), "missing_dialect")
		return
	}
	if worker.decryptor == nil {
		logValidationFailure(ref, string(target.protocol), "decrypt")
		return
	}
	if worker.stats == nil {
		logValidationFailure(ref, string(target.protocol), "conditional_recover")
		return
	}
	apiKey, err := worker.decryptor.Decrypt(ref.EncryptedValue)
	if err != nil {
		if ctx.Err() == nil {
			logValidationFailure(ref, string(target.protocol), "decrypt")
		}
		return
	}
	if err := selectedDialect.Probe(
		ctx,
		group.UpstreamURL,
		apiKey,
		group.HeaderRules,
		target.model,
	); err != nil {
		if ctx.Err() == nil {
			logValidationFailure(ref, string(target.protocol), "probe")
		}
		return
	}

	if worker.mutations == nil {
		if ctx.Err() == nil {
			logValidationFailure(ref, string(target.protocol), "conditional_recover")
		}
		return
	}

	// This callback follows Manager publishMu -> coordinator stripe ->
	// Registry/Stats locks. Keep it to current reads, pure signature work, and
	// coordinated recover/reset; decrypt, probe, DB/network, and logging stay
	// outside the publication boundary.
	recovered := worker.snapshots.WithCurrentSnapshot(func(current *state.ConfigSnapshot) bool {
		if current == nil {
			return false
		}
		currentGroup, exists := current.Groups[ref.GroupID]
		if !exists {
			return false
		}
		currentTarget, valid := buildGroupValidationTarget(currentGroup)
		if !valid || currentTarget.signature != target.signature {
			return false
		}

		var matched bool
		worker.mutations.Do(ref.ID, func() {
			matched = worker.registry.RecoverIfMatch(ref, state.DefaultWeight)
			if matched {
				worker.stats.Reset(ref.ID)
			}
		})
		return matched
	})
	if !recovered && ctx.Err() == nil {
		logValidationFailure(ref, string(target.protocol), "conditional_recover")
	}
}

func buildGroupValidationTarget(group state.GroupView) (groupValidationTarget, bool) {
	selectedProtocol, ok := representativeProtocol(group.Protocols)
	if !ok {
		return groupValidationTarget{}, false
	}
	probeModel := strings.TrimSpace(group.ValidationModel)
	if probeModel == "" && len(group.Models) > 0 {
		probeModel = strings.TrimSpace(group.Models[0].ID)
	}
	if probeModel == "" {
		return groupValidationTarget{}, false
	}
	return groupValidationTarget{
		protocol:  selectedProtocol,
		model:     probeModel,
		signature: computeGroupValidationSignature(group, selectedProtocol, probeModel),
	}, true
}

func representativeProtocol(
	values []protocol.Protocol,
) (protocol.Protocol, bool) {
	present := make(map[protocol.Protocol]struct{}, len(values))
	for _, value := range values {
		present[value] = struct{}{}
	}
	for _, value := range protocol.DataPlaneProtocols() {
		if _, exists := present[value]; exists {
			return value, true
		}
	}
	return "", false
}

func computeGroupValidationSignature(
	group state.GroupView,
	selectedProtocol protocol.Protocol,
	probeModel string,
) groupValidationSignature {
	hasher := sha256.New()
	writeValidationSignatureUint64(hasher, uint64(group.ID))
	writeValidationSignaturePart(hasher, []byte(group.UpstreamURL))
	writeValidationSignaturePart(hasher, []byte(selectedProtocol))
	writeValidationSignaturePart(hasher, []byte(probeModel))

	type headerSetPart struct {
		name  string
		value string
	}
	setParts := make([]headerSetPart, 0, len(group.HeaderRules.Set))
	for name, value := range group.HeaderRules.Set {
		setParts = append(setParts, headerSetPart{
			name:  normalizeValidationHeaderName(name),
			value: value,
		})
	}
	sort.Slice(setParts, func(i, j int) bool {
		if setParts[i].name != setParts[j].name {
			return setParts[i].name < setParts[j].name
		}
		return setParts[i].value < setParts[j].value
	})
	writeValidationSignatureUint64(hasher, uint64(len(setParts)))
	for _, part := range setParts {
		writeValidationSignaturePart(hasher, []byte(part.name))
		writeValidationSignaturePart(hasher, []byte(part.value))
	}

	removeParts := make([]string, len(group.HeaderRules.Remove))
	for index, name := range group.HeaderRules.Remove {
		removeParts[index] = normalizeValidationHeaderName(name)
	}
	sort.Strings(removeParts)
	writeValidationSignatureUint64(hasher, uint64(len(removeParts)))
	for _, name := range removeParts {
		writeValidationSignaturePart(hasher, []byte(name))
	}

	var signature groupValidationSignature
	copy(signature[:], hasher.Sum(nil))
	return signature
}

func writeValidationSignatureUint64(hasher hash.Hash, value uint64) {
	var encoded [8]byte
	binary.BigEndian.PutUint64(encoded[:], value)
	writeValidationSignaturePart(hasher, encoded[:])
}

func writeValidationSignaturePart(hasher hash.Hash, value []byte) {
	var encodedLength [8]byte
	binary.BigEndian.PutUint64(encodedLength[:], uint64(len(value)))
	_, _ = hasher.Write(encodedLength[:])
	_, _ = hasher.Write(value)
}

func normalizeValidationHeaderName(name string) string {
	return strings.ToLower(textproto.CanonicalMIMEHeaderKey(name))
}

func logValidationFailure(ref state.KeyRef, protocol, stage string) {
	logrus.WithFields(logrus.Fields{
		"key_id":   ref.ID,
		"group_id": ref.GroupID,
		"protocol": protocol,
		"stage":    stage,
	}).Warn("validation failed")
}
