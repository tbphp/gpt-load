// Package scheduler selects channel targets and credentials without IO or persistence access.
package scheduler

import (
	"errors"
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
	targetsByGroup map[uint]candidateTarget
	groupIDsByMode map[channel.RouteMode][]uint
}

type Iterator struct {
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
	for _, decision := range decisions {
		if decision.included {
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
		pool.targetsByGroup[target.target.GroupID] = target
		pool.groupIDsByMode[mode] = append(pool.groupIDsByMode[mode], target.target.GroupID)
	}
	return iterator
}

func newCandidatePool() candidatePool {
	return candidatePool{
		targetsByGroup: make(map[uint]candidateTarget),
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
	for _, mode := range modes {
		groupIDs = append(groupIDs, candidates.groupIDsByMode[mode]...)
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
			target, ok := candidates.targetsByGroup[credential.GroupID]
			if !ok {
				continue
			}
			weight := effectiveWeight(target.group.WeightManual, credential.WeightManual)
			if weight > 0 {
				weighted = append(weighted, weightedCredential{meta: credential, weight: weight})
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
			var found bool
			iterator.withWeightedPool(pool, modes, iterator.now(), func(weighted []weightedCredential) {
				selected, found = iterator.selectCredential(weighted, iterator.preferredCredentialID)
			})
			if !found {
				continue
			}
			iterator.tried[selected.ID] = struct{}{}
			return newSelection(selected, pool.targetsByGroup[selected.GroupID]), nil
		}
	}
	return Selection{}, ErrExhausted
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
		externalModel:            cloneString(query.ExternalModel),
		accessKey:                query.AccessKey,
		allowedCredentialIDs:     cloneAllowedCredentialIDs(query),
	}
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
