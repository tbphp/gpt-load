package control

import (
	"fmt"
	"net/http"
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
	config := fmt.Sprintf(`{"schema_version":1,"rules":[{"id":"deep","name":"Deep","domain":"scheduling","enabled":true,"when":%s,"actions":[{"type":"exclude_candidate"}]}]}`, condition)
	if _, err := policy.Compile([]byte(config)); err != nil {
		t.Fatal(err)
	}

	path := groupPolicyPath(g)
	if w := policyRequest(server, http.MethodPut, path, `{"expected_revision":"0","config":`+config+`}`); w.Code != http.StatusOK {
		t.Fatalf("PUT %s: %d %s", path, w.Code, w.Body.String())
	}
}

func TestPolicyTransportDepthRemainsBounded(t *testing.T) {
	for _, tc := range []struct {
		target any
		depth  int
	}{
		{&PolicyUpdateRequest{}, policy.MaxJSONDepth + 1},
		{&GroupSettingsUpdateRequest{}, maxStrictControlJSONDepth},
	} {
		body := `{"config":` + strings.Repeat("[", tc.depth) + `0` + strings.Repeat("]", tc.depth) + `}`
		if err := decodeStrictControlJSONObject([]byte(body), tc.target); err == nil || !strings.Contains(err.Error(), "nesting depth") {
			t.Fatalf("%T excessive depth error=%v", tc.target, err)
		}
	}
}
