package control

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"gpt-load/internal/channel"
	"gpt-load/internal/jev"
	"gpt-load/internal/platform/config"
	"gpt-load/internal/platform/redact"
	"gpt-load/internal/requestaudit"
	"gpt-load/internal/requestredact"
	"gpt-load/internal/storage/models"
)

func TestHomeRequestRulesExposeOnlyApplicablePolicies(t *testing.T) {
	t.Parallel()
	initControlI18n(t)
	fixture := newServiceFixture(t)
	group := createPriceTestGroup(t, fixture.db, models.Group{
		Name: "private-audit-group", ChannelID: string(channel.Jev),
		Params: models.JSON(`{}`), Models: models.JSON(`[{"id":"jev-review"}]`),
		Overrides: models.JSON(`{}`), Enabled: true,
	})
	current, err := fixture.service.CreateAccessKey(t.Context(), AccessKeyCreateRequest{Name: "policy reader"})
	if err != nil {
		t.Fatal(err)
	}
	other, err := fixture.service.CreateAccessKey(t.Context(), AccessKeyCreateRequest{Name: "other reader"})
	if err != nil {
		t.Fatal(err)
	}
	audit := requestaudit.Config{Enabled: true, AccessKeyIDs: []uint{current.ID}, Rules: []requestaudit.Rule{
		{ID: "private_data", Name: "Private data", Enabled: true, Action: requestaudit.ActionWarn, Threshold: 0.8,
			Instructions: "Review confidential@example.test and sk-example-secret-value for disclosure."},
		{ID: "inactive", Name: "inactive internal policy", Enabled: false, Action: requestaudit.ActionBlock, Threshold: 0.8, Instructions: "Disabled policy."},
	}}
	settings := map[string]any{
		"jev": jev.Config{GroupID: group.ID, Model: "jev-review", TimeoutSeconds: 2},
		"request_redaction": []requestredact.Rule{
			{Pattern: `[0-9]{11}`, Replacement: "[PHONE]"},
			{Pattern: `confidential@example\.test`, Mode: requestredact.ModeEncrypt, Replacement: "unused-private-value"},
		},
		"request_audit": audit,
	}
	save := func() {
		t.Helper()
		values := map[string]json.RawMessage{}
		for key, value := range settings {
			raw, err := json.Marshal(value)
			if err != nil {
				t.Fatal(err)
			}
			values[key] = raw
		}
		if _, err := fixture.service.UpdateSettings(t.Context(), SettingsUpdateRequest{Settings: values}); err != nil {
			t.Fatal(err)
		}
	}
	save()
	server := NewServer(&config.Config{AuthKey: "test-auth-key"}, fixture.service)
	engine := gin.New()
	server.RegisterRoutes(engine)

	type ruleView struct {
		Pattern      string `json:"pattern"`
		Replacement  string `json:"replacement"`
		Mode         string `json:"mode"`
		Name         string `json:"name"`
		Instructions string `json:"instructions"`
		Action       string `json:"action"`
	}
	type policyView struct {
		Redaction struct {
			Rules []ruleView `json:"rules"`
		} `json:"redaction"`
		Audit struct {
			Enabled     bool       `json:"enabled"`
			ChannelName string     `json:"channel_name"`
			Model       string     `json:"model"`
			Rules       []ruleView `json:"rules"`
		} `json:"audit"`
	}
	read := func(token string) *policyView {
		t.Helper()
		response := performHomeRequest(engine, "/api/home", token)
		if response.Code != http.StatusOK {
			t.Fatalf("home status = %d", response.Code)
		}
		var result struct {
			Data struct {
				Rules *policyView `json:"request_rules"`
			} `json:"data"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		for _, hidden := range []string{"private-audit-group", "inactive internal policy", "confidential@example", "sk-example-secret-value", "unused-private-value", "access_key_ids", "timeout_seconds", "threshold", "instructions_hash"} {
			if strings.Contains(response.Body.String(), hidden) {
				t.Fatalf("home disclosed protected policy data: %q", hidden)
			}
		}
		return result.Data.Rules
	}
	got := read(current.Key)
	if got == nil {
		t.Fatal("access-key home is missing request_rules")
	}
	if len(got.Redaction.Rules) != 2 || got.Redaction.Rules[0].Pattern != `[0-9]{11}` ||
		got.Redaction.Rules[0].Mode != requestredact.ModeReplace || got.Redaction.Rules[0].Replacement != "[PHONE]" ||
		got.Redaction.Rules[1].Pattern != redact.Placeholder || got.Redaction.Rules[1].Mode != requestredact.ModeEncrypt || got.Redaction.Rules[1].Replacement != "" {
		t.Fatalf("unexpected redaction projection: %+v", got.Redaction)
	}
	if !got.Audit.Enabled || got.Audit.ChannelName != "Jev" || got.Audit.Model != "jev-review" || len(got.Audit.Rules) != 1 ||
		got.Audit.Rules[0].Name != "Private data" || got.Audit.Rules[0].Action != requestaudit.ActionWarn ||
		!strings.Contains(got.Audit.Rules[0].Instructions, redact.Placeholder) {
		t.Fatalf("unexpected audit projection: %+v", got.Audit)
	}
	assertInactive := func(token string) {
		t.Helper()
		got := read(token)
		if got == nil || got.Audit.Enabled || got.Audit.Rules == nil || len(got.Audit.Rules) != 0 || got.Audit.ChannelName != "" || got.Audit.Model != "" {
			t.Fatal("inactive audit disclosed configuration or omitted its status")
		}
	}
	assertInactive(other.Key)
	if got := read("test-auth-key"); got != nil {
		t.Fatal("admin home should not contain access-user request rules")
	}
	if got := performHomeRequest(engine, "/api/settings", current.Key); got.Code != http.StatusForbidden {
		t.Fatal("policy disclosure must not grant access to settings")
	}

	audit.AccessKeyIDs = nil
	settings["request_audit"] = audit
	save()
	if got := read(other.Key); got == nil || !got.Audit.Enabled || len(got.Audit.Rules) != 1 {
		t.Fatal("home did not refresh globally applicable audit rules")
	}
	audit.Enabled = false
	settings["request_audit"] = audit
	settings["request_redaction"] = []requestredact.Rule{}
	save()
	assertInactive(current.Key)
	if got := read(current.Key); got.Redaction.Rules == nil || len(got.Redaction.Rules) != 0 {
		t.Fatal("unconfigured redaction must be an empty list")
	}
}
