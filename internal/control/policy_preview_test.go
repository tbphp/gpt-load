package control

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"gpt-load/internal/channel"
	"gpt-load/internal/storage/models"
	po "gpt-load/internal/subscription/providers/observation"
)

type policyPreviewEnvelope struct {
	Code    int                   `json:"code"`
	Message string                `json:"message"`
	Data    PolicyPreviewResponse `json:"data"`
}

// heavyAggregatePolicyConfig builds the 30-leaf x 100-rule scheduling config whose
// aggregate condition-node count (3100) exceeds the preview budget.
func heavyAggregatePolicyConfig() string {
	leaves := make([]string, 30)
	for i := range leaves {
		leaves[i] = `{"fact":"request.model","op":"eq","value":"gpt-4o"}`
	}
	rules := make([]string, 100)
	for i := range rules {
		rules[i] = fmt.Sprintf(`{"id":"r%d","name":"R","domain":"scheduling","enabled":false,"when":{"all":[%s]},"then":{"type":"exclude_candidate"}}`, i, strings.Join(leaves, ","))
	}
	return fmt.Sprintf(`{"schema_version":1,"rules":[%s]}`, strings.Join(rules, ","))
}

func createTestGroupWithTwoModels(t *testing.T, fixture serviceFixture) (uint, uint) {
	t.Helper()
	seq := testIdempotencySequence.Add(1)
	name := fmt.Sprintf("preview-grp-%d", seq)
	baseURL := fmt.Sprintf("https://api-prev-%d.example.com/v1", seq)
	result, err := fixture.service.CreateGroup(t.Context(), GroupCreateRequest{
		Name:      &name,
		ChannelID: channel.OpenAICompatible,
		Params:    json.RawMessage(fmt.Sprintf(`{"base_url":%q}`, baseURL)),
		Models: optionalGroupModels{
			Set: true,
			Values: []GroupModel{
				{ID: "upstream-gpt4", Alias: "gpt-4o", AliasEnabled: true},
				{ID: "upstream-backup", Alias: "gpt-4o", AliasEnabled: true}, // 同一 client model 的 alternate target
			},
		},
		Credentials:    "sk-test-key-1\nsk-test-key-2",
		ConnectionType: "api_key",
	})
	if err != nil {
		t.Fatalf("CreateGroup failed: %v", err)
	}

	var cred models.Credential
	if err := fixture.db.Where("group_id = ?", result.GroupID).First(&cred).Error; err != nil {
		t.Fatalf("query credential failed: %v", err)
	}

	return result.GroupID, cred.ID
}

func TestPolicyPreview_ScopedEndpointsAndAuth(t *testing.T) {
	fixture := newServiceFixture(t)
	server := setupPolicyTestServer(t, fixture)
	groupID, credID := createTestGroupWithTwoModels(t, fixture)

	// 1. 未授权访问拒绝
	reqUnauth := httptest.NewRequest(
		http.MethodPost,
		fmt.Sprintf("/api/groups/%d/policy/preview", groupID),
		bytes.NewBufferString(`{"request_model":"gpt-4o"}`),
	)
	recUnauth := httptest.NewRecorder()
	server.ServeHTTP(recUnauth, reqUnauth)
	if recUnauth.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 Unauthorized, got %d", recUnauth.Code)
	}

	// 2. 缺失 request_model 返回 400
	reqNoModel := httptest.NewRequest(
		http.MethodPost,
		fmt.Sprintf("/api/groups/%d/policy/preview", groupID),
		bytes.NewBufferString(`{}`),
	)
	reqNoModel.Header.Set("Authorization", "Bearer admin-secret-key")
	reqNoModel.Header.Set("Content-Type", "application/json")
	recNoModel := httptest.NewRecorder()
	server.ServeHTTP(recNoModel, reqNoModel)
	if recNoModel.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 BadRequest for missing request_model, got %d: %s", recNoModel.Code, recNoModel.Body.String())
	}

	// 3. 不存在的 group_id 返回 404
	reqNonExistGroup := httptest.NewRequest(
		http.MethodPost,
		"/api/groups/999999/policy/preview",
		bytes.NewBufferString(`{"request_model":"gpt-4o"}`),
	)
	reqNonExistGroup.Header.Set("Authorization", "Bearer admin-secret-key")
	reqNonExistGroup.Header.Set("Content-Type", "application/json")
	recNonExistGroup := httptest.NewRecorder()
	server.ServeHTTP(recNonExistGroup, reqNonExistGroup)
	if recNonExistGroup.Code != http.StatusNotFound {
		t.Fatalf("expected 404 NotFound, got %d", recNonExistGroup.Code)
	}

	// 4. 凭据 preview：跨分组凭据返回 404
	otherGroupID, _ := createTestGroupWithTwoModels(t, fixture)
	reqCrossGroup := httptest.NewRequest(
		http.MethodPost,
		fmt.Sprintf("/api/groups/%d/credentials/%d/policy/preview", otherGroupID, credID),
		bytes.NewBufferString(`{"request_model":"gpt-4o"}`),
	)
	reqCrossGroup.Header.Set("Authorization", "Bearer admin-secret-key")
	reqCrossGroup.Header.Set("Content-Type", "application/json")
	recCrossGroup := httptest.NewRecorder()
	server.ServeHTTP(recCrossGroup, reqCrossGroup)
	if recCrossGroup.Code != http.StatusNotFound {
		t.Fatalf("expected 404 NotFound for credential belonging to other group, got %d", recCrossGroup.Code)
	}
}

func TestPolicyPreview_GroupAndCredentialInheritanceAndProvenance(t *testing.T) {
	fixture := newServiceFixture(t)
	server := setupPolicyTestServer(t, fixture)
	groupID, credID := createTestGroupWithTwoModels(t, fixture)

	// 1. 先保存已发布的分组策略：倍率 x2
	saveGroupReq := httptest.NewRequest(
		http.MethodPut,
		fmt.Sprintf("/api/groups/%d/policy", groupID),
		bytes.NewBufferString(`{
			"expected_revision": "0",
			"config": {
				"schema_version": 1,
				"rules": [
					{
						"id": "group-pricing",
						"name": "Group Pricing",
						"domain": "pricing",
						"enabled": true,
						"when": {
							"fact": "request.model",
							"op": "eq",
							"value": "gpt-4o"
						},
						"then": {
							"type": "multiply_price",
							"factor": "2"
						}
					}
				]
			}
		}`),
	)
	saveGroupReq.Header.Set("Authorization", "Bearer admin-secret-key")
	saveGroupReq.Header.Set("Content-Type", "application/json")
	recSaveGroup := httptest.NewRecorder()
	server.ServeHTTP(recSaveGroup, saveGroupReq)
	if recSaveGroup.Code != http.StatusOK {
		t.Fatalf("save group policy failed: %d %s", recSaveGroup.Code, recSaveGroup.Body.String())
	}

	var groupBinding PolicyBindingResponse
	var groupEnv policyResponseEnvelope
	_ = json.Unmarshal(recSaveGroup.Body.Bytes(), &groupEnv)
	groupBinding = groupEnv.Data
	if groupBinding.RevisionText != "1" {
		t.Fatalf("expected revision_text '1', got %q", groupBinding.RevisionText)
	}

	// 2. 预览凭据草稿策略：倍率 x3
	// 预期结果：凭据草稿为 override，仅凭据 draft 规则生效（revision_text="draft", provenance="draft"），
	// 分组 saved 规则被抑制，总倍率应为 3
	previewCredReq := httptest.NewRequest(
		http.MethodPost,
		fmt.Sprintf("/api/groups/%d/credentials/%d/policy/preview", groupID, credID),
		bytes.NewBufferString(`{
			"request_model": "gpt-4o",
			"config": {
				"schema_version": 1,
				"group_policy": "override",
				"rules": [
					{
						"id": "cred-pricing",
						"name": "Cred Pricing",
						"domain": "pricing",
						"enabled": true,
						"when": {
							"fact": "upstream.model",
							"op": "eq",
							"value": "upstream-gpt4"
						},
						"then": {
							"type": "multiply_price",
							"factor": "3"
						}
					}
				]
			}
		}`),
	)
	previewCredReq.Header.Set("Authorization", "Bearer admin-secret-key")
	previewCredReq.Header.Set("Content-Type", "application/json")
	recPreviewCred := httptest.NewRecorder()
	server.ServeHTTP(recPreviewCred, previewCredReq)
	if recPreviewCred.Code != http.StatusOK {
		t.Fatalf("preview cred policy failed: %d %s", recPreviewCred.Code, recPreviewCred.Body.String())
	}

	var previewEnv policyPreviewEnvelope
	if err := json.Unmarshal(recPreviewCred.Body.Bytes(), &previewEnv); err != nil {
		t.Fatalf("unmarshal preview response failed: %v", err)
	}
	res := previewEnv.Data

	if len(res.Candidates) != 1 {
		t.Fatalf("expected 1 candidate, got %d", len(res.Candidates))
	}
	cand := res.Candidates[0]
	if cand.CredentialID != credID {
		t.Fatalf("expected candidate credential_id %d, got %d", credID, cand.CredentialID)
	}

	// 应解析出两个 alternate targets ("upstream-gpt4" 和 "upstream-backup")
	if len(cand.Targets) != 2 {
		t.Fatalf("expected 2 alternate targets, got %d", len(cand.Targets))
	}

	// 查找 upstream-gpt4 目标
	var gpt4Target *PolicyPreviewTargetResponse
	for i := range cand.Targets {
		if cand.Targets[i].UpstreamModel == "upstream-gpt4" {
			gpt4Target = &cand.Targets[i]
			break
		}
	}
	if gpt4Target == nil {
		t.Fatalf("expected target 'upstream-gpt4' in results")
	}

	// override：仅凭据草稿生效，分组 saved 规则被抑制
	if len(gpt4Target.GroupRules) != 0 {
		t.Fatalf("override must not surface group rules, got %d", len(gpt4Target.GroupRules))
	}

	if len(gpt4Target.CredentialRules) != 1 {
		t.Fatalf("expected 1 credential rule, got %d", len(gpt4Target.CredentialRules))
	}
	credRule := gpt4Target.CredentialRules[0]
	if credRule.Provenance != "draft" || credRule.RevisionText != "draft" {
		t.Errorf("expected cred rule provenance='draft', revision_text='draft', got prov=%q, rev=%q", credRule.Provenance, credRule.RevisionText)
	}

	// 累计倍率只取唯一边界（凭据草稿 x3）
	if gpt4Target.Pricing.CumulativeMultiplier != "3" {
		t.Errorf("expected cumulative multiplier 3, got %q", gpt4Target.Pricing.CumulativeMultiplier)
	}
	if len(gpt4Target.Pricing.Matches) != 1 || gpt4Target.Pricing.Matches[0].BindingScope != "credential" {
		t.Fatalf("expected single credential pricing match, got %+v", gpt4Target.Pricing.Matches)
	}
}

func TestPolicyPreview_RealClockQuotaVsSimulatedTimeWindow(t *testing.T) {
	fixture := newServiceFixture(t)
	server := setupPolicyTestServer(t, fixture)
	groupID, credID := createTestGroupWithTwoModels(t, fixture)

	// 配置一个依赖周一工作时间段与额度余量 < 10% 的调度排除规则
	draftConfig := `{
		"schema_version": 1,
		"group_policy": "override",
		"rules": [
			{
				"id": "time-window-rule",
				"name": "Time Window Exclude",
				"domain": "scheduling",
				"enabled": true,
				"when": {
					"predicate": "time_window",
					"weekdays": [1],
					"ranges": [["09:00", "12:00"]]
				},
				"then": {"type": "exclude_candidate"}
			}
		]
	}`

	// 获取服务真实时区，并在此时区下构造周一 10:00（命中）与周一 14:00（未命中）
	loc := fixture.service.now().Location()
	simTimeHit := time.Date(2026, 10, 5, 10, 0, 0, 0, loc).Format(time.RFC3339)
	previewHitReq := httptest.NewRequest(
		http.MethodPost,
		fmt.Sprintf("/api/groups/%d/credentials/%d/policy/preview", groupID, credID),
		bytes.NewBufferString(fmt.Sprintf(`{
			"request_model": "gpt-4o",
			"simulated_time": %q,
			"config": %s
		}`, simTimeHit, draftConfig)),
	)
	previewHitReq.Header.Set("Authorization", "Bearer admin-secret-key")
	previewHitReq.Header.Set("Content-Type", "application/json")
	recHit := httptest.NewRecorder()
	server.ServeHTTP(recHit, previewHitReq)
	if recHit.Code != http.StatusOK {
		t.Fatalf("preview with sim time hit failed: %d %s", recHit.Code, recHit.Body.String())
	}

	var envHit policyPreviewEnvelope
	_ = json.Unmarshal(recHit.Body.Bytes(), &envHit)
	candHit := envHit.Data.Candidates[0]
	if !candHit.Targets[0].Scheduling.Excluded {
		t.Fatalf("expected candidate excluded during simulated time window (Monday 10:00)")
	}

	// 模拟时间设为周一 14:00 (未命中时间段)
	simTimeMiss := time.Date(2026, 10, 5, 14, 0, 0, 0, loc).Format(time.RFC3339)
	previewMissReq := httptest.NewRequest(
		http.MethodPost,
		fmt.Sprintf("/api/groups/%d/credentials/%d/policy/preview", groupID, credID),
		bytes.NewBufferString(fmt.Sprintf(`{
			"request_model": "gpt-4o",
			"simulated_time": %q,
			"config": %s
		}`, simTimeMiss, draftConfig)),
	)
	previewMissReq.Header.Set("Authorization", "Bearer admin-secret-key")
	previewMissReq.Header.Set("Content-Type", "application/json")
	recMiss := httptest.NewRecorder()
	server.ServeHTTP(recMiss, previewMissReq)
	if recMiss.Code != http.StatusOK {
		t.Fatalf("preview with sim time miss failed: %d %s", recMiss.Code, recMiss.Body.String())
	}

	var envMiss policyPreviewEnvelope
	_ = json.Unmarshal(recMiss.Body.Bytes(), &envMiss)
	candMiss := envMiss.Data.Candidates[0]
	if candMiss.Targets[0].Scheduling.Excluded {
		t.Fatalf("expected candidate NOT excluded outside simulated time window (Monday 14:00)")
	}

	// 验证额度判断在模拟未来/过去时间下依然以服务器真实时间 realNow 求值
	quotaDraft := `{
		"schema_version": 1,
		"group_policy": "override",
		"rules": [
			{
				"id": "quota-rule",
				"name": "Quota Exclude",
				"domain": "scheduling",
				"enabled": true,
				"when": {
					"fact": "credential.quota.remaining_ratio",
					"op": "lt",
					"value": 0.1,
					"select": {"scope": "account", "window_seconds": 18000},
					"reduce": "min"
				},
				"then": {"type": "exclude_candidate"}
			}
		]
	}`
	ref, _ := fixture.service.registry.CredentialRef(credID)
	realNow := fixture.service.now()
	observed := realNow.Add(-time.Hour).UnixMilli()
	reset := realNow.Add(time.Hour).UnixMilli()
	sec := int64(18000)
	u := 0.95
	w := po.QuotaWindow{Scope: "account", WindowSeconds: &sec, ResetAtMS: &reset, ObservedAtMS: &observed, Utilization: &u, State: "available"}
	if !fixture.service.registry.ApplyQuotaWindows(credID, ref.IdentityGeneration, []po.QuotaWindow{w}) {
		t.Fatal("apply quota")
	}

	for _, sim := range []string{"2020-01-01T00:00:00Z", "2030-01-01T00:00:00Z"} {
		previewQuotaReq := httptest.NewRequest(
			http.MethodPost,
			fmt.Sprintf("/api/groups/%d/credentials/%d/policy/preview", groupID, credID),
			bytes.NewBufferString(fmt.Sprintf(`{
				"request_model": "gpt-4o",
				"simulated_time": %q,
				"config": %s
			}`, sim, quotaDraft)),
		)
		previewQuotaReq.Header.Set("Authorization", "Bearer admin-secret-key")
		previewQuotaReq.Header.Set("Content-Type", "application/json")
		recQuota := httptest.NewRecorder()
		server.ServeHTTP(recQuota, previewQuotaReq)
		if recQuota.Code != http.StatusOK {
			t.Fatalf("quota preview under %s failed: %d %s", sim, recQuota.Code, recQuota.Body.String())
		}
		var envQuota policyPreviewEnvelope
		_ = json.Unmarshal(recQuota.Body.Bytes(), &envQuota)
		if !envQuota.Data.Candidates[0].Targets[0].Scheduling.Excluded {
			t.Fatalf("expected candidate excluded based on real-time quota regardless of simulated time %s", sim)
		}
	}

	// 验证 caveat_codes 包含了 mixed_simulation_context
	var hasMixedCaveat bool
	for _, code := range envHit.Data.CaveatCodes {
		if code == "policy.preview.caveat.mixed_simulation_context" {
			hasMixedCaveat = true
			break
		}
	}
	if !hasMixedCaveat {
		t.Errorf("expected caveat code 'policy.preview.caveat.mixed_simulation_context' in preview response")
	}
}

func TestPolicyPreview_CASRevisionTextAndExpectedRevisionVariants(t *testing.T) {
	fixture := newServiceFixture(t)
	server := setupPolicyTestServer(t, fixture)
	groupID, _ := createTestGroupWithTwoModels(t, fixture)

	emptyCfg := `{"schema_version":1,"rules":[]}`

	// 1. 允许 expected_revision 为字符串 "0"
	reqStr0 := httptest.NewRequest(
		http.MethodPut,
		fmt.Sprintf("/api/groups/%d/policy", groupID),
		bytes.NewBufferString(fmt.Sprintf(`{"expected_revision":"0","config":%s}`, emptyCfg)),
	)
	reqStr0.Header.Set("Authorization", "Bearer admin-secret-key")
	reqStr0.Header.Set("Content-Type", "application/json")
	recStr0 := httptest.NewRecorder()
	server.ServeHTTP(recStr0, reqStr0)
	if recStr0.Code != http.StatusOK {
		t.Fatalf("expected 200 for expected_revision='0', got %d: %s", recStr0.Code, recStr0.Body.String())
	}

	// 2. 拒绝浮点数 1.0
	reqFloat := httptest.NewRequest(
		http.MethodPut,
		fmt.Sprintf("/api/groups/%d/policy", groupID),
		bytes.NewBufferString(fmt.Sprintf(`{"expected_revision":1.0,"config":%s}`, emptyCfg)),
	)
	reqFloat.Header.Set("Authorization", "Bearer admin-secret-key")
	reqFloat.Header.Set("Content-Type", "application/json")
	recFloat := httptest.NewRecorder()
	server.ServeHTTP(recFloat, reqFloat)
	if recFloat.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for float expected_revision, got %d", recFloat.Code)
	}

	// 3. 拒绝前导零 "01"
	reqLeadZero := httptest.NewRequest(
		http.MethodPut,
		fmt.Sprintf("/api/groups/%d/policy", groupID),
		bytes.NewBufferString(fmt.Sprintf(`{"expected_revision":"01","config":%s}`, emptyCfg)),
	)
	reqLeadZero.Header.Set("Authorization", "Bearer admin-secret-key")
	reqLeadZero.Header.Set("Content-Type", "application/json")
	recLeadZero := httptest.NewRecorder()
	server.ServeHTTP(recLeadZero, reqLeadZero)
	if recLeadZero.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for leading zero expected_revision, got %d", recLeadZero.Code)
	}

	// 4. 拒绝符号 "+1"
	reqPlus := httptest.NewRequest(
		http.MethodPut,
		fmt.Sprintf("/api/groups/%d/policy", groupID),
		bytes.NewBufferString(fmt.Sprintf(`{"expected_revision":"+1","config":%s}`, emptyCfg)),
	)
	reqPlus.Header.Set("Authorization", "Bearer admin-secret-key")
	reqPlus.Header.Set("Content-Type", "application/json")
	recPlus := httptest.NewRecorder()
	server.ServeHTTP(recPlus, reqPlus)
	if recPlus.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for signed expected_revision, got %d", recPlus.Code)
	}

	// 5. 拒绝数值类型 expected_revision (仅允许 string)
	reqNum1 := httptest.NewRequest(
		http.MethodPut,
		fmt.Sprintf("/api/groups/%d/policy", groupID),
		bytes.NewBufferString(fmt.Sprintf(`{"expected_revision":1,"config":%s}`, emptyCfg)),
	)
	reqNum1.Header.Set("Authorization", "Bearer admin-secret-key")
	reqNum1.Header.Set("Content-Type", "application/json")
	recNum1 := httptest.NewRecorder()
	server.ServeHTTP(recNum1, reqNum1)
	if recNum1.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for numeric expected_revision=1, got %d", recNum1.Code)
	}

	// 6. 允许合法字符串 "1"
	reqStr1 := httptest.NewRequest(
		http.MethodPut,
		fmt.Sprintf("/api/groups/%d/policy", groupID),
		bytes.NewBufferString(fmt.Sprintf(`{"expected_revision":"1","config":%s}`, emptyCfg)),
	)
	reqStr1.Header.Set("Authorization", "Bearer admin-secret-key")
	reqStr1.Header.Set("Content-Type", "application/json")
	recStr1 := httptest.NewRecorder()
	server.ServeHTTP(recStr1, reqStr1)
	if recStr1.Code != http.StatusOK {
		t.Fatalf("expected 200 for string expected_revision=\"1\", got %d: %s", recStr1.Code, recStr1.Body.String())
	}
}

func TestPolicyPreview_BudgetRejection(t *testing.T) {
	fixture := newServiceFixture(t)
	server := setupPolicyTestServer(t, fixture)
	groupID, _ := createTestGroupWithTwoModels(t, fixture)

	// 构造超过 10000 聚合节点的配置 (2 candidates * 2 targets * 3100 = 12400)
	heavyConfig := heavyAggregatePolicyConfig()

	reqHeavy := httptest.NewRequest(
		http.MethodPost,
		fmt.Sprintf("/api/groups/%d/policy/preview", groupID),
		bytes.NewBufferString(fmt.Sprintf(`{"request_model":"gpt-4o","config":%s}`, heavyConfig)),
	)
	reqHeavy.Header.Set("Authorization", "Bearer admin-secret-key")
	reqHeavy.Header.Set("Content-Type", "application/json")
	recHeavy := httptest.NewRecorder()
	server.ServeHTTP(recHeavy, reqHeavy)

	if recHeavy.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 BadRequest for aggregate condition nodes > 10000, got %d: %s", recHeavy.Code, recHeavy.Body.String())
	}
	if !strings.Contains(recHeavy.Body.String(), "exceeds maximum preview budget") {
		t.Fatalf("expected budget rejection message, got: %s", recHeavy.Body.String())
	}
}

func TestPolicyPreview_TargetBudgetRejection(t *testing.T) {
	fixture := newServiceFixture(t)
	server := setupPolicyTestServer(t, fixture)

	// 创建一个拥有 12 个同 client model 别名 upstream targets 的分组
	seq := testIdempotencySequence.Add(1)
	name := fmt.Sprintf("target-budget-%d", seq)
	baseURL := fmt.Sprintf("https://api-t-%d.example.com/v1", seq)
	var modelsList []GroupModel
	for i := 1; i <= 12; i++ {
		modelsList = append(modelsList, GroupModel{
			ID:           fmt.Sprintf("upstream-%d", i),
			Alias:        "many-targets-model",
			AliasEnabled: true,
		})
	}
	result, err := fixture.service.CreateGroup(t.Context(), GroupCreateRequest{
		Name:           &name,
		ChannelID:      channel.OpenAICompatible,
		Params:         json.RawMessage(fmt.Sprintf(`{"base_url":%q}`, baseURL)),
		Models:         optionalGroupModels{Set: true, Values: modelsList},
		Credentials:    "sk-test-1",
		ConnectionType: "api_key",
	})
	if err != nil {
		t.Fatalf("CreateGroup failed: %v", err)
	}

	req := httptest.NewRequest(
		http.MethodPost,
		fmt.Sprintf("/api/groups/%d/policy/preview", result.GroupID),
		bytes.NewBufferString(`{"request_model":"many-targets-model"}`),
	)
	req.Header.Set("Authorization", "Bearer admin-secret-key")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 BadRequest for exceeding target budget (12 > 10), got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "target count 12 exceeds maximum preview budget of 10") {
		t.Fatalf("expected target budget error message, got: %s", rec.Body.String())
	}
}
