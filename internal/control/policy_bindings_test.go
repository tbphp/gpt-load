package control

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/gin-gonic/gin"

	"gpt-load/internal/channel"
	"gpt-load/internal/platform/config"
	"gpt-load/internal/storage/models"
)

type policyResponseEnvelope struct {
	Code    int                   `json:"code"`
	Message string                `json:"message"`
	Data    PolicyBindingResponse `json:"data"`
}

type policyErrorEnvelope struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func createUniqueGroupWithCredentials(t *testing.T, fixture serviceFixture, credentials string) uint {
	t.Helper()
	seq := testIdempotencySequence.Add(1)
	name := fmt.Sprintf("policy-group-%d", seq)
	baseURL := fmt.Sprintf("https://api-%d.example.com/v1", seq)
	result, err := fixture.service.CreateGroup(t.Context(), GroupCreateRequest{
		Name:      &name,
		ChannelID: channel.OpenAICompatible,
		Params:    json.RawMessage(fmt.Sprintf(`{"base_url":%q}`, baseURL)),
		Models: optionalGroupModels{
			Set:    true,
			Values: []GroupModel{{ID: "gpt-4o"}},
		},
		Credentials:    credentials,
		ConnectionType: "api_key",
	})
	if err != nil {
		t.Fatalf("CreateGroup(%q) error = %v", name, err)
	}
	return result.GroupID
}

func setupPolicyTestServer(t *testing.T, fixture serviceFixture) *gin.Engine {
	t.Helper()
	initControlI18n(t)
	engine := gin.New()
	server := NewServer(&config.Config{AuthKey: "admin-secret-key"}, fixture.service)
	server.RegisterRoutes(engine)
	return engine
}

func parsePolicyResponse(t *testing.T, body []byte) PolicyBindingResponse {
	t.Helper()
	var env policyResponseEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("unmarshal policy envelope %s: %v", string(body), err)
	}
	return env.Data
}

func parseConfigRulesCount(t *testing.T, configText string) int {
	t.Helper()
	var m struct {
		Rules []json.RawMessage `json:"rules"`
	}
	if err := json.Unmarshal([]byte(configText), &m); err != nil {
		t.Fatalf("unmarshal config text: %v", err)
	}
	return len(m.Rules)
}

// 1. 未配置时读取 group 与 credential 策略返回默认：schema_version=1, rules=[], revision=0
func TestPolicyDefaultWhenUnconfigured(t *testing.T) {
	t.Parallel()
	fixture := newServiceFixture(t)
	engine := setupPolicyTestServer(t, fixture)
	groupID := createUniqueGroupWithCredentials(t, fixture, "sk-test-unconfigured")

	var cred models.Credential
	if err := fixture.db.Where("group_id = ?", groupID).First(&cred).Error; err != nil {
		t.Fatalf("query credential: %v", err)
	}

	// 1.1 读取 Group 默认策略
	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/groups/%d/policy", groupID), nil)
	req.Header.Set("Authorization", "Bearer admin-secret-key")
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GET group policy status = %d: %s", w.Code, w.Body.String())
	}
	groupData := parsePolicyResponse(t, w.Body.Bytes())
	if groupData.Scope != "group" || groupData.ID != groupID || groupData.RevisionText != "0" {
		t.Fatalf("unexpected group default policy data: %+v", groupData)
	}
	if parseConfigRulesCount(t, groupData.ConfigText) != 0 {
		t.Fatalf("expected empty rules for unconfigured group, got %s", groupData.ConfigText)
	}

	// 1.2 读取 Credential 默认策略
	reqCred := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/groups/%d/credentials/%d/policy", groupID, cred.ID), nil)
	reqCred.Header.Set("Authorization", "Bearer admin-secret-key")
	wCred := httptest.NewRecorder()
	engine.ServeHTTP(wCred, reqCred)
	if wCred.Code != http.StatusOK {
		t.Fatalf("GET credential policy status = %d: %s", wCred.Code, wCred.Body.String())
	}
	credData := parsePolicyResponse(t, wCred.Body.Bytes())
	if credData.Scope != "credential" || credData.ID != cred.ID || credData.RevisionText != "0" {
		t.Fatalf("unexpected credential default policy data: %+v", credData)
	}
	if parseConfigRulesCount(t, credData.ConfigText) != 0 {
		t.Fatalf("expected empty rules for unconfigured credential, got %s", credData.ConfigText)
	}
}

// 2. 两种 scope 隔离：同一 Group 下的 group 策略与 credential 策略独立存储，互不影响
func TestPolicyScopeIsolation(t *testing.T) {
	t.Parallel()
	fixture := newServiceFixture(t)
	engine := setupPolicyTestServer(t, fixture)
	groupID := createUniqueGroupWithCredentials(t, fixture, "sk-test-scope-isolation")

	var cred models.Credential
	if err := fixture.db.Where("group_id = ?", groupID).First(&cred).Error; err != nil {
		t.Fatalf("query credential: %v", err)
	}

	// 更新 Group 策略 (revision 0 -> 1)
	groupConfigJSON := `{"schema_version":1,"rules":[{"id":"r-grp","name":"grp rule","domain":"scheduling","enabled":true,"when":{"fact":"request.model","op":"eq","value":"gpt-4o"},"then":{"type":"exclude_candidate"}}]}`
	groupBody := fmt.Sprintf(`{"expected_revision":"0","config":%s}`, groupConfigJSON)
	req := httptest.NewRequest(http.MethodPut, fmt.Sprintf("/api/groups/%d/policy", groupID), bytes.NewBufferString(groupBody))
	req.Header.Set("Authorization", "Bearer admin-secret-key")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("PUT group policy status = %d: %s", w.Code, w.Body.String())
	}
	grpRes := parsePolicyResponse(t, w.Body.Bytes())
	if grpRes.RevisionText != "1" || parseConfigRulesCount(t, grpRes.ConfigText) != 1 {
		t.Fatalf("expected group revision 1 and 1 rule, got %+v", grpRes)
	}
	if fixture.manager.Current().Policies == nil || fixture.manager.Current().Policies.GroupPolicy(groupID) == nil {
		t.Fatal("group policy was not published to the live snapshot")
	}

	// 此时 Credential 策略应当依然是未配置 (revision 0, rules=[])
	reqCred := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/groups/%d/credentials/%d/policy", groupID, cred.ID), nil)
	reqCred.Header.Set("Authorization", "Bearer admin-secret-key")
	wCred := httptest.NewRecorder()
	engine.ServeHTTP(wCred, reqCred)
	if wCred.Code != http.StatusOK {
		t.Fatalf("GET credential policy status = %d: %s", wCred.Code, wCred.Body.String())
	}
	credRes := parsePolicyResponse(t, wCred.Body.Bytes())
	if credRes.RevisionText != "0" || parseConfigRulesCount(t, credRes.ConfigText) != 0 {
		t.Fatalf("credential policy was polluted by group policy: %+v", credRes)
	}

	// 更新 Credential 策略 (revision 0 -> 1)
	credConfigJSON := `{"schema_version":1,"rules":[{"id":"r-crd","name":"crd rule","domain":"pricing","enabled":true,"when":{"fact":"credential.quota.remaining_ratio","select":{"scope":"account","window_seconds":18000},"reduce":"min","op":"lt","value":0.1},"then":{"type":"multiply_price","factor":"1.5"}}]}`
	credBody := fmt.Sprintf(`{"expected_revision":"0","config":%s}`, credConfigJSON)
	reqCredPut := httptest.NewRequest(http.MethodPut, fmt.Sprintf("/api/groups/%d/credentials/%d/policy", groupID, cred.ID), bytes.NewBufferString(credBody))
	reqCredPut.Header.Set("Authorization", "Bearer admin-secret-key")
	reqCredPut.Header.Set("Content-Type", "application/json")
	wCredPut := httptest.NewRecorder()
	engine.ServeHTTP(wCredPut, reqCredPut)
	if wCredPut.Code != http.StatusOK {
		t.Fatalf("PUT credential policy status = %d: %s", wCredPut.Code, wCredPut.Body.String())
	}
	credRes2 := parsePolicyResponse(t, wCredPut.Body.Bytes())
	if credRes2.RevisionText != "1" || parseConfigRulesCount(t, credRes2.ConfigText) != 1 {
		t.Fatalf("expected credential revision 1 and 1 rule, got %+v", credRes2)
	}
	if fixture.manager.Current().Policies == nil || fixture.manager.Current().Policies.CredentialPolicy(cred.ID) == nil {
		t.Fatal("credential policy was not published to the live snapshot")
	}

	// 重新读取 Group 策略，确认未受 Credential 策略更新影响
	reqGrpGet := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/groups/%d/policy", groupID), nil)
	reqGrpGet.Header.Set("Authorization", "Bearer admin-secret-key")
	wGrpGet := httptest.NewRecorder()
	engine.ServeHTTP(wGrpGet, reqGrpGet)
	grpRes2 := parsePolicyResponse(t, wGrpGet.Body.Bytes())
	if grpRes2.RevisionText != "1" || parseConfigRulesCount(t, grpRes2.ConfigText) != 1 {
		t.Fatalf("group policy was corrupted: %+v", grpRes2)
	}
}

// 3. credential 跨 group 越权 404 / 拒绝，不泄露凭据信息
func TestPolicyCredentialCrossGroupUnauthorized(t *testing.T) {
	t.Parallel()
	fixture := newServiceFixture(t)
	engine := setupPolicyTestServer(t, fixture)
	groupA := createUniqueGroupWithCredentials(t, fixture, "sk-group-a")
	groupB := createUniqueGroupWithCredentials(t, fixture, "sk-group-b")

	var credB models.Credential
	if err := fixture.db.Where("group_id = ?", groupB).First(&credB).Error; err != nil {
		t.Fatalf("query credB: %v", err)
	}

	// 尝试通过 groupA 的路径读取 groupB 的 credential 策略 -> 404
	reqGet := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/groups/%d/credentials/%d/policy", groupA, credB.ID), nil)
	reqGet.Header.Set("Authorization", "Bearer admin-secret-key")
	wGet := httptest.NewRecorder()
	engine.ServeHTTP(wGet, reqGet)
	if wGet.Code != http.StatusNotFound {
		t.Fatalf("cross-group GET credential policy status = %d, want 404", wGet.Code)
	}

	// 尝试通过 groupA 的路径更新 groupB 的 credential 策略 -> 404
	reqPut := httptest.NewRequest(http.MethodPut, fmt.Sprintf("/api/groups/%d/credentials/%d/policy", groupA, credB.ID), bytes.NewBufferString(`{"expected_revision":"0","config":{"schema_version":1,"rules":[]}}`))
	reqPut.Header.Set("Authorization", "Bearer admin-secret-key")
	reqPut.Header.Set("Content-Type", "application/json")
	wPut := httptest.NewRecorder()
	engine.ServeHTTP(wPut, reqPut)
	if wPut.Code != http.StatusNotFound {
		t.Fatalf("cross-group PUT credential policy status = %d, want 404", wPut.Code)
	}
}

// 4. 显式合法空 rules 清空规则
func TestPolicyExplicitEmptyRulesClears(t *testing.T) {
	t.Parallel()
	fixture := newServiceFixture(t)
	engine := setupPolicyTestServer(t, fixture)
	groupID := createUniqueGroupWithCredentials(t, fixture, "sk-test-empty-rules")

	// 4.1 先保存有 1 条规则的策略 (revision 1)
	cfg := `{"expected_revision":"0","config":{"schema_version":1,"rules":[{"id":"r-1","name":"r1","domain":"scheduling","enabled":true,"when":{"fact":"request.model","op":"eq","value":"gpt-4o"},"then":{"type":"exclude_candidate"}}]}}`
	req1 := httptest.NewRequest(http.MethodPut, fmt.Sprintf("/api/groups/%d/policy", groupID), bytes.NewBufferString(cfg))
	req1.Header.Set("Authorization", "Bearer admin-secret-key")
	req1.Header.Set("Content-Type", "application/json")
	w1 := httptest.NewRecorder()
	engine.ServeHTTP(w1, req1)
	if w1.Code != http.StatusOK {
		t.Fatalf("step 1 PUT error: %s", w1.Body.String())
	}
	res1 := parsePolicyResponse(t, w1.Body.Bytes())
	if res1.RevisionText != "1" || parseConfigRulesCount(t, res1.ConfigText) != 1 {
		t.Fatalf("unexpected res1: %+v", res1)
	}

	// 4.2 显式传 empty rules 清空规则 (revision 1 -> 2)
	clearCfg := `{"expected_revision":"1","config":{"schema_version":1,"rules":[]}}`
	req2 := httptest.NewRequest(http.MethodPut, fmt.Sprintf("/api/groups/%d/policy", groupID), bytes.NewBufferString(clearCfg))
	req2.Header.Set("Authorization", "Bearer admin-secret-key")
	req2.Header.Set("Content-Type", "application/json")
	w2 := httptest.NewRecorder()
	engine.ServeHTTP(w2, req2)
	if w2.Code != http.StatusOK {
		t.Fatalf("step 2 PUT error: %s", w2.Body.String())
	}
	res2 := parsePolicyResponse(t, w2.Body.Bytes())
	if res2.RevisionText != "2" || parseConfigRulesCount(t, res2.ConfigText) != 0 {
		t.Fatalf("expected cleared rules with revision 2, got %+v", res2)
	}

	// 4.3 再次 GET 确认是空规则
	req3 := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/groups/%d/policy", groupID), nil)
	req3.Header.Set("Authorization", "Bearer admin-secret-key")
	w3 := httptest.NewRecorder()
	engine.ServeHTTP(w3, req3)
	res3 := parsePolicyResponse(t, w3.Body.Bytes())
	if res3.RevisionText != "2" || parseConfigRulesCount(t, res3.ConfigText) != 0 {
		t.Fatalf("GET after clear got %+v", res3)
	}
}

// 5. 遗漏 config 字段时保持已有配置不变
func TestPolicyOmittedConfigPreservesExisting(t *testing.T) {
	t.Parallel()
	fixture := newServiceFixture(t)
	engine := setupPolicyTestServer(t, fixture)
	groupID := createUniqueGroupWithCredentials(t, fixture, "sk-test-omitted-config")

	// 先存一条规则 (revision 1)
	cfg := `{"expected_revision":"0","config":{"schema_version":1,"rules":[{"id":"r-keep","name":"keep me","domain":"scheduling","enabled":true,"when":{"fact":"request.model","op":"eq","value":"gpt-4o"},"then":{"type":"exclude_candidate"}}]}}`
	req1 := httptest.NewRequest(http.MethodPut, fmt.Sprintf("/api/groups/%d/policy", groupID), bytes.NewBufferString(cfg))
	req1.Header.Set("Authorization", "Bearer admin-secret-key")
	req1.Header.Set("Content-Type", "application/json")
	w1 := httptest.NewRecorder()
	engine.ServeHTTP(w1, req1)
	if w1.Code != http.StatusOK {
		t.Fatalf("save policy error: %s", w1.Body.String())
	}

	// 遗漏 config 字段的更新 (只有 expected_revision: "1")
	req2 := httptest.NewRequest(http.MethodPut, fmt.Sprintf("/api/groups/%d/policy", groupID), bytes.NewBufferString(`{"expected_revision":"1"}`))
	req2.Header.Set("Authorization", "Bearer admin-secret-key")
	req2.Header.Set("Content-Type", "application/json")
	w2 := httptest.NewRecorder()
	engine.ServeHTTP(w2, req2)
	if w2.Code != http.StatusOK {
		t.Fatalf("omitted config update error: %s", w2.Body.String())
	}
	res2 := parsePolicyResponse(t, w2.Body.Bytes())
	if parseConfigRulesCount(t, res2.ConfigText) != 1 {
		t.Fatalf("expected preserved 1 rule, got %s", res2.ConfigText)
	}

	// 验证数据库中的记录仍完整保留原有规则
	req3 := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/groups/%d/policy", groupID), nil)
	req3.Header.Set("Authorization", "Bearer admin-secret-key")
	w3 := httptest.NewRecorder()
	engine.ServeHTTP(w3, req3)
	res3 := parsePolicyResponse(t, w3.Body.Bytes())
	if parseConfigRulesCount(t, res3.ConfigText) != 1 {
		t.Fatalf("GET expected preserved 1 rule, got %s", res3.ConfigText)
	}
}

// 6. null / 空白 / 坏 JSON / 未知字段 / 非法 disabled 规则均拒绝且绝不写库
func TestPolicyInvalidConfigurationsRejected(t *testing.T) {
	t.Parallel()
	fixture := newServiceFixture(t)
	engine := setupPolicyTestServer(t, fixture)
	groupID := createUniqueGroupWithCredentials(t, fixture, "sk-test-invalids")

	// 先写入一个合法的基准策略 (revision 1)
	validCfg := `{"expected_revision":"0","config":{"schema_version":1,"rules":[{"id":"r-base","name":"base","domain":"scheduling","enabled":true,"when":{"fact":"request.model","op":"eq","value":"gpt-4o"},"then":{"type":"exclude_candidate"}}]}}`
	reqBase := httptest.NewRequest(http.MethodPut, fmt.Sprintf("/api/groups/%d/policy", groupID), bytes.NewBufferString(validCfg))
	reqBase.Header.Set("Authorization", "Bearer admin-secret-key")
	reqBase.Header.Set("Content-Type", "application/json")
	wBase := httptest.NewRecorder()
	engine.ServeHTTP(wBase, reqBase)
	if wBase.Code != http.StatusOK {
		t.Fatalf("base write failed: %s", wBase.Body.String())
	}

	invalidCases := []struct {
		name    string
		payload string
	}{
		{"null config", `{"expected_revision":"1","config":null}`},
		{"empty string config", `{"expected_revision":"1","config":""}`},
		{"malformed JSON", `{"expected_revision":"1","config":{bad_json}}`},
		{"unknown field in config", `{"expected_revision":"1","config":{"schema_version":1,"rules":[],"unexpected_field":true}}`},
		{"illegal disabled rule", `{"expected_revision":"1","config":{"schema_version":1,"rules":[{"id":"r-dis","name":"bad","domain":"scheduling","enabled":false,"when":{"fact":"unknown.fact","op":"eq","value":"val"},"then":{"type":"exclude_candidate"}}]}}`},
		{"out of range quota threshold", `{"expected_revision":"1","config":{"schema_version":1,"rules":[{"id":"r-dis","name":"bad","domain":"scheduling","enabled":true,"when":{"fact":"credential.quota.remaining_ratio","select":{"scope":"account","window_seconds":18000},"reduce":"min","op":"lt","value":1.0000000000000001},"then":{"type":"exclude_candidate"}}]}}`},
	}

	for _, tc := range invalidCases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPut, fmt.Sprintf("/api/groups/%d/policy", groupID), bytes.NewBufferString(tc.payload))
			req.Header.Set("Authorization", "Bearer admin-secret-key")
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			engine.ServeHTTP(w, req)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("%s returned status %d, want 400: %s", tc.name, w.Code, w.Body.String())
			}

			// 检查数据库记录未被篡改，依然是基准配置，版本仍为 1
			reqCheck := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/groups/%d/policy", groupID), nil)
			reqCheck.Header.Set("Authorization", "Bearer admin-secret-key")
			wCheck := httptest.NewRecorder()
			engine.ServeHTTP(wCheck, reqCheck)
			resCheck := parsePolicyResponse(t, wCheck.Body.Bytes())
			if resCheck.RevisionText != "1" || parseConfigRulesCount(t, resCheck.ConfigText) != 1 {
				t.Fatalf("database content was corrupted after %s: %+v", tc.name, resCheck)
			}
		})
	}
}

// 7. expected_revision 冲突返回 409 且内容不变
func TestPolicyExpectedRevisionConflict409(t *testing.T) {
	t.Parallel()
	fixture := newServiceFixture(t)
	engine := setupPolicyTestServer(t, fixture)
	groupID := createUniqueGroupWithCredentials(t, fixture, "sk-test-conflict-409")

	// 7.1 未配置时传入 expected_revision = 1 (应该传 0) -> 409
	badReq := httptest.NewRequest(http.MethodPut, fmt.Sprintf("/api/groups/%d/policy", groupID), bytes.NewBufferString(`{"expected_revision":"1","config":{"schema_version":1,"rules":[]}}`))
	badReq.Header.Set("Authorization", "Bearer admin-secret-key")
	badReq.Header.Set("Content-Type", "application/json")
	wBad := httptest.NewRecorder()
	engine.ServeHTTP(wBad, badReq)
	if wBad.Code != http.StatusConflict {
		t.Fatalf("expected 409 for initial expected_revision=1, got %d: %s", wBad.Code, wBad.Body.String())
	}

	// 7.2 写入正确初始配置 (expected_revision: 0 -> 得到 revision 1)
	okReq := httptest.NewRequest(http.MethodPut, fmt.Sprintf("/api/groups/%d/policy", groupID), bytes.NewBufferString(`{"expected_revision":"0","config":{"schema_version":1,"rules":[{"id":"r-1","name":"r1","domain":"scheduling","enabled":true,"when":{"fact":"request.model","op":"eq","value":"gpt-4o"},"then":{"type":"exclude_candidate"}}]}}`))
	okReq.Header.Set("Authorization", "Bearer admin-secret-key")
	okReq.Header.Set("Content-Type", "application/json")
	wOk := httptest.NewRecorder()
	engine.ServeHTTP(wOk, okReq)
	if wOk.Code != http.StatusOK {
		t.Fatalf("initial write error: %s", wOk.Body.String())
	}
	resOk := parsePolicyResponse(t, wOk.Body.Bytes())
	if resOk.RevisionText != "1" {
		t.Fatalf("expected revision 1, got %s", resOk.RevisionText)
	}

	// 7.3 使用旧的 expected_revision: 0 再次更新 -> 409 Conflict
	staleReq := httptest.NewRequest(http.MethodPut, fmt.Sprintf("/api/groups/%d/policy", groupID), bytes.NewBufferString(`{"expected_revision":"0","config":{"schema_version":1,"rules":[]}}`))
	staleReq.Header.Set("Authorization", "Bearer admin-secret-key")
	staleReq.Header.Set("Content-Type", "application/json")
	wStale := httptest.NewRecorder()
	engine.ServeHTTP(wStale, staleReq)
	if wStale.Code != http.StatusConflict {
		t.Fatalf("expected 409 for stale revision 0, got %d: %s", wStale.Code, wStale.Body.String())
	}

	// 7.4 验证内容依然是原有的 1 条规则，版本依然是 1
	wVerify := httptest.NewRecorder()
	reqVerify := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/groups/%d/policy", groupID), nil)
	reqVerify.Header.Set("Authorization", "Bearer admin-secret-key")
	engine.ServeHTTP(wVerify, reqVerify)
	resVerify := parsePolicyResponse(t, wVerify.Body.Bytes())
	if resVerify.RevisionText != "1" || parseConfigRulesCount(t, resVerify.ConfigText) != 1 {
		t.Fatalf("database content changed after conflict: %+v", resVerify)
	}
}

// 8. 并发 CAS 更新：相同 expected_revision 下最多只有 1 次成功
func TestPolicyConcurrentCAS(t *testing.T) {
	t.Parallel()
	fixture := newServiceFixture(t)
	engine := setupPolicyTestServer(t, fixture)
	groupID := createUniqueGroupWithCredentials(t, fixture, "sk-test-concurrent-cas")

	const concurrency = 10
	var wg sync.WaitGroup
	var successCount atomic.Int32
	var conflictCount atomic.Int32

	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			payload := fmt.Sprintf(`{"expected_revision":"0","config":{"schema_version":1,"rules":[{"id":"r-%d","name":"concurrent","domain":"scheduling","enabled":true,"when":{"fact":"request.model","op":"eq","value":"gpt-4o"},"then":{"type":"exclude_candidate"}}]}}`, idx)
			req := httptest.NewRequest(http.MethodPut, fmt.Sprintf("/api/groups/%d/policy", groupID), bytes.NewBufferString(payload))
			req.Header.Set("Authorization", "Bearer admin-secret-key")
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			engine.ServeHTTP(w, req)
			if w.Code == http.StatusOK {
				successCount.Add(1)
			} else if w.Code == http.StatusConflict {
				conflictCount.Add(1)
			}
		}(i)
	}
	wg.Wait()

	if successCount.Load() != 1 {
		t.Fatalf("expected exactly 1 success in concurrent CAS, got %d successes, %d conflicts", successCount.Load(), conflictCount.Load())
	}
	if conflictCount.Load() != concurrency-1 {
		t.Fatalf("expected %d conflicts in concurrent CAS, got %d", concurrency-1, conflictCount.Load())
	}

	// 最终记录的 revision 必须为 1
	wFinal := httptest.NewRecorder()
	reqFinal := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/groups/%d/policy", groupID), nil)
	reqFinal.Header.Set("Authorization", "Bearer admin-secret-key")
	engine.ServeHTTP(wFinal, reqFinal)
	resFinal := parsePolicyResponse(t, wFinal.Body.Bytes())
	if resFinal.RevisionText != "1" {
		t.Fatalf("expected final revision 1, got %s", resFinal.RevisionText)
	}
}

// 9. 旧 Group Overrides 更新绝不影响已存 Policy
func TestPolicyOldGroupOverridesDoesNotAffectPolicy(t *testing.T) {
	t.Parallel()
	fixture := newServiceFixture(t)
	engine := setupPolicyTestServer(t, fixture)
	groupID := createUniqueGroupWithCredentials(t, fixture, "sk-test-isolation-from-overrides")

	// 9.1 保存 Policy (revision 1)
	policyJSON := `{"expected_revision":"0","config":{"schema_version":1,"rules":[{"id":"r-safe","name":"safe rule","domain":"scheduling","enabled":true,"when":{"fact":"request.model","op":"eq","value":"gpt-4o"},"then":{"type":"exclude_candidate"}}]}}`
	reqPolicy := httptest.NewRequest(http.MethodPut, fmt.Sprintf("/api/groups/%d/policy", groupID), bytes.NewBufferString(policyJSON))
	reqPolicy.Header.Set("Authorization", "Bearer admin-secret-key")
	reqPolicy.Header.Set("Content-Type", "application/json")
	wPolicy := httptest.NewRecorder()
	engine.ServeHTTP(wPolicy, reqPolicy)
	if wPolicy.Code != http.StatusOK {
		t.Fatalf("save policy error: %s", wPolicy.Body.String())
	}

	// 9.2 通过旧的 UpdateGroupSettings 接口更新 Group Overrides
	overridesPayload := `{"overrides":{"concurrency_limit":42}}`
	reqSettings := httptest.NewRequest(http.MethodPut, fmt.Sprintf("/api/groups/%d/settings", groupID), bytes.NewBufferString(overridesPayload))
	reqSettings.Header.Set("Authorization", "Bearer admin-secret-key")
	reqSettings.Header.Set("Content-Type", "application/json")
	wSettings := httptest.NewRecorder()
	engine.ServeHTTP(wSettings, reqSettings)
	if wSettings.Code != http.StatusOK {
		t.Fatalf("update settings error: %s", wSettings.Body.String())
	}

	// 9.3 验证 Policy 依然保持 revision 1 以及 1 条规则，完全不受 Overrides 保存影响
	wCheck := httptest.NewRecorder()
	reqCheck := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/groups/%d/policy", groupID), nil)
	reqCheck.Header.Set("Authorization", "Bearer admin-secret-key")
	engine.ServeHTTP(wCheck, reqCheck)
	resCheck := parsePolicyResponse(t, wCheck.Body.Bytes())
	if resCheck.RevisionText != "1" || parseConfigRulesCount(t, resCheck.ConfigText) != 1 {
		t.Fatalf("policy was affected by group overrides update! got %+v", resCheck)
	}
}

// 10. 管理员权限校验：无 Authorization 或非管理员拒绝
func TestPolicyRequiresAdminAuth(t *testing.T) {
	t.Parallel()
	fixture := newServiceFixture(t)
	engine := setupPolicyTestServer(t, fixture)
	groupID := createUniqueGroupWithCredentials(t, fixture, "sk-test-auth")

	// 无 Token 请求 GET -> 401
	reqAnon := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/groups/%d/policy", groupID), nil)
	wAnon := httptest.NewRecorder()
	engine.ServeHTTP(wAnon, reqAnon)
	if wAnon.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous request expected 401, got %d", wAnon.Code)
	}

	// 错误 Token 请求 GET -> 401
	reqBad := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/groups/%d/policy", groupID), nil)
	reqBad.Header.Set("Authorization", "Bearer wrong-key")
	wBad := httptest.NewRecorder()
	engine.ServeHTTP(wBad, reqBad)
	if wBad.Code != http.StatusUnauthorized {
		t.Fatalf("bad token request expected 401, got %d", wBad.Code)
	}
}

func TestPolicyUpdateNonExistentGroupReturns404AndNoOrphan(t *testing.T) {
	t.Parallel()
	fixture := newServiceFixture(t)
	engine := setupPolicyTestServer(t, fixture)

	reqBody := `{"expected_revision":"0","config":{"schema_version":1,"rules":[]}}`
	req := httptest.NewRequest(http.MethodPut, "/api/groups/999999/policy", bytes.NewReader([]byte(reqBody)))
	req.Header.Set("Authorization", "Bearer admin-secret-key")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("PUT /api/groups/999999/policy status = %d; want 404: %s", w.Code, w.Body.String())
	}

	// 确认数据库中绝对无孤儿记录
	var count int64
	if err := fixture.db.Model(&models.PolicyBinding{}).Where("group_id = ?", 999999).Count(&count).Error; err != nil {
		t.Fatalf("count policy bindings: %v", err)
	}
	if count != 0 {
		t.Fatalf("found %d orphan policy bindings for non-existent group 999999", count)
	}
}

func TestPolicyUpdateNonExistentCredentialReturns404AndNoOrphan(t *testing.T) {
	t.Parallel()
	fixture := newServiceFixture(t)
	engine := setupPolicyTestServer(t, fixture)
	grpID := createUniqueGroupWithCredentials(t, fixture, "sk-cred-valid")

	reqBody := `{"expected_revision":"0","config":{"schema_version":1,"rules":[]}}`
	req := httptest.NewRequest(http.MethodPut, fmt.Sprintf("/api/groups/%d/credentials/999999/policy", grpID), bytes.NewReader([]byte(reqBody)))
	req.Header.Set("Authorization", "Bearer admin-secret-key")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("PUT credential 999999 policy status = %d; want 404: %s", w.Code, w.Body.String())
	}

	var count int64
	if err := fixture.db.Model(&models.PolicyBinding{}).Where("credential_id = ?", 999999).Count(&count).Error; err != nil {
		t.Fatalf("count policy bindings: %v", err)
	}
	if count != 0 {
		t.Fatalf("found %d orphan policy bindings for non-existent credential 999999", count)
	}
}

func TestPolicyRevisionOverflowRejected(t *testing.T) {
	t.Parallel()
	fixture := newServiceFixture(t)
	engine := setupPolicyTestServer(t, fixture)
	grpID := createUniqueGroupWithCredentials(t, fixture, "sk-overflow-test")

	// 直接在数据库插入 Revision 为 MaxUint64 (^uint64(0)) 的记录
	maxRev := ^uint64(0)
	binding := models.PolicyBinding{
		Scope:         models.PolicyScopeGroup,
		GroupID:       grpID,
		CredentialID:  0,
		Revision:      models.PolicyRevision(maxRev),
		SchemaVersion: 1,
		Config:        models.JSON(`{"schema_version":1,"rules":[]}`),
		CreatedAtMS:   1000,
		UpdatedAtMS:   1000,
	}
	if err := fixture.db.Create(&binding).Error; err != nil {
		t.Fatalf("create max revision binding: %v", err)
	}

	// 尝试在 maxRev 基础上继续 CAS 更新
	reqBody := fmt.Sprintf(`{"expected_revision":"%d","config":{"schema_version":1,"rules":[]}}`, maxRev)
	req := httptest.NewRequest(http.MethodPut, fmt.Sprintf("/api/groups/%d/policy", grpID), bytes.NewReader([]byte(reqBody)))
	req.Header.Set("Authorization", "Bearer admin-secret-key")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("PUT /api/groups/%d/policy with max revision status = %d; want 409: %s", grpID, w.Code, w.Body.String())
	}

	// 验证数据库中 Revision 原值未变，未回绕为 0，且 UpdatedAtMS 未被篡改
	var reloaded models.PolicyBinding
	if err := fixture.db.First(&reloaded, binding.ID).Error; err != nil {
		t.Fatalf("reload binding: %v", err)
	}
	if uint64(reloaded.Revision) != maxRev {
		t.Fatalf("binding revision wrapped or changed: got %d, want %d", reloaded.Revision, maxRev)
	}
	if reloaded.UpdatedAtMS != 1000 {
		t.Fatalf("binding was modified on overflow: UpdatedAtMS = %d, want 1000", reloaded.UpdatedAtMS)
	}
}

func TestPolicyMalformedBindingRowFailsCompileOnRead(t *testing.T) {
	t.Parallel()
	fixture := newServiceFixture(t)
	engine := setupPolicyTestServer(t, fixture)
	grpID := createUniqueGroupWithCredentials(t, fixture, "sk-malformed-read")

	// 插入一条具有不支持未来版本或损坏内容的记录
	corruptBinding := models.PolicyBinding{
		Scope:         models.PolicyScopeGroup,
		GroupID:       grpID,
		CredentialID:  0,
		Revision:      1,
		SchemaVersion: 999, // 未来不支持的版本
		Config:        models.JSON(`{"schema_version":999,"rules":[]}`),
		CreatedAtMS:   1000,
		UpdatedAtMS:   1000,
	}
	if err := fixture.db.Create(&corruptBinding).Error; err != nil {
		t.Fatalf("create corrupt binding: %v", err)
	}

	// GET 读取时必须经由 policy.Compile 严格验证，拒绝伪装成 schema_version=1 或空规则
	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/groups/%d/policy", grpID), nil)
	req.Header.Set("Authorization", "Bearer admin-secret-key")
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("GET /api/groups/%d/policy for corrupt storage status = %d; want 500: %s", grpID, w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), `"schema_version":1`) {
		t.Fatalf("corrupt storage response disguised as schema_version=1: %s", w.Body.String())
	}
}
