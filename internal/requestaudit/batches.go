package requestaudit

import (
	"encoding/json"
	"sort"
	"time"
)

const maxContextBytes = 4 << 10
const maxQuestionBytes = 10 << 10

type reviewBatch struct {
	payload []byte
	rules   []Rule
	checks  []check
}

func verdictKey(pending check) [32]byte {
	encoded, _ := json.Marshal(pending.proofs)
	return hashParts([]byte("verdict"), encoded)
}

func (r *Review) buildBatches(model string, doc reviewDocument, now time.Time) {
	// 长规则也需要分组，避免合法的 16 条规则本身就占满单次上下文。
	for start := 0; start < len(r.checks); {
		end, size := start, 0
		for end < len(r.checks) {
			encoded, _ := json.Marshal(question(r.checks[end].rule, []int{0}))
			if end > start && size+len(encoded) > maxQuestionBytes {
				break
			}
			size += len(encoded)
			end++
		}
		checks := r.checks[start:end]
		targets := map[int]bool{}
		for _, pending := range checks {
			for _, target := range pending.targets {
				targets[target] = true
			}
		}
		ordered := make([]int, 0, len(targets))
		for target := range targets {
			ordered = append(ordered, target)
		}
		sort.Ints(ordered)
		for first := 0; first < len(ordered); {
			var batch reviewBatch
			last := first
			try := func(count int) bool {
				candidate := makeBatch(model, doc, checks, ordered[first:first+count])
				if len(candidate.payload) > MaxRequestBytes {
					return false
				}
				batch, last = candidate, first+count
				return true
			}
			// 倍增确定搜索范围，再二分缩小；只保留实际编码长度合格的批次。
			remaining := len(ordered) - first
			good, bad := 0, remaining+1
			for count := 1; ; count = min(count*2, remaining) {
				if !try(count) {
					bad = count
					break
				}
				good = count
				if count == remaining {
					break
				}
			}
			for bad-good > 1 {
				middle := good + (bad-good)/2
				if try(middle) {
					good = middle
				} else {
					bad = middle
				}
			}
			if last == first {
				r.Reason = "content_too_large"
				return
			}
			// 缓存命中只复用完整的判定集合；不会把一次告警归因到某条消息。
			uncached := batch.checks[:0]
			for _, pending := range batch.checks {
				if cached, found := r.cache.lookup(pending.key, now); found {
					r.add(pending.rule, cached.probability)
				} else {
					uncached = append(uncached, pending)
				}
			}
			if len(uncached) > 0 {
				batch = makeBatch(model, doc, uncached, ordered[first:last])
				r.batches = append(r.batches, batch)
				if len(r.batches) > MaxReviewBatches {
					r.Reason = "content_too_large"
					return
				}
			}
			first = last
		}
		start = end
	}
	r.currentBatch()
}

func question(rule Rule, targets []int) any {
	return map[string]any{"type": "noul", "instructions": map[string]any{"task": instructions, "policy": rule.Instructions, "target_ids": targets}}
}

func makeBatch(model string, doc reviewDocument, checks []check, selected []int) reviewBatch {
	batch := reviewBatch{}
	wanted := map[int]bool{}
	for _, target := range selected {
		wanted[target] = true
	}
	questions := map[string]any{}
	targets := map[int]bool{}
	for _, pending := range checks {
		part := check{rule: pending.rule}
		// targets 已排序；只遍历本批范围，长历史不反复扫描整个待检集合。
		first := sort.SearchInts(pending.targets, selected[0])
		last := sort.SearchInts(pending.targets, selected[len(selected)-1]+1)
		for index := first; index < last; index++ {
			target := pending.targets[index]
			if wanted[target] {
				part.targets = append(part.targets, target)
				part.proofs = append(part.proofs, pending.proofs[index])
				targets[target] = true
			}
		}
		if len(part.targets) == 0 {
			continue
		}
		part.key = verdictKey(part)
		batch.checks = append(batch.checks, part)
		batch.rules = append(batch.rules, part.rule)
		questions[part.rule.ID] = question(part.rule, part.targets)
	}
	type unit struct {
		ID      int             `json:"id"`
		Source  int             `json:"source_id"`
		Content json.RawMessage `json:"content"`
	}
	units := []unit{}
	fullSources := map[int]int{}
	context := map[int]bool{}
	for _, index := range selected {
		if !targets[index] {
			continue
		}
		source := doc.sources[index]
		units = append(units, unit{index, source, doc.units[index]})
		if len(doc.original.Units[source]) <= maxUnitBytes {
			fullSources[source] = index
		}
		for previous := max(0, source-2); previous < source; previous++ {
			context[previous] = true
		}
		for _, related := range doc.original.Related[source] {
			context[related] = true
		}
	}
	anchors := []int{}
	anchorSet := map[int]bool{}
	for _, source := range doc.original.Anchors {
		anchorSet[source] = true
	}
	truncated := false
	// 公共指令与近期/关联前文各占一半预算，不能由巨大工具定义挤掉所有前文。
	appendContext := func(sources []int, budget int, isAnchor bool) {
		pending := []int{}
		for _, source := range sources {
			if id, found := fullSources[source]; found {
				if isAnchor {
					anchors = append(anchors, id)
				}
				continue
			}
			pending = append(pending, source)
		}
		for index, source := range pending {
			allowance := budget/(len(pending)-index) - 32
			raw := doc.original.Units[source]
			content := contextExcerpt(raw, allowance)
			truncated = truncated || len(content) != len(raw)
			if len(content) == 0 {
				continue
			}
			id := len(doc.units) + source
			units = append(units, unit{id, source, content})
			budget -= len(content) + 32
			if isAnchor {
				anchors = append(anchors, id)
			}
		}
	}
	appendContext(doc.original.Anchors, maxContextBytes/2, true)
	neighbors := []int{}
	for source := range context {
		if !anchorSet[source] {
			neighbors = append(neighbors, source)
		}
	}
	sort.Ints(neighbors)
	appendContext(neighbors, maxContextBytes/2, false)
	sort.Slice(units, func(i, j int) bool {
		if units[i].Source != units[j].Source {
			return units[i].Source < units[j].Source
		}
		return units[i].ID < units[j].ID
	})
	batch.payload, _ = json.Marshal(map[string]any{
		"model":     model,
		"state":     map[string]any{"anchors": anchors, "units": units, "context_truncated": truncated},
		"questions": questions,
	})
	return batch
}

func (r *Review) currentBatch() {
	r.Payload, r.Rules = nil, nil
	if r.next < len(r.batches) {
		r.Payload, r.Rules = r.batches[r.next].payload, r.batches[r.next].rules
	}
}
