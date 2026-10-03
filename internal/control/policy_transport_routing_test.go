package control

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"gpt-load/internal/policy"
)

func TestPolicyTransportAcceptsMaxConditionDepth(t *testing.T) {
	f := newServiceFixture(t)
	g, _ := createTestGroupWithTwoModels(t, f)
	server := setupPolicyTestServer(t, f)
	condition := `{"predicate":"time_window","weekdays":[1],"ranges":[["09:00","23:59"]]}`
	for depth := 1; depth < policy.MaxConditionDepth; depth++ {
		condition = `{"all":[` + condition + `]}`
	}
	config := fmt.Sprintf(`{"schema_version":1,"rules":[{"id":"deep","name":"Deep","domain":"scheduling","enabled":true,"when":%s,"then":{"type":"exclude_candidate"}}]}`, condition)
	if _, err := policy.Compile([]byte(config)); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		method string
		path   string
		body   string
	}{
		{http.MethodPut, fmt.Sprintf("/api/groups/%d/policy", g), `{"expected_revision":"0","config":` + config + `}`},
		{http.MethodPost, fmt.Sprintf("/api/groups/%d/policy/preview", g), `{"request_model":"gpt-4o","config":` + config + `}`},
	} {
		r := httptest.NewRequest(tc.method, tc.path, bytes.NewBufferString(tc.body))
		r.Header.Set("Authorization", "Bearer admin-secret-key")
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		server.ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("%s %s: %d %s", tc.method, tc.path, w.Code, w.Body.String())
		}
	}
}

func TestPolicyTransportDepthRemainsBounded(t *testing.T) {
	for _, tc := range []struct {
		target any
		depth  int
	}{
		{&PolicyPreviewRequest{}, policy.MaxJSONDepth + 1},
		{&PolicyUpdateRequest{}, policy.MaxJSONDepth + 1},
		{&GroupSettingsUpdateRequest{}, maxStrictControlJSONDepth},
	} {
		body := `{"config":` + strings.Repeat("[", tc.depth) + `0` + strings.Repeat("]", tc.depth) + `}`
		if err := decodeStrictControlJSONObject([]byte(body), tc.target); err == nil || !strings.Contains(err.Error(), "nesting depth") {
			t.Fatalf("%T excessive depth error=%v", tc.target, err)
		}
	}
}

func TestPolicyPreviewRejectsAliasedInternalModelName(t *testing.T) {
	f := newServiceFixture(t)
	g, _ := createTestGroupWithTwoModels(t, f)
	for _, model := range []string{"upstream-gpt4", "upstream-backup"} {
		if _, err := f.service.PreviewGroupPolicy(t.Context(), g, PolicyPreviewRequest{RequestModel: model}); err == nil {
			t.Fatalf("accepted internal aliased model %q", model)
		}
	}
	result, err := f.service.PreviewGroupPolicy(t.Context(), g, PolicyPreviewRequest{RequestModel: "gpt-4o"})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Candidates) != 2 || len(result.Candidates[0].Targets) != 2 {
		t.Fatalf("alias did not retain alternate targets: %+v", result.Candidates)
	}
}
