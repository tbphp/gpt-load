package requestaudit

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestLongHistoryIsFullyReviewedInBoundedBatches(t *testing.T) {
	messages := []any{map[string]string{"role": "system", "content": "Review the supplied text."}}
	for i := range 80 {
		messages = append(messages, map[string]string{"role": "user", "content": fmt.Sprintf("target-%03d ", i) + strings.Repeat("text ", 350)})
	}
	body, _ := json.Marshal(map[string]any{"messages": messages})
	doc := document(t, string(body))
	var cache Cache
	now := time.Now()
	review := cache.Prepare(nil, "jev", doc, DefaultConfig().Rules, now)
	if review.Reason != "" {
		t.Fatalf("long text history was rejected before review: %s", review.Reason)
	}
	seen := map[string]bool{}
	calls := 0
	for len(review.Payload) > 0 {
		if len(review.Payload) > 24<<10 {
			t.Fatalf("batch exceeds conservative encoded budget: %d", len(review.Payload))
		}
		for i := range 80 {
			marker := fmt.Sprintf("target-%03d", i)
			if bytes.Contains(review.Payload, []byte(marker)) {
				seen[marker] = true
			}
		}
		answer(t, review, now)
		calls++
		if calls > 64 {
			t.Fatal("review did not finish within its call budget")
		}
	}
	if calls < 2 || len(seen) != 80 || review.Status != "passed" {
		t.Fatalf("incomplete history review: calls=%d targets=%d status=%s", calls, len(seen), review.Status)
	}
	if again := cache.Prepare(nil, "jev", doc, DefaultConfig().Rules, now); len(again.Payload) != 0 || again.Reason != "" {
		t.Fatal("completed batches were not reusable")
	}
}

func TestOversizedMessageKeepsMiddleAndTailForEveryRule(t *testing.T) {
	text := "HEAD-MARKER " + strings.Repeat("普通内容 ", 6000) + " MIDDLE-SECRET " + strings.Repeat("more text ", 6000) + " TAIL-MARKER"
	body, _ := json.Marshal(map[string]any{"messages": []any{map[string]string{"role": "tool", "tool_call_id": "call-a", "content": text}}})
	var cache Cache
	now := time.Now()
	rules := DefaultConfig().Rules
	review := cache.Prepare(nil, "jev", document(t, string(body)), rules, now)
	if review.Reason != "" {
		t.Fatalf("large single message was not split: %s", review.Reason)
	}
	seen := map[string]map[string]bool{}
	for len(review.Payload) > 0 {
		if !json.Valid(review.Payload) || len(review.Payload) > 24<<10 {
			t.Fatal("invalid or oversized batch")
		}
		var payload struct {
			State struct {
				Units []struct {
					ID      int             `json:"id"`
					Content json.RawMessage `json:"content"`
				} `json:"units"`
			} `json:"state"`
			Questions map[string]struct {
				Instructions struct {
					TargetIDs []int `json:"target_ids"`
				} `json:"instructions"`
			} `json:"questions"`
		}
		if err := json.Unmarshal(review.Payload, &payload); err != nil {
			t.Fatal(err)
		}
		for ruleID, question := range payload.Questions {
			if seen[ruleID] == nil {
				seen[ruleID] = map[string]bool{}
			}
			for _, id := range question.Instructions.TargetIDs {
				for _, unit := range payload.State.Units {
					if unit.ID != id {
						continue
					}
					for _, marker := range []string{"HEAD-MARKER", "MIDDLE-SECRET", "TAIL-MARKER"} {
						if bytes.Contains(unit.Content, []byte(marker)) {
							seen[ruleID][marker] = true
						}
					}
				}
			}
		}
		answer(t, review, now)
	}
	for _, rule := range rules {
		if len(seen[rule.ID]) != 3 {
			t.Fatalf("rule %s did not inspect the whole message: %v", rule.ID, seen[rule.ID])
		}
	}
}

func TestLargeRuleSetIsBatchedWithoutDroppingPolicies(t *testing.T) {
	rules := []Rule{}
	for i := range 16 {
		rules = append(rules, Rule{ID: fmt.Sprintf("rule_%d", i), Name: "Policy", Enabled: true, Instructions: strings.Repeat("policy ", 580), Action: ActionWarn, Threshold: .8})
	}
	var cache Cache
	now := time.Now()
	review := cache.Prepare(nil, "jev", document(t, `{"input":"text to inspect"}`), rules, now)
	if review.Reason != "" {
		t.Fatal(review.Reason)
	}
	seen := map[string]bool{}
	for len(review.Payload) > 0 {
		if len(review.Payload) > 24<<10 {
			t.Fatal("rules exceeded the batch budget")
		}
		for _, rule := range review.Rules {
			seen[rule.ID] = true
		}
		answer(t, review, now)
	}
	if len(seen) != len(rules) {
		t.Fatalf("reviewed %d of %d policies", len(seen), len(rules))
	}
}

func TestLargeMessageFragmentsCoverEveryByteAndKeepTheirRole(t *testing.T) {
	text := strings.Repeat("中文\"\n<>&😀 ", 4000)
	body, _ := json.Marshal(map[string]any{"messages": []any{map[string]string{"role": "tool", "tool_call_id": "call-source", "content": text}}})
	doc, err := prepareDocument(document(t, string(body)))
	if err != nil {
		t.Fatal(err)
	}
	covered := make([]bool, len(text))
	previousEnd, chunks := 0, 0
	for _, raw := range doc.units {
		var fragment struct {
			Path    string           `json:"path"`
			Value   string           `json:"value"`
			Offset  int              `json:"offset"`
			Context []map[string]any `json:"context"`
		}
		if err := json.Unmarshal(raw, &fragment); err != nil {
			t.Fatal(err)
		}
		if fragment.Path != "/content/content" {
			continue
		}
		if !utf8.ValidString(fragment.Value) || len(raw) > maxUnitBytes || text[fragment.Offset:fragment.Offset+len(fragment.Value)] != fragment.Value {
			t.Fatal("fragment changed source text")
		}
		roleFound := false
		for _, metadata := range fragment.Context {
			roleFound = roleFound || metadata["role"] == "tool" && metadata["tool_call_id"] == "call-source"
		}
		if !roleFound {
			t.Fatal("fragment lost its tool origin")
		}
		if chunks > 0 && fragment.Offset >= previousEnd {
			t.Fatal("adjacent fragments have no overlap")
		}
		for index := fragment.Offset; index < fragment.Offset+len(fragment.Value); index++ {
			covered[index] = true
		}
		previousEnd = fragment.Offset + len(fragment.Value)
		chunks++
	}
	for index, found := range covered {
		if !found {
			t.Fatalf("source byte %d was omitted", index)
		}
	}
}

func TestLargeCachedAnchorUsesBoundedContextOnTheNextTurn(t *testing.T) {
	messages := []any{
		map[string]string{"role": "system", "content": "SYSTEM-HEAD " + strings.Repeat("long instruction ", 7000) + " SYSTEM-TAIL"},
		map[string]string{"role": "user", "content": "initial task"},
	}
	body, _ := json.Marshal(map[string]any{"messages": messages})
	var cache Cache
	now := time.Now()
	rules := DefaultConfig().Rules
	review := cache.Prepare(nil, "jev", document(t, string(body)), rules, now)
	if review.Reason != "" {
		t.Fatal(review.Reason)
	}
	for len(review.Payload) > 0 {
		answer(t, review, now)
	}
	messages = append(messages, map[string]string{"role": "tool", "content": "new tool output"})
	body, _ = json.Marshal(map[string]any{"messages": messages})
	review = cache.Prepare(nil, "jev", document(t, string(body)), rules, now)
	if review.Reason != "" || len(review.Payload) == 0 || len(review.Payload) > 8<<10 {
		t.Fatalf("cached anchor remained oversized: reason=%s bytes=%d", review.Reason, len(review.Payload))
	}
	for _, marker := range []string{"SYSTEM-HEAD", "SYSTEM-TAIL", "new tool output", `"context_truncated":true`} {
		if !bytes.Contains(review.Payload, []byte(marker)) {
			t.Fatalf("missing context marker %s", marker)
		}
	}
	for _, pending := range review.checks {
		if len(pending.targets) != 1 {
			t.Fatal("already reviewed anchor became a target again")
		}
	}
}

func TestPartialReviewNeverCachesUnreviewedTargets(t *testing.T) {
	body, _ := json.Marshal(map[string]any{"input": strings.Repeat("text ", 40000) + "UNREVIEWED-TAIL"})
	doc := document(t, string(body))
	var cache Cache
	now := time.Now()
	rules := DefaultConfig().Rules
	review := cache.Prepare(nil, "jev", doc, rules, now)
	if review.Reason != "" {
		t.Fatal(review.Reason)
	}
	answer(t, review, now)
	if len(review.Payload) == 0 {
		t.Fatal("fixture needs another batch")
	}
	if reason := review.Resolve([]byte(`{"answers":{}}`), now); reason != "invalid_response" {
		t.Fatal("failed batch was accepted")
	}
	review = cache.Prepare(nil, "jev", doc, rules, now)
	seenTail := false
	for len(review.Payload) > 0 {
		seenTail = seenTail || bytes.Contains(review.Payload, []byte("UNREVIEWED-TAIL"))
		answer(t, review, now)
	}
	if !seenTail {
		t.Fatal("unfinished content reused a pass proof")
	}
}

func shortMessageHistory(tb testing.TB, count int) Document {
	tb.Helper()
	messages := make([]any, count)
	for index := range messages {
		messages[index] = map[string]string{"role": "user", "content": fmt.Sprintf("short message %d", index)}
	}
	body, err := json.Marshal(map[string]any{"messages": messages})
	if err != nil {
		tb.Fatal(err)
	}
	doc, reason := Extract(body)
	if reason != "" {
		tb.Fatal(reason)
	}
	return doc
}

func TestShortMessageReviewHasBoundedAllocationsAndCompleteCoverage(t *testing.T) {
	const count = 1000
	doc := shortMessageHistory(t, count)
	rules := DefaultConfig().Rules
	now := time.Now()
	var review *Review
	allocations := testing.AllocsPerRun(1, func() {
		var cache Cache
		review = cache.Prepare(nil, "jev", doc, rules, now)
	})
	// 使用宽松的分配次数上限防止逐条重建回归，不依赖机器速度或运行时间。
	if allocations > 50000 {
		t.Fatalf("short-message review allocated %.0f objects, want at most 50000", allocations)
	}
	if review.Reason != "" {
		t.Fatal(review.Reason)
	}
	seen := map[string]map[int]bool{}
	for len(review.Payload) > 0 {
		if len(review.Payload) > MaxRequestBytes {
			t.Fatal("batch exceeds the encoded request budget")
		}
		var payload struct {
			Questions map[string]struct {
				Instructions struct {
					Targets []int `json:"target_ids"`
				} `json:"instructions"`
			} `json:"questions"`
		}
		if err := json.Unmarshal(review.Payload, &payload); err != nil {
			t.Fatal(err)
		}
		for rule, question := range payload.Questions {
			if seen[rule] == nil {
				seen[rule] = map[int]bool{}
			}
			for _, target := range question.Instructions.Targets {
				if target < 0 || target >= count || seen[rule][target] {
					t.Fatalf("rule %s has an invalid or repeated target %d", rule, target)
				}
				seen[rule][target] = true
			}
		}
		answer(t, review, now)
	}
	for _, rule := range rules {
		if len(seen[rule.ID]) != count {
			t.Fatalf("rule %s reviewed %d of %d messages", rule.ID, len(seen[rule.ID]), count)
		}
	}
}

func BenchmarkPrepareShortMessageHistory(b *testing.B) {
	for _, count := range []int{1000, 12000} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			doc := shortMessageHistory(b, count)
			rules := DefaultConfig().Rules
			now := time.Now()
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				var cache Cache
				review := cache.Prepare(nil, "jev", doc, rules, now)
				if count == 1000 && review.Reason != "" {
					b.Fatal(review.Reason)
				}
			}
		})
	}
}
