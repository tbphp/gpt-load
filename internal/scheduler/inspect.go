package scheduler

import (
	"errors"
	"fmt"
	"sort"
	"time"

	"gpt-load/internal/protocol"
	"gpt-load/internal/state"
)

var ErrInconsistentSnapshot = errors.New("inconsistent scheduler snapshot")

type ReasonCode string

const (
	ReasonAccessKeyDisabled ReasonCode = "access_key_disabled"
	ReasonProtocolFiltered  ReasonCode = "protocol_filtered"
	ReasonModelFiltered     ReasonCode = "model_filtered"
	ReasonNoRouteTarget     ReasonCode = "no_route_target"
	ReasonGroupDisabled     ReasonCode = "group_disabled"
	ReasonGroupFiltered     ReasonCode = "group_filtered"
	ReasonNoAvailableGroup  ReasonCode = "no_available_group"
	ReasonNoKeys            ReasonCode = "no_keys"
	ReasonGroupWeightZero   ReasonCode = "group_weight_zero"
	ReasonKeyDisabled       ReasonCode = "key_disabled"
	ReasonKeyBlacklisted    ReasonCode = "key_blacklisted"
	ReasonKeyCooldown       ReasonCode = "key_cooldown"
	ReasonKeyWeightZero     ReasonCode = "key_weight_zero"
	ReasonNoAvailableKey    ReasonCode = "no_available_key"
)

type Inspection struct {
	Routable bool
	Reason   ReasonCode
	Groups   []GroupInspection
}

type GroupInspection struct {
	GroupID         uint
	GroupName       string
	UpstreamModelID string
	WeightManual    *int
	Included        bool
	Routable        bool
	Reason          ReasonCode
	Keys            []KeyInspection
}

type KeyInspection struct {
	KeyID           uint
	Available       bool
	Reason          ReasonCode
	WeightManual    *int
	WeightAuto      int
	EffectiveWeight int64
	CooldownUntil   time.Time
}

type targetDecision struct {
	target   state.RouteTarget
	group    state.GroupCatalogView
	included bool
	reason   ReasonCode
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
	index map[protocol.Protocol]map[string][]state.RouteTarget,
	query Query,
) ([]targetDecision, ReasonCode, error) {
	if snapshot == nil {
		return nil, "", fmt.Errorf("%w: nil ConfigSnapshot", ErrInconsistentSnapshot)
	}
	if len(query.AccessKey.Filters.Protocols) > 0 {
		if _, allowed := query.AccessKey.Filters.Protocols[query.Protocol]; !allowed {
			return []targetDecision{}, ReasonProtocolFiltered, nil
		}
	}
	if len(query.AccessKey.Filters.Models) > 0 {
		if _, allowed := query.AccessKey.Filters.Models[query.ExternalModel]; !allowed {
			return []targetDecision{}, ReasonModelFiltered, nil
		}
	}
	routes := index[query.Protocol][query.ExternalModel]
	if len(routes) == 0 {
		return []targetDecision{}, ReasonNoRouteTarget, nil
	}

	decisions := make([]targetDecision, 0, len(routes))
	seenGroups := make(map[uint]struct{}, len(routes))
	included := 0
	for _, target := range routes {
		if _, duplicate := seenGroups[target.GroupID]; duplicate {
			continue
		}
		seenGroups[target.GroupID] = struct{}{}
		group, exists := snapshot.GroupCatalog[target.GroupID]
		if !exists {
			return nil, "", fmt.Errorf(
				"%w: route target group %d missing from catalog",
				ErrInconsistentSnapshot,
				target.GroupID,
			)
		}
		decision := targetDecision{target: target, group: group, included: true}
		switch {
		case !group.Enabled:
			decision.included = false
			decision.reason = ReasonGroupDisabled
		case len(query.AccessKey.Filters.Groups) > 0:
			if _, allowed := query.AccessKey.Filters.Groups[target.GroupID]; !allowed {
				decision.included = false
				decision.reason = ReasonGroupFiltered
			}
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

func normalizedAutoWeight(weight int) int {
	if weight == 0 {
		return state.DefaultWeight
	}
	return weight
}

func effectiveWeight(groupManual, keyManual *int, keyAuto int) int64 {
	groupWeight := state.DefaultWeight
	if groupManual != nil {
		groupWeight = *groupManual
	}
	keyWeight := normalizedAutoWeight(keyAuto)
	if keyManual != nil {
		keyWeight = *keyManual
	}
	if groupWeight <= 0 || keyWeight <= 0 {
		return 0
	}
	return int64(groupWeight) * int64(keyWeight)
}

func inspectKey(
	group state.GroupCatalogView,
	key state.KeyRuntimeView,
	now time.Time,
) KeyInspection {
	result := KeyInspection{
		KeyID: key.ID, WeightManual: cloneWeight(key.WeightManual),
		WeightAuto: normalizedAutoWeight(key.WeightAuto),
	}
	if group.WeightManual != nil && *group.WeightManual == 0 {
		result.Reason = ReasonGroupWeightZero
		return result
	}
	if key.Status != state.KeyStatusActive {
		result.Reason = ReasonKeyDisabled
		return result
	}
	if key.WeightManual != nil && *key.WeightManual == 0 {
		result.Reason = ReasonKeyWeightZero
		return result
	}
	switch key.RuntimeState(now) {
	case state.KeyRuntimeBlacklisted:
		result.Reason = ReasonKeyBlacklisted
	case state.KeyRuntimeCooldown:
		result.Reason = ReasonKeyCooldown
		result.CooldownUntil = key.CooldownUntil
	default:
		result.Available = true
		result.EffectiveWeight = effectiveWeight(
			group.WeightManual,
			key.WeightManual,
			key.WeightAuto,
		)
	}
	return result
}

func Inspect(
	snapshot *state.ConfigSnapshot,
	keys []state.KeyRuntimeView,
	query Query,
	now time.Time,
) (Inspection, error) {
	result := Inspection{Groups: []GroupInspection{}}
	if snapshot == nil {
		return Inspection{}, fmt.Errorf("%w: nil ConfigSnapshot", ErrInconsistentSnapshot)
	}
	if query.AccessKey.Status == state.AccessKeyStatusDisabled {
		result.Reason = ReasonAccessKeyDisabled
		return result, nil
	}

	keysByGroup := make(map[uint][]state.KeyRuntimeView)
	for _, key := range keys {
		if _, exists := snapshot.GroupCatalog[key.GroupID]; !exists {
			return Inspection{}, fmt.Errorf(
				"%w: Registry key %d group %d missing from catalog",
				ErrInconsistentSnapshot,
				key.ID,
				key.GroupID,
			)
		}
		cloned := key
		cloned.WeightManual = cloneWeight(key.WeightManual)
		keysByGroup[key.GroupID] = append(keysByGroup[key.GroupID], cloned)
	}
	for groupID := range keysByGroup {
		sort.Slice(keysByGroup[groupID], func(i, j int) bool {
			return keysByGroup[groupID][i].ID < keysByGroup[groupID][j].ID
		})
	}

	decisions, staticReason, err := evaluateTargets(
		snapshot,
		snapshot.RouteCatalog,
		query,
	)
	if err != nil {
		return Inspection{}, err
	}
	for _, decision := range decisions {
		groupResult := GroupInspection{
			GroupID: decision.group.ID, GroupName: decision.group.Name,
			UpstreamModelID: decision.target.UpstreamModelID,
			WeightManual:    cloneWeight(decision.group.WeightManual),
			Included:        decision.included, Reason: decision.reason,
			Keys: []KeyInspection{},
		}
		if !decision.included {
			result.Groups = append(result.Groups, groupResult)
			continue
		}
		groupKeys := keysByGroup[decision.group.ID]
		groupWeightZero := decision.group.WeightManual != nil &&
			*decision.group.WeightManual == 0
		for _, key := range groupKeys {
			keyResult := inspectKey(decision.group, key, now)
			if keyResult.Available && keyResult.EffectiveWeight > 0 {
				groupResult.Routable = true
			}
			groupResult.Keys = append(groupResult.Keys, keyResult)
		}
		switch {
		case groupWeightZero:
			groupResult.Reason = ReasonGroupWeightZero
		case len(groupKeys) == 0:
			groupResult.Reason = ReasonNoKeys
		case !groupResult.Routable:
			groupResult.Reason = ReasonNoAvailableKey
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
		result.Reason = ReasonNoAvailableKey
	}
	return result, nil
}
