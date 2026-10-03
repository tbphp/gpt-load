package control

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

type policyDiscoveryEnvelope struct {
	Code    int                     `json:"code"`
	Message string                  `json:"message"`
	Data    PolicyDiscoveryResponse `json:"data"`
}

func TestPolicyDiscovery_AdminRequiredAndTruthfulCapabilities(t *testing.T) {
	fixture := newServiceFixture(t)
	server := setupPolicyTestServer(t, fixture)

	// 1. 未授权访问应返回 401 Unauthorized
	reqUnauth := httptest.NewRequest(http.MethodGet, "/api/policy/discovery", nil)
	recUnauth := httptest.NewRecorder()
	server.ServeHTTP(recUnauth, reqUnauth)
	if recUnauth.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 Unauthorized without auth, got %d", recUnauth.Code)
	}

	// 2. 管理员访问返回 200 OK
	reqAuth := httptest.NewRequest(http.MethodGet, "/api/policy/discovery", nil)
	reqAuth.Header.Set("Authorization", "Bearer admin-secret-key")
	recAuth := httptest.NewRecorder()
	server.ServeHTTP(recAuth, reqAuth)
	if recAuth.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", recAuth.Code, recAuth.Body.String())
	}

	var env policyDiscoveryEnvelope
	if err := json.Unmarshal(recAuth.Body.Bytes(), &env); err != nil {
		t.Fatalf("failed to unmarshal discovery response: %v", err)
	}

	disc := env.Data

	// 验证真实能力声明（不虚假承诺）
	if !disc.Capabilities.AccountWise {
		t.Errorf("expected Capabilities.AccountWise = true")
	}
	if disc.Capabilities.GroupAggregation {
		t.Errorf("expected Capabilities.GroupAggregation = false")
	}
	if disc.Capabilities.FixedRecovery != "unsupported" {
		t.Errorf("expected Capabilities.FixedRecovery = 'unsupported', got %q", disc.Capabilities.FixedRecovery)
	}
	if disc.Capabilities.LiveDynamicPricing {
		t.Errorf("expected Capabilities.LiveDynamicPricing = false")
	}

	// 验证参数元数据
	paramKeys := make(map[string]bool)
	for _, p := range disc.Parameters {
		paramKeys[p.Key] = true
	}
	for _, requiredKey := range []string{"request.model", "upstream.model", "credential.quota.remaining_ratio"} {
		if !paramKeys[requiredKey] {
			t.Errorf("expected parameter %q in discovery list", requiredKey)
		}
	}

	// 验证谓词元数据
	predicateNames := make(map[string]bool)
	for _, pred := range disc.Predicates {
		predicateNames[pred.Name] = true
	}
	if !predicateNames["time_window"] {
		t.Errorf("expected predicate 'time_window' in discovery list")
	}

	// 验证动作元数据
	actionTypes := make(map[string]bool)
	for _, act := range disc.Actions {
		actionTypes[string(act.Type)] = true
	}
	if !actionTypes["exclude_candidate"] {
		t.Errorf("expected action 'exclude_candidate' in discovery list")
	}
	if !actionTypes["multiply_price"] {
		t.Errorf("expected action 'multiply_price' in discovery list")
	}
}
