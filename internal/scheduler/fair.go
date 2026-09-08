package scheduler

import (
	"time"

	"gpt-load/internal/state"
)

// ChargeReplay 为已允许的显式同凭据重放取得名额，不放松普通重试的 tried 去重。
func (iterator *Iterator) ChargeReplay(selection Selection, ref state.CredentialRef) bool {
	if ref.ID != selection.CredentialID || ref.GroupID != selection.GroupID {
		return false
	}
	var charged bool
	consume := func(metas []state.CredentialMeta) {
		for _, meta := range metas {
			if meta.ID != ref.ID || meta.IdentityGeneration != ref.IdentityGeneration {
				continue
			}
			if selection.UpstreamModelID != nil && modelCooldownUntil(meta.ModelCooldowns, *selection.UpstreamModelID, iterator.operation, iterator.now()).After(iterator.now()) {
				continue
			}
			weight := effectiveWeight(selection.Group.WeightManual, meta.WeightManual)
			if weight > 0 {
				_, charged = iterator.selectCredential([]weightedCredential{{meta: meta, weight: weight}}, ref.ID)
			}
		}
	}
	groups := []uint{ref.GroupID}
	if source, ok := iterator.credentials.(interface {
		WithCredentialCandidates([]uint, func(uint) bool, time.Time, func([]state.CredentialMeta))
	}); ok {
		source.WithCredentialCandidates(groups, nil, iterator.now(), consume)
	} else {
		consume(iterator.credentials.CollectCredentialCandidates(groups, nil, iterator.now()))
	}
	return charged
}

// selectCredential 只操作内存；真实 Registry 调用方同时持有凭据读锁。
func (iterator *Iterator) selectCredential(candidates []weightedCredential, preferred uint) (state.CredentialMeta, bool) {
	var selected state.CredentialMeta
	var found bool
	iterator.progress.WithLock(func(ledger *state.SchedulingLedger) {
		eligible := candidates[:0]
		baseline := ledger.Watermark
		hasBaseline := false
		for _, candidate := range candidates {
			member := ledger.Members[candidate.meta.ID]
			if member == nil || member.GroupID != candidate.meta.GroupID ||
				member.IdentityGeneration != candidate.meta.IdentityGeneration {
				continue
			}
			eligible = append(eligible, candidate)
			if !member.Pending && (!hasBaseline || member.Progress.Compare(baseline) > 0) {
				baseline, hasBaseline = member.Progress, true
			}
		}
		if len(eligible) == 0 || ledger.Sequence == ^uint64(0) {
			return
		}
		// 同批成员共用已有成员的领先进度，不能继承落后成员的历史欠额。
		for _, candidate := range eligible {
			member := ledger.Members[candidate.meta.ID]
			if member.Pending && (!ledger.GroupsKnown || ledger.Groups[member.GroupID]) {
				member.Admit(baseline)
			}
		}
		less := func(left, right weightedCredential) bool {
			a, b := ledger.Members[left.meta.ID], ledger.Members[right.meta.ID]
			if order := a.Progress.Compare(b.Progress); order != 0 {
				return order < 0
			}
			if a.LastSelected != b.LastSelected {
				return a.LastSelected < b.LastSelected
			}
			return a.ID < b.ID
		}
		first := eligible[0]
		for _, candidate := range eligible[1:] {
			if less(candidate, first) {
				first = candidate
			}
		}
		preferredFound := false
		for _, candidate := range eligible {
			if candidate.meta.ID == preferred {
				first, preferredFound = candidate, true
				break
			}
		}
		if !preferredFound && ledger.LastMember == first.meta.ID && len(eligible) > 1 && ledger.Consecutive >= 100 {
			var alternative weightedCredential
			for _, candidate := range eligible {
				if candidate.meta.ID != first.meta.ID && (alternative.meta.ID == 0 || less(candidate, alternative)) {
					alternative = candidate
				}
			}
			limit := uint64(99 + (first.weight+alternative.weight-1)/alternative.weight)
			if ledger.Consecutive >= limit {
				first = alternative
			}
		}
		member := ledger.Members[first.meta.ID]
		next, ok := member.Progress.Advance(uint64(first.weight))
		if !ok {
			return
		}
		member.Progress = next
		ledger.Sequence++
		member.LastSelected = ledger.Sequence
		ledger.Started = true
		if next.Compare(ledger.Watermark) > 0 {
			ledger.Watermark = next
		}
		if ledger.LastMember != member.ID {
			ledger.LastMember, ledger.Consecutive = member.ID, 1
		} else if ledger.Consecutive < 99+state.MaxWeight*state.MaxWeight {
			// 超过所有合法阈值后无需继续增长；亲和和单候选仍可持续分配。
			ledger.Consecutive++
		}
		selected, found = first.meta, true
	})
	return selected, found
}
