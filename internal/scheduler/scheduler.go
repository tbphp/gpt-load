// Package scheduler selects upstream keys without IO or persistence access.
package scheduler

import (
	"errors"
	"math/rand"
	"time"

	"gpt-load/internal/protocol"
	"gpt-load/internal/state"
)

var ErrExhausted = errors.New("scheduler exhausted")

type KeySource interface {
	CollectCandidates(groupIDs []uint, excluded func(uint) bool, now time.Time) []state.KeyMeta
}

type Query struct {
	Protocol      protocol.Protocol
	ExternalModel string
	AccessKey     state.AccessKeyView
}

type Selection struct {
	KeyID           uint
	GroupID         uint
	UpstreamModelID string
	Group           state.GroupView
}

type candidateTarget struct {
	target state.RouteTarget
	group  state.GroupView
}

type weightedKey struct {
	meta   state.KeyMeta
	weight int64
}

type Iterator struct {
	keys          KeySource
	random        *rand.Rand
	targets       map[uint]candidateTarget
	groupIDs      []uint
	tried         map[uint]struct{}
	skippedGroups map[uint]struct{}
	now           func() time.Time
}

func New(snapshot *state.ConfigSnapshot, keys KeySource, query Query, random *rand.Rand) *Iterator {
	return newWithClock(snapshot, keys, query, random, time.Now)
}

func newWithClock(
	snapshot *state.ConfigSnapshot,
	keys KeySource,
	query Query,
	random *rand.Rand,
	now func() time.Time,
) *Iterator {
	iterator := &Iterator{
		keys:          keys,
		random:        random,
		targets:       make(map[uint]candidateTarget),
		tried:         make(map[uint]struct{}),
		skippedGroups: make(map[uint]struct{}),
		now:           now,
	}
	for _, target := range filterTargets(snapshot, query) {
		iterator.targets[target.target.GroupID] = target
		iterator.groupIDs = append(iterator.groupIDs, target.target.GroupID)
	}
	return iterator
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

func (iterator *Iterator) Next() (Selection, error) {
	if iterator == nil || iterator.keys == nil || iterator.random == nil || iterator.now == nil || len(iterator.groupIDs) == 0 {
		return Selection{}, ErrExhausted
	}
	pool := iterator.keys.CollectCandidates(iterator.groupIDs, func(keyID uint) bool {
		_, tried := iterator.tried[keyID]
		return tried
	}, iterator.now())
	weighted := make([]weightedKey, 0, len(pool))
	var total int64
	for _, key := range pool {
		if _, skipped := iterator.skippedGroups[key.GroupID]; skipped {
			continue
		}
		target, ok := iterator.targets[key.GroupID]
		if !ok {
			continue
		}
		weight := effectiveWeight(key, target.group)
		if weight <= 0 {
			continue
		}
		weighted = append(weighted, weightedKey{meta: key, weight: weight})
		total += weight
	}
	if total <= 0 {
		return Selection{}, ErrExhausted
	}

	ticket := iterator.random.Int63n(total)
	selected := weighted[len(weighted)-1].meta
	for _, candidate := range weighted {
		if ticket < candidate.weight {
			selected = candidate.meta
			break
		}
		ticket -= candidate.weight
	}
	iterator.tried[selected.ID] = struct{}{}
	target := iterator.targets[selected.GroupID]
	return Selection{
		KeyID:           selected.ID,
		GroupID:         selected.GroupID,
		UpstreamModelID: target.target.UpstreamModelID,
		Group:           target.group,
	}, nil
}

func effectiveWeight(key state.KeyMeta, group state.GroupView) int64 {
	groupWeight := state.DefaultWeight
	if group.WeightManual != nil {
		groupWeight = *group.WeightManual
	}
	keyWeight := key.WeightAuto
	if keyWeight == 0 {
		keyWeight = state.DefaultWeight
	}
	if key.WeightManual != nil {
		keyWeight = *key.WeightManual
	}
	if groupWeight <= 0 || keyWeight <= 0 {
		return 0
	}
	return int64(groupWeight) * int64(keyWeight)
}

func filterTargets(snapshot *state.ConfigSnapshot, query Query) []candidateTarget {
	if snapshot == nil {
		return nil
	}
	if len(query.AccessKey.Filters.Protocols) > 0 {
		if _, ok := query.AccessKey.Filters.Protocols[query.Protocol]; !ok {
			return nil
		}
	}
	if len(query.AccessKey.Filters.Models) > 0 {
		if _, ok := query.AccessKey.Filters.Models[query.ExternalModel]; !ok {
			return nil
		}
	}

	byModel := snapshot.Candidates[query.Protocol]
	targets := make([]candidateTarget, 0, len(byModel[query.ExternalModel]))
	seenGroups := make(map[uint]struct{})
	for _, target := range byModel[query.ExternalModel] {
		if len(query.AccessKey.Filters.Groups) > 0 {
			if _, ok := query.AccessKey.Filters.Groups[target.GroupID]; !ok {
				continue
			}
		}
		group, ok := snapshot.Groups[target.GroupID]
		if !ok {
			continue
		}
		if _, duplicate := seenGroups[target.GroupID]; duplicate {
			continue
		}
		seenGroups[target.GroupID] = struct{}{}
		targets = append(targets, candidateTarget{target: target, group: group})
	}
	return targets
}
