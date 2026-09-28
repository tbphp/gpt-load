package requestaudit

import (
	"bytes"
	"encoding/json"
	"sort"
)

const maxContextBytes = 4 << 10
const minTargetBytes = 256

type reviewUnit struct {
	ID      int             `json:"id"`
	Content json.RawMessage `json:"content"`
}

type reviewPayload struct {
	Model string `json:"model"`
	State struct {
		Anchors          []int        `json:"anchors"`
		Units            []reviewUnit `json:"units"`
		ContextTruncated bool         `json:"context_truncated"`
		ContentTruncated bool         `json:"content_truncated"`
	} `json:"state"`
	Questions map[string]any `json:"questions"`
}

func verdictKey(pending check) [32]byte {
	encoded, _ := json.Marshal(pending.proofs)
	return hashParts([]byte("verdict"), encoded)
}

func (r *Review) buildPayload(model string, doc Document) {
	targets, required := map[int]bool{}, map[int]bool{}
	size := 0
	for _, pending := range r.checks {
		r.Rules = append(r.Rules, pending.rule)
		// 每条规则至少保留其最新待检目标，不通过拆分或截短规则减少开销。
		required[pending.targets[len(pending.targets)-1]] = true
		for _, target := range pending.targets {
			if !targets[target] {
				targets[target] = true
				size += len(doc.Units[target])
			}
		}
	}
	ordered := sortedTargets(targets)
	if size <= MaxRequestBytes {
		payload := makePayload(model, doc, r.checks, targets)
		for i := range payload.State.Units {
			unit := &payload.State.Units[i]
			if targets[unit.ID] {
				unit.Content = doc.Units[unit.ID]
			}
		}
		encoded, _ := json.Marshal(payload)
		if len(encoded) <= MaxRequestBytes {
			r.Payload = encoded
			return
		}
	}

	// 目标过多时优先近期内容；为每条选中消息保留取样空间，避免巨大工具结果独占预算。
	count := min(len(ordered), MaxRequestBytes/minTargetBytes)
	for count >= len(required) {
		selected := make(map[int]bool)
		for target := range required {
			selected[target] = true
		}
		for i := len(ordered) - 1; i >= 0 && len(selected) < count; i-- {
			selected[ordered[i]] = true
		}
		payload := makePayload(model, doc, r.checks, selected)
		payload.State.ContentTruncated = true
		encoded, _ := json.Marshal(payload)
		// null 占四字节；所有规则、目标编号、辅助前文和 JSON 包装先按实际编码扣除。
		budget := MaxRequestBytes - len(encoded) + 4*count - 32
		if budget < count*minTargetBytes {
			if count == len(required) {
				break
			}
			count = max(len(required), min(count-1, budget/minTargetBytes))
			continue
		}
		complete := map[int]bool{}
		remaining := count
		fits := true
		for i := len(payload.State.Units) - 1; i >= 0; i-- {
			unit := &payload.State.Units[i]
			if !selected[unit.ID] {
				continue
			}
			raw := doc.Units[unit.ID]
			unit.Content = contentExcerpt(raw, budget/remaining)
			if len(unit.Content) == 0 {
				fits = false
				break
			}
			complete[unit.ID] = bytes.Equal(raw, unit.Content)
			budget -= len(unit.Content)
			remaining--
		}
		if !fits {
			if count == len(required) {
				break
			}
			// 来源元数据也需要空间；减少目标数量，仍然只准备一次外发请求。
			count = max(len(required), count/2)
			continue
		}
		for i, pending := range r.checks {
			part := check{rule: pending.rule, key: pending.key, complete: true}
			for index, target := range pending.targets {
				if selected[target] {
					part.targets = append(part.targets, target)
				}
				if complete[target] {
					part.proofs = append(part.proofs, pending.proofs[index])
				} else {
					part.complete = false
				}
			}
			r.checks[i] = part
			r.partial = r.partial || !part.complete
		}
		payload.State.ContentTruncated = r.partial
		encoded, _ = json.Marshal(payload)
		if len(encoded) <= MaxRequestBytes {
			r.Payload = encoded
			return
		}
		break
	}
	r.Reason = "content_too_large"
}

func sortedTargets(targets map[int]bool) []int {
	ordered := make([]int, 0, len(targets))
	for target := range targets {
		ordered = append(ordered, target)
	}
	sort.Ints(ordered)
	return ordered
}

// 先为目标保留占位，辅助前文最多使用固定预算，不能挤占整次调用。
func makePayload(model string, doc Document, checks []check, selected map[int]bool) reviewPayload {
	payload := reviewPayload{Model: model, Questions: map[string]any{}}
	payload.State.Anchors = []int{}
	context := map[int]bool{}
	for _, target := range sortedTargets(selected) {
		payload.State.Units = append(payload.State.Units, reviewUnit{ID: target})
		for previous := max(0, target-2); previous < target; previous++ {
			context[previous] = true
		}
		for _, related := range doc.Related[target] {
			context[related] = true
		}
	}
	for _, pending := range checks {
		targets := []int{}
		for _, target := range pending.targets {
			if selected[target] {
				targets = append(targets, target)
			}
		}
		payload.Questions[pending.rule.ID] = map[string]any{"type": "noul", "instructions": map[string]any{"task": instructions, "policy": pending.rule.Instructions, "target_ids": targets}}
	}
	anchors := map[int]bool{}
	for _, source := range doc.Anchors {
		anchors[source] = true
		delete(context, source)
	}
	appendContext := func(sources map[int]bool, budget int, isAnchor bool) {
		pending := []int{}
		for _, source := range sortedTargets(sources) {
			if selected[source] {
				if isAnchor {
					payload.State.Anchors = append(payload.State.Anchors, source)
				}
			} else {
				pending = append(pending, source)
			}
		}
		if limit := budget / minTargetBytes; len(pending) > limit {
			pending = pending[len(pending)-limit:]
			payload.State.ContextTruncated = true
		}
		for i := len(pending) - 1; i >= 0; i-- {
			source := pending[i]
			raw := doc.Units[source]
			content := contentExcerpt(raw, budget/(i+1)-32)
			payload.State.ContextTruncated = payload.State.ContextTruncated || !bytes.Equal(raw, content)
			if len(content) == 0 {
				continue
			}
			payload.State.Units = append(payload.State.Units, reviewUnit{ID: source, Content: content})
			budget -= len(content) + 32
			if isAnchor {
				payload.State.Anchors = append(payload.State.Anchors, source)
			}
		}
	}
	appendContext(anchors, maxContextBytes/2, true)
	appendContext(context, maxContextBytes/2, false)
	sort.Ints(payload.State.Anchors)
	sort.Slice(payload.State.Units, func(i, j int) bool { return payload.State.Units[i].ID < payload.State.Units[j].ID })
	return payload
}
