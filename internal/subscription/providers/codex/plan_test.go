package codex

import (
	"fmt"
	"testing"
)

func TestCodexPlanClaimsPreserveCanonicalCredential(t *testing.T) {
	t.Parallel()
	token := identityTestToken(t, map[string]string{"chatgpt_plan_type": "plus"}, false)
	for _, test := range []struct {
		name, idToken, accessToken, storedPlan, wantPlan string
	}{
		{"ID token claim", token, "access", "", "plus"},
		{"access token claim", "", token, "", "plus"},
		{"explicit plan takes precedence", token, "access", "pro", "pro"},
		{"missing plan stays unknown", "", "access", "", ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			planField := ""
			if test.storedPlan != "" {
				planField = fmt.Sprintf(`,"plan_type":%q`, test.storedPlan)
			}
			// 套餐展示信息不得改变旧凭据的字段和顺序，否则启动时无法验证原指纹。
			raw := []byte(fmt.Sprintf(`{"type":"codex"%s%s,"access_token":%q,"refresh_token":"refresh","account_id":"workspace"}`, planField, identityTestIDTokenField(test.idToken), test.accessToken))
			credential, err := newCodexDriver().Parse(raw)
			if err != nil {
				t.Fatal(err)
			}
			if string(credential.Canonical()) != string(raw) {
				t.Fatal("plan derivation changed canonical credential bytes")
			}
			if got := credential.Account().PlanType; got != test.wantPlan {
				t.Fatalf("account plan = %q, want %q", got, test.wantPlan)
			}
		})
	}
}
