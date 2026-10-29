package control

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"gpt-load/internal/platform/config"
	"gpt-load/internal/requestredact"
	"gpt-load/internal/storage/models"
)

func TestRequestRedactionPreview(t *testing.T) {
	initControlI18n(t)
	f := newServiceFixture(t)
	engine := gin.New()
	NewServer(&config.Config{AuthKey: "test-auth-key"}, f.service).RegisterRoutes(engine)
	before := f.manager.Current()
	var settingsBefore, logsBefore int64
	if err := f.db.Model(&models.SystemSetting{}).Count(&settingsBefore).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.db.Model(&models.RequestLog{}).Count(&logsBefore).Error; err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name  string
		rules []requestredact.Rule
		text  string
		want  string
	}{
		{
			name: "unicode and literal replacements",
			rules: []requestredact.Rule{
				{Pattern: "private", Replacement: "$1\\literal"},
				{Pattern: "密😀", Mode: requestredact.ModeEncrypt},
				{Pattern: "remove", Replacement: ""},
			},
			text: "前 private 密😀 remove\n<script>",
			want: `{"match_count":3,"segments":[{"kind":"text","text":"前 "},{"kind":"match","text":"private","rule_index":0,"mode":"replace","replacement":"$1\\literal"},{"kind":"text","text":" "},{"kind":"match","text":"密😀","rule_index":1,"mode":"encrypt"},{"kind":"text","text":" "},{"kind":"match","text":"remove","rule_index":2,"mode":"replace","replacement":""},{"kind":"text","text":"\n<script>"}]}`,
		},
		{
			name: "overlapping rules retain original priority",
			rules: []requestredact.Rule{
				{Pattern: "bc", Mode: requestredact.ModeEncrypt},
				{Pattern: "abc", Replacement: "ignored"},
				{Pattern: "cd", Replacement: "ignored"},
			},
			text: "abcde",
			want: `{"match_count":1,"segments":[{"kind":"match","text":"abcd","rule_index":0,"mode":"encrypt"},{"kind":"text","text":"e"}]}`,
		},
		{
			name:  "zero length matches do not create segments",
			rules: []requestredact.Rule{{Pattern: "^|$", Replacement: "ignored"}},
			text:  "plain",
			want:  `{"match_count":0,"segments":[{"kind":"text","text":"plain"}]}`,
		},
		{
			name:  "literal keywords include Chinese and regex punctuation",
			rules: []requestredact.Rule{{Pattern: regexp.QuoteMeta("机密项目.a+b[1]"), Mode: requestredact.ModeEncrypt}},
			text:  "这是机密项目.a+b[1]资料",
			want:  `{"match_count":1,"segments":[{"kind":"text","text":"这是"},{"kind":"match","text":"机密项目.a+b[1]","rule_index":0,"mode":"encrypt"},{"kind":"text","text":"资料"}]}`,
		},
		{
			name:  "literal prefix requires eight suffix characters",
			rules: []requestredact.Rule{{Pattern: regexp.QuoteMeta("demo.+_") + `[A-Za-z0-9_-]{8,}`, Mode: requestredact.ModeEncrypt}},
			text:  "demo.+_1234567 demo.+_aB12_-34",
			want:  `{"match_count":1,"segments":[{"kind":"text","text":"demo.+_1234567 "},{"kind":"match","text":"demo.+_aB12_-34","rule_index":0,"mode":"encrypt"}]}`,
		},
		{
			name:  "json remains ordinary text",
			rules: []requestredact.Rule{{Pattern: "model", Mode: requestredact.ModeEncrypt}},
			text:  `{"model":"plain"}`,
			want:  `{"match_count":1,"segments":[{"kind":"text","text":"{\""},{"kind":"match","text":"model","rule_index":0,"mode":"encrypt"},{"kind":"text","text":"\":\"plain\"}"}]}`,
		},
		{name: "empty text", rules: []requestredact.Rule{}, want: `{"match_count":0,"segments":[]}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			body, err := json.Marshal(map[string]any{"rules": test.rules, "text": test.text})
			if err != nil {
				t.Fatal(err)
			}
			request := httptest.NewRequest(http.MethodPost, "/api/settings/request-redaction/preview", strings.NewReader(string(body)))
			request.Header.Set("Authorization", "Bearer test-auth-key")
			request.Header.Set("Content-Type", "application/json")
			res := httptest.NewRecorder()
			engine.ServeHTTP(res, request)
			if res.Code != http.StatusOK {
				t.Fatalf("preview status = %d, want 200: %s", res.Code, res.Body)
			}
			if res.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("preview must not be cached")
			}
			var got struct{ Data json.RawMessage }
			if err := json.Unmarshal(res.Body.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			var actual, expected any
			if err := json.Unmarshal(got.Data, &actual); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal([]byte(test.want), &expected); err != nil {
				t.Fatal(err)
			}
			actualJSON, err := json.Marshal(actual)
			if err != nil {
				t.Fatal(err)
			}
			expectedJSON, err := json.Marshal(expected)
			if err != nil {
				t.Fatal(err)
			}
			if string(actualJSON) != string(expectedJSON) {
				t.Fatalf("preview = %s, want %s", actualJSON, expectedJSON)
			}
		})
	}
	var settingsAfter, logsAfter int64
	if err := f.db.Model(&models.SystemSetting{}).Count(&settingsAfter).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.db.Model(&models.RequestLog{}).Count(&logsAfter).Error; err != nil {
		t.Fatal(err)
	}
	if f.manager.Current() != before || settingsBefore != settingsAfter || logsBefore != logsAfter {
		t.Fatal("preview changed settings, snapshot or request logs")
	}
}

func TestRequestRedactionPreviewBoundaries(t *testing.T) {
	initControlI18n(t)
	f := newServiceFixture(t)
	engine := gin.New()
	NewServer(&config.Config{AuthKey: "test-auth-key"}, f.service).RegisterRoutes(engine)
	for _, test := range []struct {
		name, body string
		status     int
	}{
		{"invalid regex", `{"rules":[{"pattern":"[","replacement":""}],"text":"sample"}`, http.StatusBadRequest},
		{"missing rules", `{"text":"sample"}`, http.StatusBadRequest},
		{"null rules", `{"rules":null,"text":"sample"}`, http.StatusBadRequest},
		{"missing text", `{"rules":[]}`, http.StatusBadRequest},
		{"null text", `{"rules":[],"text":null}`, http.StatusBadRequest},
		{"unknown field", `{"rules":[],"text":"sample","access_key_id":1}`, http.StatusBadRequest},
		{"unknown rule field", `{"rules":[{"pattern":"x","replacement":"","capture_group":1}],"text":"x"}`, http.StatusBadRequest},
		{"text at limit", `{"rules":[],"text":"` + strings.Repeat("a", 64<<10) + `"}`, http.StatusOK},
		{"escaped text at limit", `{"rules":[],"text":"` + strings.Repeat(`\u0061`, 64<<10) + `"}`, http.StatusOK},
		{"text over limit", `{"rules":[],"text":"` + strings.Repeat("界", (64<<10)/3+1) + `"}`, http.StatusBadRequest},
		{"matches at limit", `{"rules":[{"pattern":"x","mode":"encrypt"}],"text":"` + strings.Repeat("x ", 200) + `"}`, http.StatusOK},
		{"matches over limit", `{"rules":[{"pattern":"x","mode":"encrypt"}],"text":"` + strings.Repeat("x ", 201) + `"}`, http.StatusBadRequest},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/api/settings/request-redaction/preview", strings.NewReader(test.body))
			request.Header.Set("Authorization", "Bearer test-auth-key")
			request.Header.Set("Content-Type", "application/json")
			res := httptest.NewRecorder()
			engine.ServeHTTP(res, request)
			if res.Code != test.status {
				t.Fatalf("status = %d, want %d", res.Code, test.status)
			}
		})
	}
	request := httptest.NewRequest(http.MethodPost, "/api/settings/request-redaction/preview", strings.NewReader(`{"rules":[],"text":"sample"}`))
	request.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	engine.ServeHTTP(res, request)
	if res.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated preview = %d", res.Code)
	}
}

type previewCountingReader struct {
	reader *strings.Reader
	read   int64
}

func (r *previewCountingReader) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	r.read += int64(n)
	return n, err
}

func TestRequestRedactionPreviewBodyLimit(t *testing.T) {
	initControlI18n(t)
	f := newServiceFixture(t)
	engine := gin.New()
	NewServer(&config.Config{AuthKey: "test-auth-key"}, f.service).RegisterRoutes(engine)
	const limit int64 = 1 << 20
	const prefix = `{"rules":[],"text":"sample"}`
	for _, test := range []struct {
		name          string
		size          int64
		contentLength int64
		wantStatus    int
		maxRead       int64
	}{
		{"known length at limit", limit, limit, http.StatusOK, limit},
		{"unknown length at limit", limit, -1, http.StatusOK, limit},
		{"known length over limit", limit + 1, limit + 1, http.StatusRequestEntityTooLarge, 0},
		{"unknown length over limit", 2 * limit, -1, http.StatusRequestEntityTooLarge, limit + 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			body := prefix + strings.Repeat(" ", int(test.size)-len(prefix))
			reader := &previewCountingReader{reader: strings.NewReader(body)}
			request := httptest.NewRequest(http.MethodPost, "/api/settings/request-redaction/preview", reader)
			request.ContentLength = test.contentLength
			request.Header.Set("Authorization", "Bearer test-auth-key")
			request.Header.Set("Content-Type", "application/json")
			res := httptest.NewRecorder()
			engine.ServeHTTP(res, request)
			if res.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d", res.Code, test.wantStatus)
			}
			if reader.read > test.maxRead {
				t.Errorf("read %d bytes, want at most %d", reader.read, test.maxRead)
			}
			if res.Header().Get("Cache-Control") != "no-store" {
				t.Error("preview response must not be cached")
			}
			if test.wantStatus == http.StatusRequestEntityTooLarge {
				var envelope struct{ Code string }
				if err := json.Unmarshal(res.Body.Bytes(), &envelope); err != nil {
					t.Fatal(err)
				}
				if envelope.Code != "REQUEST_TOO_LARGE" {
					t.Errorf("code = %q, want REQUEST_TOO_LARGE", envelope.Code)
				}
			}
		})
	}
}
