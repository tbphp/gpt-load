package requestaudit

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func document(t *testing.T, body string) Document {
	t.Helper()
	doc, reason := Extract([]byte(body))
	if reason != "" {
		t.Fatal(reason)
	}
	return doc
}

func answer(t *testing.T, review *Review, now time.Time, probabilities ...float64) {
	t.Helper()
	answers := map[string]any{}
	for i, rule := range review.Rules {
		p := 0.01
		if i < len(probabilities) {
			p = probabilities[i]
		}
		answers[rule.ID] = map[string]any{"type": "noul", "noul": p}
	}
	body, _ := json.Marshal(map[string]any{"answers": answers})
	if reason := review.Resolve(body, now); reason != "" {
		t.Fatal(reason)
	}
}

func TestDefaultPresetIsReadyForWarningOnlyReview(t *testing.T) {
	defaults := DefaultConfig()
	if defaults.Enabled {
		t.Fatal("guardrails must remain opt-in")
	}
	config, err := Decode([]byte(`{"enabled":true}`))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"credential_leakage": true, "personal_data": true, "prompt_injection": true}
	if len(config.Rules) != len(want) {
		t.Fatalf("default preset has %d rules, want %d", len(config.Rules), len(want))
	}
	for _, rule := range config.Rules {
		if !want[rule.ID] || !rule.Enabled || rule.Action != ActionWarn || rule.Threshold != 0.8 {
			t.Fatalf("default rule is not ready for warning-only review: %+v", rule)
		}
		delete(want, rule.ID)
	}
	var cache Cache
	now := time.Now()
	review := cache.Prepare(nil, "jev", document(t, `{"input":"sample text"}`), config.Rules, now)
	answer(t, review, now, .99, .99, .99)
	if review.Status != "warned" || len(review.Findings) != 3 {
		t.Fatal("preset hits should record all matching rules without blocking")
	}
}

func TestPresetDoesNotReplaceExplicitRules(t *testing.T) {
	config, err := Decode([]byte(`{"enabled":true,"rules":[{"id":"personal_data","name":"Custom policy","instructions":"Custom condition","enabled":true,"action":"block","threshold":0.9}]}`))
	if err != nil || len(config.Rules) != 1 || config.Rules[0].Name != "Custom policy" || config.Rules[0].Instructions != "Custom condition" || config.Rules[0].Action != ActionBlock || config.Rules[0].Threshold != .9 {
		t.Fatalf("preset replaced an explicit rule: %+v, %v", config, err)
	}
	config, err = Decode([]byte(`{"rules":[]}`))
	if err != nil || len(config.Rules) != 0 {
		t.Fatalf("preset restored explicitly removed rules: %+v, %v", config, err)
	}
}

func TestCurrentPayloadReviewDoesNotDetectLocallyOrRequireStoredHistory(t *testing.T) {
	doc := document(t, `{"previous_response_id":"resp_old","conversation":"conv_old","input":"password=example-password"}`)
	var cache Cache
	review := cache.Prepare(nil, "jev", doc, DefaultConfig().Rules, time.Now())
	if len(review.Payload) == 0 || len(review.Findings) != 0 || review.Reason != "" || bytes.Contains(review.Payload, []byte("resp_old")) {
		t.Fatalf("current payload must be judged only by Jev: %+v", review)
	}
}

func TestTextSchemasAndStructuredToolDataAreNotAttachments(t *testing.T) {
	for _, body := range []string{
		`{"messages":[{"role":"user","content":"read the file"}],"tools":[{"name":"read_file","input_schema":{"type":"object","properties":{"file_id":{"type":"string"}}}}]}`,
		`{"messages":[{"role":"assistant","content":[{"type":"tool_use","id":"call_a","name":"read_file","input":{"file_id":"file_a"}}]}]}`,
		`{"contents":[{"role":"user","parts":[{"functionResponse":{"name":"read_file","response":{"type":"file","file_id":"file_a","content":"plain-text-tool-output"}}}]}]}`,
		`{"input":"return JSON","text":{"format":{"type":"json_schema","name":"file","schema":{"type":"object","properties":{"file_id":{"type":"string"}}}}}}`,
		`{"contents":[{"parts":[{"text":"return JSON"}]}],"generationConfig":{"responseSchema":{"type":"OBJECT","properties":{"file_id":{"type":"STRING"}}}}}`,
	} {
		doc := document(t, body)
		var cache Cache
		review := cache.Prepare(nil, "jev", doc, DefaultConfig().Rules, time.Now())
		if !bytes.Contains(review.Payload, []byte("file_id")) || review.Reason != "" {
			t.Fatal("textual data was dropped from the review")
		}
	}
	for _, body := range []string{
		`{"messages":[{"role":"user","content":[{"type":"tool_result","tool_use_id":"call_a","content":[{"type":"image","source":{"type":"base64","data":"opaque"}}]}]}]}`,
		`{"contents":[{"parts":[{"functionResponse":{"name":"tool","response":{"ok":true},"parts":[{"inlineData":{"mimeType":"image/png","data":"opaque"}}]}}]}]}`,
	} {
		if _, reason := Extract([]byte(body)); reason != "unsupported_content" {
			t.Fatal("real tool attachment was accepted as text")
		}
	}
}

func TestTokenizedInputsAreNotDeclaredReviewedAsText(t *testing.T) {
	for _, body := range []string{`{"input":[123,456]}`, `{"input":[[123,456],[789]]}`} {
		if _, reason := Extract([]byte(body)); reason != "unsupported_content" {
			t.Fatal("token IDs cannot be reviewed without decoding their text")
		}
	}
}

func TestRuleThresholdIsTheOnlyClassificationBoundary(t *testing.T) {
	_, reason := Interpret([]byte(`{"answers":{"personal_data":{"type":"noul","noul":0.5},"prompt_injection":{"type":"noul","noul":0.01},"credential_leakage":{"type":"noul","noul":0.01}}}`), DefaultConfig().Rules)
	if reason != "" {
		t.Fatalf("valid below-threshold answers are not failed reviews: %s", reason)
	}
}

func TestReferencedToolCallIsIncludedEvenWhenNotAdjacent(t *testing.T) {
	var cache Cache
	now := time.Now()
	rules := DefaultConfig().Rules
	body := `{"input":[{"role":"user","content":"initial"},{"type":"function_call","call_id":"call_x","name":"read_config","arguments":"{}"},{"role":"assistant","content":"middle"},{"role":"assistant","content":"near-a"},{"role":"assistant","content":"near-b"}]}`
	answer(t, cache.Prepare(nil, "jev", document(t, body), rules, now), now)
	body = strings.Replace(body, `"near-b"}]`, `"near-b"},{"type":"function_call_output","call_id":"call_x","output":"result"}]`, 1)
	review := cache.Prepare(nil, "jev", document(t, body), rules, now)
	if !bytes.Contains(review.Payload, []byte("read_config")) || bytes.Contains(review.Payload, []byte("middle")) {
		t.Fatal("tool result lost its related call or retained unrelated history")
	}
}

func TestReviewReusesHistoryPerRuleAndIncludesNecessaryContext(t *testing.T) {
	var cache Cache
	now := time.Now()
	rules := DefaultConfig().Rules
	body := `{"system":"trusted","messages":[{"role":"user","content":"initial"},{"role":"assistant","content":"old-unneeded"},{"role":"user","content":"middle"},{"role":"assistant","content":"neighbor-a"},{"role":"user","content":"neighbor-b"}]}`
	first := cache.Prepare([]byte("key-1"), "jev", document(t, body), rules, now)
	if len(first.Rules) != len(rules) || first.Reason != "" {
		t.Fatal("rules were not combined into one review")
	}
	answer(t, first, now)
	body = strings.Replace(body, `"neighbor-b"}]`, `"neighbor-b"},{"role":"tool","content":"new-output"}]`, 1)
	second := cache.Prepare([]byte("key-1"), "jev", document(t, body), rules, now.Add(time.Minute))
	for _, expected := range []string{"trusted", "initial", "neighbor-a", "neighbor-b", "new-output"} {
		if !bytes.Contains(second.Payload, []byte(expected)) {
			t.Fatalf("missing required context %s", expected)
		}
	}
	if bytes.Contains(second.Payload, []byte("old-unneeded")) || bytes.Contains(second.Payload, []byte("middle")) {
		t.Fatal("unneeded reviewed history was sent again")
	}
	for _, check := range second.checks {
		if len(check.targets) != 1 {
			t.Fatalf("already reviewed targets repeated: %v", check.targets)
		}
	}
	// 条件变更只补审该规则；未审的远处内容不得被省略。
	rules[1].Instructions = "new policy"
	changed := cache.Prepare([]byte("key-1"), "jev", document(t, body), rules, now)
	if len(changed.checks[0].targets) != 1 || len(changed.checks[1].targets) != 7 || !bytes.Contains(changed.Payload, []byte("old-unneeded")) {
		t.Fatal("changed rule skipped old content")
	}
}

func TestGeminiIncrementalReviewKeepsImplicitInitialUserContext(t *testing.T) {
	for _, role := range []string{"", `"role":"",`, `"role":"user",`} {
		t.Run(role, func(t *testing.T) {
			var cache Cache
			now := time.Now()
			rules := DefaultConfig().Rules
			body := `{"contents":[{` + role + `"parts":[{"text":"initial-task"}]},{"role":"model","parts":[{"text":"old-unneeded"}]},{"role":"user","parts":[{"text":"middle"}]},{"role":"model","parts":[{"text":"neighbor-reply"}]},{"role":"user","parts":[{"text":"neighbor-task"}]}]}`
			initial := document(t, body)
			answer(t, cache.Prepare(nil, "jev", initial, rules, now), now)
			body = strings.Replace(body, `"neighbor-task"}]}]}`, `"neighbor-task"}]},{"role":"model","parts":[{"text":"latest-reply"}]},{"role":"user","parts":[{"text":"new-task"}]}]}`, 1)
			doc := document(t, body)
			if len(doc.Anchors) == 0 || doc.Anchors[0] != 0 {
				t.Fatalf("initial Gemini task was not anchored: %v", doc.Anchors)
			}
			review := cache.Prepare(nil, "jev", doc, rules, now.Add(time.Minute))
			for _, text := range []string{"initial-task", "neighbor-reply", "neighbor-task", "latest-reply", "new-task"} {
				if !bytes.Contains(review.Payload, []byte(text)) {
					t.Fatalf("incremental review omitted %s", text)
				}
			}
			if bytes.Contains(review.Payload, []byte("old-unneeded")) || bytes.Contains(review.Payload, []byte("middle")) {
				t.Fatal("incremental review repeated unrelated history")
			}
			for _, check := range review.checks {
				if len(check.targets) != 2 || check.targets[0] != 5 || check.targets[1] != 6 {
					t.Fatalf("cached history was re-reviewed: %v", check.targets)
				}
			}
		})
	}
}

func TestHistoryMutationsInvalidateAffectedSuffix(t *testing.T) {
	var cache Cache
	now := time.Now()
	original := `{"messages":[{"role":"user","content":"A"},{"role":"assistant","content":"B"},{"role":"user","content":"C"}]}`
	rules := DefaultConfig().Rules
	answer(t, cache.Prepare(nil, "jev", document(t, original), rules, now), now)
	for _, body := range []string{
		strings.Replace(original, `"B"`, `"changed"`, 1),
		strings.Replace(original, `{"role":"assistant","content":"B"},`, ``, 1),
		strings.Replace(original, `"assistant"`, `"tool"`, 1),
		`{"messages":[{"role":"user","content":"A"},{"role":"user","content":"C"},{"role":"assistant","content":"B"}]}`,
		strings.Replace(original, `"B"`, `"B"},{"role":"tool","content":"inserted"`, 1),
	} {
		review := cache.Prepare(nil, "jev", document(t, body), rules, now)
		if len(review.Payload) == 0 || len(review.checks[0].targets) == 0 || review.checks[0].targets[0] < 1 || review.checks[0].targets[0] > 2 {
			t.Fatalf("mutation reused an affected proof: %+v", review.checks)
		}
	}
	changed := cache.Prepare(nil, "jev", document(t, strings.Replace(original, `"A"`, `"new initial"`, 1)), rules, now)
	if len(changed.checks[0].targets) != 3 {
		t.Fatal("initial context change reused earlier proof")
	}
}

func TestCanonicalIdentityAndIsolation(t *testing.T) {
	var cache Cache
	now := time.Now()
	rules := DefaultConfig().Rules
	first := document(t, `{"model":"a","messages":[{"role":"user","content":"a  b","score":0.75}]}`)
	answer(t, cache.Prepare([]byte("key1/model1"), "jev", first, rules, now), now)
	reordered := document(t, `{"messages":[{"score":0.75,"content":"a  b","role":"user"}],"model":"b","stream":true,"generate":false,"stream_id":"prewarm","reasoning_effort":"high"}`)
	if first.Digest != reordered.Digest || len(cache.Prepare([]byte("key1/model1"), "jev", reordered, rules, now).Payload) != 0 {
		t.Fatal("object order or transport flags broke reuse")
	}
	if len(cache.Prepare([]byte("key2/model1"), "jev", first, rules, now).Payload) == 0 || len(cache.Prepare([]byte("key1/model2"), "jev", first, rules, now).Payload) == 0 {
		t.Fatal("cache crossed identity/model boundary")
	}
	for _, body := range []string{`{"input":"a","input":"b"}`, `{"input":"a"}{}`, `{"input":[{"type":"input_image","image_url":"https://example.com/x"}]}`} {
		if _, reason := Extract([]byte(body)); reason == "" {
			t.Fatal("ambiguous or unsupported content accepted")
		}
	}
}

func TestActionsAndAggregateHitsAreNotAssignedToIndividualMessages(t *testing.T) {
	var cache Cache
	now := time.Now()
	rules := DefaultConfig().Rules[:1]
	doc := document(t, `{"input":["first","second"]}`)
	first := cache.Prepare(nil, "jev", doc, rules, now)
	answer(t, first, now, .99)
	if first.Status != "warned" || len(first.Findings) != 1 {
		t.Fatal("warning not recorded")
	}
	rules[0].Action = ActionBlock
	rules[0].Name = "renamed"
	cached := cache.Prepare(nil, "jev", doc, rules, now)
	if len(cached.Payload) != 0 || cached.Status != "blocked" || cached.Findings[0].Name != "renamed" {
		t.Fatal("cached hit ignored current action")
	}
	removed := cache.Prepare(nil, "jev", document(t, `{"input":["first"]}`), rules, now)
	if len(removed.Payload) == 0 {
		t.Fatal("aggregate hit was assigned to a remaining individual message")
	}
}

func TestExpiryDoesNotSlideAndCacheIsBounded(t *testing.T) {
	var cache Cache
	now := time.Now()
	doc := document(t, `{"input":"hello"}`)
	rules := DefaultConfig().Rules
	answer(t, cache.Prepare(nil, "jev", doc, rules, now), now)
	if len(cache.Prepare(nil, "jev", doc, rules, now.Add(59*time.Minute)).Payload) != 0 || len(cache.Prepare(nil, "jev", doc, rules, now.Add(time.Hour)).Payload) == 0 {
		t.Fatal("fixed TTL was extended by reading")
	}
	for i := range cacheCapacity + 1 {
		encoded, _ := json.Marshal(i)
		cache.put(evidence{key: hashParts(encoded), expires: now.Add(time.Hour)})
	}
	if len(cache.entries) != cacheCapacity {
		t.Fatal("unbounded cache")
	}
}

func TestSingleReviewBudgetAndInvalidResultsNeverCreateProofs(t *testing.T) {
	var cache Cache
	now := time.Now()
	rules := DefaultConfig().Rules
	large := document(t, `{"input":"`+strings.Repeat("x", MaxStateBytes)+`"}`)
	if review := cache.Prepare(nil, "jev", large, rules, now); review.Reason != "content_too_large" || len(review.Payload) != 0 {
		t.Fatal("oversized content was truncated or split")
	}
	doc := document(t, `{"input":"hello"}`)
	for _, body := range []string{`{"answers":{}}`, `{"answers":{"personal_data":{"type":"noul","noul":2},"prompt_injection":{"type":"noul","noul":0},"credential_leakage":{"type":"noul","noul":0}}}`, `{"answers":{"personal_data":{"type":"noul","noul":0},"personal_data":{"type":"noul","noul":1},"prompt_injection":{"type":"noul","noul":0},"credential_leakage":{"type":"noul","noul":0}}}`} {
		review := cache.Prepare(nil, "jev", doc, rules, now)
		if reason := review.Resolve([]byte(body), now); reason != "invalid_response" || len(cache.entries) != 0 {
			t.Fatal("invalid response produced cached proof")
		}
	}
}

func TestConfigValidatesRuleActionsAndUpgradesLegacyModes(t *testing.T) {
	for _, raw := range []string{`{"mode":"silent"}`, `{"enabled":true,"rules":[]}`, `{"rules":[{"id":"x","name":"x","instructions":"x","action":"redact"}]}`, `{"rules":[{"id":"x","name":"x","instructions":"x","threshold":0}]}`} {
		if _, err := Decode([]byte(raw)); err == nil {
			t.Fatalf("accepted invalid config %s", raw)
		}
	}
	cfg, err := Decode([]byte(`{"enabled":true,"mode":"observe","local_secrets":true,"semantic_enabled":true}`))
	if err != nil || cfg.Rules[0].Action != ActionWarn {
		t.Fatalf("legacy experimental settings not upgraded: %v", err)
	}
}

func TestEarlierExperimentalLogsKeepOnlyActualMatches(t *testing.T) {
	var result Result
	if err := json.Unmarshal([]byte(`{"mode":"observe","status":"matched","findings":[{"rule_id":"one","name":"One","status":"passed"},{"rule_id":"two","name":"Two","status":"matched"}],"calls":[]}`), &result); err != nil {
		t.Fatal(err)
	}
	if result.Status != "warned" || len(result.Findings) != 1 || result.Findings[0].Action != ActionWarn {
		t.Fatal("older log lost its action or displayed a non-matching rule")
	}
}
