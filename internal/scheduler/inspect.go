package scheduler

import (
	"errors"
	"fmt"
	"sort"
	"time"

	"gpt-load/internal/channel"
	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
	"gpt-load/internal/state"
)

var ErrInconsistentSnapshot = errors.New("inconsistent scheduler snapshot")

type ReasonCode string

const (
	ReasonAccessKeyDisabled            ReasonCode = "access_key_disabled"
	ReasonProtocolFiltered             ReasonCode = "protocol_filtered"
	ReasonModelFiltered                ReasonCode = "model_filtered"
	ReasonModelRequiredByFilter        ReasonCode = "model_required_by_filter"
	ReasonOperationUnsupported         ReasonCode = "operation_unsupported"
	ReasonNativeRouteRequired          ReasonCode = "native_route_required"
	ReasonNoRouteTarget                ReasonCode = "no_route_target"
	ReasonGroupDisabled                ReasonCode = "group_disabled"
	ReasonGroupFiltered                ReasonCode = "group_filtered"
	ReasonNoAvailableGroup             ReasonCode = "no_available_group"
	ReasonNoCredentials                ReasonCode = "no_credentials"
	ReasonGroupWeightZero              ReasonCode = "group_weight_zero"
	ReasonCredentialDisabled           ReasonCode = "credential_disabled"
	ReasonCredentialAuthUnavailable    ReasonCode = "credential_auth_unavailable"
	ReasonCredentialBlacklisted        ReasonCode = "credential_blacklisted"
	ReasonCredentialCooldown           ReasonCode = "credential_cooldown"
	ReasonCredentialQuotaExhausted     ReasonCode = "credential_quota_exhausted"
	ReasonCredentialQuotaDeprioritized ReasonCode = "credential_quota_deprioritized"
	ReasonCredentialWeightZero         ReasonCode = "credential_weight_zero"
	ReasonCredentialNotAllowed         ReasonCode = "credential_not_allowed"
	ReasonNoAvailableCredential        ReasonCode = "no_available_credential"
)

type Inspection struct {
	ClientProtocol   protocol.Protocol
	Operation        execution.Operation
	RouteRequirement execution.RouteRequirement
	ExternalModel    *string
	Routable         bool
	Reason           ReasonCode
	Groups           []GroupInspection
}

type GroupInspection struct {
	GroupID                   uint
	GroupName                 string
	ChannelID                 channel.ID
	RouteMode                 channel.RouteMode
	RouteRequirementSatisfied bool
	UpstreamModelID           *string
	WeightManual              *int
	Included                  bool
	Routable                  bool
	Reason                    ReasonCode
	Credentials               []CredentialInspection
}

type CredentialInspection struct {
	CredentialID    uint
	Available       bool
	Reason          ReasonCode
	WeightManual    *int
	WeightAuto      int
	EffectiveWeight int64
	CooldownUntil   time.Time
}

// CredentialRuntimeView is the scheduler's neutral view of runtime health.
type CredentialRuntimeView = state.CredentialRuntimeView

type targetDecision struct {
	target        state.RouteTarget
	group         state.GroupCatalogView
	requirementOK bool
	included      bool
	reason        ReasonCode
}

func cloneWeight(weight *int) *int {
	if weight == nil {
		return nil
	}
	value := *weight
	return &value
}

func evaluateTargets(
	snapshot *state.ConfigSnapshot,
	index state.ExecutionCandidateIndex,
	query normalizedQuery,
) ([]targetDecision, ReasonCode, error) {
	if snapshot == nil {
		return nil, "", fmt.Errorf("%w: nil ConfigSnapshot", ErrInconsistentSnapshot)
	}
	if query.accessKey.Status == state.AccessKeyStatusDisabled {
		return []targetDecision{}, ReasonAccessKeyDisabled, nil
	}
	if len(query.accessKey.Filters.Protocols) > 0 {
		if _, allowed := query.accessKey.Filters.Protocols[query.clientProtocol]; !allowed {
			return []targetDecision{}, ReasonProtocolFiltered, nil
		}
	}
	if len(query.accessKey.Filters.Models) > 0 {
		if query.externalModel == nil {
			return []targetDecision{}, ReasonModelRequiredByFilter, nil
		}
		if _, allowed := query.accessKey.Filters.Models[*query.externalModel]; !allowed {
			return []targetDecision{}, ReasonModelFiltered, nil
		}
	}
	if !query.clientProtocol.Valid() || !query.operation.Valid() || !query.routeRequirement.Valid() {
		return []targetDecision{}, ReasonOperationUnsupported, nil
	}
	byOperation := index[query.clientProtocol]
	if len(byOperation) == 0 {
		return []targetDecision{}, ReasonNoRouteTarget, nil
	}
	byModel, operationSupported := byOperation[query.operation]
	if !operationSupported {
		return []targetDecision{}, ReasonOperationUnsupported, nil
	}
	modelKey := state.NoModelRouteKey
	if query.externalModel != nil {
		modelKey = *query.externalModel
	}
	routes := byModel[modelKey]
	if len(routes) == 0 {
		return []targetDecision{}, ReasonNoRouteTarget, nil
	}

	decisions := make([]targetDecision, 0, len(routes))
	seenGroups := make(map[uint]struct{}, len(routes))
	included := 0
	for _, route := range routes {
		if _, duplicate := seenGroups[route.GroupID]; duplicate {
			continue
		}
		seenGroups[route.GroupID] = struct{}{}
		group, exists := snapshot.GroupCatalog[route.GroupID]
		if !exists {
			return nil, "", fmt.Errorf(
				"%w: route target group %d missing from catalog",
				ErrInconsistentSnapshot,
				route.GroupID,
			)
		}
		requirementOK := routeRequirementSatisfied(query, route)
		decision := targetDecision{
			target: cloneRouteTarget(route), group: group,
			requirementOK: requirementOK, included: true,
		}
		groupFiltered := false
		if len(query.accessKey.Filters.Groups) > 0 {
			_, allowed := query.accessKey.Filters.Groups[route.GroupID]
			groupFiltered = !allowed
		}
		switch {
		case !requirementOK:
			decision.included = false
			decision.reason = ReasonNativeRouteRequired
		case !group.Enabled:
			decision.included = false
			decision.reason = ReasonGroupDisabled
		case groupFiltered:
			decision.included = false
			decision.reason = ReasonGroupFiltered
		}
		if decision.included {
			included++
		}
		decisions = append(decisions, decision)
	}
	if included > 0 {
		return decisions, "", nil
	}
	reason := decisions[0].reason
	for _, decision := range decisions[1:] {
		if decision.reason != reason {
			return decisions, ReasonNoAvailableGroup, nil
		}
	}
	return decisions, reason, nil
}

func routeRequirementSatisfied(query normalizedQuery, route state.RouteTarget) bool {
	if !query.routeRequirement.Allows(execution.RouteMode(route.Mode)) {
		return false
	}
	if query.operation != execution.OperationResponsesCreate ||
		query.routeRequirement.Normalize() != execution.RouteRequirementNative {
		return true
	}
	return route.ResolvedTarget.SupportsResponsesLifecycle()
}

func accessKeyAllowsGroup(accessKey state.AccessKeyView, groupID uint) bool {
	if len(accessKey.Filters.Groups) == 0 {
		return true
	}
	_, allowed := accessKey.Filters.Groups[groupID]
	return allowed
}

func normalizedAutoWeight(weight int) int {
	if weight == 0 {
		return state.DefaultWeight
	}
	return weight
}

func effectiveWeight(groupManual, credentialManual *int, credentialAuto int) int64 {
	groupWeight := state.DefaultWeight
	if groupManual != nil {
		groupWeight = *groupManual
	}
	credentialWeight := normalizedAutoWeight(credentialAuto)
	if credentialManual != nil {
		credentialWeight = *credentialManual
	}
	if groupWeight <= 0 || credentialWeight <= 0 {
		return 0
	}
	return int64(groupWeight) * int64(credentialWeight)
}

func inspectCredential(
	group state.GroupCatalogView,
	credential CredentialRuntimeView,
	allowedCredentialIDs map[uint]struct{},
	now time.Time,
) CredentialInspection {
	result := CredentialInspection{
		CredentialID: credential.ID,
		WeightManual: cloneWeight(credential.WeightManual),
		WeightAuto:   normalizedAutoWeight(credential.WeightAuto),
	}
	if group.WeightManual != nil && *group.WeightManual == 0 {
		result.Reason = ReasonGroupWeightZero
		return result
	}
	if allowedCredentialIDs != nil {
		if _, allowed := allowedCredentialIDs[credential.ID]; !allowed {
			result.Reason = ReasonCredentialNotAllowed
			return result
		}
	}
	if credential.Status != state.CredentialStatusActive {
		result.Reason = ReasonCredentialDisabled
		return result
	}
	if !credential.AuthReady() {
		result.Reason = ReasonCredentialAuthUnavailable
		return result
	}
	if credential.WeightManual != nil && *credential.WeightManual == 0 {
		result.Reason = ReasonCredentialWeightZero
		return result
	}
	switch credential.RuntimeState(now) {
	case state.CredentialRuntimeBlacklisted:
		result.Reason = ReasonCredentialBlacklisted
	case state.CredentialRuntimeCooldown:
		result.Reason = ReasonCredentialCooldown
		result.CooldownUntil = credential.CooldownUntil
	default:
		if credential.QuotaExhausted(now) {
			result.Reason = ReasonCredentialQuotaExhausted
			return result
		}
		result.Available = true
		result.EffectiveWeight = effectiveWeight(
			group.WeightManual,
			credential.WeightManual,
			credential.WeightAuto,
		)
	}
	return result
}

func applyInspectionQuotaPriority(
	group state.GroupCatalogView,
	credentials []CredentialRuntimeView,
	results []CredentialInspection,
	now time.Time,
) {
	if !group.QuotaPriority || len(credentials) != len(results) {
		return
	}
	priority := quotaPriority{}
	for index, result := range results {
		if !result.Available || result.EffectiveWeight <= 0 {
			continue
		}
		priority = observeQuotaPriority(priority, credentials[index].FreshQuotaRemaining(now))
	}
	for index := range results {
		if !results[index].Available || results[index].EffectiveWeight <= 0 {
			continue
		}
		if quotaPriorityAllows(priority, credentials[index].FreshQuotaRemaining(now)) {
			continue
		}
		results[index].Available = false
		results[index].Reason = ReasonCredentialQuotaDeprioritized
		results[index].EffectiveWeight = 0
	}
}

func Inspect(
	snapshot *state.ConfigSnapshot,
	credentials []CredentialRuntimeView,
	query Query,
	now time.Time,
) (Inspection, error) {
	normalized := normalizeQuery(query)
	result := Inspection{
		ClientProtocol:   normalized.clientProtocol,
		Operation:        normalized.operation,
		RouteRequirement: normalized.routeRequirement,
		ExternalModel:    cloneString(normalized.externalModel),
		Groups:           []GroupInspection{},
	}
	if snapshot == nil {
		return Inspection{}, fmt.Errorf("%w: nil ConfigSnapshot", ErrInconsistentSnapshot)
	}
	if normalized.accessKey.Status == state.AccessKeyStatusDisabled {
		result.Reason = ReasonAccessKeyDisabled
		return result, nil
	}

	credentialsByGroup := make(map[uint][]CredentialRuntimeView)
	for _, credential := range credentials {
		if _, exists := snapshot.GroupCatalog[credential.GroupID]; !exists {
			return Inspection{}, fmt.Errorf(
				"%w: registry credential %d group %d missing from catalog",
				ErrInconsistentSnapshot,
				credential.ID,
				credential.GroupID,
			)
		}
		cloned := credential
		cloned.WeightManual = cloneWeight(credential.WeightManual)
		credentialsByGroup[credential.GroupID] = append(credentialsByGroup[credential.GroupID], cloned)
	}
	for groupID := range credentialsByGroup {
		sort.Slice(credentialsByGroup[groupID], func(i, j int) bool {
			return credentialsByGroup[groupID][i].ID < credentialsByGroup[groupID][j].ID
		})
	}

	decisions, staticReason, err := evaluateTargets(
		snapshot,
		snapshot.ExecutionRouteCatalog,
		normalized,
	)
	if err != nil {
		return Inspection{}, err
	}
	for _, decision := range decisions {
		groupResult := GroupInspection{
			GroupID: decision.group.ID, GroupName: decision.group.Name,
			ChannelID:                 decision.target.ResolvedTarget.ChannelID,
			RouteMode:                 decision.target.Mode,
			RouteRequirementSatisfied: decision.requirementOK,
			UpstreamModelID:           optionalModel(decision.target.UpstreamModelID),
			WeightManual:              cloneWeight(decision.group.WeightManual),
			Included:                  decision.included, Reason: decision.reason,
			Credentials: []CredentialInspection{},
		}
		if !decision.included {
			result.Groups = append(result.Groups, groupResult)
			continue
		}
		groupCredentials := credentialsByGroup[decision.group.ID]
		groupWeightZero := decision.group.WeightManual != nil &&
			*decision.group.WeightManual == 0
		for _, credential := range groupCredentials {
			credentialResult := inspectCredential(
				decision.group,
				credential,
				normalized.allowedCredentialIDs,
				now,
			)
			groupResult.Credentials = append(groupResult.Credentials, credentialResult)
		}
		applyInspectionQuotaPriority(
			decision.group,
			groupCredentials,
			groupResult.Credentials,
			now,
		)
		for _, credential := range groupResult.Credentials {
			if credential.Available && credential.EffectiveWeight > 0 {
				groupResult.Routable = true
				break
			}
		}
		switch {
		case groupWeightZero:
			groupResult.Reason = ReasonGroupWeightZero
		case len(groupCredentials) == 0:
			groupResult.Reason = ReasonNoCredentials
		case !groupResult.Routable:
			groupResult.Reason = ReasonNoAvailableCredential
		}
		if groupResult.Routable {
			result.Routable = true
		}
		result.Groups = append(result.Groups, groupResult)
	}
	if result.Routable {
		return result, nil
	}
	if staticReason != "" {
		result.Reason = staticReason
	} else {
		result.Reason = ReasonNoAvailableCredential
	}
	return result, nil
}
