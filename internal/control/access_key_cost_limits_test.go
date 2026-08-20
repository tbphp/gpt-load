package control

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"testing"

	"github.com/gin-gonic/gin"

	"gpt-load/internal/platform/config"
	"gpt-load/internal/storage/models"
)

func TestAccessKeyCostLimitRulesCreateAndReconcileDesiredList(t *testing.T) {
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

func TestAccessKeyCostLimitRulesAreValidatedAndIdempotent(t *testing.T) {
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
