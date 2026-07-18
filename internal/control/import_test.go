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
	"reflect"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"
	"gorm.io/gorm"

	"gpt-load/internal/dialect"
	"gpt-load/internal/platform/config"
	"gpt-load/internal/platform/encryption"
	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/protocol"
	"gpt-load/internal/state"
	stateloader "gpt-load/internal/state/loader"
	"gpt-load/internal/storage/models"
)

func TestOptionalGroupModelsTracksPresenceAndRejectsInvalidValues(t *testing.T) {
	tests := []struct {
		name      string
		body      string
		wantSet   bool
		wantCount int
		wantError bool
	}{
		{
			name: "omitted",
			body: `{"upstream_url":"https://example.com","protocols":["openai"],"keys":"sk-a"}`,
		},
		{
			name:    "empty",
			body:    `{"upstream_url":"https://example.com","protocols":["openai"],"keys":"sk-a","models":[]}`,
			wantSet: true,
		},
		{
			name:      "values",
			body:      `{"upstream_url":"https://example.com","protocols":["openai"],"keys":"sk-a","models":[{"id":" gpt-4o ","alias":" primary "}]}`,
			wantSet:   true,
			wantCount: 1,
		},
		{
			name:      "null",
			body:      `{"upstream_url":"https://example.com","protocols":["openai"],"keys":"sk-a","models":null}`,
			wantError: true,
		},
		{
			name:      "unknown model field",
			body:      `{"upstream_url":"https://example.com","protocols":["openai"],"keys":"sk-a","models":[{"id":"gpt-4o","extra":true}]}`,
			wantError: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			decoder := json.NewDecoder(strings.NewReader(test.body))
			decoder.DisallowUnknownFields()
			var request ImportRequest
			err := decoder.Decode(&request)
			if test.wantError {
				if err == nil {
					t.Fatal("Decode() error = nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("Decode() error = %v", err)
			}
			if request.Models.Set != test.wantSet || len(request.Models.Values) != test.wantCount {
				t.Fatalf("Models = %#v, want set=%t count=%d", request.Models, test.wantSet, test.wantCount)
			}
		})
	}

	var modelsValue optionalGroupModels
	if err := modelsValue.UnmarshalJSON([]byte(`[{"id":"gpt-4o","alias":""}] []`)); err == nil {
		t.Fatal("UnmarshalJSON() accepted a trailing JSON value")
	}
}

func TestNormalizeImportModels(t *testing.T) {
	got, err := normalizeImportModels([]GroupModel{
		{ID: " gpt-4o ", Alias: " primary "},
		{ID: "gpt-4o", Alias: "primary"},
		{ID: "gpt-4o", Alias: " backup "},
		{ID: "vendor/model v1", Alias: "internal  alias"},
	})
	if err != nil {
		t.Fatalf("normalizeImportModels() error = %v", err)
	}
	want := []GroupModel{
		{ID: "gpt-4o", Alias: "primary"},
		{ID: "gpt-4o", Alias: "backup"},
		{ID: "vendor/model v1", Alias: "internal  alias"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("normalizeImportModels() = %#v, want %#v", got, want)
	}

	empty, err := normalizeImportModels([]GroupModel{})
	if err != nil || empty == nil || len(empty) != 0 {
		t.Fatalf("normalizeImportModels(empty) = %#v, %v, want non-nil empty", empty, err)
	}

	for _, id := range []string{"", "   ", "\t\n"} {
		if _, err := normalizeImportModels([]GroupModel{{ID: id}}); err == nil {
			t.Fatalf("normalizeImportModels(ID=%q) error = nil", id)
		}
	}
}

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
	if got.duplicateLines != 1 {
		t.Fatalf("duplicateLines = %d, want 1", got.duplicateLines)
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
	if len(first.Models) != 0 {
		t.Fatalf("first model result = %#v, want empty models", first)
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

func TestImportModelsPresenceControlsPersistence(t *testing.T) {
	tests := []struct {
		name     string
		existing bool
		seed     []GroupModel
		input    optionalGroupModels
		want     []GroupModel
	}{
		{
			name: "new omitted",
			want: []GroupModel{},
		},
		{
			name:  "new explicit empty",
			input: optionalGroupModels{Set: true, Values: []GroupModel{}},
			want:  []GroupModel{},
		},
		{
			name:  "new values",
			input: optionalGroupModels{Set: true, Values: []GroupModel{{ID: " gpt-4o ", Alias: " primary "}, {ID: "gpt-4o", Alias: "backup"}}},
			want:  []GroupModel{{ID: "gpt-4o", Alias: "primary"}, {ID: "gpt-4o", Alias: "backup"}},
		},
		{
			name:     "existing omitted preserves",
			existing: true,
			seed:     []GroupModel{{ID: "existing", Alias: "keep"}},
			want:     []GroupModel{{ID: "existing", Alias: "keep"}},
		},
		{
			name:     "existing explicit empty clears",
			existing: true,
			seed:     []GroupModel{{ID: "existing", Alias: "remove"}},
			input:    optionalGroupModels{Set: true, Values: []GroupModel{}},
			want:     []GroupModel{},
		},
		{
			name:     "existing values replace without merge",
			existing: true,
			seed:     []GroupModel{{ID: "existing", Alias: "remove"}},
			input:    optionalGroupModels{Set: true, Values: []GroupModel{{ID: "replacement", Alias: "one"}, {ID: "replacement", Alias: "two"}}},
			want:     []GroupModel{{ID: "replacement", Alias: "one"}, {ID: "replacement", Alias: "two"}},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newServiceFixture(t)
			upstreamURL := "https://" + strings.ReplaceAll(test.name, " ", "-") + ".example"
			groupID := uint(0)
			if test.existing {
				initial, err := fixture.service.Import(context.Background(), ImportRequest{
					UpstreamURL: upstreamURL,
					Protocols:   []protocol.Protocol{protocol.OpenAI},
					Keys:        "sk-initial",
				})
				if err != nil {
					t.Fatalf("initial Import() error = %v", err)
				}
				groupID = initial.GroupID
				encoded, err := json.Marshal(test.seed)
				if err != nil {
					t.Fatalf("json.Marshal(seed) error = %v", err)
				}
				if err := fixture.db.Model(&models.Group{}).Where("id = ?", groupID).
					Update("models", models.JSON(encoded)).Error; err != nil {
					t.Fatalf("seed group models: %v", err)
				}
				input, err := stateloader.BuildCompileInput(context.Background(), fixture.db)
				if err != nil {
					t.Fatalf("BuildCompileInput(seed) error = %v", err)
				}
				if _, err := fixture.manager.Publish(input); err != nil {
					t.Fatalf("Publish(seed) error = %v", err)
				}
			}

			before := fixture.manager.Current().Revision
			result, err := fixture.service.Import(context.Background(), ImportRequest{
				UpstreamURL: upstreamURL,
				Protocols:   []protocol.Protocol{protocol.OpenAI},
				Keys:        "sk-request",
				Models:      test.input,
			})
			if err != nil {
				t.Fatalf("Import() error = %v", err)
			}
			if groupID != 0 && result.GroupID != groupID {
				t.Fatalf("GroupID = %d, want existing %d", result.GroupID, groupID)
			}
			if got := fixture.manager.Current().Revision; got != before+1 {
				t.Fatalf("revision = %d, want %d", got, before+1)
			}

			stored := loadImportedGroupModels(t, fixture.db, result.GroupID)
			if !reflect.DeepEqual(stored, test.want) {
				t.Fatalf("stored models = %#v, want %#v", stored, test.want)
			}
			if !reflect.DeepEqual(result.Models, test.want) {
				t.Fatalf("result models = %#v, want %#v", result.Models, test.want)
			}
			assertImportedSnapshotModels(t, fixture.manager.Current(), result.GroupID, test.want)
		})
	}
}

func TestImportDoesNotDiscoverModels(t *testing.T) {
	called := make(chan struct{}, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		called <- struct{}{}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"object":"list","data":[{"id":"upstream-only"}]}`))
	}))
	t.Cleanup(upstream.Close)

	fixture := newServiceFixture(t)
	fixture.service.dialects = dialect.NewSet(dialect.NewOpenAI(upstream.Client()))
	result, err := fixture.service.Import(context.Background(), ImportRequest{
		UpstreamURL: upstream.URL,
		Protocols:   []protocol.Protocol{protocol.OpenAI},
		Keys:        "sk-import",
		Models: optionalGroupModels{
			Set: true, Values: []GroupModel{{ID: "user-configured"}},
		},
	})
	if err != nil {
		t.Fatalf("Import() error = %v", err)
	}
	select {
	case <-called:
		t.Fatal("Import() called Dialect.ListModels")
	default:
	}
	want := []GroupModel{{ID: "user-configured"}}
	if !reflect.DeepEqual(result.Models, want) {
		t.Fatalf("result models = %#v, want %#v", result.Models, want)
	}
}

func TestImportModelReplacementRejectsBlankIDWithoutMutation(t *testing.T) {
	fixture := newServiceFixture(t)
	initial, err := fixture.service.Import(context.Background(), ImportRequest{
		UpstreamURL: "https://rollback-models.example",
		Protocols:   []protocol.Protocol{protocol.OpenAI},
		Keys:        "sk-existing",
		Models: optionalGroupModels{
			Set: true, Values: []GroupModel{{ID: "existing", Alias: "keep"}},
		},
	})
	if err != nil {
		t.Fatalf("initial Import() error = %v", err)
	}
	beforeRevision := fixture.manager.Current().Revision
	beforeModels := loadImportedGroupModels(t, fixture.db, initial.GroupID)
	var beforeKeys int64
	if err := fixture.db.Model(&models.UpstreamKey{}).Count(&beforeKeys).Error; err != nil {
		t.Fatalf("count keys before invalid import: %v", err)
	}

	_, err = fixture.service.Import(context.Background(), ImportRequest{
		UpstreamURL: "https://rollback-models.example",
		Protocols:   []protocol.Protocol{protocol.OpenAI},
		Keys:        "sk-must-not-persist",
		Models: optionalGroupModels{
			Set: true, Values: []GroupModel{{ID: "   ", Alias: "invalid"}},
		},
	})
	if err == nil {
		t.Fatal("Import() error = nil")
	}
	if got := fixture.manager.Current().Revision; got != beforeRevision {
		t.Fatalf("revision = %d, want %d", got, beforeRevision)
	}
	if got := loadImportedGroupModels(t, fixture.db, initial.GroupID); !reflect.DeepEqual(got, beforeModels) {
		t.Fatalf("stored models = %#v, want unchanged %#v", got, beforeModels)
	}
	var afterKeys int64
	if err := fixture.db.Model(&models.UpstreamKey{}).Count(&afterKeys).Error; err != nil {
		t.Fatalf("count keys after invalid import: %v", err)
	}
	if afterKeys != beforeKeys {
		t.Fatalf("key count = %d, want unchanged %d", afterKeys, beforeKeys)
	}
}

func TestImportModelReplacementRollsBackWhenKeyEncryptionFails(t *testing.T) {
	fixture := newServiceFixture(t)
	initial, err := fixture.service.Import(context.Background(), ImportRequest{
		UpstreamURL: "https://rollback-transaction.example",
		Protocols:   []protocol.Protocol{protocol.OpenAI},
		Keys:        "sk-existing",
		Models: optionalGroupModels{
			Set: true, Values: []GroupModel{{ID: "existing", Alias: "keep"}},
		},
	})
	if err != nil {
		t.Fatalf("initial Import() error = %v", err)
	}

	beforeRevision := fixture.manager.Current().Revision
	beforeModels := loadImportedGroupModels(t, fixture.db, initial.GroupID)
	beforeCandidates := fixture.registry.CollectCandidates([]uint{initial.GroupID}, nil)
	var beforeKeys int64
	if err := fixture.db.Model(&models.UpstreamKey{}).Count(&beforeKeys).Error; err != nil {
		t.Fatalf("count keys before failed import: %v", err)
	}
	fixture.service.encryption = failingEncryptService{Service: fixture.encryption}

	_, err = fixture.service.Import(context.Background(), ImportRequest{
		UpstreamURL: "https://rollback-transaction.example",
		Protocols:   []protocol.Protocol{protocol.OpenAI},
		Keys:        "sk-new-must-not-persist",
		Models: optionalGroupModels{
			Set: true, Values: []GroupModel{{ID: "replacement", Alias: "discard"}},
		},
	})
	if err == nil {
		t.Fatal("Import() error = nil, want encryption failure")
	}
	if got := fixture.manager.Current().Revision; got != beforeRevision {
		t.Fatalf("revision = %d, want unchanged %d", got, beforeRevision)
	}
	if got := loadImportedGroupModels(t, fixture.db, initial.GroupID); !reflect.DeepEqual(got, beforeModels) {
		t.Fatalf("stored models = %#v, want unchanged %#v", got, beforeModels)
	}
	var afterKeys int64
	if err := fixture.db.Model(&models.UpstreamKey{}).Count(&afterKeys).Error; err != nil {
		t.Fatalf("count keys after failed import: %v", err)
	}
	if afterKeys != beforeKeys {
		t.Fatalf("key count = %d, want unchanged %d", afterKeys, beforeKeys)
	}
	if got := fixture.registry.CollectCandidates([]uint{initial.GroupID}, nil); !reflect.DeepEqual(got, beforeCandidates) {
		t.Fatalf("registry candidates = %#v, want unchanged %#v", got, beforeCandidates)
	}
}

type failingEncryptService struct {
	encryption.Service
}

func (failingEncryptService) Encrypt(string) (string, error) {
	return "", errors.New("forced encryption failure")
}

func loadImportedGroupModels(t *testing.T, db interface {
	First(any, ...any) *gorm.DB
}, groupID uint) []GroupModel {
	t.Helper()
	var group models.Group
	if err := db.First(&group, groupID).Error; err != nil {
		t.Fatalf("query imported group: %v", err)
	}
	var result []GroupModel
	if err := json.Unmarshal(group.Models, &result); err != nil {
		t.Fatalf("decode imported models: %v", err)
	}
	if result == nil {
		result = make([]GroupModel, 0)
	}
	return result
}

func assertImportedSnapshotModels(
	t *testing.T,
	snapshot *state.ConfigSnapshot,
	groupID uint,
	values []GroupModel,
) {
	t.Helper()
	expected := make(map[string]struct{}, len(values))
	for _, value := range values {
		expected[value.ID] = struct{}{}
	}
	actual := snapshot.Candidates[protocol.OpenAI]
	if len(actual) != len(expected) {
		t.Fatalf("snapshot models = %#v, want IDs %#v", actual, expected)
	}
	for modelID := range expected {
		targets := actual[modelID]
		if len(targets) != 1 || targets[0].GroupID != groupID || targets[0].UpstreamModelID != modelID {
			t.Fatalf("snapshot model %q targets = %#v, want group %d", modelID, targets, groupID)
		}
	}
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
