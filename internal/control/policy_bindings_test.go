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

// createUniqueGroupWithCredentials 与共享的 createGroupWithCredentials 同签名，
// 但每个分组使用独立的 compatible channel target；同一 fixture 内建多个分组时共享 helper 会撞 target。
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

func groupPolicyPath(groupID uint) string {
	return fmt.Sprintf("/api/groups/%d/policy", groupID)
}

func credentialPolicyPath(groupID, credentialID uint) string {
	return fmt.Sprintf("/api/groups/%d/credentials/%d/policy", groupID, credentialID)
}

// policyRequest 发起带管理员鉴权的策略请求。
func policyRequest(engine *gin.Engine, method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer admin-secret-key")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)
	return w
}

// getPolicy 读取策略并要求 200。
func getPolicy(t *testing.T, engine *gin.Engine, path string) PolicyBindingResponse {
	t.Helper()
	w := policyRequest(engine, http.MethodGet, path, "")
	if w.Code != http.StatusOK {
		t.Fatalf("GET %s status = %d: %s", path, w.Code, w.Body.String())
	}
	return parsePolicyResponse(t, w.Body.Bytes())
}

// putPolicy 保存策略并要求 200。
func putPolicy(t *testing.T, engine *gin.Engine, path, body string) PolicyBindingResponse {
	t.Helper()
	w := policyRequest(engine, http.MethodPut, path, body)
	if w.Code != http.StatusOK {
		t.Fatalf("PUT %s status = %d: %s", path, w.Code, w.Body.String())
	}
	return parsePolicyResponse(t, w.Body.Bytes())
}

// configBody 构造策略更新体。
func configBody(configJSON string) string {
	return fmt.Sprintf(`{"config":%s}`, configJSON)
}

// 1. 未配置时读取 group 与 credential 策略返回默认：schema_version=1, rules=[], revision=0
// newPolicyBindingEnv 建立策略绑定用例共用的 fixture、HTTP 路由与唯一分组。
func newPolicyBindingEnv(t *testing.T, key string) (serviceFixture, *gin.Engine, uint) {
	t.Helper()
	fixture := newServiceFixture(t)
	return fixture, setupPolicyTestServer(t, fixture), createUniqueGroupWithCredentials(t, fixture, key)
}

func TestPolicyDefaultWhenUnconfigured(t *testing.T) {
	t.Parallel()
	fixture, engine, groupID := newPolicyBindingEnv(t, "sk-test-unconfigured")

	var cred models.Credential
	if err := fixture.db.Where("group_id = ?", groupID).First(&cred).Error; err != nil {
		t.Fatalf("query credential: %v", err)
	}

	groupData := getPolicy(t, engine, groupPolicyPath(groupID))
	if groupData.Scope != "group" || groupData.ID != groupID || groupData.RevisionText != "0" || parseConfigRulesCount(t, groupData.ConfigText) != 0 {
		t.Fatalf("unexpected group default policy data: %+v", groupData)
	}
	credData := getPolicy(t, engine, credentialPolicyPath(groupID, cred.ID))
	if credData.Scope != "credential" || credData.ID != cred.ID || credData.RevisionText != "0" || parseConfigRulesCount(t, credData.ConfigText) != 0 {
		t.Fatalf("unexpected credential default policy data: %+v", credData)
	}
}

// 2. 两种 scope 隔离：同一 Group 下的 group 策略与 credential 策略独立存储，互不影响
func TestPolicyScopeIsolation(t *testing.T) {
	t.Parallel()
	fixture, engine, groupID := newPolicyBindingEnv(t, "sk-test-scope-isolation")

	var cred models.Credential
	if err := fixture.db.Where("group_id = ?", groupID).First(&cred).Error; err != nil {
		t.Fatalf("query credential: %v", err)
	}

	grpRes := putPolicy(t, engine, groupPolicyPath(groupID), configBody(`{"schema_version":1,"rules":[{"id":"r-grp","name":"grp rule","domain":"scheduling","enabled":true,"when":{"fact":"request.model","op":"eq","value":"gpt-4o"},"actions":[{"type":"exclude_candidate"}]}]}`))
	if grpRes.RevisionText != "1" || parseConfigRulesCount(t, grpRes.ConfigText) != 1 {
		t.Fatalf("expected group revision 1 and 1 rule, got %+v", grpRes)
	}
	if fixture.manager.Current().Policies == nil || fixture.manager.Current().Policies.GroupPolicy(groupID) == nil {
		t.Fatal("group policy was not published to the live snapshot")
	}

	// 此时 credential 策略依然是未配置。
	credRes := getPolicy(t, engine, credentialPolicyPath(groupID, cred.ID))
	if credRes.RevisionText != "0" || parseConfigRulesCount(t, credRes.ConfigText) != 0 {
		t.Fatalf("credential policy was polluted by group policy: %+v", credRes)
	}

	credRes2 := putPolicy(t, engine, credentialPolicyPath(groupID, cred.ID), configBody(`{"schema_version":1,"rules":[{"id":"r-crd","name":"crd rule","domain":"pricing","enabled":true,"when":{"fact":"credential.quota.remaining_ratio","select":{"scope":"account","window_seconds":18000},"reduce":"min","op":"lt","value":0.1},"actions":[{"type":"multiply_price","factor":"1.5"}]}]}`))
	if credRes2.RevisionText != "1" || parseConfigRulesCount(t, credRes2.ConfigText) != 1 {
		t.Fatalf("expected credential revision 1 and 1 rule, got %+v", credRes2)
	}
	if fixture.manager.Current().Policies == nil || fixture.manager.Current().Policies.CredentialPolicy(cred.ID) == nil {
		t.Fatal("credential policy was not published to the live snapshot")
	}

	grpRes2 := getPolicy(t, engine, groupPolicyPath(groupID))
	if grpRes2.RevisionText != "1" || parseConfigRulesCount(t, grpRes2.ConfigText) != 1 {
		t.Fatalf("group policy was corrupted: %+v", grpRes2)
	}
}

// 3. credential 跨 group 越权 404 / 拒绝，不泄露凭据信息
func TestPolicyCredentialCrossGroupUnauthorized(t *testing.T) {
	t.Parallel()
	fixture, engine, groupA := newPolicyBindingEnv(t, "sk-group-a")
	groupB := createUniqueGroupWithCredentials(t, fixture, "sk-group-b")

	var credB models.Credential
	if err := fixture.db.Where("group_id = ?", groupB).First(&credB).Error; err != nil {
		t.Fatalf("query credB: %v", err)
	}

	path := credentialPolicyPath(groupA, credB.ID)
	if w := policyRequest(engine, http.MethodGet, path, ""); w.Code != http.StatusNotFound {
		t.Fatalf("cross-group GET credential policy status = %d, want 404", w.Code)
	}
	if w := policyRequest(engine, http.MethodPut, path, configBody(`{"schema_version":1,"rules":[]}`)); w.Code != http.StatusNotFound {
		t.Fatalf("cross-group PUT credential policy status = %d, want 404", w.Code)
	}
}

// 4. 显式合法空 rules 清空规则
func TestPolicyExplicitEmptyRulesClears(t *testing.T) {
	t.Parallel()
	_, engine, groupID := newPolicyBindingEnv(t, "sk-test-empty-rules")
	path := groupPolicyPath(groupID)

	res1 := putPolicy(t, engine, path, configBody(`{"schema_version":1,"rules":[{"id":"r-1","name":"r1","domain":"scheduling","enabled":true,"when":{"fact":"request.model","op":"eq","value":"gpt-4o"},"actions":[{"type":"exclude_candidate"}]}]}`))
	if res1.RevisionText != "1" || parseConfigRulesCount(t, res1.ConfigText) != 1 {
		t.Fatalf("unexpected res1: %+v", res1)
	}

	// 显式空 rules 清空 (revision 1 -> 2)。
	res2 := putPolicy(t, engine, path, configBody(`{"schema_version":1,"rules":[]}`))
	if res2.RevisionText != "2" || parseConfigRulesCount(t, res2.ConfigText) != 0 {
		t.Fatalf("expected cleared rules with revision 2, got %+v", res2)
	}
	if res3 := getPolicy(t, engine, path); res3.RevisionText != "2" || parseConfigRulesCount(t, res3.ConfigText) != 0 {
		t.Fatalf("GET after clear got %+v", res3)
	}
}

// 5. 遗漏 config 字段时保持已有配置不变
func TestPolicyOmittedConfigPreservesExisting(t *testing.T) {
	t.Parallel()
	_, engine, groupID := newPolicyBindingEnv(t, "sk-test-omitted-config")
	path := groupPolicyPath(groupID)

	putPolicy(t, engine, path, configBody(`{"schema_version":1,"rules":[{"id":"r-keep","name":"keep me","domain":"scheduling","enabled":true,"when":{"fact":"request.model","op":"eq","value":"gpt-4o"},"actions":[{"type":"exclude_candidate"}]}]}`))

	// 遗漏 config 字段的更新（只有 expected_revision: "1"）。
	res2 := putPolicy(t, engine, path, `{"expected_revision":"1"}`)
	if parseConfigRulesCount(t, res2.ConfigText) != 1 {
		t.Fatalf("expected preserved 1 rule, got %s", res2.ConfigText)
	}
	if res3 := getPolicy(t, engine, path); parseConfigRulesCount(t, res3.ConfigText) != 1 {
		t.Fatalf("GET expected preserved 1 rule, got %s", res3.ConfigText)
	}
}

// 6. null / 空白 / 坏 JSON / 非法 disabled 规则均拒绝且绝不写库（未知扩展字段按前向兼容忽略）
func TestPolicyInvalidConfigurationsRejected(t *testing.T) {
	t.Parallel()
	_, engine, groupID := newPolicyBindingEnv(t, "sk-test-invalids")
	path := groupPolicyPath(groupID)

	// 先写入一个合法的基准策略 (revision 1)。
	putPolicy(t, engine, path, configBody(`{"schema_version":1,"rules":[{"id":"r-base","name":"base","domain":"scheduling","enabled":true,"when":{"fact":"request.model","op":"eq","value":"gpt-4o"},"actions":[{"type":"exclude_candidate"}]}]}`))

	for _, tc := range []struct{ name, payload string }{
		{"null config", `{"expected_revision":"1","config":null}`},
		{"empty string config", `{"expected_revision":"1","config":""}`},
		{"malformed JSON", `{"expected_revision":"1","config":{bad_json}}`},
		{"illegal disabled rule", `{"expected_revision":"1","config":{"schema_version":1,"rules":[{"id":"r-dis","name":"bad","domain":"scheduling","enabled":false,"when":{"fact":"unknown.fact","op":"eq","value":"val"},"actions":[{"type":"exclude_candidate"}]}]}}`},
		{"out of range quota threshold", `{"expected_revision":"1","config":{"schema_version":1,"rules":[{"id":"r-dis","name":"bad","domain":"scheduling","enabled":true,"when":{"fact":"credential.quota.remaining_ratio","select":{"scope":"account","window_seconds":18000},"reduce":"min","op":"lt","value":1.1},"actions":[{"type":"exclude_candidate"}]}]}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if w := policyRequest(engine, http.MethodPut, path, tc.payload); w.Code != http.StatusBadRequest {
				t.Fatalf("%s returned status %d, want 400: %s", tc.name, w.Code, w.Body.String())
			}
			// 数据库记录未被篡改，依然是基准配置，版本仍为 1。
			res := getPolicy(t, engine, path)
			if res.RevisionText != "1" || parseConfigRulesCount(t, res.ConfigText) != 1 {
				t.Fatalf("database content was corrupted after %s: %+v", tc.name, res)
			}
		})
	}
}

// 7. 并发保存：最新写入覆盖，全部成功且 revision 依次递增
func TestPolicyConcurrentLastWriteWins(t *testing.T) {
	t.Parallel()
	_, engine, groupID := newPolicyBindingEnv(t, "sk-test-concurrent-cas")
	path := groupPolicyPath(groupID)

	const concurrency = 10
	var wg sync.WaitGroup
	var successCount atomic.Int32
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			rule := fmt.Sprintf(`{"schema_version":1,"rules":[{"id":"r-%d","name":"concurrent","domain":"scheduling","enabled":true,"when":{"fact":"request.model","op":"eq","value":"gpt-4o"},"actions":[{"type":"exclude_candidate"}]}]}`, idx)
			if w := policyRequest(engine, http.MethodPut, path, configBody(rule)); w.Code == http.StatusOK {
				successCount.Add(1)
			}
		}(i)
	}
	wg.Wait()

	if successCount.Load() != concurrency {
		t.Fatalf("expected all %d concurrent saves to succeed, got %d", concurrency, successCount.Load())
	}
	if res := getPolicy(t, engine, path); res.RevisionText != fmt.Sprintf("%d", concurrency) {
		t.Fatalf("expected final revision %d, got %s", concurrency, res.RevisionText)
	}
}

// 8. 旧 Group Overrides 更新绝不影响已存 Policy
func TestPolicyOldGroupOverridesDoesNotAffectPolicy(t *testing.T) {
	t.Parallel()
	_, engine, groupID := newPolicyBindingEnv(t, "sk-test-isolation-from-overrides")
	path := groupPolicyPath(groupID)

	putPolicy(t, engine, path, configBody(`{"schema_version":1,"rules":[{"id":"r-safe","name":"safe rule","domain":"scheduling","enabled":true,"when":{"fact":"request.model","op":"eq","value":"gpt-4o"},"actions":[{"type":"exclude_candidate"}]}]}`))

	// 通过旧的 UpdateGroupSettings 接口更新 Group Overrides。
	wSettings := policyRequest(engine, http.MethodPut, fmt.Sprintf("/api/groups/%d/settings", groupID), `{"overrides":{"concurrency_limit":42}}`)
	if wSettings.Code != http.StatusOK {
		t.Fatalf("update settings error: %s", wSettings.Body.String())
	}

	res := getPolicy(t, engine, path)
	if res.RevisionText != "1" || parseConfigRulesCount(t, res.ConfigText) != 1 {
		t.Fatalf("policy was affected by group overrides update! got %+v", res)
	}
}

// 9. 管理员权限校验：无 Authorization 或非管理员拒绝
func TestPolicyRequiresAdminAuth(t *testing.T) {
	t.Parallel()
	_, engine, groupID := newPolicyBindingEnv(t, "sk-test-auth")
	path := groupPolicyPath(groupID)

	// 无 Token 请求 GET -> 401。
	reqAnon := httptest.NewRequest(http.MethodGet, path, nil)
	wAnon := httptest.NewRecorder()
	engine.ServeHTTP(wAnon, reqAnon)
	if wAnon.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous request expected 401, got %d", wAnon.Code)
	}

	// 错误 Token 请求 GET -> 401。
	reqBad := httptest.NewRequest(http.MethodGet, path, nil)
	reqBad.Header.Set("Authorization", "Bearer wrong-key")
	wBad := httptest.NewRecorder()
	engine.ServeHTTP(wBad, reqBad)
	if wBad.Code != http.StatusUnauthorized {
		t.Fatalf("bad token request expected 401, got %d", wBad.Code)
	}
}

// assertPolicyUpdate404LeavesNoOrphan 断言不存在的目标返回 404，且该作用域没有写入孤儿绑定。
func assertPolicyUpdate404LeavesNoOrphan(t *testing.T, engine *gin.Engine, fixture serviceFixture, path, column string) {
	t.Helper()
	if w := policyRequest(engine, http.MethodPut, path, configBody(`{"schema_version":1,"rules":[]}`)); w.Code != http.StatusNotFound {
		t.Fatalf("PUT %s status = %d; want 404: %s", path, w.Code, w.Body.String())
	}
	var count int64
	if err := fixture.db.Model(&models.PolicyBinding{}).Where(column+" = ?", 999999).Count(&count).Error; err != nil {
		t.Fatalf("count policy bindings: %v", err)
	}
	if count != 0 {
		t.Fatalf("found %d orphan policy bindings for %s 999999", count, column)
	}
}

func TestPolicyUpdateNonExistentGroupReturns404AndNoOrphan(t *testing.T) {
	t.Parallel()
	fixture := newServiceFixture(t)
	assertPolicyUpdate404LeavesNoOrphan(t, setupPolicyTestServer(t, fixture), fixture, "/api/groups/999999/policy", "group_id")
}

func TestPolicyUpdateNonExistentCredentialReturns404AndNoOrphan(t *testing.T) {
	t.Parallel()
	fixture := newServiceFixture(t)
	grpID := createUniqueGroupWithCredentials(t, fixture, "sk-cred-valid")
	path := fmt.Sprintf("/api/groups/%d/credentials/999999/policy", grpID)
	assertPolicyUpdate404LeavesNoOrphan(t, setupPolicyTestServer(t, fixture), fixture, path, "credential_id")
}

func TestPolicyMalformedBindingRowFailsCompileOnRead(t *testing.T) {
	t.Parallel()
	fixture, engine, grpID := newPolicyBindingEnv(t, "sk-malformed-read")

	// 插入一条不支持的未来版本记录。
	corrupt := models.PolicyBinding{
		Scope:         models.PolicyScopeGroup,
		GroupID:       grpID,
		Revision:      1,
		SchemaVersion: 999,
		Config:        models.JSON(`{"schema_version":999,"rules":[]}`),
		CreatedAtMS:   1000,
		UpdatedAtMS:   1000,
	}
	if err := fixture.db.Create(&corrupt).Error; err != nil {
		t.Fatalf("create corrupt binding: %v", err)
	}

	// GET 必须经由 policy.Compile 严格验证，不得伪装成 schema_version=1 或空规则。
	w := policyRequest(engine, http.MethodGet, groupPolicyPath(grpID), "")
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("GET for corrupt storage status = %d; want 500: %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), `"schema_version":1`) {
		t.Fatalf("corrupt storage response disguised as schema_version=1: %s", w.Body.String())
	}
}

// createTestGroupWithTwoModels 建立带两个同别名上游模型（gpt-4o）的分组，返回分组与首个凭据 ID。
func createTestGroupWithTwoModels(t *testing.T, fixture serviceFixture) (uint, uint) {
	t.Helper()
	seq := testIdempotencySequence.Add(1)
	name := fmt.Sprintf("policy-two-models-%d", seq)
	baseURL := fmt.Sprintf("https://api-two-%d.example.com/v1", seq)
	result, err := fixture.service.CreateGroup(t.Context(), GroupCreateRequest{
		Name:      &name,
		ChannelID: channel.OpenAICompatible,
		Params:    json.RawMessage(fmt.Sprintf(`{"base_url":%q}`, baseURL)),
		Models: optionalGroupModels{
			Set: true,
			Values: []GroupModel{
				{ID: "upstream-gpt4", Alias: "gpt-4o", AliasEnabled: true},
				{ID: "upstream-backup", Alias: "gpt-4o", AliasEnabled: true},
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

// expected_revision 仅为兼容旧客户端保留的可选字段，保存一律按最新写入覆盖。
func TestPolicyUpdateIgnoresExpectedRevision(t *testing.T) {
	fixture := newServiceFixture(t)
	server := setupPolicyTestServer(t, fixture)
	groupID, _ := createTestGroupWithTwoModels(t, fixture)
	emptyCfg := `{"schema_version":1,"rules":[]}`

	for _, tc := range []struct {
		name string
		body string
		want int
	}{
		{"omitted", configBody(emptyCfg), http.StatusOK},
		{"stale legacy revision", fmt.Sprintf(`{"expected_revision":"999","config":%s}`, emptyCfg), http.StatusOK},
		{"numeric revision", fmt.Sprintf(`{"expected_revision":1,"config":%s}`, emptyCfg), http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if w := policyRequest(server, http.MethodPut, groupPolicyPath(groupID), tc.body); w.Code != tc.want {
				t.Fatalf("status = %d, want %d: %s", w.Code, tc.want, w.Body.String())
			}
		})
	}
}

// 策略保存外层信封：大小写别名与未知扩展字段一律拒绝。
func TestPolicyUpdateOuterTransportCanonicalAllowlist(t *testing.T) {
	for _, body := range []string{
		`{"expected_revision":"0","Expected_Revision":"1","config":{"schema_version":1,"rules":[]}}`,
		`{"Expected_Revision":"1","config":{"schema_version":1,"rules":[]}}`,
		`{"expectedRevision":"1","config":{"schema_version":1,"rules":[]}}`,
		`{"extra":"field","expected_revision":"0","config":{"schema_version":1,"rules":[]}}`,
	} {
		if err := decodeStrictControlJSONObject([]byte(body), &PolicyUpdateRequest{}); err == nil {
			t.Errorf("expected rejection for non-canonical or alias outer field, accepted: %s", body)
		}
	}
}

// 保存-读取往返必须逐字保留原始小数文本，避免浮点重编码。
func TestPolicyExactDecimalLiteralPreserved(t *testing.T) {
	f := newServiceFixture(t)
	g, _ := createTestGroupWithTwoModels(t, f)
	raw := `{"expected_revision":"0","config":{"schema_version":1,"rules":[{"id":"q","name":"Q","domain":"scheduling","enabled":true,"when":{"fact":"credential.quota.remaining_ratio","select":{"scope":"account","window_seconds":18000},"reduce":"min","op":"lt","value":0.10000000000000001},"actions":[{"type":"exclude_candidate"}]}]}}`
	var req PolicyUpdateRequest
	if err := decodeStrictControlJSONObject([]byte(raw), &req); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.UpdateGroupPolicy(t.Context(), g, req); err != nil {
		t.Fatal(err)
	}
	dto, err := f.service.GetGroupPolicy(t.Context(), g)
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(dto)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(b, []byte("0.10000000000000001")) {
		t.Fatal("server lost decimal")
	}
	if dto.ConfigText == "" || !strings.Contains(dto.ConfigText, "0.10000000000000001") {
		t.Fatal("ConfigText did not preserve exact decimal literal")
	}
}

// 分组 scope 拒绝 override，凭据 scope 接受 override。
func TestPolicyGroupModeScopeContract(t *testing.T) {
	f := newServiceFixture(t)
	g, c := createTestGroupWithTwoModels(t, f)
	server := setupPolicyTestServer(t, f)

	overrideConfig := `"config":{"schema_version":1,"group_policy":"override","rules":[]}`

	for _, tc := range []struct {
		name   string
		method string
		path   string
		body   string
		want   int
	}{
		{"group binding rejects override", http.MethodPut, fmt.Sprintf("/api/groups/%d/policy", g), `{"expected_revision":"0",` + overrideConfig + `}`, http.StatusBadRequest},
		{"credential binding accepts override", http.MethodPut, fmt.Sprintf("/api/groups/%d/credentials/%d/policy", g, c), `{"expected_revision":"0",` + overrideConfig + `}`, http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := policyRequest(server, tc.method, tc.path, tc.body).Code; got != tc.want {
				t.Fatalf("status = %d, want %d", got, tc.want)
			}
		})
	}
}
