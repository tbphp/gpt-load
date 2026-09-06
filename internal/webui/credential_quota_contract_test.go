package webui

import (
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"

	providerobservation "gpt-load/internal/subscription/providers/observation"
)

// 后端增加额度元数据时，前端 DTO 和严格字段白名单必须一起更新。
func TestCredentialQuotaWindowFrontendContract(t *testing.T) {
	readSection := func(path, pattern string) string {
		t.Helper()
		content, err := os.ReadFile(filepath.Join("..", "..", "web", "src", path))
		if err != nil {
			t.Fatal(err)
		}
		match := regexp.MustCompile(pattern).FindStringSubmatch(string(content))
		if len(match) != 2 {
			t.Fatalf("contract section not found in %s", path)
		}
		return match[1]
	}
	fields := readSection("app/resources/credentials.ts", `(?s)const quotaWindowFields = \[(.*?)\] as const`)
	dto := readSection("api/control/types.ts", `(?s)export interface CredentialQuotaWindowDto \{(.*?)\n\}`)

	windowType := reflect.TypeOf(providerobservation.QuotaWindow{})
	for index := 0; index < windowType.NumField(); index++ {
		field := windowType.Field(index)
		tag := strings.Split(field.Tag.Get("json"), ",")
		name := tag[0]
		if name == "" || name == "-" {
			continue
		}
		if !strings.Contains(fields, "'"+name+"'") {
			t.Errorf("quotaWindowFields must accept backend field %q", name)
		}
		optional := ""
		if len(tag) > 1 && tag[1] == "omitempty" {
			optional = `\?`
		}
		if !regexp.MustCompile(`(?m)^\s*` + regexp.QuoteMeta(name) + optional + `\s*:`).MatchString(dto) {
			t.Errorf("CredentialQuotaWindowDto must declare backend field %q with matching optionality", name)
		}
	}
}
