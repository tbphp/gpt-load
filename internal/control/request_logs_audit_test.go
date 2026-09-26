package control

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"gpt-load/internal/platform/config"
	"gpt-load/internal/requestaudit"
)

func TestRequestAuditLogQueryValidation(t *testing.T) {
	for _, status := range []string{"allowed", "blocked", "failed", "warned", "incomplete"} {
		query, err := parseRequestLogQuery("audit_status=" + status + "&audit_rule=Private%20data")
		if err != nil || query.AuditStatus != status || query.AuditRule != "Private data" {
			t.Fatalf("guardrail query was rejected: %+v, %v", query, err)
		}
	}
	for _, raw := range []string{
		"audit_status=success", "audit_status=warned&audit_status=blocked",
		"audit_rule=a&audit_rule=b", "audit_rule=%0A", "audit_rule=%20", "audit_rule=" + strings.Repeat("x", 257),
	} {
		if _, err := parseRequestLogQuery(raw); err == nil {
			t.Fatalf("invalid guardrail query accepted: %s", raw)
		}
	}
}

func TestRequestAuditFinalOutcomeProjection(t *testing.T) {
	for _, test := range []struct{ status, reason, mode, want string }{
		{"passed", "", "", "allowed"},
		{"warned", "", "", "warned"},
		{"incomplete", "content_truncated", "", "allowed"},
		{"warned", "content_truncated", "", "warned"},
		{"blocked", "content_truncated", "", "blocked"},
		{"incomplete", "timeout", "", "failed"},
		{"incomplete", "invalid_response", "", "failed"},
		{"incomplete", "canceled", "", "failed"},
		{"matched", "", "observe", "warned"},
		{"matched", "", "enforce", "blocked"},
	} {
		t.Run(test.status+"/"+test.reason+"/"+test.mode, func(t *testing.T) {
			response := mapRequestAudit(&requestaudit.Result{Status: test.status, Reason: test.reason, Mode: test.mode})
			encoded, err := json.Marshal(response)
			if err != nil {
				t.Fatal(err)
			}
			var value map[string]any
			if err := json.Unmarshal(encoded, &value); err != nil {
				t.Fatal(err)
			}
			if value["outcome"] != test.want || response.Reason != test.reason {
				t.Fatalf("outcome or detail changed: %s", encoded)
			}
		})
	}
}

func TestRequestAuditLogFiltersKeepAccessKeyScope(t *testing.T) {
	initControlI18n(t)
	fixture := newServiceFixture(t)
	key, err := fixture.service.CreateAccessKey(t.Context(), AccessKeyCreateRequest{Name: "guardrail log viewer"})
	if err != nil {
		t.Fatal(err)
	}
	reader := &recordingRequestLogReader{}
	fixture.service.requestLogs = reader
	engine := gin.New()
	NewServer(&config.Config{AuthKey: "test-auth-key"}, fixture.service).RegisterRoutes(engine)
	response := performRequestLogRequest(engine, key.Key, "audit_status=warned&audit_rule=Privacy")
	if response.Code != http.StatusOK || len(reader.queries) != 1 {
		t.Fatalf("guardrail filters failed: %d %s", response.Code, response.Body)
	}
	query := reader.queries[0]
	if query.AccessKeyID == nil || *query.AccessKeyID != key.ID || query.AuditStatus != "warned" || query.AuditRule != "Privacy" {
		t.Fatalf("guardrail filters lost access-key scope: %+v", query)
	}
}
