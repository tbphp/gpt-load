package control

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/binary"
	"hash"
	"net/http"
	"net/textproto"
	"sort"
	"strings"
	"sync"

	"github.com/sirupsen/logrus"

	"gpt-load/internal/channel"
	"gpt-load/internal/execution"
	"gpt-load/internal/health"
	"gpt-load/internal/outboundproxy"
	"gpt-load/internal/platform/encryption"
	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/platform/utils"
	"gpt-load/internal/protocol"
	"gpt-load/internal/state"
	"gpt-load/internal/storage/models"
)

const validationConcurrency = 8

type validationSweep interface {
	Validate(context.Context)
}

type validationRegistry interface {
	BlacklistedCredentials() []state.CredentialRef
	RecoverIfMatch(ref state.CredentialRef, weight int) bool
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
	mutations credentialMutationCoordinator
	decryptor credentialDecryptor
	channels  *channel.Registry
	executor  execution.Executor
}

type groupValidationSignature [sha256.Size]byte

type groupValidationTarget struct {
	protocol         protocol.Protocol
	fallbackProtocol protocol.Protocol
	model            string
	signature        groupValidationSignature
}

var _ validationSweep = (*validationWorker)(nil)

func newValidationWorker(
	manager *state.Manager,
	registry *state.CredentialRegistry,
	stats *health.StatsStore,
	mutations *health.MutationCoordinator,
	decryptor encryption.Service,
	channels *channel.Registry,
	executor execution.Executor,
) *validationWorker {
	return &validationWorker{
		snapshots: manager,
		registry:  registry,
		stats:     stats,
		mutations: mutations,
		decryptor: decryptor,
		channels:  channels,
		executor:  executor,
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
	refs := worker.registry.BlacklistedCredentials()
	if len(refs) == 0 {
		return
	}

	concurrency := min(validationConcurrency, len(refs))

	jobs := make(chan state.CredentialRef)
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
	jobs <-chan state.CredentialRef,
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

func (worker *validationWorker) validateRef(ctx context.Context, snapshot *state.ConfigSnapshot, ref state.CredentialRef) {
	if ctx.Err() != nil {
		return
	}
	group, ok := snapshot.Groups[ref.GroupID]
	if !ok {
		logValidationFailure(ref, "", "missing_group")
		return
	}
	target, ok := buildGroupValidationTarget(group)
	if !ok {
		logValidationFailure(ref, "", "missing_target_or_model")
		return
	}
	if worker.executor == nil || worker.channels == nil {
		logValidationFailure(ref, string(target.protocol), "missing_executor")
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
	probe := newCredentialProbeExecutor(
		worker.decryptor,
		worker.channels,
		worker.executor,
	)
	executed, err := probe.Probe(ctx, group, target, ref)
	if err != nil {
		if ctx.Err() == nil {
			logValidationFailure(ref, string(target.protocol), credentialProbeFailureStage(err))
		}
		return
	}
	if ctx.Err() != nil {
		return
	}
	if !credentialProbePassed(executed.result) {
		if ctx.Err() == nil {
			logValidationFailure(ref, string(executed.protocol), "probe")
		}
		return
	}

	if worker.mutations == nil {
		if ctx.Err() == nil {
			logValidationFailure(ref, string(executed.protocol), "conditional_recover")
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
		logValidationFailure(ref, string(executed.protocol), "conditional_recover")
		return
	}
	if recovered {
		logValidationRecovery(ref, string(executed.protocol))
	}
}

func validationAttemptProxy(
	decryptor credentialDecryptor,
	groupProxy outboundproxy.Effective,
	ref state.CredentialRef,
) (outboundproxy.Effective, string, error) {
	if groupProxy.Config.Mode == "" && ref.EncryptedProxy == "" {
		return outboundproxy.Effective{}, "", nil
	}
	if ref.EncryptedProxy != "" {
		hasher, ok := decryptor.(interface{ Hash(string) string })
		if !ok {
			return outboundproxy.Effective{}, "", app_errors.ErrInternalServer
		}
		plaintext, err := decryptor.Decrypt(ref.EncryptedProxy)
		if err != nil {
			return outboundproxy.Effective{}, "", err
		}
		fingerprint := hasher.Hash(plaintext)
		if subtle.ConstantTimeCompare([]byte(fingerprint), []byte(ref.ProxyFingerprint)) != 1 {
			plaintext = ""
			return outboundproxy.Effective{}, "", app_errors.ErrInternalServer
		}
		config, err := outboundproxy.Decode(plaintext)
		plaintext = ""
		if err != nil {
			return outboundproxy.Effective{}, "", err
		}
		effective, err := outboundproxy.Resolve(&config, nil, nil, nil)
		return effective, fingerprint, err
	}
	effective, err := outboundproxy.NormalizeEffective(groupProxy)
	if err != nil {
		return outboundproxy.Effective{}, "", err
	}
	hasher, ok := decryptor.(interface{ Hash(string) string })
	if !ok {
		return effective, "", nil
	}
	identity := `{"mode":"environment"}`
	if effective.Config.Mode != outboundproxy.ModeEnvironment {
		identity, err = outboundproxy.Encode(effective.Config)
		if err != nil {
			return outboundproxy.Effective{}, "", err
		}
	}
	return effective, hasher.Hash(identity), nil
}

func buildGroupValidationTarget(group state.GroupView) (groupValidationTarget, bool) {
	if strings.TrimSpace(group.ConnectionType) == string(models.ConnectionTypeSubscription) {
		return groupValidationTarget{}, false
	}
	if group.ChannelID == "" || group.ResolvedTarget.ChannelID != group.ChannelID ||
		!group.ResolvedTarget.ProviderKind.Valid() {
		return groupValidationTarget{}, false
	}
	probeModel := strings.TrimSpace(group.ValidationModel)
	if probeModel == "" && len(group.Models) > 0 {
		probeModel = strings.TrimSpace(group.Models[0].ID)
	}
	if probeModel == "" {
		return groupValidationTarget{}, false
	}
	selectedProtocol, ok := validationProtocol(group.ResolvedTarget, probeModel)
	if !ok {
		return groupValidationTarget{}, false
	}
	fallbackProtocol := protocol.Protocol("")
	if selectedProtocol != protocol.OpenAIEmbeddings {
		if mode, supported := group.ResolvedTarget.ModeForModel(
			protocol.OpenAIEmbeddings,
			execution.OperationProbe,
			probeModel,
		); supported && mode == channel.RouteNative {
			fallbackProtocol = protocol.OpenAIEmbeddings
		}
	}
	return groupValidationTarget{
		protocol: selectedProtocol, fallbackProtocol: fallbackProtocol,
		model:     probeModel,
		signature: computeGroupValidationSignature(group, selectedProtocol, probeModel),
	}, true
}

func validationProtocol(target channel.ResolvedTarget, model string) (protocol.Protocol, bool) {
	return target.PreferredProtocol(execution.OperationProbe, model)
}

func validationProbeNeedsEmbeddingsFallback(result execution.AttemptResult) bool {
	if result.Validate() != nil || result.Error == nil ||
		result.DispatchState != execution.DispatchMaybeSent || !result.ResponseStarted {
		return false
	}
	if result.Error.OriginHint != execution.ErrorOriginUpstream &&
		result.Error.OriginHint != execution.ErrorOriginClient {
		return false
	}
	evidence := result.Error
	status := result.StatusCode
	if status == 0 {
		status = evidence.StatusCode
	}
	switch status {
	case http.StatusUnauthorized, http.StatusPaymentRequired, http.StatusForbidden,
		http.StatusRequestTimeout, http.StatusConflict, http.StatusTooEarly,
		http.StatusTooManyRequests:
		return false
	case http.StatusMethodNotAllowed, http.StatusNotImplemented:
		return true
	}
	if status >= http.StatusInternalServerError ||
		evidence.Hint == execution.FailureHintInvalidCredential ||
		evidence.Hint == execution.FailureHintRefreshRequired ||
		evidence.Hint == execution.FailureHintReauthorizationRequired ||
		evidence.Hint == execution.FailureHintRateLimited ||
		evidence.Hint == execution.FailureHintCandidateUnavailable ||
		evidence.Hint == execution.FailureHintHostError {
		return false
	}
	if evidence.Hint == execution.FailureHintModelUnavailable {
		return true
	}
	for _, value := range []string{evidence.Type, evidence.Code} {
		switch strings.ToLower(strings.TrimSpace(value)) {
		case "model_not_found", "model_not_available", "deployment_not_found",
			"unsupported_model", "unsupported_operation", "operation_not_supported",
			"not_implemented":
			return true
		}
	}
	if evidence.Hint != execution.FailureHintRequestRejected {
		return false
	}
	summary := strings.ToLower(evidence.Summary)
	for _, marker := range []string{
		"not a chat model",
		"does not support chat completions",
		"chat completions are not supported",
		"not supported in the v1/chat/completions endpoint",
		"operation is not supported",
	} {
		if strings.Contains(summary, marker) {
			return true
		}
	}
	return false
}

func computeGroupValidationSignature(
	group state.GroupView,
	selectedProtocol protocol.Protocol,
	probeModel string,
) groupValidationSignature {
	hasher := sha256.New()
	writeValidationSignatureUint64(hasher, uint64(group.ID))
	writeValidationSignaturePart(hasher, []byte(group.ChannelID))
	writeValidationSignaturePart(hasher, []byte(group.ResolvedTarget.ProviderKind))
	writeValidationSignaturePart(hasher, group.ResolvedTarget.TargetConfig)
	writeValidationSignaturePart(hasher, []byte(selectedProtocol))
	writeValidationSignaturePart(hasher, []byte(probeModel))
	writeValidationSignaturePart(hasher, []byte(group.Proxy.Source))
	writeValidationSignaturePart(hasher, []byte(group.Proxy.Config.Mode))
	writeValidationSignaturePart(hasher, []byte(group.Proxy.Config.URL))

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

func logValidationRecovery(ref state.CredentialRef, protocol string) {
	utils.LogPlaneBestEffort(
		logrus.StandardLogger(),
		logrus.InfoLevel,
		utils.LogPlaneControl,
		logrus.Fields{
			"event":         "credential_recovered",
			"credential_id": ref.ID,
			"group_id":      ref.GroupID,
			"protocol":      protocol,
		},
		"Credential recovered",
	)
}

func logValidationFailure(ref state.CredentialRef, protocol, stage string) {
	utils.LogPlaneBestEffort(
		logrus.StandardLogger(),
		logrus.WarnLevel,
		utils.LogPlaneControl,
		logrus.Fields{
			"credential_id": ref.ID,
			"group_id":      ref.GroupID,
			"protocol":      protocol,
			"stage":         stage,
		},
		"Validation failed",
	)
}
