// Package scheduler selects channel targets and credentials without IO or persistence access.
package scheduler

import (
	"errors"
	"slices"
	"strings"
	"time"

	"gpt-load/internal/channel"
	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
	"gpt-load/internal/state"
)

var ErrExhausted = errors.New("scheduler exhausted")

type CredentialSource interface {
	SchedulingState() *state.SchedulingState
	CollectCredentialCandidates(groupIDs []uint, excluded func(uint) bool, now time.Time) []state.CredentialMeta
}

type Query struct {
	ClientProtocol           protocol.Protocol
	Operation                execution.Operation
	RouteRequirement         execution.RouteRequirement
	ResponsesStorePreference execution.ResponsesStorePreference
	ExternalModel            *string
	AccessKey                state.AccessKeyView
	AllowedCredentialIDs     map[uint]struct{}
	PreferredCredentialID    uint
	AllowedCredentialRefs    map[uint]state.CredentialRef

	// ResponsesWebsocket 非 nil 时按原生 WS 合同准入，不要求 HTTP 资源接口。
	ResponsesWebsocket *execution.WebsocketCapabilities
}

type Selection struct {
	CredentialID             uint
	GroupID                  uint
	ChannelID                channel.ID
	ResolvedTarget           channel.ResolvedTarget
	RouteMode                channel.RouteMode
	UpstreamModelID          *string
	Group                    state.GroupView
	ResponsesStoreDowngraded bool
}

type candidateTarget struct {
	target                   state.RouteTarget
	group                    state.GroupView
	responsesStoreDowngraded bool
}

type weightedCredential struct {
	meta   state.CredentialMeta
	weight int64
}

type candidatePool struct {
	targetsByGroup map[uint][]candidateTarget
	groupIDsByMode map[channel.RouteMode][]uint
}

type Iterator struct {
	snapshot              *state.ConfigSnapshot
	query                 Query
	operation             execution.Operation
	credentials           CredentialSource
	progress              *state.SchedulingState
	regular               candidatePool
	storeDowngraded       candidatePool
	routeModeTiers        [][]channel.RouteMode
	allowedCredentialIDs  map[uint]struct{}
	preferredCredentialID uint
	tried                 map[uint]struct{}
	skippedGroups         map[uint]struct{}
	allowedCredentialRefs map[uint]credentialIdentity
	staticReason          ReasonCode
	now                   func() time.Time
}

type normalizedQuery struct {
	clientProtocol           protocol.Protocol
	operation                execution.Operation
	routeRequirement         execution.RouteRequirement
	responsesStorePreference execution.ResponsesStorePreference
	responsesWebsocket       *execution.WebsocketCapabilities
	externalModel            *string
	accessKey                state.AccessKeyView
	allowedCredentialIDs     map[uint]struct{}
}

func New(snapshot *state.ConfigSnapshot, credentials CredentialSource, query Query) *Iterator {
	return newWithClock(snapshot, credentials, query, time.Now)
}

// CandidateGroupIDsForQuery returns the frozen credential-capture scope for a
// fully classified execution query.
func CandidateGroupIDsForQuery(snapshot *state.ConfigSnapshot, query Query) []uint {
	if snapshot == nil {
		return nil
	}
	decisions, _, err := evaluateTargets(
		snapshot,
		snapshot.ExecutionCandidates,
		normalizeQuery(query),
	)
	if err != nil {
		return []uint{}
	}
	groupIDs := make([]uint, 0, len(decisions))
	seen := make(map[uint]struct{}, len(decisions))
	for _, decision := range decisions {
		if decision.included {
			if _, exists := seen[decision.target.GroupID]; exists {
				continue
			}
			seen[decision.target.GroupID] = struct{}{}
			groupIDs = append(groupIDs, decision.target.GroupID)
		}
	}
	return groupIDs
}

func newWithClock(
	snapshot *state.ConfigSnapshot,
	credentials CredentialSource,
	query Query,
	now func() time.Time,
) *Iterator {
	iterator := &Iterator{
		snapshot: snapshot, query: query, operation: normalizeQuery(query).operation,
		credentials:           credentials,
		allowedCredentialRefs: cloneCredentialIdentities(query.AllowedCredentialRefs),
		regular:               newCandidatePool(),
		storeDowngraded:       newCandidatePool(),
		routeModeTiers:        [][]channel.RouteMode{{channel.RouteNative}, {channel.RouteConverted}},
		allowedCredentialIDs:  cloneAllowedCredentialIDs(query),
		preferredCredentialID: query.PreferredCredentialID,
		tried:                 make(map[uint]struct{}),
		skippedGroups:         make(map[uint]struct{}),
		now:                   now,
	}

	if credentials != nil {
		iterator.progress = credentials.SchedulingState()
		iterator.progress.SyncGroups(snapshot)
	}

	if snapshot != nil && snapshot.Settings.RouteStrategy == state.RouteStrategyWeightedMix {
		iterator.routeModeTiers = [][]channel.RouteMode{{channel.RouteNative, channel.RouteConverted}}
	}
	targets, staticReason := filterTargetsWithReason(snapshot, query)
	iterator.staticReason = staticReason
	for _, target := range targets {
		pool := &iterator.regular
		if target.responsesStoreDowngraded {
			pool = &iterator.storeDowngraded
		}
		mode := target.target.Mode
		groupID := target.target.GroupID
		pool.targetsByGroup[groupID] = append(pool.targetsByGroup[groupID], target)
		if !slices.Contains(pool.groupIDsByMode[mode], groupID) {
			pool.groupIDsByMode[mode] = append(pool.groupIDsByMode[mode], groupID)
		}
	}
	for _, pool := range []*candidatePool{&iterator.regular, &iterator.storeDowngraded} {
		for _, targets := range pool.targetsByGroup {
			slices.SortFunc(targets, func(a, b candidateTarget) int {
				return strings.Compare(a.target.UpstreamModelID, b.target.UpstreamModelID)
			})
		}
	}
	return iterator
}

func newCandidatePool() candidatePool {
	return candidatePool{
		targetsByGroup: make(map[uint][]candidateTarget),
		groupIDsByMode: make(map[channel.RouteMode][]uint),
	}
}

func (iterator *Iterator) StaticReason() ReasonCode {
	if iterator == nil {
		return ""
	}
	return iterator.staticReason
}

func cloneAllowedCredentialIDs(query Query) map[uint]struct{} {
	source := query.AllowedCredentialIDs
	if source == nil {
		return nil
	}
	cloned := make(map[uint]struct{}, len(source))
	for credentialID := range source {
		cloned[credentialID] = struct{}{}
	}
	return cloned
}

func (iterator *Iterator) SkipGroup(groupID uint) {
	if iterator == nil || groupID == 0 {
		return
	}
	if iterator.skippedGroups == nil {
		iterator.skippedGroups = make(map[uint]struct{})
	}
	iterator.skippedGroups[groupID] = struct{}{}
}

func (iterator *Iterator) weightedPoolForMode(
	mode channel.RouteMode,
	now time.Time,
) ([]weightedCredential, int64) {
	if iterator == nil {
		return nil, 0
	}
	var weighted []weightedCredential
	var total int64
	iterator.withWeightedPool(&iterator.regular, []channel.RouteMode{mode}, now, func(pool []weightedCredential) {
		weighted = pool
		for _, candidate := range pool {
			total += candidate.weight
		}
	})
	return weighted, total
}

func (iterator *Iterator) withWeightedPool(candidates *candidatePool, modes []channel.RouteMode, now time.Time, fn func([]weightedCredential)) {
	var groupIDs []uint
	seenGroups := make(map[uint]struct{})
	for _, mode := range modes {
		for _, groupID := range candidates.groupIDsByMode[mode] {
			if _, exists := seenGroups[groupID]; !exists {
				seenGroups[groupID] = struct{}{}
				groupIDs = append(groupIDs, groupID)
			}
		}
	}
	excluded := func(id uint) bool { _, tried := iterator.tried[id]; return tried }
	consume := func(pool []state.CredentialMeta) {
		weighted := make([]weightedCredential, 0, len(pool))
		for _, credential := range pool {
			if iterator.allowedCredentialIDs != nil {
				if _, ok := iterator.allowedCredentialIDs[credential.ID]; !ok {
					continue
				}
			}
			if iterator.allowedCredentialRefs != nil {
				ref, ok := iterator.allowedCredentialRefs[credential.ID]
				if !ok || ref.GroupID != credential.GroupID || ref.IdentityGeneration != credential.IdentityGeneration {
					continue
				}
			}
			if _, skipped := iterator.skippedGroups[credential.GroupID]; skipped {
				continue
			}
			for _, target := range candidates.targetsByGroup[credential.GroupID] {
				if !iterator.targetAvailable(target, credential, modes, now) {
					continue
				}
				weight := effectiveWeight(target.group.WeightManual, credential.WeightManual)
				if weight > 0 {
					weighted = append(weighted, weightedCredential{meta: credential, weight: weight})
				}
				break
			}
		}
		fn(weighted)
	}
	if source, ok := iterator.credentials.(interface {
		WithCredentialCandidates([]uint, func(uint) bool, time.Time, func([]state.CredentialMeta))
	}); ok {
		source.WithCredentialCandidates(groupIDs, excluded, now, consume)
	} else {
		consume(iterator.credentials.CollectCredentialCandidates(groupIDs, excluded, now))
	}
}

func (iterator *Iterator) Next() (Selection, error) {
	if iterator == nil || iterator.credentials == nil || iterator.progress == nil || iterator.now == nil {
		return Selection{}, ErrExhausted
	}
	for _, pool := range []*candidatePool{&iterator.regular, &iterator.storeDowngraded} {
		for _, modes := range iterator.routeModeTiers {
			var selected state.CredentialMeta
			var target candidateTarget
			var found bool
			now := iterator.now()
			iterator.withWeightedPool(pool, modes, now, func(weighted []weightedCredential) {
				selected, found = iterator.selectCredential(weighted, iterator.preferredCredentialID)
				if !found {
					return
				}
				target = iterator.selectTarget(pool, modes, selected, now)
			})
			if !found {
				continue
			}
			iterator.tried[selected.ID] = struct{}{}
			return newSelection(selected, target), nil
		}
	}
	return Selection{}, ErrExhausted
}

func (iterator *Iterator) targetAvailable(target candidateTarget, credential state.CredentialMeta, modes []channel.RouteMode, now time.Time) bool {
	return slices.Contains(modes, target.target.Mode) &&
		!modelCooldownUntil(credential.ModelCooldowns, target.target.UpstreamModelID, iterator.operation, now).After(now)
}

// 只为已选凭据收集模型，避免每个凭据都复制完整候选列表。
func (iterator *Iterator) selectTarget(pool *candidatePool, modes []channel.RouteMode, credential state.CredentialMeta, now time.Time) candidateTarget {
	targets := pool.targetsByGroup[credential.GroupID]
	models := make([]string, 0, len(targets))
	for _, target := range targets {
		if iterator.targetAvailable(target, credential, modes, now) {
			models = append(models, target.target.UpstreamModelID)
		}
	}
	model := models[0]
	if iterator.query.ExternalModel != nil {
		model = iterator.progress.SelectModel(credential.GroupID, *iterator.query.ExternalModel, models)
	}
	for _, target := range targets {
		if target.target.UpstreamModelID == model {
			return target
		}
	}
	return targets[0]
}

func filterTargetsWithReason(
	snapshot *state.ConfigSnapshot,
	query Query,
) ([]candidateTarget, ReasonCode) {
	if snapshot == nil {
		return nil, ""
	}
	decisions, staticReason, err := evaluateTargets(
		snapshot,
		snapshot.ExecutionCandidates,
		normalizeQuery(query),
	)
	if err != nil {
		return nil, ""
	}
	targets := make([]candidateTarget, 0, len(decisions))
	for _, decision := range decisions {
		if !decision.included {
			continue
		}
		group, exists := snapshot.Groups[decision.target.GroupID]
		if !exists {
			continue
		}
		targets = append(targets, candidateTarget{
			target:                   cloneRouteTarget(decision.target),
			group:                    group,
			responsesStoreDowngraded: decision.responsesStoreDowngraded,
		})
	}
	return targets, staticReason
}

func normalizeQuery(query Query) normalizedQuery {
	clientProtocol := query.ClientProtocol
	operation := query.Operation
	if operation == "" {
		if clientProtocol == protocol.OpenAIImages {
			// Images endpoints always select generate or edit explicitly. Keep an
			// omitted operation invalid instead of silently changing the action.
		} else if clientProtocol == protocol.OpenAIResponses {
			if query.ExternalModel == nil {
				operation = execution.OperationResponsesRetrieve
			} else {
				operation = execution.OperationResponsesCreate
			}
		} else {
			operation = execution.OperationChatCompletion
		}
	}
	return normalizedQuery{
		clientProtocol:           clientProtocol,
		operation:                operation,
		routeRequirement:         query.RouteRequirement.Normalize(),
		responsesStorePreference: query.ResponsesStorePreference,
		responsesWebsocket:       cloneWebsocketCapabilities(query.ResponsesWebsocket),
		externalModel:            cloneString(query.ExternalModel),
		accessKey:                query.AccessKey,
		allowedCredentialIDs:     cloneAllowedCredentialIDs(query),
	}
}

func cloneWebsocketCapabilities(value *execution.WebsocketCapabilities) *execution.WebsocketCapabilities {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func newSelection(credential state.CredentialMeta, target candidateTarget) Selection {
	upstreamModelID := optionalModel(target.target.UpstreamModelID)
	resolvedTarget := target.target.ResolvedTarget
	resolvedTarget.TargetConfig = append([]byte(nil), resolvedTarget.TargetConfig...)
	return Selection{
		CredentialID:             credential.ID,
		GroupID:                  credential.GroupID,
		ChannelID:                resolvedTarget.ChannelID,
		ResolvedTarget:           resolvedTarget,
		RouteMode:                target.target.Mode,
		UpstreamModelID:          upstreamModelID,
		Group:                    cloneGroupView(target.group),
		ResponsesStoreDowngraded: target.responsesStoreDowngraded,
	}
}

func optionalModel(value string) *string {
	if value == "" {
		return nil
	}
	return cloneString(&value)
}

func cloneRouteTarget(target state.RouteTarget) state.RouteTarget {
	target.ResolvedTarget.TargetConfig = append([]byte(nil), target.ResolvedTarget.TargetConfig...)
	return target
}

func cloneGroupView(group state.GroupView) state.GroupView {
	group.Params = append([]byte(nil), group.Params...)
	group.ClientProtocols = append([]protocol.Protocol(nil), group.ClientProtocols...)
	group.Models = append([]state.ModelConfig(nil), group.Models...)
	group.WeightManual = cloneWeight(group.WeightManual)
	group.HeaderRules.Set = cloneStringMap(group.HeaderRules.Set)
	group.HeaderRules.Remove = append([]string(nil), group.HeaderRules.Remove...)
	group.ResolvedTarget.TargetConfig = append([]byte(nil), group.ResolvedTarget.TargetConfig...)
	group.ParameterOverrides = group.ParameterOverrides.Clone()
	return group
}

func cloneStringMap(source map[string]string) map[string]string {
	if source == nil {
		return nil
	}
	cloned := make(map[string]string, len(source))
	for key, value := range source {
		cloned[key] = value
	}
	return cloned
}

func cloneString(value *string) *string {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

type credentialIdentity struct {
	GroupID            uint
	IdentityGeneration uint64
}

func cloneCredentialIdentities(refs map[uint]state.CredentialRef) map[uint]credentialIdentity {
	if refs == nil {
		return nil
	}
	identities := make(map[uint]credentialIdentity, len(refs))
	for id, ref := range refs {
		identities[id] = credentialIdentity{GroupID: ref.GroupID, IdentityGeneration: ref.IdentityGeneration}
	}
	return identities
}
