package control

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"gpt-load/internal/policy"
	"gpt-load/internal/state"
	"gpt-load/internal/storage/models"
	"gpt-load/internal/subscription/providers/claude"
	po "gpt-load/internal/subscription/providers/observation"
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

	// 全局视图不携带观测窗口：必须是已初始化的空数组，而非 null。
	if env.Data.QuotaWindows == nil || len(env.Data.QuotaWindows) != 0 {
		t.Errorf("expected global quota_windows = [], got %#v", env.Data.QuotaWindows)
	}
}

func applyDiscoveryQuotaWindow(t *testing.T, fixture serviceFixture, credentialID uint, windowSeconds int64) {
	t.Helper()
	ref, ok := fixture.registry.CredentialRef(credentialID)
	if !ok {
		t.Fatalf("credential %d missing from registry", credentialID)
	}
	now := fixture.service.now()
	observed, reset := now.Add(-time.Hour).UnixMilli(), now.Add(time.Hour).UnixMilli()
	seconds, utilization := windowSeconds, 0.5
	if !fixture.registry.ApplyQuotaWindows(credentialID, ref.IdentityGeneration, []po.QuotaWindow{{
		Scope:         "account",
		WindowSeconds: &seconds,
		ResetAtMS:     &reset,
		ObservedAtMS:  &observed,
		Utilization:   &utilization,
		State:         "available",
		SourceID:      "codex",
	}}) {
		t.Fatalf("ApplyQuotaWindows(%d) returned false", credentialID)
	}
}

func TestPolicyDiscovery_ScopedQuotaWindows(t *testing.T) {
	fixture := newServiceFixture(t)
	engine := setupPolicyTestServer(t, fixture)
	groupID := createUniqueGroupWithCredentials(t, fixture, "sk-disc-a\nsk-disc-b")
	otherGroupID := createUniqueGroupWithCredentials(t, fixture, "sk-disc-other")

	var creds []models.Credential
	if err := fixture.db.Where("group_id = ?", groupID).Order("id ASC").Find(&creds).Error; err != nil || len(creds) != 2 {
		t.Fatalf("query group credentials: %v (%d)", err, len(creds))
	}
	var otherCred models.Credential
	if err := fixture.db.Where("group_id = ?", otherGroupID).First(&otherCred).Error; err != nil {
		t.Fatalf("query other credential: %v", err)
	}
	applyDiscoveryQuotaWindow(t, fixture, creds[0].ID, 18000)
	applyDiscoveryQuotaWindow(t, fixture, creds[1].ID, 604800)

	get := func(query string) (int, []int64) {
		req := httptest.NewRequest(http.MethodGet, "/api/policy/discovery"+query, nil)
		req.Header.Set("Authorization", "Bearer admin-secret-key")
		rec := httptest.NewRecorder()
		engine.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			return rec.Code, nil
		}
		var env policyDiscoveryEnvelope
		if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
			t.Fatalf("decode %q: %v", query, err)
		}
		return rec.Code, env.Data.QuotaWindows
	}

	for _, test := range []struct {
		name  string
		query string
		code  int
		want  []int64
	}{
		{"global stays empty", "", http.StatusOK, []int64{}},
		{"group union", fmt.Sprintf("?group_id=%d", groupID), http.StatusOK, []int64{18000, 604800}},
		{"credential scope", fmt.Sprintf("?group_id=%d&credential_id=%d", groupID, creds[0].ID), http.StatusOK, []int64{18000}},
		{"credential requires group", fmt.Sprintf("?credential_id=%d", creds[0].ID), http.StatusBadRequest, nil},
		{"unknown group", "?group_id=999999", http.StatusNotFound, nil},
		{"foreign credential", fmt.Sprintf("?group_id=%d&credential_id=%d", groupID, otherCred.ID), http.StatusNotFound, nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			code, windows := get(test.query)
			if code != test.code {
				t.Fatalf("status = %d, want %d", code, test.code)
			}
			if test.want != nil && !reflect.DeepEqual(windows, test.want) {
				t.Fatalf("windows = %#v, want %#v", windows, test.want)
			}
		})
	}

	req := httptest.NewRequest(http.MethodGet, "/api/policy/discovery", nil)
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 without auth, got %d", rec.Code)
	}
}

func TestObservedAccountQuotaWindows(t *testing.T) {
	fact := func(scope string, seconds int, source string, generation uint64) policy.QuotaWindowFact {
		return policy.QuotaWindowFact{Scope: scope, WindowSeconds: seconds, SourceID: source, IdentityGeneration: generation}
	}
	view := func(id uint, generation uint64, facts ...policy.QuotaWindowFact) state.CredentialRuntimeView {
		return state.CredentialRuntimeView{ID: id, GroupID: 7, IdentityGeneration: generation, QuotaWindows: facts}
	}

	for _, test := range []struct {
		name  string
		views []state.CredentialRuntimeView
		want  []int64
	}{
		{
			name: "union sorted and deduped",
			views: []state.CredentialRuntimeView{
				view(1, 1, fact("account", 604800, "codex", 1)),
				view(2, 1, fact("account", 18000, "codex", 1), fact("account", 3600, "codex", 1)),
				view(3, 1, fact("account", 604800, "codex_bengalfox", 1)),
			},
			want: []int64{3600, 18000, 604800},
		},
		{
			name: "model scope excluded",
			views: []state.CredentialRuntimeView{
				view(1, 1, fact("model", 604800, "codex", 1), fact("account", 18000, "codex", 1)),
			},
			want: []int64{18000},
		},
		{
			name:  "optional source included",
			views: []state.CredentialRuntimeView{view(1, 1, fact("account", 18000, "", 1))},
			want:  []int64{18000},
		},
		{
			name:  "stale identity excluded",
			views: []state.CredentialRuntimeView{view(1, 2, fact("account", 18000, "codex", 1))},
			want:  []int64{},
		},
		{
			name:  "no observation stays empty",
			views: nil,
			want:  []int64{},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := observedAccountQuotaWindows(test.views, 7, nil)
			if got == nil {
				t.Fatal("expected non-nil empty catalog")
			}
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("windows = %#v, want %#v", got, test.want)
			}
		})
	}
}

func TestObservedAccountQuotaWindowsIncludesClaudePeriods(t *testing.T) {
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	reset := now.Add(time.Hour).Format(time.RFC3339)
	utilization := 95.0
	raw, err := claude.NormalizeObservation(claude.AccountObservation{Usage: claude.Usage{
		FiveHour: &claude.UsageWindow{Utilization: &utilization, ResetsAt: &reset},
		SevenDay: &claude.UsageWindow{Utilization: &utilization, ResetsAt: &reset},
	}})
	if err != nil {
		t.Fatal(err)
	}
	var snap po.Snapshot
	if err := json.Unmarshal(raw, &snap); err != nil {
		t.Fatal(err)
	}
	at := now.UnixMilli()
	for i := range snap.QuotaWindows {
		snap.QuotaWindows[i].ObservedAtMS = &at
	}
	facts := state.NormalizeQuotaWindows(snap.QuotaWindows, 7)
	for _, f := range facts {
		if f.State != policy.FactStateMeasured {
			t.Fatalf("fact unavailable: %#v", f)
		}
	}
	got := observedAccountQuotaWindows([]state.CredentialRuntimeView{{ID: 3, GroupID: 1, IdentityGeneration: 7, QuotaWindows: facts}}, 1, nil)
	if !reflect.DeepEqual(got, []int64{18000, 604800}) {
		t.Fatalf("real Claude account windows missing: got %v want [18000 604800]", got)
	}
}
