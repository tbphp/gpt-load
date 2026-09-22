package requestlog

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"

	"gpt-load/internal/platform/config"
	"gpt-load/internal/storage"
	"gpt-load/internal/storage/models"
)

func TestRequestAuditLogFilters(t *testing.T) {
	db, _ := openRequestLogFileDB(t)
	testRequestAuditLogFilters(t, db)
}

func TestExternalRequestAuditLogFilters(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("GPT_LOAD_DATABASE_TEST_DSN"))
	if dsn == "" {
		t.Skip("GPT_LOAD_DATABASE_TEST_DSN is not set")
	}
	db, err := storage.OpenWithSource(dsn, config.DatabaseSourceExternal)
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := storage.AutoMigrate(db); err != nil {
		t.Fatal(err)
	}
	testRequestAuditLogFilters(t, db)
}

func testRequestAuditLogFilters(t *testing.T, db *gorm.DB) {
	t.Helper()
	now := time.Now()
	model := fmt.Sprintf("guardrail-filters-%d", now.UnixNano())
	fixtures := []struct {
		name, audit string
	}{
		{"unreviewed", ""},
		{"unreviewed_json", "null"},
		{"empty_findings", `{"status":"passed","findings":null,"calls":[]}`},
		{"passed", `{"status":"passed","findings":[],"calls":[]}`},
		{"warned", `{"status":"warned","findings":[{"rule_id":"removed_rule","name":"Privacy_100%! 个人隐私","action":"warn"}],"calls":[]}`},
		{"blocked", `{"status":"blocked","findings":[{"rule_id":"credential_leakage","name":"Credential leakage","action":"block"}],"calls":[]}`},
		{"incomplete", `{"status":"incomplete","reason":"timeout","findings":[{"rule_id":"personal_data","name":"Private data","action":"warn"}],"calls":[]}`},
		{"legacy_warned", `{"status":"matched","mode":"observe","findings":[{"rule_id":"credential_leakage","name":"Credential leakage","status":"passed"},{"rule_id":"personal_data","name":"Private data","status":"matched"}],"calls":[]}`},
		{"legacy_blocked", `{"status":"matched","mode":"enforce","findings":[{"rule_id":"credential_leakage","name":"Credential leakage","status":"matched"}],"calls":[]}`},
		{"other_key", `{"status":"warned","findings":[{"rule_id":"personal_data","name":"Private data","action":"warn"}],"calls":[]}`},
		{"upstream_error", `{"status":"warned","findings":[{"rule_id":"personal_data","name":"Private data","action":"warn"}],"calls":[]}`},
	}
	ids := map[string]string{}
	for index, fixture := range fixtures {
		id := fmt.Sprintf("00000000-0000-4000-8000-%012x", (uint64(now.UnixNano())&0xffffffff0000)+uint64(index))
		ids[fixture.name] = id
		row := aggregationRow(id, now.Add(time.Duration(index)*time.Second), 1, model)
		row.ModelConsistency = "unknown"
		row.RequestAudit = models.JSON(fixture.audit)
		if strings.Contains(fixture.name, "blocked") || fixture.name == "incomplete" || fixture.name == "upstream_error" {
			row.Status, row.StatusCode = "error", 403
			row.ModelConsistency = "not_applicable"
		}
		if fixture.name == "other_key" {
			row.AccessKeyID = 2
		}
		if err := db.Create(&row).Error; err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		if err := db.Where("client_model = ?", model).Delete(&models.RequestLog{}).Error; err != nil {
			t.Error(err)
		}
	})
	service := newRequestLogTestService(db)
	key := uint(1)
	for _, test := range []struct {
		name  string
		query ListQuery
		want  []string
	}{
		{"warnings regardless of request outcome", ListQuery{AuditStatus: "warned"}, []string{"warned", "legacy_warned", "other_key", "upstream_error"}},
		{"blocked", ListQuery{AuditStatus: "blocked"}, []string{"blocked", "legacy_blocked"}},
		{"incomplete", ListQuery{AuditStatus: "incomplete"}, []string{"incomplete"}},
		{"rule keyword ignores legacy non-matches", ListQuery{AuditRule: "CREDENTIAL"}, []string{"blocked", "legacy_blocked"}},
		{"literal percent", ListQuery{AuditRule: "100%"}, []string{"warned"}},
		{"literal underscore", ListQuery{AuditRule: "_"}, []string{"warned"}},
		{"literal punctuation", ListQuery{AuditRule: "!"}, []string{"warned"}},
		{"Chinese keyword", ListQuery{AuditRule: "隐私"}, []string{"warned"}},
		{"combined filters", ListQuery{AuditStatus: "warned", AuditRule: "data", Status: "success", AccessKeyID: &key}, []string{"legacy_warned"}},
		{"no match", ListQuery{AuditRule: "unknown rule"}, nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			test.query.ClientModel = model
			page, err := service.List(t.Context(), test.query)
			if err != nil {
				t.Fatal(err)
			}
			want := map[string]bool{}
			for _, name := range test.want {
				want[ids[name]] = true
			}
			if len(page.Items) != len(want) {
				t.Fatalf("got %d rows, want %d", len(page.Items), len(want))
			}
			for _, row := range page.Items {
				if !want[row.RequestID] {
					t.Fatalf("unexpected row %s", row.RequestID)
				}
			}
		})
	}
	query := ListQuery{ClientModel: model, AuditStatus: "warned", Limit: 1}
	for _, name := range []string{"upstream_error", "other_key", "legacy_warned", "warned"} {
		page, err := service.List(t.Context(), query)
		if err != nil || len(page.Items) != 1 || page.Items[0].RequestID != ids[name] {
			t.Fatalf("filtered cursor page for %s: %+v, %v", name, page, err)
		}
		query.Cursor = page.NextCursor
	}
	if query.Cursor != nil {
		t.Fatal("filtered pagination included unrelated rows")
	}
}
