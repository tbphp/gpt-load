package control

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"

	"gpt-load/internal/dialect"
	"gpt-load/internal/platform/config"
	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/protocol"
	stateloader "gpt-load/internal/state/loader"
	"gpt-load/internal/storage/models"
)

func TestNormalizeUpstreamURL(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		want      string
		wantError bool
	}{
		{name: "trim lower host and slash", input: " HTTPS://API.Example.COM/v1/ ", want: "https://api.example.com/v1"},
		{name: "preserve fixed query", input: "https://API.Example.com/base/?api-version=2026-01", want: "https://api.example.com/base?api-version=2026-01"},
		{name: "preserve internal double slash", input: "https://api.example.com/a//b/", want: "https://api.example.com/a//b"},
		{name: "ipv6 without port", input: "https://[::1]/v1/", want: "https://[::1]/v1"},
		{name: "ipv6 with port", input: "http://[2001:db8::1]:8080/v1/", want: "http://[2001:db8::1]:8080/v1"},
		{name: "reject relative", input: "/v1", wantError: true},
		{name: "reject ftp", input: "ftp://api.example.com", wantError: true},
		{name: "reject userinfo", input: "https://user:pass@api.example.com", wantError: true},
		{name: "reject fragment", input: "https://api.example.com/#secret", wantError: true},
		{name: "reject missing host", input: "https:///v1", wantError: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, signature, _, err := normalizeUpstreamURL(test.input)
			if test.wantError {
				if err == nil {
					t.Fatalf("normalizeUpstreamURL(%q) error = nil", test.input)
				}
				return
			}
			if err != nil {
				t.Fatalf("normalizeUpstreamURL(%q) error = %v", test.input, err)
			}
			if got != test.want {
				t.Fatalf("normalizeUpstreamURL(%q) = %q, want %q", test.input, got, test.want)
			}
			wantHash := sha256.Sum256([]byte(test.want))
			if signature != hex.EncodeToString(wantHash[:]) || len(signature) != 64 {
				t.Fatalf("signature = %q, want SHA-256 of %q", signature, test.want)
			}
		})
	}

	_, firstSignature, _, err := normalizeUpstreamURL("https://API.Example.com/v1/")
	if err != nil {
		t.Fatal(err)
	}
	_, secondSignature, _, err := normalizeUpstreamURL(" https://api.example.com/v1 ")
	if err != nil {
		t.Fatal(err)
	}
	if firstSignature != secondSignature {
		t.Fatalf("equivalent signatures differ: %q != %q", firstSignature, secondSignature)
	}
	_, otherQuerySignature, _, err := normalizeUpstreamURL("https://api.example.com/v1?tenant=other")
	if err != nil {
		t.Fatal(err)
	}
	if firstSignature == otherQuerySignature {
		t.Fatal("different fixed query produced the same signature")
	}
}

func TestNormalizeImportInput(t *testing.T) {
	fixture := newServiceFixture(t)
	request := ImportRequest{
		UpstreamURL: "https://api.example.com/",
		Protocols:   []protocol.Protocol{protocol.OpenAI, protocol.OpenAI, protocol.Anthropic},
		Keys:        " sk-a \n\n sk-a\nsk-b\n",
	}
	got, err := fixture.service.normalizeImportInput(request)
	if err != nil {
		t.Fatalf("normalizeImportInput() error = %v", err)
	}
	if len(got.protocols) != 2 || got.protocols[0] != protocol.OpenAI || got.protocols[1] != protocol.Anthropic {
		t.Fatalf("protocols = %#v, want stable unique openai/anthropic", got.protocols)
	}
	if len(got.keys) != 2 || got.keys[0].plaintext != "sk-a" || got.keys[1].plaintext != "sk-b" {
		t.Fatalf("keys = %#v, want two stable unique candidates", got.keys)
	}
	if got.keys[0].hash == "" || got.keys[0].hash == got.keys[1].hash {
		t.Fatalf("key hashes = %#v, want distinct non-empty HMACs", got.keys)
	}
	if got.firstKey != "sk-a" || got.duplicateLines != 1 {
		t.Fatalf("firstKey/duplicates = %q/%d, want sk-a/1", got.firstKey, got.duplicateLines)
	}
}

func TestNormalizeImportInputRejectsInvalidRequests(t *testing.T) {
	fixture := newServiceFixture(t)
	valid := ImportRequest{
		UpstreamURL: "https://api.example.com",
		Protocols:   []protocol.Protocol{protocol.OpenAI},
		Keys:        "sk-valid",
	}
	blank := "   "
	tooLong := strings.Repeat("名", 86)
	controlName := "bad\nname"
	tests := []struct {
		name   string
		mutate func(*ImportRequest)
	}{
		{name: "empty protocols", mutate: func(request *ImportRequest) { request.Protocols = nil }},
		{name: "unknown protocol", mutate: func(request *ImportRequest) { request.Protocols = []protocol.Protocol{"unknown"} }},
		{name: "reserved response protocol", mutate: func(request *ImportRequest) { request.Protocols = []protocol.Protocol{protocol.OpenAIResponse} }},
		{name: "empty keys", mutate: func(request *ImportRequest) { request.Keys = " \n\t" }},
		{name: "too many non-empty lines", mutate: func(request *ImportRequest) { request.Keys = importLines(1001) }},
		{name: "blank explicit name", mutate: func(request *ImportRequest) { request.Name = &blank }},
		{name: "name over 255 bytes", mutate: func(request *ImportRequest) { request.Name = &tooLong }},
		{name: "control character in name", mutate: func(request *ImportRequest) { request.Name = &controlName }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := valid
			test.mutate(&request)
			if _, err := fixture.service.normalizeImportInput(request); err == nil {
				t.Fatal("normalizeImportInput() error = nil")
			}
		})
	}
}

func TestNormalizeImportInputAllowsExactlyOneThousandNonEmptyLines(t *testing.T) {
	fixture := newServiceFixture(t)
	got, err := fixture.service.normalizeImportInput(ImportRequest{
		UpstreamURL: "https://api.example.com",
		Protocols:   []protocol.Protocol{protocol.OpenAI},
		Keys:        importLines(1000),
	})
	if err != nil {
		t.Fatalf("normalizeImportInput() error = %v", err)
	}
	if len(got.keys)+got.duplicateLines != 1000 {
		t.Fatalf("non-empty line count = %d, want 1000", len(got.keys)+got.duplicateLines)
	}
}

func TestPrivateImportHostUsesOnlyLiteralHostClassification(t *testing.T) {
	tests := []struct {
		host string
		want bool
	}{
		{host: "localhost", want: true},
		{host: "LOCALHOST.", want: true},
		{host: "127.0.0.1", want: true},
		{host: "10.1.2.3", want: true},
		{host: "172.16.0.1", want: true},
		{host: "192.168.1.1", want: true},
		{host: "::1", want: true},
		{host: "fe80::1", want: true},
		{host: "0.0.0.0", want: true},
		{host: "8.8.8.8", want: false},
		{host: "api.example.com", want: false},
	}
	for _, test := range tests {
		t.Run(test.host, func(t *testing.T) {
			if got := isLiteralPrivateHost(test.host); got != test.want {
				t.Fatalf("isLiteralPrivateHost(%q) = %t, want %t", test.host, got, test.want)
			}
		})
	}
}

func TestImportNormalizesGroupsAndCountsDuplicates(t *testing.T) {
	fixture := newServiceFixture(t)
	before := fixture.manager.Current().Revision
	first, err := fixture.service.Import(context.Background(), ImportRequest{
		UpstreamURL: "HTTPS://API.Example.COM/v1/",
		Protocols:   []protocol.Protocol{protocol.OpenAI},
		Keys:        "sk-a\nsk-a\nsk-b",
	})
	if err != nil {
		t.Fatalf("first Import() error = %v", err)
	}
	if !first.Created || first.GroupName != "api.example.com" || first.KeysAdded != 2 || first.KeysDuplicated != 1 {
		t.Fatalf("first result = %#v", first)
	}
	if first.ModelsFetched || len(first.Models) != 0 {
		t.Fatalf("first model result = %#v, want unfetched empty models", first)
	}
	if got := fixture.manager.Current().Revision; got != before+1 {
		t.Fatalf("first revision = %d, want %d", got, before+1)
	}

	rename := "ignored-new-name"
	second, err := fixture.service.Import(context.Background(), ImportRequest{
		UpstreamURL: "https://api.example.com/v1",
		Protocols:   []protocol.Protocol{protocol.Anthropic},
		Name:        &rename,
		Keys:        "sk-a\nsk-b",
	})
	if err != nil {
		t.Fatalf("second Import() error = %v", err)
	}
	if second.Created || second.GroupID != first.GroupID || second.GroupName != first.GroupName ||
		second.KeysAdded != 0 || second.KeysDuplicated != 2 {
		t.Fatalf("second result = %#v", second)
	}
	if got := fixture.manager.Current().Revision; got != before+2 {
		t.Fatalf("second revision = %d, want %d", got, before+2)
	}
	assertGroupCount(t, fixture.db, 1)

	var group models.Group
	if err := fixture.db.First(&group, first.GroupID).Error; err != nil {
		t.Fatalf("query imported group: %v", err)
	}
	var protocols []protocol.Protocol
	if err := json.Unmarshal(group.Protocols, &protocols); err != nil {
		t.Fatalf("decode protocols: %v", err)
	}
	if len(protocols) != 2 || protocols[0] != protocol.OpenAI || protocols[1] != protocol.Anthropic {
		t.Fatalf("stored protocols = %#v, want stable union", protocols)
	}
	var keyCount int64
	if err := fixture.db.Model(&models.UpstreamKey{}).Count(&keyCount).Error; err != nil {
		t.Fatalf("count upstream keys: %v", err)
	}
	if keyCount != 2 {
		t.Fatalf("upstream key count = %d, want 2", keyCount)
	}
}

func TestImportPersistsOnlyEncryptedKeysAndPublishesRegistryEntries(t *testing.T) {
	fixture := newServiceFixture(t)
	result, err := fixture.service.Import(context.Background(), ImportRequest{
		UpstreamURL: "https://secure.example.com/v1/",
		Protocols:   []protocol.Protocol{protocol.OpenAI},
		Keys:        "sk-secret-a\nsk-secret-b",
	})
	if err != nil {
		t.Fatalf("Import() error = %v", err)
	}

	var group models.Group
	if err := fixture.db.First(&group, result.GroupID).Error; err != nil {
		t.Fatalf("query group: %v", err)
	}
	if group.UpstreamURL != "https://secure.example.com/v1" || string(group.Config) != "{}" || string(group.Models) != "[]" || !group.Enabled {
		t.Fatalf("stored group = %#v", group)
	}
	var rows []models.UpstreamKey
	if err := fixture.db.Order("id ASC").Find(&rows).Error; err != nil {
		t.Fatalf("query upstream keys: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("stored keys = %#v, want two", rows)
	}
	for index, plaintext := range []string{"sk-secret-a", "sk-secret-b"} {
		row := rows[index]
		if row.KeyValue == "" || row.KeyHash == "" || strings.Contains(row.KeyValue, plaintext) || strings.Contains(row.KeyHash, plaintext) {
			t.Fatalf("stored key %d exposes plaintext: %#v", row.ID, row)
		}
		decrypted, err := fixture.encryption.Decrypt(row.KeyValue)
		if err != nil || decrypted != plaintext {
			t.Fatalf("Decrypt(key %d) = %q, %v, want %q", row.ID, decrypted, err, plaintext)
		}
		if got, ok := fixture.registry.EncryptedValue(row.ID); !ok || got != row.KeyValue {
			t.Fatalf("Registry key %d = %q, %t, want persisted cipher", row.ID, got, ok)
		}
	}
}

func TestImportRepairsMissingRegistryKeyWithoutResettingExistingRuntime(t *testing.T) {
	fixture := newServiceFixture(t)
	request := ImportRequest{
		UpstreamURL: "https://repair.example.com",
		Protocols:   []protocol.Protocol{protocol.OpenAI},
		Keys:        "sk-repair",
	}
	if _, err := fixture.service.Import(context.Background(), request); err != nil {
		t.Fatalf("initial Import() error = %v", err)
	}
	var row models.UpstreamKey
	if err := fixture.db.First(&row).Error; err != nil {
		t.Fatalf("query imported key: %v", err)
	}
	if count, ok := fixture.registry.IncrFailure(row.ID); !ok || count != 1 {
		t.Fatalf("first failure count = %d, %t", count, ok)
	}
	if _, err := fixture.service.Import(context.Background(), request); err != nil {
		t.Fatalf("present-key reimport error = %v", err)
	}
	if count, ok := fixture.registry.IncrFailure(row.ID); !ok || count != 2 {
		t.Fatalf("second failure count = %d, %t, want preserved runtime count 2", count, ok)
	}

	if !fixture.registry.RemoveKey(row.ID) {
		t.Fatal("RemoveKey() = false, want simulated missing entry")
	}
	result, err := fixture.service.Import(context.Background(), request)
	if err != nil {
		t.Fatalf("repair Import() error = %v", err)
	}
	if result.KeysAdded != 0 || result.KeysDuplicated != 1 {
		t.Fatalf("repair result = %#v, want added=0 duplicated=1", result)
	}
	if got, ok := fixture.registry.EncryptedValue(row.ID); !ok || got != row.KeyValue {
		t.Fatalf("repaired Registry value = %q, %t, want %q", got, ok, row.KeyValue)
	}
}

func TestImportRejectsExplicitNameCollisionAndPreservesExistingName(t *testing.T) {
	fixture := newServiceFixture(t)
	name := "shared-name"
	first, err := fixture.service.Import(context.Background(), ImportRequest{
		UpstreamURL: "https://first.example.com", Protocols: []protocol.Protocol{protocol.OpenAI},
		Name: &name, Keys: "sk-first",
	})
	if err != nil {
		t.Fatalf("first Import() error = %v", err)
	}
	beforeConflict := fixture.manager.Current().Revision
	_, err = fixture.service.Import(context.Background(), ImportRequest{
		UpstreamURL: "https://second.example.com", Protocols: []protocol.Protocol{protocol.OpenAI},
		Name: &name, Keys: "sk-second",
	})
	var apiErr *app_errors.APIError
	if !errors.As(err, &apiErr) || apiErr.HTTPStatus != http.StatusConflict {
		t.Fatalf("name collision error = %#v, want typed 409", err)
	}
	assertGroupCount(t, fixture.db, 1)
	if got := fixture.manager.Current().Revision; got != beforeConflict {
		t.Fatalf("revision after collision = %d, want %d", got, beforeConflict)
	}

	rename := "ignored-rename"
	second, err := fixture.service.Import(context.Background(), ImportRequest{
		UpstreamURL: "https://first.example.com", Protocols: []protocol.Protocol{protocol.Anthropic},
		Name: &rename, Keys: "sk-first",
	})
	if err != nil {
		t.Fatalf("same-signature Import() error = %v", err)
	}
	if second.GroupID != first.GroupID || second.GroupName != name {
		t.Fatalf("same-signature result = %#v, want original name %q", second, name)
	}
}

func TestImportHandlerRejectsInvalidInputWithoutMutation(t *testing.T) {
	initControlI18n(t)
	fixture := newServiceFixture(t)
	engine := gin.New()
	NewServer(&config.Config{AuthKey: "test-auth-key"}, fixture.service).RegisterRoutes(engine)
	before := fixture.manager.Current().Revision

	tests := []struct {
		name string
		body []byte
	}{
		{name: "invalid json", body: []byte(`{"upstream_url":`)},
		{name: "reserved protocol", body: mustJSON(t, ImportRequest{
			UpstreamURL: "https://api.example.com", Protocols: []protocol.Protocol{protocol.OpenAIResponse}, Keys: "sk-one",
		})},
		{name: "too many keys", body: mustJSON(t, ImportRequest{
			UpstreamURL: "https://api.example.com", Protocols: []protocol.Protocol{protocol.OpenAI}, Keys: importLines(1001),
		})},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodPost, "/api/import", bytes.NewReader(test.body))
			request.Header.Set("Authorization", "Bearer test-auth-key")
			request.Header.Set("Content-Type", "application/json")
			engine.ServeHTTP(recorder, request)
			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body=%s", recorder.Code, recorder.Body.String())
			}
		})
	}
	assertGroupCount(t, fixture.db, 0)
	if got := fixture.manager.Current().Revision; got != before {
		t.Fatalf("revision = %d, want %d", got, before)
	}
}

func TestImportPrivateHostWarningDoesNotExposeURLOrCredentials(t *testing.T) {
	fixture := newServiceFixture(t)
	const (
		rawURL    = "http://127.0.0.1/base?tenant_secret=known-query-secret"
		plaintext = "known-upstream-key"
	)
	var logs bytes.Buffer
	previousOutput := logrus.StandardLogger().Out
	logrus.SetOutput(&logs)
	t.Cleanup(func() { logrus.SetOutput(previousOutput) })

	result, err := fixture.service.Import(context.Background(), ImportRequest{
		UpstreamURL: rawURL, Protocols: []protocol.Protocol{protocol.OpenAI}, Keys: plaintext,
	})
	if err != nil {
		t.Fatalf("Import() error = %v", err)
	}
	var row models.UpstreamKey
	if err := fixture.db.First(&row).Error; err != nil {
		t.Fatalf("query imported key: %v", err)
	}
	var group models.Group
	if err := fixture.db.First(&group, result.GroupID).Error; err != nil {
		t.Fatalf("query imported group: %v", err)
	}
	if !strings.Contains(logs.String(), "127.0.0.1") || !strings.Contains(logs.String(), group.Signature) {
		t.Fatalf("private warning missing safe host/signature metadata: %s", logs.String())
	}
	for _, forbidden := range []string{rawURL, "known-query-secret", plaintext, row.KeyValue, row.KeyHash} {
		if strings.Contains(logs.String(), forbidden) {
			t.Fatalf("private warning exposes %q: %s", forbidden, logs.String())
		}
	}
}

func TestImportFetchesModelsWithEmptyRules(t *testing.T) {
	requests := make(chan http.Header, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests <- request.Header.Clone()
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"object":"list","data":[{"id":"gpt-4o"},{"id":"gpt-4.1"}]}`))
	}))
	t.Cleanup(upstream.Close)

	fixture := newServiceFixture(t)
	fixture.service.dialects = dialectSetForOpenAI(upstream.Client())
	result, err := fixture.service.Import(context.Background(), ImportRequest{
		UpstreamURL: upstream.URL, Protocols: []protocol.Protocol{protocol.OpenAI}, Keys: "sk-model-list",
	})
	if err != nil {
		t.Fatalf("Import() error = %v", err)
	}
	if !result.ModelsFetched || fmt.Sprint(result.Models) != "[gpt-4o gpt-4.1]" {
		t.Fatalf("model result = %#v", result)
	}
	headers := <-requests
	if got := headers.Get("Authorization"); got != "Bearer sk-model-list" {
		t.Fatalf("Authorization = %q, want default Bearer credential", got)
	}

	var group models.Group
	if err := fixture.db.First(&group, result.GroupID).Error; err != nil {
		t.Fatalf("query imported group: %v", err)
	}
	if string(group.Models) != `[{"id":"gpt-4o","alias":""},{"id":"gpt-4.1","alias":""}]` {
		t.Fatalf("stored models = %s", group.Models)
	}
}

func TestImportFetchesModelsWithExistingHeaderRulesAndStableMerge(t *testing.T) {
	headersSeen := make(chan http.Header, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		headersSeen <- request.Header.Clone()
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"object":"list","data":[{"id":"existing"},{"id":"new"},{"id":"new"}]}`))
	}))
	t.Cleanup(upstream.Close)

	fixture := newServiceFixture(t)
	initial, err := fixture.service.Import(context.Background(), ImportRequest{
		UpstreamURL: upstream.URL, Protocols: []protocol.Protocol{protocol.OpenAI}, Keys: "sk-initial",
	})
	if err != nil {
		t.Fatalf("initial Import() error = %v", err)
	}
	if err := fixture.db.Model(&models.Group{}).Where("id = ?", initial.GroupID).Updates(map[string]any{
		"models": models.JSON(`[{"id":"existing","alias":"Keep"}]`),
		"config": models.JSON(`{"header_rules":{"set":{"Authorization":"Token ${API_KEY}","X-Custom":"key=${API_KEY}"}}}`),
	}).Error; err != nil {
		t.Fatalf("seed group runtime config: %v", err)
	}
	input, err := stateloader.BuildCompileInput(context.Background(), fixture.db)
	if err != nil {
		t.Fatalf("BuildCompileInput() error = %v", err)
	}
	if _, err := fixture.manager.Publish(input); err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	fixture.service.dialects = dialectSetForOpenAI(upstream.Client())

	result, err := fixture.service.Import(context.Background(), ImportRequest{
		UpstreamURL: upstream.URL, Protocols: []protocol.Protocol{protocol.OpenAI}, Keys: "sk-new",
	})
	if err != nil {
		t.Fatalf("merged Import() error = %v", err)
	}
	if !result.ModelsFetched || fmt.Sprint(result.Models) != "[existing new]" {
		t.Fatalf("merged model result = %#v", result)
	}
	headers := <-headersSeen
	if headers.Get("Authorization") != "Token sk-new" || headers.Get("X-Custom") != "key=sk-new" {
		t.Fatalf("model-list headers = %#v", headers)
	}

	var group models.Group
	if err := fixture.db.First(&group, initial.GroupID).Error; err != nil {
		t.Fatalf("query merged group: %v", err)
	}
	var stored []GroupModel
	if err := json.Unmarshal(group.Models, &stored); err != nil {
		t.Fatalf("decode merged models: %v", err)
	}
	if len(stored) != 2 || stored[0].ID != "existing" || stored[0].Alias != "Keep" || stored[1].ID != "new" {
		t.Fatalf("stored merged models = %#v", stored)
	}
}

func TestImportFetchesModelsTreatsUpstreamFailuresAsBestEffort(t *testing.T) {
	tests := []struct {
		name          string
		status        int
		body          string
		waitForCancel bool
		wantFetched   bool
	}{
		{name: "empty list", status: http.StatusOK, body: `{"object":"list","data":[]}`, wantFetched: true},
		{name: "upstream error", status: http.StatusInternalServerError, body: `{"error":"failed"}`},
		{name: "malformed json", status: http.StatusOK, body: `{"data":`},
		{name: "blank model", status: http.StatusOK, body: `{"data":[{"id":""}]}`},
		{name: "boundary whitespace", status: http.StatusOK, body: `{"data":[{"id":" gpt-4o "}]}`},
		{name: "timeout", waitForCancel: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				if test.waitForCancel {
					<-request.Context().Done()
					return
				}
				writer.WriteHeader(test.status)
				_, _ = writer.Write([]byte(test.body))
			}))
			t.Cleanup(upstream.Close)

			fixture := newServiceFixture(t)
			fixture.service.dialects = dialectSetForOpenAI(upstream.Client())
			if test.waitForCancel {
				fixture.service.modelFetchTimeout = 20 * time.Millisecond
			}
			result, err := fixture.service.Import(context.Background(), ImportRequest{
				UpstreamURL: upstream.URL, Protocols: []protocol.Protocol{protocol.OpenAI}, Keys: "sk-best-effort",
			})
			if err != nil {
				t.Fatalf("Import() error = %v", err)
			}
			if result.ModelsFetched != test.wantFetched || len(result.Models) != 0 {
				t.Fatalf("result = %#v, want fetched=%t and empty models", result, test.wantFetched)
			}
			assertGroupCount(t, fixture.db, 1)
		})
	}
}

func TestImportFetchesModelsSkipsUnregisteredProtocols(t *testing.T) {
	called := make(chan struct{}, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		called <- struct{}{}
	}))
	t.Cleanup(upstream.Close)
	fixture := newServiceFixture(t)
	fixture.service.dialects = dialectSetForOpenAI(upstream.Client())

	result, err := fixture.service.Import(context.Background(), ImportRequest{
		UpstreamURL: upstream.URL, Protocols: []protocol.Protocol{protocol.Anthropic, protocol.Gemini}, Keys: "sk-no-lister",
	})
	if err != nil {
		t.Fatalf("Import() error = %v", err)
	}
	if result.ModelsFetched {
		t.Fatalf("result = %#v, want models_fetched=false", result)
	}
	select {
	case <-called:
		t.Fatal("unregistered protocol triggered an HTTP model-list call")
	default:
	}
}

func TestImportFetchesModelsStopsOnParentCancellation(t *testing.T) {
	called := make(chan struct{}, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		called <- struct{}{}
	}))
	t.Cleanup(upstream.Close)
	fixture := newServiceFixture(t)
	fixture.service.dialects = dialectSetForOpenAI(upstream.Client())
	before := fixture.manager.Current().Revision
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := fixture.service.Import(ctx, ImportRequest{
		UpstreamURL: upstream.URL, Protocols: []protocol.Protocol{protocol.OpenAI}, Keys: "sk-canceled",
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Import() error = %v, want context.Canceled", err)
	}
	assertGroupCount(t, fixture.db, 0)
	if got := fixture.manager.Current().Revision; got != before {
		t.Fatalf("revision = %d, want %d", got, before)
	}
	select {
	case <-called:
		t.Fatal("canceled parent triggered a model-list call")
	default:
	}
}

func TestImportModelFetchDoesNotHoldWriteLock(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		close(entered)
		<-release
		_, _ = writer.Write([]byte(`{"object":"list","data":[{"id":"gpt-4o"}]}`))
	}))
	t.Cleanup(upstream.Close)

	fixture := newServiceFixture(t)
	fixture.service.dialects = dialectSetForOpenAI(upstream.Client())
	firstDone := make(chan error, 1)
	go func() {
		_, err := fixture.service.Import(context.Background(), ImportRequest{
			UpstreamURL: upstream.URL, Protocols: []protocol.Protocol{protocol.OpenAI}, Keys: "sk-first",
		})
		firstDone <- err
	}()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("first import did not enter ListModels")
	}

	secondDone := make(chan error, 1)
	go func() {
		_, err := fixture.service.Import(context.Background(), ImportRequest{
			UpstreamURL: "https://second.example.com", Protocols: []protocol.Protocol{protocol.Anthropic}, Keys: "sk-second",
		})
		secondDone <- err
	}()
	select {
	case err := <-secondDone:
		if err != nil {
			t.Fatalf("second Import() error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("second control write blocked behind model fetch")
	}
	close(release)
	select {
	case err := <-firstDone:
		if err != nil {
			t.Fatalf("first Import() error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("first import did not finish after model fetch release")
	}
}

func dialectSetForOpenAI(client *http.Client) dialect.Set {
	return dialect.NewSet(dialect.NewOpenAI(client))
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("json.Marshal(%T) error = %v", value, err)
	}
	return encoded
}

func importLines(count int) string {
	var builder strings.Builder
	for index := range count {
		fmt.Fprintf(&builder, "sk-%d\n", index)
	}
	return builder.String()
}
