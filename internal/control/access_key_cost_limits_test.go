package control

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"gpt-load/internal/accessquota"
	"gpt-load/internal/platform/config"
	"gpt-load/internal/storage/models"
)

func TestAccessKeyCostLimitRulesCreateAndReconcileDesiredList(t *testing.T) {
	t.Parallel()
	fixture, engine := newAccessKeyCostLimitHTTPFixture(t)
	created := serveAccessKeyCostLimitRequest(t, engine, http.MethodPost, "/api/access-keys", `{
		"name":"limited",
		"cost_limit_rules":[
			{"kind":"total","limit_usd":"100"},
			{"kind":"periodic","limit_usd":"20","period_seconds":18000},
			{"kind":"periodic","limit_usd":"30","period_seconds":86400}
		]
	}`, "00000000-0000-4000-8000-000000009001")
	if created.Code != http.StatusOK {
		t.Fatalf("POST = %d %s, want 200", created.Code, created.Body.String())
	}

	var createdEnvelope struct {
		Data AccessKeyCreateResult `json:"data"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &createdEnvelope); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	if len(createdEnvelope.Data.CostLimitRules) != 3 {
		t.Fatalf("created rules = %#v", createdEnvelope.Data.CostLimitRules)
	}
	rules := loadAccessKeyCostLimitRules(t, fixture, createdEnvelope.Data.ID)
	if len(rules) != 3 {
		t.Fatalf("stored rules = %#v", rules)
	}
	var stateCount int64
	if err := fixture.db.Model(&models.AccessKeyCostLimitState{}).
		Where("rule_id IN ?", []uint{rules[0].ID, rules[1].ID, rules[2].ID}).
		Count(&stateCount).Error; err != nil {
		t.Fatalf("count cost limit states: %v", err)
	}
	if stateCount != 3 {
		t.Fatalf("state count = %d, want 3", stateCount)
	}

	periodic := make([]models.AccessKeyCostLimitRule, 0, 2)
	for _, rule := range rules {
		if rule.Kind == models.AccessKeyCostLimitKindPeriodic {
			periodic = append(periodic, rule)
		}
	}
	sort.Slice(periodic, func(i, j int) bool { return periodic[i].PeriodSeconds < periodic[j].PeriodSeconds })
	if err := fixture.db.Model(&models.AccessKeyCostLimitState{}).
		Where("rule_id = ?", periodic[0].ID).
		Updates(map[string]any{
			"used_nano_usd":        int64(15_000_000_000),
			"window_started_at_ms": int64(1_787_184_000_000),
			"window_ends_at_ms":    int64(1_787_202_000_000),
			"window_generation":    uint64(2),
			"snapshot_version":     uint64(4),
		}).Error; err != nil {
		t.Fatalf("seed periodic state: %v", err)
	}

	path := fmt.Sprintf("/api/access-keys/%d", createdEnvelope.Data.ID)
	updated := serveAccessKeyCostLimitRequest(t, engine, http.MethodPut, path, fmt.Sprintf(`{
		"cost_limit_rules":[
			{"id":%d,"kind":"total","limit_usd":"120"},
			{"id":%d,"kind":"periodic","limit_usd":"25","period_seconds":18000},
			{"kind":"periodic","limit_usd":"40","period_seconds":36000}
		]
	}`, rules[0].ID, periodic[0].ID), "")
	if updated.Code != http.StatusOK {
		t.Fatalf("PUT amount/delete/add = %d %s, want 200", updated.Code, updated.Body.String())
	}
	current := loadAccessKeyCostLimitRules(t, fixture, createdEnvelope.Data.ID)
	if len(current) != 3 {
		t.Fatalf("updated stored rules = %#v", current)
	}
	var preserved models.AccessKeyCostLimitState
	if err := fixture.db.First(&preserved, periodic[0].ID).Error; err != nil {
		t.Fatalf("load preserved state: %v", err)
	}
	if preserved.UsedNanoUSD != 15_000_000_000 || preserved.RuleRevision != periodic[0].RuleRevision ||
		preserved.WindowGeneration != 2 {
		t.Fatalf("amount update reset state = %#v", preserved)
	}
	var deletedCount int64
	if err := fixture.db.Model(&models.AccessKeyCostLimitRule{}).
		Where("id = ?", periodic[1].ID).Count(&deletedCount).Error; err != nil {
		t.Fatalf("count deleted rule: %v", err)
	}
	if deletedCount != 0 {
		t.Fatalf("removed desired-list rule still exists")
	}

	changedDuration := serveAccessKeyCostLimitRequest(t, engine, http.MethodPut, path, fmt.Sprintf(`{
		"cost_limit_rules":[
			{"id":%d,"kind":"total","limit_usd":"120"},
			{"id":%d,"kind":"periodic","limit_usd":"25","period_seconds":21600},
			{"id":%d,"kind":"periodic","limit_usd":"40","period_seconds":36000}
		]
	}`, current[0].ID, periodic[0].ID, current[2].ID), "")
	if changedDuration.Code != http.StatusOK {
		t.Fatalf("PUT duration = %d %s, want 200", changedDuration.Code, changedDuration.Body.String())
	}
	var reset models.AccessKeyCostLimitState
	if err := fixture.db.First(&reset, periodic[0].ID).Error; err != nil {
		t.Fatalf("load reset state: %v", err)
	}
	if reset.RuleRevision != periodic[0].RuleRevision+1 || reset.UsedNanoUSD != 0 ||
		reset.WindowStartedAtMS != nil || reset.WindowEndsAtMS != nil || reset.WindowGeneration != 0 {
		t.Fatalf("duration update state = %#v, want reset next revision", reset)
	}
}

func TestAccessKeyCostLimitRulesResetOnlySelectedRules(t *testing.T) {
	t.Parallel()
	fixture, engine := newAccessKeyCostLimitHTTPFixture(t)
	created := serveAccessKeyCostLimitRequest(t, engine, http.MethodPost, "/api/access-keys", `{
		"name":"resettable",
		"cost_limit_rules":[
			{"kind":"total","limit_usd":"100"},
			{"kind":"periodic","limit_usd":"20","period_seconds":18000},
			{"kind":"periodic","limit_usd":"30","period_seconds":86400}
		]
	}`, "00000000-0000-4000-8000-000000009004")
	if created.Code != http.StatusOK {
		t.Fatalf("POST = %d %s, want 200", created.Code, created.Body.String())
	}
	var envelope struct {
		Data AccessKeyCreateResult `json:"data"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	rules := envelope.Data.CostLimitRules
	if len(rules) != 3 {
		t.Fatalf("created rules = %#v", rules)
	}

	now := time.Unix(1_787_184_000, 0).UTC()
	ticket, decision := fixture.accessQuota.Admit(envelope.Data.ID, now)
	if !decision.Allowed {
		t.Fatalf("Admit() = %#v", decision)
	}
	fixture.accessQuota.Complete(ticket, 15_000_000_000)

	response := serveAccessKeyCostLimitRequest(
		t,
		engine,
		http.MethodPost,
		fmt.Sprintf("/api/access-keys/%d/cost-limits/reset", envelope.Data.ID),
		fmt.Sprintf(`{"rule_ids":[%d,%d]}`, rules[0].ID, rules[1].ID),
		"",
	)
	if response.Code != http.StatusOK {
		t.Fatalf("POST reset = %d %s, want 200", response.Code, response.Body.String())
	}

	view := fixture.accessQuota.Snapshot(envelope.Data.ID, now.Add(time.Minute))
	if len(view.Rules) != 3 {
		t.Fatalf("runtime rules = %#v", view.Rules)
	}
	if view.Rules[0].ID != rules[0].ID || view.Rules[0].UsedNanoUSD != 0 ||
		view.Rules[0].Status != accessquota.RuleStatusAvailable {
		t.Fatalf("reset total runtime = %#v", view.Rules[0])
	}
	if view.Rules[1].ID != rules[1].ID || view.Rules[1].UsedNanoUSD != 0 ||
		view.Rules[1].Status != accessquota.RuleStatusInactive ||
		view.Rules[1].WindowStartedAtMS != nil || view.Rules[1].WindowEndsAtMS != nil {
		t.Fatalf("reset periodic runtime = %#v", view.Rules[1])
	}
	if view.Rules[2].ID != rules[2].ID || view.Rules[2].UsedNanoUSD != 15_000_000_000 ||
		view.Rules[2].Status != accessquota.RuleStatusAvailable ||
		view.Rules[2].WindowStartedAtMS == nil || view.Rules[2].WindowEndsAtMS == nil {
		t.Fatalf("unselected periodic runtime = %#v", view.Rules[2])
	}

	stored := loadAccessKeyCostLimitRules(t, fixture, envelope.Data.ID)
	for index, rule := range stored {
		wantRevision := uint64(1)
		if index < 2 {
			wantRevision = 2
		}
		if rule.RuleRevision != wantRevision {
			t.Fatalf("rule %d revision = %d, want %d", rule.ID, rule.RuleRevision, wantRevision)
		}
		var state models.AccessKeyCostLimitState
		if err := fixture.db.First(&state, rule.ID).Error; err != nil {
			t.Fatal(err)
		}
		if index < 2 && (state.RuleRevision != 2 || state.UsedNanoUSD != 0 ||
			state.WindowStartedAtMS != nil || state.WindowEndsAtMS != nil ||
			state.WindowGeneration != 0 || state.SnapshotVersion != 1) {
			t.Fatalf("reset state for rule %d = %#v", rule.ID, state)
		}
	}
}

func TestAccessKeyCostLimitRulesResetRejectsInvalidSelection(t *testing.T) {
	t.Parallel()
	_, engine := newAccessKeyCostLimitHTTPFixture(t)
	created := serveAccessKeyCostLimitRequest(
		t,
		engine,
		http.MethodPost,
		"/api/access-keys",
		`{"name":"limited","cost_limit_rules":[{"kind":"total","limit_usd":"10"}]}`,
		"00000000-0000-4000-8000-000000009005",
	)
	if created.Code != http.StatusOK {
		t.Fatalf("POST = %d %s, want 200", created.Code, created.Body.String())
	}
	var envelope struct {
		Data AccessKeyCreateResult `json:"data"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}

	for _, body := range []string{`{"rule_ids":[]}`, `{"rule_ids":[1,1]}`, `{"rule_ids":[999999]}`} {
		response := serveAccessKeyCostLimitRequest(
			t,
			engine,
			http.MethodPost,
			fmt.Sprintf("/api/access-keys/%d/cost-limits/reset", envelope.Data.ID),
			body,
			"",
		)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("POST reset %s = %d %s, want 400", body, response.Code, response.Body.String())
		}
	}
}

func TestAccessKeyCostLimitRulesAreValidatedAndIdempotent(t *testing.T) {
	t.Parallel()
	fixture, engine := newAccessKeyCostLimitHTTPFixture(t)
	const idempotencyKey = "00000000-0000-4000-8000-000000009002"
	payload := `{"name":"idempotent","cost_limit_rules":[{"kind":"total","limit_usd":"10"}]}`
	first := serveAccessKeyCostLimitRequest(t, engine, http.MethodPost, "/api/access-keys", payload, idempotencyKey)
	second := serveAccessKeyCostLimitRequest(t, engine, http.MethodPost, "/api/access-keys", payload, idempotencyKey)
	if first.Code != http.StatusOK || second.Code != http.StatusOK {
		t.Fatalf("idempotent POST = %d/%d, bodies=%s / %s", first.Code, second.Code, first.Body.String(), second.Body.String())
	}
	var keyCount, ruleCount, stateCount int64
	if err := fixture.db.Model(&models.AccessKey{}).Count(&keyCount).Error; err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.Model(&models.AccessKeyCostLimitRule{}).Count(&ruleCount).Error; err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.Model(&models.AccessKeyCostLimitState{}).Count(&stateCount).Error; err != nil {
		t.Fatal(err)
	}
	if keyCount != 1 || ruleCount != 1 || stateCount != 1 {
		t.Fatalf("idempotent counts = key:%d rule:%d state:%d", keyCount, ruleCount, stateCount)
	}

	invalid := []string{
		`{"name":"zero","cost_limit_rules":[{"kind":"total","limit_usd":"0"}]}`,
		`{"name":"duplicate","cost_limit_rules":[{"kind":"periodic","limit_usd":"1","period_seconds":300},{"kind":"periodic","limit_usd":"2","period_seconds":300}]}`,
		`{"name":"null","cost_limit_rules":null}`,
	}
	for index, body := range invalid {
		response := serveAccessKeyCostLimitRequest(
			t, engine, http.MethodPost, "/api/access-keys", body,
			fmt.Sprintf("00000000-0000-4000-8000-%012d", 9100+index),
		)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("invalid payload %d = %d %s, want 400", index, response.Code, response.Body.String())
		}
	}
}

func TestAccessKeyCostLimitRuleKindCannotBeChangedInPlace(t *testing.T) {
	t.Parallel()
	fixture, engine := newAccessKeyCostLimitHTTPFixture(t)
	created := serveAccessKeyCostLimitRequest(
		t,
		engine,
		http.MethodPost,
		"/api/access-keys",
		`{"name":"fixed-kind","cost_limit_rules":[{"kind":"total","limit_usd":"10"}]}`,
		"00000000-0000-4000-8000-000000009003",
	)
	if created.Code != http.StatusOK {
		t.Fatalf("POST = %d %s, want 200", created.Code, created.Body.String())
	}
	var envelope struct {
		Data AccessKeyCreateResult `json:"data"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	rule := envelope.Data.CostLimitRules[0]
	updated := serveAccessKeyCostLimitRequest(
		t,
		engine,
		http.MethodPut,
		fmt.Sprintf("/api/access-keys/%d", envelope.Data.ID),
		fmt.Sprintf(
			`{"cost_limit_rules":[{"id":%d,"kind":"periodic","limit_usd":"10","period_seconds":300}]}`,
			rule.ID,
		),
		"",
	)
	if updated.Code != http.StatusBadRequest {
		t.Fatalf("PUT kind change = %d %s, want 400", updated.Code, updated.Body.String())
	}
	stored := loadAccessKeyCostLimitRules(t, fixture, envelope.Data.ID)
	if len(stored) != 1 || stored[0].ID != rule.ID || stored[0].Kind != models.AccessKeyCostLimitKindTotal ||
		stored[0].RuleRevision != 1 {
		t.Fatalf("stored rules after rejected kind change = %#v", stored)
	}
}

func TestAccessKeyCostLimitRulesAllowRetainedPeriodPermutations(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name    string
		initial []int64
		desired []int64
	}{
		{name: "two rule swap", initial: []int64{300, 600}, desired: []int64{600, 300}},
		{name: "three rule rotation", initial: []int64{300, 600, 900}, desired: []int64{600, 900, 300}},
	} {
		t.Run(test.name, func(t *testing.T) {
			assertAccessKeyCostLimitPeriodPermutation(
				t,
				newServiceFixture(t),
				test.initial,
				test.desired,
			)
		})
	}
}

func assertAccessKeyCostLimitPeriodPermutation(
	t *testing.T,
	fixture serviceFixture,
	initial []int64,
	desired []int64,
) {
	t.Helper()
	if len(initial) == 0 || len(initial) != len(desired) {
		t.Fatalf("invalid permutation fixture: initial=%v desired=%v", initial, desired)
	}
	definitions := make([]AccessKeyCostLimitRuleRequest, 0, len(initial))
	for _, period := range initial {
		definitions = append(definitions, AccessKeyCostLimitRuleRequest{
			Kind: accessquota.KindPeriodic, LimitUSD: "10", PeriodSeconds: period,
		})
	}
	created, err := fixture.service.CreateAccessKey(t.Context(), AccessKeyCreateRequest{
		Name: "period-permutation",
		CostLimitRules: OptionalAccessKeyCostLimitRules{
			Set: true, Values: definitions,
		},
	})
	if err != nil {
		t.Fatalf("CreateAccessKey() error = %v", err)
	}
	current := loadAccessKeyCostLimitRules(t, fixture, created.ID)
	if len(current) != len(initial) {
		t.Fatalf("created rules = %#v, want %d", current, len(initial))
	}
	for index, rule := range current {
		windowStart := int64(1_000)
		windowEnd := windowStart + rule.PeriodSeconds*1_000
		if err := fixture.db.Model(&models.AccessKeyCostLimitState{}).
			Where("rule_id = ?", rule.ID).
			Updates(map[string]any{
				"used_nano_usd":        int64(index + 1),
				"window_started_at_ms": windowStart,
				"window_ends_at_ms":    windowEnd,
				"window_generation":    uint64(2),
				"snapshot_version":     uint64(3),
			}).Error; err != nil {
			t.Fatalf("seed rule %d state: %v", rule.ID, err)
		}
	}

	desiredRules := make([]AccessKeyCostLimitRuleRequest, 0, len(desired))
	for index, period := range desired {
		desiredRules = append(desiredRules, AccessKeyCostLimitRuleRequest{
			ID: current[index].ID, Kind: accessquota.KindPeriodic,
			LimitUSD: "10", PeriodSeconds: period,
		})
	}
	if _, err := fixture.service.UpdateAccessKey(t.Context(), created.ID, AccessKeyUpdateRequest{
		CostLimitRules: OptionalAccessKeyCostLimitRules{Set: true, Values: desiredRules},
	}); err != nil {
		t.Fatalf("UpdateAccessKey(period permutation) error = %v", err)
	}

	var finalRules []models.AccessKeyCostLimitRule
	if err := fixture.db.Where("access_key_id = ?", created.ID).Order("id ASC").Find(&finalRules).Error; err != nil {
		t.Fatal(err)
	}
	finalByID := make(map[uint]models.AccessKeyCostLimitRule, len(finalRules))
	for _, rule := range finalRules {
		finalByID[rule.ID] = rule
	}
	for index, original := range current {
		final, exists := finalByID[original.ID]
		if !exists || final.PeriodSeconds != desired[index] ||
			final.RuleRevision != original.RuleRevision+1 {
			t.Fatalf(
				"final rule %d = %#v, want period %d revision %d",
				original.ID,
				final,
				desired[index],
				original.RuleRevision+1,
			)
		}
		var state models.AccessKeyCostLimitState
		if err := fixture.db.First(&state, original.ID).Error; err != nil {
			t.Fatal(err)
		}
		if state.RuleRevision != original.RuleRevision+1 || state.UsedNanoUSD != 0 ||
			state.WindowStartedAtMS != nil || state.WindowEndsAtMS != nil ||
			state.WindowGeneration != 0 || state.SnapshotVersion != 1 {
			t.Fatalf("reset state for rule %d = %#v", original.ID, state)
		}
	}
}

func newAccessKeyCostLimitHTTPFixture(t *testing.T) (serviceFixture, *gin.Engine) {
	t.Helper()
	initControlI18n(t)
	fixture := newServiceFixture(t)
	fixture.service.random = bytes.NewReader(bytes.Repeat([]byte{0x11}, 64))
	engine := gin.New()
	NewServer(&config.Config{AuthKey: "test-auth-key"}, fixture.service).RegisterRoutes(engine)
	return fixture, engine
}

func serveAccessKeyCostLimitRequest(
	t *testing.T,
	engine *gin.Engine,
	method, path, payload, idempotencyKey string,
) *httptest.ResponseRecorder {
	t.Helper()
	response := httptest.NewRecorder()
	request := httptest.NewRequest(method, path, bytes.NewBufferString(payload))
	request.Header.Set("Authorization", "Bearer test-auth-key")
	request.Header.Set("Content-Type", "application/json")
	if idempotencyKey != "" {
		request.Header.Set("Idempotency-Key", idempotencyKey)
	}
	engine.ServeHTTP(response, request)
	return response
}

func loadAccessKeyCostLimitRules(
	t *testing.T,
	fixture serviceFixture,
	accessKeyID uint,
) []models.AccessKeyCostLimitRule {
	t.Helper()
	var rules []models.AccessKeyCostLimitRule
	if err := fixture.db.Where("access_key_id = ?", accessKeyID).
		Order("CASE WHEN kind = 'total' THEN 0 ELSE 1 END ASC, period_seconds ASC, id ASC").Find(&rules).Error; err != nil {
		t.Fatalf("load cost limit rules: %v", err)
	}
	return rules
}
