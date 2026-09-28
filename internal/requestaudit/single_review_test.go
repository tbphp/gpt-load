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

func TestOversizedReviewUsesOneBoundedSampleWithoutFullPassProof(t *testing.T) {
	text := "HEAD-MARKER " + strings.Repeat("plain text ", 40000) + " MIDDLE-MARKER " + strings.Repeat("plain text ", 40000) + " TAIL-MARKER"
	body, _ := json.Marshal(map[string]any{"input": []any{map[string]string{"type": "function_call_output", "call_id": "call-source", "output": text}}})
	doc := document(t, string(body))
	var cache Cache
	now := time.Now()
	review := cache.Prepare(nil, "jev", doc, DefaultConfig().Rules, now)
	if review.Reason != "" || len(review.Payload) == 0 || len(review.Payload) > MaxRequestBytes {
		t.Fatalf("sample unavailable: reason=%s bytes=%d", review.Reason, len(review.Payload))
	}
	for _, marker := range []string{"HEAD-MARKER", "MIDDLE-MARKER", "TAIL-MARKER", "call-source"} {
		if !bytes.Contains(review.Payload, []byte(marker)) {
			t.Fatalf("single review omitted %s", marker)
		}
	}
	answer(t, review, now)
	if len(review.Payload) != 0 || review.Status != "incomplete" || review.Reason != "content_truncated" {
		t.Fatalf("sample was treated as complete or needs another call: status=%s reason=%s bytes=%d", review.Status, review.Reason, len(review.Payload))
	}
	if again := cache.Prepare(nil, "jev", doc, DefaultConfig().Rules, now); len(again.Payload) == 0 {
		t.Fatal("truncated target was cached as fully reviewed")
	}
}

func TestSingleReviewPrioritizesRecentTargetsAndPreservesEveryRule(t *testing.T) {
	messages := make([]any, 1000)
	for i := range messages {
		messages[i] = map[string]string{"role": "user", "content": fmt.Sprintf("message-%04d ", i) + strings.Repeat("text ", 100)}
	}
	body, _ := json.Marshal(map[string]any{"messages": messages})
	doc := document(t, string(body))
	var cache Cache
	now := time.Now()
	rules := DefaultConfig().Rules
	review := cache.Prepare(nil, "jev", doc, rules, now)
	if review.Reason != "" || len(review.Payload) > MaxRequestBytes || len(review.Rules) != len(rules) || !bytes.Contains(review.Payload, []byte("message-0999")) {
		t.Fatalf("recent content or rules missing: reason=%s bytes=%d rules=%d", review.Reason, len(review.Payload), len(review.Rules))
	}
	answer(t, review, now)
	if review.Status != "incomplete" || len(review.Payload) != 0 {
		t.Fatal("omitted history was treated as fully reviewed")
	}
	again := cache.Prepare(nil, "jev", doc, rules, now)
	if len(again.Payload) == 0 {
		t.Fatal("omitted targets were cached as passed")
	}
}

func TestOversizedRulesAreNeverSplitOrTruncated(t *testing.T) {
	rules := []Rule{}
	for i := range 16 {
		rules = append(rules, Rule{ID: fmt.Sprintf("rule_%d", i), Name: "Policy", Enabled: true, Instructions: strings.Repeat("policy ", 580), Action: ActionWarn, Threshold: .8})
	}
	var cache Cache
	review := cache.Prepare(nil, "jev", document(t, `{"input":"text to inspect"}`), rules, time.Now())
	if review.Reason != "content_too_large" || len(review.Payload) != 0 || len(cache.entries) != 0 {
		t.Fatal("oversized rules caused a partial rule set or a fabricated pass")
	}
}

func TestSingleReviewDistributesSpaceAcrossLargeToolResults(t *testing.T) {
	items := []any{}
	for i := range 3 {
		items = append(items, map[string]string{"type": "function_call_output", "call_id": fmt.Sprintf("call-%d", i), "output": fmt.Sprintf("HEAD-%d ", i) + strings.Repeat("x", 100000) + fmt.Sprintf(" TAIL-%d", i)})
	}
	body, _ := json.Marshal(map[string]any{"input": items})
	var cache Cache
	review := cache.Prepare(nil, "jev", document(t, string(body)), DefaultConfig().Rules, time.Now())
	if review.Reason != "" || len(review.Payload) > MaxRequestBytes {
		t.Fatalf("large tools exceeded budget: %s", review.Reason)
	}
	for i := range 3 {
		for _, marker := range []string{fmt.Sprintf("HEAD-%d", i), fmt.Sprintf("TAIL-%d", i), fmt.Sprintf("call-%d", i)} {
			if !bytes.Contains(review.Payload, []byte(marker)) {
				t.Fatalf("tool sample omitted %s", marker)
			}
		}
	}
}

func TestSingleReviewKeepsToolIdentityWithinSampleBudget(t *testing.T) {
	items := []any{}
	for range 80 {
		items = append(items, map[string]string{"role": "tool", "name": strings.Repeat("n", 128), "tool_call_id": strings.Repeat("i", 128), "content": strings.Repeat("x", 1000)})
	}
	body, _ := json.Marshal(map[string]any{"input": items})
	var cache Cache
	review := cache.Prepare(nil, "jev", document(t, string(body)), DefaultConfig().Rules, time.Now())
	if review.Reason != "" || len(review.Payload) == 0 || len(review.Payload) > MaxRequestBytes {
		t.Fatalf("tool identity prevented a bounded sample: reason=%s bytes=%d", review.Reason, len(review.Payload))
	}
}

func TestExcerptPreservesUTF8OffsetsAndBudget(t *testing.T) {
	body, _ := json.Marshal(map[string]any{"input": []any{map[string]string{"role": "tool", "tool_call_id": "call-source", "content": strings.Repeat("中文\"\n<>&😀 ", 4000)}}})
	raw := document(t, string(body)).Units[0]
	for _, budget := range []int{256, 1024, 4096} {
		excerpt := contentExcerpt(raw, budget)
		if len(excerpt) == 0 || len(excerpt) > budget || !json.Valid(excerpt) || !utf8.Valid(excerpt) {
			t.Fatalf("invalid excerpt for budget %d", budget)
		}
		var value struct {
			Truncated bool          `json:"truncated"`
			Origin    excerptOrigin `json:"origin"`
			Excerpts  []struct {
				Offset int    `json:"offset"`
				Text   string `json:"text"`
			} `json:"excerpts"`
		}
		if err := json.Unmarshal(excerpt, &value); err != nil {
			t.Fatal(err)
		}
		if !value.Truncated || len(value.Excerpts) != 3 || value.Origin.Role != "tool" || value.Origin.ToolCallID != "call-source" {
			t.Fatal("excerpt lost its origin or omission marker")
		}
		for _, part := range value.Excerpts {
			if !utf8.ValidString(part.Text) || string(raw[part.Offset:part.Offset+len(part.Text)]) != part.Text {
				t.Fatal("excerpt changed the source text")
			}
		}
	}
}

func TestLargeCachedAnchorUsesBoundedContextOnTheNextTurn(t *testing.T) {
	messages := []any{
		map[string]string{"role": "system", "content": "SYSTEM-HEAD " + strings.Repeat("long instruction ", 500) + " SYSTEM-TAIL"},
		map[string]string{"role": "user", "content": "initial task"},
	}
	body, _ := json.Marshal(map[string]any{"messages": messages})
	var cache Cache
	now := time.Now()
	rules := DefaultConfig().Rules
	review := cache.Prepare(nil, "jev", document(t, string(body)), rules, now)
	answer(t, review, now)
	if review.Status != "passed" {
		t.Fatal("bounded initial content was truncated")
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
	answer(t, review, now)
	if review.Status != "passed" || review.Reason != "" {
		t.Fatal("excerpted supporting context made complete target review partial")
	}
}

func TestPartialReviewCachesOnlyWholeTargetsAndKeepsMatchedActions(t *testing.T) {
	for _, action := range []string{ActionWarn, ActionBlock} {
		t.Run(action, func(t *testing.T) {
			body, _ := json.Marshal(map[string]any{"input": []any{strings.Repeat("long text ", 20000), "complete-new-target"}})
			doc := document(t, string(body))
			var cache Cache
			now := time.Now()
			rules := DefaultConfig().Rules[:1]
			review := cache.Prepare(nil, "jev", doc, rules, now)
			answer(t, review, now)
			again := cache.Prepare(nil, "jev", doc, rules, now)
			if len(again.checks) != 1 || len(again.checks[0].targets) != 1 || again.checks[0].targets[0] != 0 {
				t.Fatal("truncated target was cached or complete target was not reused")
			}
			rules[0].Action = action
			again = cache.Prepare(nil, "jev", doc, rules, now)
			answer(t, again, now, .99)
			want := "warned"
			if action == ActionBlock {
				want = "blocked"
			}
			if again.Status != want || again.Reason != "content_truncated" || len(again.Findings) != 1 {
				t.Fatal("partial review lost its match or coverage")
			}
		})
	}
}

func TestShortMessageReviewHasBoundedAllocations(t *testing.T) {
	messages := make([]any, 1000)
	for i := range messages {
		messages[i] = map[string]string{"role": "user", "content": fmt.Sprintf("short message %d", i)}
	}
	body, _ := json.Marshal(map[string]any{"messages": messages})
	doc := document(t, string(body))
	rules := DefaultConfig().Rules
	now := time.Now()
	allocations := testing.AllocsPerRun(1, func() {
		var cache Cache
		review := cache.Prepare(nil, "jev", doc, rules, now)
		if review.Reason != "" || len(review.Payload) > MaxRequestBytes {
			t.Fatal("short message sampling failed")
		}
	})
	if allocations > 50000 {
		t.Fatalf("short-message review allocated %.0f objects, want at most 50000", allocations)
	}
}
