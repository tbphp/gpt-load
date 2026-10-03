package control

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"gpt-load/internal/policy"
	"gpt-load/internal/pricing"
	"gpt-load/internal/state"
	"gpt-load/internal/storage/models"
	po "gpt-load/internal/subscription/providers/observation"
)

func policyPreviewTestRequest(t *testing.T, cfg string) PolicyPreviewRequest {
	t.Helper()
	var r PolicyPreviewRequest
	if err := json.Unmarshal([]byte(`{"request_model":"gpt-4o","config":`+cfg+`}`), &r); err != nil {
		t.Fatal(err)
	}
	return r
}

func TestPolicyPreview_SchedulerHardFilters(t *testing.T) {
	for _, kind := range []string{"auth", "credential_weight", "group_weight"} {
		t.Run(kind, func(t *testing.T) {
			f := newServiceFixture(t)
			g, c := createTestGroupWithTwoModels(t, f)
			z := 0
			switch kind {
			case "auth":
				f.service.registry.SetCredentialAuthState(c, state.CredentialAuthStateRefreshing)
			case "credential_weight":
				if err := f.service.registry.UpdateCredentialConfig(c, state.CredentialStatusActive, &z); err != nil {
					t.Fatal(err)
				}
			case "group_weight":
				snap := f.service.manager.Current()
				group := snap.Groups[g]
				group.WeightManual = &z
				snap.Groups[g] = group
			}
			out, err := f.service.PreviewCredentialPolicy(t.Context(), g, c, policyPreviewTestRequest(t, `{"schema_version":1,"rules":[]}`))
			if err != nil {
				t.Fatal(err)
			}
			for _, target := range out.Candidates[0].Targets {
				if target.Available {
					t.Errorf("%s upstream=%s unexpectedly available=true", kind, target.UpstreamModel)
				}
			}
		})
	}
}

func TestPolicyPreview_CaptureObservationClockOnce(t *testing.T) {
	f := newServiceFixture(t)
	g, c := createTestGroupWithTwoModels(t, f)
	calls := 0
	n := time.Now()
	f.service.now = func() time.Time {
		calls++
		return n.Add(time.Duration(calls) * time.Hour)
	}
	_, err := f.service.PreviewCredentialPolicy(t.Context(), g, c, policyPreviewTestRequest(t, `{"schema_version":1,"rules":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("preview called now %d times; want one observation clock", calls)
	}
}

func TestPolicyPreview_RuleArraysNonNull(t *testing.T) {
	f := newServiceFixture(t)
	g, c := createTestGroupWithTwoModels(t, f)
	out, err := f.service.PreviewCredentialPolicy(t.Context(), g, c, policyPreviewTestRequest(t, `{"schema_version":1,"rules":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range out.Candidates[0].Targets {
		if target.GroupRules == nil || target.CredentialRules == nil {
			t.Errorf("%s has nil arrays (Modern UI dereferences .length)", target.UpstreamModel)
		}
	}
}

func TestPolicyPreview_OuterTransportCanonicalAllowlist(t *testing.T) {
	for _, tc := range []struct {
		body   string
		target any
	}{
		{`{"request_model":"gpt-4o","Request_Model":"upstream-backup"}`, &PolicyPreviewRequest{}},
		{`{"Request_Model":"gpt-4o"}`, &PolicyPreviewRequest{}},
		{`{"requestModel":"gpt-4o"}`, &PolicyPreviewRequest{}},
		{`{"unknown_field":"val"}`, &PolicyPreviewRequest{}},
		{`{"expected_revision":"0","Expected_Revision":"1","config":{"schema_version":1,"rules":[]}}`, &PolicyUpdateRequest{}},
		{`{"Expected_Revision":"1","config":{"schema_version":1,"rules":[]}}`, &PolicyUpdateRequest{}},
		{`{"expectedRevision":"1","config":{"schema_version":1,"rules":[]}}`, &PolicyUpdateRequest{}},
		{`{"extra":"field","expected_revision":"0","config":{"schema_version":1,"rules":[]}}`, &PolicyUpdateRequest{}},
	} {
		if err := decodeStrictControlJSONObject([]byte(tc.body), tc.target); err == nil {
			t.Errorf("expected rejection for non-canonical or alias outer field, accepted: %s", tc.body)
		}
	}
}

func TestPolicyPreview_CumulativeMultiplierExact(t *testing.T) {
	for _, tc := range []struct {
		ms   []pricing.PriceMultiplier
		want string
	}{
		{[]pricing.PriceMultiplier{1, 1}, "0.000000000001"},
		{[]pricing.PriceMultiplier{1000000000, 1000000000, 1000000000, 1000000000, 1000000000}, "1000000000000000"},
	} {
		if got := computeCumulativeMultiplierString(tc.ms); got != tc.want {
			t.Errorf("cumulative %v = %s want %s", tc.ms, got, tc.want)
		}
	}
}

func TestPolicyPreview_ComprehensiveRegressionMatrix(t *testing.T) {
	f := newServiceFixture(t)
	g, c := createTestGroupWithTwoModels(t, f)
	server := setupPolicyTestServer(t, f)
	key, err := f.service.CreateAccessKey(t.Context(), AccessKeyCreateRequest{Name: "preview-denied"})
	if err != nil {
		t.Fatal(err)
	}
	for _, endpoint := range []string{"/api/policy/discovery", fmt.Sprintf("/api/groups/%d/policy/preview", g), fmt.Sprintf("/api/groups/%d/credentials/%d/policy/preview", g, c)} {
		method := http.MethodPost
		if strings.Contains(endpoint, "discovery") {
			method = http.MethodGet
		}
		r := httptest.NewRequest(method, endpoint, strings.NewReader(`{"request_model":"gpt-4o"}`))
		r.Header.Set("Authorization", "Bearer "+key.Key)
		w := httptest.NewRecorder()
		server.ServeHTTP(w, r)
		if w.Code != http.StatusForbidden {
			t.Fatalf("access key %s status=%d", endpoint, w.Code)
		}
	}

	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	real := time.Date(2026, 3, 8, 1, 59, 0, 0, loc)
	f.service.now = func() time.Time { return real }
	ref, _ := f.service.registry.CredentialRef(c)
	quotaDraft := `{"schema_version":1,"rules":[{"id":"q","name":"Quota","domain":"scheduling","enabled":true,"when":{"fact":"credential.quota.remaining_ratio","op":"lt","value":0.1,"select":{"scope":"account","window_seconds":18000},"reduce":"min"},"then":{"type":"exclude_candidate"}}]}`
	for _, qcase := range []string{"valid", "reset", "future", "missing"} {
		observed := real.Add(-time.Hour).UnixMilli()
		reset := real.Add(time.Hour).UnixMilli()
		sec := int64(18000)
		u := 0.95
		if qcase == "reset" {
			reset = real.Add(-time.Minute).UnixMilli()
		}
		if qcase == "future" {
			observed = real.Add(time.Minute).UnixMilli()
		}
		w := po.QuotaWindow{Scope: "account", WindowSeconds: &sec, ResetAtMS: &reset, ObservedAtMS: &observed, Utilization: &u, State: "available"}
		if qcase == "missing" {
			w.ResetAtMS = nil
		}
		if !f.service.registry.ApplyQuotaWindows(c, ref.IdentityGeneration, []po.QuotaWindow{w}) {
			t.Fatal("apply")
		}
		for _, sim := range []string{"2020-01-01T00:00:00Z", "2030-01-01T00:00:00Z"} {
			req := policyPreviewTestRequest(t, quotaDraft)
			req.SimulatedTime = &sim
			res, err := f.service.PreviewCredentialPolicy(t.Context(), g, c, req)
			if err != nil {
				t.Fatal(err)
			}
			if got := res.Candidates[0].Targets[0].Scheduling.Excluded; got != (qcase == "valid") {
				t.Fatalf("%s simulated %s exclusion=%t", qcase, sim, got)
			}
		}
	}

	sim := "2026-03-08T07:30:00Z"
	req := policyPreviewTestRequest(t, `{"schema_version":1,"rules":[{"id":"dst","name":"DST","domain":"scheduling","enabled":true,"when":{"predicate":"time_window","weekdays":[0],"ranges":[["03:00","04:00"]]},"then":{"type":"exclude_candidate"}}]}`)
	req.SimulatedTime = &sim
	res, err := f.service.PreviewCredentialPolicy(t.Context(), g, c, req)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Candidates[0].Targets[0].Scheduling.Excluded || *res.SimulatedTime != "2026-03-08T03:30:00-04:00" {
		t.Fatalf("DST preview=%+v", res)
	}

	before := f.service.registry.Snapshot()
	fairBefore := f.service.registry.SchedulingState().CaptureCheckpoint()
	snap := f.service.manager.Current()
	quotaBefore := f.accessQuota.Snapshot(key.ID, real)
	for i := 0; i < 5; i++ {
		_, err = f.service.PreviewCredentialPolicy(t.Context(), g, c, req)
		if err != nil {
			t.Fatal(err)
		}
	}
	if !reflect.DeepEqual(fairBefore, f.service.registry.SchedulingState().CaptureCheckpoint()) {
		t.Fatal("fairness/model cursors mutated")
	}
	if !reflect.DeepEqual(before, f.service.registry.Snapshot()) || snap != f.service.manager.Current() {
		t.Fatal("mutated registry/publication")
	}
	if !reflect.DeepEqual(quotaBefore, f.accessQuota.Snapshot(key.ID, real)) {
		t.Fatal("access quota mutated")
	}
	var count int64
	f.db.Model(&models.PolicyBinding{}).Count(&count)
	if count != 0 {
		t.Fatal("policy DB written")
	}

	// Aggregate nodes rejection (2 candidates * 2 targets * 3100 = 12400 > 10000).
	cfg := heavyAggregatePolicyConfig()
	compiled, err := policy.Compile([]byte(cfg))
	if err != nil || compiled.NodeCount() != 3100 {
		t.Fatalf("compile heavy: %v", err)
	}
	_, err = f.service.PreviewGroupPolicy(t.Context(), g, policyPreviewTestRequest(t, cfg))
	if err == nil {
		t.Fatal("aggregate >10000 admitted")
	}

	// Exact 512 KiB envelope validation through HTTP.
	for _, size := range []int{maxStrictPolicyRequestBytes, maxStrictPolicyRequestBytes + 1} {
		base := `{"request_model":"gpt-4o"}`
		body := base + strings.Repeat(" ", size-len(base))
		r := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/groups/%d/policy/preview", g), bytes.NewBufferString(body))
		r.Header.Set("Authorization", "Bearer admin-secret-key")
		w := httptest.NewRecorder()
		server.ServeHTTP(w, r)
		if size == maxStrictPolicyRequestBytes && w.Code != 200 {
			t.Fatalf("exact bytes status %d", w.Code)
		}
		if size > maxStrictPolicyRequestBytes && w.Code != 413 {
			t.Fatalf("over bytes status %d", w.Code)
		}
	}

	deep := `{"request_model":"gpt-4o","config":` + strings.Repeat("[", 33) + `0` + strings.Repeat("]", 33) + `}`
	if err = decodeStrictControlJSONObject([]byte(deep), &PolicyPreviewRequest{}); err == nil || !strings.Contains(err.Error(), "nesting depth") {
		t.Fatalf("deep error=%v", err)
	}
}

func TestPolicyPreview_BackendExactCAS(t *testing.T) {
	f := newServiceFixture(t)
	g, _ := createTestGroupWithTwoModels(t, f)
	max := ^uint64(0)
	cfg := models.JSON(`{"schema_version":1,"rules":[]}`)
	binding := models.PolicyBinding{Scope: models.PolicyScopeGroup, GroupID: g, Revision: models.PolicyRevision(max - 1), SchemaVersion: 1, Config: cfg}
	if err := f.db.Create(&binding).Error; err != nil {
		t.Fatal(err)
	}
	read, err := f.service.GetGroupPolicy(t.Context(), g)
	if err != nil || read.RevisionText != "18446744073709551614" {
		t.Fatalf("GET %+v %v", read, err)
	}
	var req PolicyUpdateRequest
	if err := decodeStrictControlJSONObject([]byte(`{"expected_revision":"18446744073709551614","config":{"schema_version":1,"rules":[]}}`), &req); err != nil {
		t.Fatal(err)
	}
	if req.ExpectedRevision == nil || *req.ExpectedRevision != "18446744073709551614" {
		t.Fatal("CAS loss")
	}
	saved, err := f.service.UpdateGroupPolicy(t.Context(), g, req)
	if err != nil || saved.RevisionText != "18446744073709551615" {
		t.Fatalf("save %+v %v", saved, err)
	}
	maxStr := "18446744073709551615"
	req.ExpectedRevision = &maxStr
	_, err = f.service.UpdateGroupPolicy(t.Context(), g, req)
	if err == nil {
		t.Fatal("overflow not rejected")
	}
}

func TestPolicyPreview_ClearAndModelCooldown(t *testing.T) {
	f := newServiceFixture(t)
	g, c := createTestGroupWithTwoModels(t, f)
	cfg := `{"schema_version":1,"rules":[{"id":"r","name":"R","domain":"scheduling","enabled":true,"when":{"fact":"request.model","op":"eq","value":"gpt-4o"},"then":{"type":"exclude_candidate"}}]}`
	var update PolicyUpdateRequest
	_ = json.Unmarshal([]byte(`{"expected_revision":"0","config":`+cfg+`}`), &update)
	if _, err := f.service.UpdateGroupPolicy(t.Context(), g, update); err != nil {
		t.Fatal(err)
	}
	clear := policyPreviewTestRequest(t, `{"schema_version":1,"rules":[]}`)
	res, err := f.service.PreviewGroupPolicy(t.Context(), g, clear)
	if err != nil {
		t.Fatal(err)
	}
	if res.Candidates[0].Targets[0].Scheduling.Excluded {
		t.Fatal("clear fell back saved")
	}
	ref, _ := f.service.registry.CredentialRef(c)
	now := f.service.now()
	f.service.registry.SetModelCooldown(ref, "upstream-backup", now.Add(time.Hour), now)
	res, err = f.service.PreviewGroupPolicy(t.Context(), g, clear)
	if err != nil {
		t.Fatal(err)
	}
	for _, cand := range res.Candidates {
		if cand.CredentialID == c {
			for _, target := range cand.Targets {
				if target.UpstreamModel == "upstream-backup" && (target.Available || target.HardFilterState != "model_cooldown") {
					t.Fatal("backup cooldown ignored")
				}
				if target.UpstreamModel == "upstream-gpt4" && !target.Available {
					t.Fatal("other target wrongly cooled")
				}
			}
		}
	}
}

func TestPolicyPreview_ExactDecimalLiteralPreserved(t *testing.T) {
	f := newServiceFixture(t)
	g, _ := createTestGroupWithTwoModels(t, f)
	raw := `{"expected_revision":"0","config":{"schema_version":1,"rules":[{"id":"q","name":"Q","domain":"scheduling","enabled":true,"when":{"fact":"credential.quota.remaining_ratio","select":{"scope":"account","window_seconds":18000},"reduce":"min","op":"lt","value":0.10000000000000001},"then":{"type":"exclude_candidate"}}]}}`
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

func TestPolicyPreview_Regression_DetachedNameCapture(t *testing.T) {
	f := newServiceFixture(t)
	g, c := createTestGroupWithTwoModels(t, f)
	f.service.registry.UpdateCredentialName(g, c, "captured-old-name")
	n := time.Now()
	f.service.now = func() time.Time {
		f.service.registry.UpdateCredentialName(g, c, "changed-after-key-snapshot")
		return n
	}
	var req PolicyPreviewRequest
	_ = json.Unmarshal([]byte(`{"request_model":"gpt-4o","config":{"schema_version":1,"rules":[]}}`), &req)
	res, err := f.service.PreviewCredentialPolicy(t.Context(), g, c, req)
	if err != nil {
		t.Fatal(err)
	}
	if res.Candidates[0].CredentialName != "captured-old-name" {
		t.Fatalf("name came from live registry after key snapshot: %q", res.Candidates[0].CredentialName)
	}
}

func TestPolicyPreview_Regression_CandidateAndNodeBoundaries(t *testing.T) {
	for _, n := range []int{50, 51} {
		f := newServiceFixture(t)
		keys := make([]string, n)
		for i := range keys {
			keys[i] = fmt.Sprintf("sk-review-%03d", i)
		}
		g := createUniqueGroupWithCredentials(t, f, strings.Join(keys, "\n"))
		var req PolicyPreviewRequest
		_ = json.Unmarshal([]byte(`{"request_model":"gpt-4o","config":{"schema_version":1,"rules":[]}}`), &req)
		res, err := f.service.PreviewGroupPolicy(t.Context(), g, req)
		if n == 50 && (err != nil || len(res.Candidates) != 50) {
			t.Fatalf("50 boundary: %v", err)
		}
		if n == 51 && err == nil {
			t.Fatal("51 accepted")
		}
	}

	f := newServiceFixture(t)
	keys := make([]string, 50)
	for i := range keys {
		keys[i] = fmt.Sprintf("sk-node-review-%03d", i)
	}
	g := createUniqueGroupWithCredentials(t, f, strings.Join(keys, "\n"))
	for _, leaves := range []int{99, 100} {
		all := make([]string, leaves)
		for i := range all {
			all[i] = `{"fact":"request.model","op":"eq","value":"gpt-4o"}`
		}
		cfg := fmt.Sprintf(`{"schema_version":1,"rules":[{"id":"a","name":"A","domain":"scheduling","enabled":false,"when":{"all":[%s]},"then":{"type":"exclude_candidate"}},{"id":"b","name":"B","domain":"scheduling","enabled":false,"when":{"all":[%s]},"then":{"type":"exclude_candidate"}}]}`, strings.Join(all, ","), strings.Join(all, ","))
		var req PolicyPreviewRequest
		_ = json.Unmarshal([]byte(`{"request_model":"gpt-4o","config":`+cfg+`}`), &req)
		_, err := f.service.PreviewGroupPolicy(t.Context(), g, req)
		if leaves == 99 && err != nil {
			t.Fatalf("10000 nodes rejected: %v", err)
		}
		if leaves == 100 && err == nil {
			t.Fatal("10100 nodes admitted")
		}
	}
}

func TestPolicyPreview_Regression_CompilerByteBoundary(t *testing.T) {
	f := newServiceFixture(t)
	g, _ := createTestGroupWithTwoModels(t, f)
	for _, n := range []int{policy.MaxConfigBytes, policy.MaxConfigBytes + 1} {
		base := `{"schema_version":1,"rules":[]`
		cfg := base + strings.Repeat(" ", n-len(base)-1) + `}`
		var req PolicyPreviewRequest
		err := decodeStrictControlJSONObject([]byte(`{"request_model":"gpt-4o","config":`+cfg+`}`), &req)
		if err != nil {
			t.Fatal(err)
		}
		_, err = f.service.PreviewGroupPolicy(t.Context(), g, req)
		if n == policy.MaxConfigBytes && err != nil {
			t.Fatalf("compiler exact 256KiB rejected: %v", err)
		}
		if n > policy.MaxConfigBytes && err == nil {
			t.Fatal("compiler +1 admitted")
		}
	}
}

func TestPolicyPreview_Regression_ExactMaxUintProvenance(t *testing.T) {
	f := newServiceFixture(t)
	g, c := createTestGroupWithTwoModels(t, f)
	max := ^uint64(0)
	cfg := []byte(`{"schema_version":1,"rules":[{"id":"r","name":"R","domain":"pricing","enabled":true,"when":{"fact":"request.model","op":"eq","value":"gpt-4o"},"then":{"type":"multiply_price","factor":"2"}}]}`)
	view, err := policy.CompileRuntimeView([]policy.BindingConfig{
		{Scope: "group", GroupID: g, Revision: max, Config: cfg},
		{Scope: "credential", GroupID: g, CredentialID: c, Revision: max - 1, Config: cfg},
	})
	if err != nil {
		t.Fatal(err)
	}
	snapshot := f.service.manager.Current()
	snapshot.Revision = max
	snapshot.Policies = view
	entries, err := f.service.registry.SnapshotGroupCredentialEntriesExact(g, []uint{c})
	if err != nil {
		t.Fatal(err)
	}
	entries[0].Version = max
	entries[0].IdentityGeneration = max - 1
	if err = f.service.registry.RestoreGroupCredentialEntriesExact(g, entries); err != nil {
		t.Fatal(err)
	}

	req := PolicyPreviewRequest{RequestModel: "gpt-4o"}
	out, err := f.service.PreviewCredentialPolicy(t.Context(), g, c, req)
	if err != nil {
		t.Fatal(err)
	}
	if out.SnapshotRevisionText != "18446744073709551615" || out.Candidates[0].CredentialVersion != "18446744073709551615" || out.Candidates[0].IdentityGeneration != "18446744073709551614" {
		t.Fatalf("provenance %+v", out)
	}
	target := out.Candidates[0].Targets[0]
	if target.GroupRules[0].RevisionText != "18446744073709551615" || target.CredentialRules[0].RevisionText != "18446744073709551614" || target.Pricing.Matches[0].RevisionText != "18446744073709551615" || target.Pricing.Matches[1].RevisionText != "18446744073709551614" {
		t.Fatalf("rule/pricing provenance %+v", target)
	}
	b, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err = json.Unmarshal(b, &raw); err != nil {
		t.Fatal(err)
	}
	if _, ok := raw["snapshot_revision_text"].(string); !ok {
		t.Fatal("not string")
	}

	req.Config.Specified = true
	req.Config.Raw = cfg
	out, err = f.service.PreviewCredentialPolicy(t.Context(), g, c, req)
	if err != nil {
		t.Fatal(err)
	}
	target = out.Candidates[0].Targets[0]
	if target.GroupRules[0].Provenance != "saved" || target.CredentialRules[0].Provenance != "draft" || target.CredentialRules[0].RevisionText != "draft" || target.Pricing.Matches[1].RevisionText != "draft" {
		t.Fatalf("draft provenance %+v", target)
	}
}

func TestPolicyPreview_Regression_StrictDepth32Boundary(t *testing.T) {
	for _, n := range []int{31, 32} {
		body := `{"request_model":"gpt-4o","config":` + strings.Repeat("[", n) + strings.Repeat("]", n) + `}`
		var req PolicyPreviewRequest
		err := decodeStrictControlJSONObject([]byte(body), &req)
		if n == 31 && err != nil {
			t.Fatalf("depth32 rejected: %v", err)
		}
		if n == 32 && (err == nil || !strings.Contains(err.Error(), "nesting depth")) {
			t.Fatalf("depth33 accepted: %v", err)
		}
	}
}

func TestPolicyPreview_Regression_SchedulingReasonAndConditionTree(t *testing.T) {
	f := newServiceFixture(t)
	g, c := createTestGroupWithTwoModels(t, f)
	cfg := []byte(`{"schema_version":1,"rules":[{"id":"s","name":"S","domain":"scheduling","enabled":true,"when":{"all":[{"fact":"request.model","op":"eq","value":"gpt-4o"},{"fact":"request.model","op":"eq","value":"gpt-4o"}]},"then":{"type":"exclude_candidate"}}]}`)
	req := PolicyPreviewRequest{RequestModel: "gpt-4o"}
	req.Config.Specified = true
	req.Config.Raw = cfg
	res, err := f.service.PreviewCredentialPolicy(t.Context(), g, c, req)
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(res)
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	if !strings.Contains(s, `"reason":{"rule_id":"s","name_snapshot":"S","domain":"scheduling"}`) || strings.Contains(s, `"RuleID"`) {
		t.Fatalf("unexpected scheduling reason JSON: %s", s)
	}
	if !strings.Contains(s, `"children":[{"kind":"param","fact":"request.model","truth":"true"},{"kind":"param","fact":"request.model","truth":"true"}]`) {
		t.Fatalf("unexpected condition tree JSON: %s", s)
	}
}
