// Package scheduler selects upstream keys without IO or persistence access.
package scheduler

import (
	"errors"
	"math/rand"

	"gpt-load/internal/protocol"
	"gpt-load/internal/state"
)

var ErrExhausted = errors.New("scheduler exhausted")

type KeySource interface {
	CollectCandidates(groupIDs []uint, excluded func(uint) bool) []state.KeyMeta
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

type Iterator struct {
	keys     KeySource
	random   *rand.Rand
	targets  map[uint]candidateTarget
	groupIDs []uint
	tried    map[uint]struct{}
}

func New(snapshot *state.ConfigSnapshot, keys KeySource, query Query, random *rand.Rand) *Iterator {
	iterator := &Iterator{
		keys:    keys,
		random:  random,
		targets: make(map[uint]candidateTarget),
		tried:   make(map[uint]struct{}),
	}
	for _, target := range filterTargets(snapshot, query) {
		iterator.targets[target.target.GroupID] = target
		iterator.groupIDs = append(iterator.groupIDs, target.target.GroupID)
	}
	return iterator
}

func (iterator *Iterator) Next() (Selection, error) {
	if iterator == nil || iterator.keys == nil || iterator.random == nil || len(iterator.groupIDs) == 0 {
		return Selection{}, ErrExhausted
	}
	pool := iterator.keys.CollectCandidates(iterator.groupIDs, func(keyID uint) bool {
		_, tried := iterator.tried[keyID]
		return tried
	})
	filtered := pool[:0]
	for _, key := range pool {
		if _, ok := iterator.targets[key.GroupID]; ok {
			filtered = append(filtered, key)
		}
	}
	if len(filtered) == 0 {
		return Selection{}, ErrExhausted
	}

	selected := filtered[iterator.random.Intn(len(filtered))]
	iterator.tried[selected.ID] = struct{}{}
	target := iterator.targets[selected.GroupID]
	return Selection{
		KeyID:           selected.ID,
		GroupID:         selected.GroupID,
		UpstreamModelID: target.target.UpstreamModelID,
		Group:           target.group,
	}, nil
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
