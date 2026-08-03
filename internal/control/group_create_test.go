package control

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/sirupsen/logrus"

	"gpt-load/internal/catalog"
	"gpt-load/internal/dialect"
	"gpt-load/internal/platform/encryption"
	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/protocol"
	"gpt-load/internal/state"
	"gpt-load/internal/storage/models"
)

func TestCreateGroupNormalizesAndPublishesOnce(t *testing.T) {
	fixture := newServiceFixture(t)
	if err := fixture.db.Create(&models.SystemSetting{
		Key:   state.SettingInjectUsageOptions,
		Value: "false",
	}).Error; err != nil {
		t.Fatalf("seed system setting: %v", err)
	}
	name := "  primary upstream  "
	beforeRevision := fixture.manager.Current().Revision

	result, err := fixture.service.CreateGroup(t.Context(), GroupCreateRequest{
		Name:        &name,
		UpstreamURL: " HTTPS://API.Example.COM/v1/ ",
		Protocols: []protocol.Protocol{
			protocol.OpenAIResponses,
			protocol.Anthropic,
			protocol.OpenAICompletions,
			protocol.OpenAIResponses,
		},
		Models: optionalGroupModels{
			Set: true,
			Values: []GroupModel{
				{ID: " gpt-4o ", Alias: " primary ", AliasEnabled: true},
				{ID: "claude-3-5", Alias: "must-be-ignored"},
			},
		},
		Keys: " sk-a \n\n sk-a\nsk-b\n",
	})
	if err != nil {
		t.Fatalf("CreateGroup() error = %v", err)
	}
	if result.GroupID == 0 || result.GroupName != "primary upstream" ||
		result.KeysAdded != 2 || result.KeysDuplicated != 1 {
		t.Fatalf("CreateGroup() result = %#v", result)
	}
	wantModels := []GroupModel{{ID: "gpt-4o", Alias: "primary"}, {ID: "claude-3-5", Alias: ""}}
	encodedResult, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("encode create result: %v", err)
	}
	var resultFields map[string]json.RawMessage
	if err := json.Unmarshal(encodedResult, &resultFields); err != nil {
		t.Fatalf("decode create result fields: %v", err)
	}
	if len(resultFields) != 4 || resultFields["models"] != nil {
		t.Fatalf("create result fields = %#v, want only group and key counts", resultFields)
	}

	var group models.Group
	if err := fixture.db.First(&group, result.GroupID).Error; err != nil {
		t.Fatalf("query created group: %v", err)
	}
	if group.Name != "primary upstream" || group.UpstreamURL != "https://api.example.com/v1" ||
		string(group.Config) != `{}` || !group.Enabled {
		t.Fatalf("stored group = %#v", group)
	}
	var storedProtocols []protocol.Protocol
	if err := json.Unmarshal(group.Protocols, &storedProtocols); err != nil {
		t.Fatalf("decode stored protocols: %v", err)
	}
	wantProtocols := []protocol.Protocol{
		protocol.OpenAICompletions,
		protocol.OpenAIResponses,
		protocol.Anthropic,
	}
	if !reflect.DeepEqual(storedProtocols, wantProtocols) {
		t.Fatalf("stored protocols = %#v", storedProtocols)
	}
	if storedModels := loadCreatedGroupModels(t, fixture, result.GroupID); !reflect.DeepEqual(storedModels, wantModels) {
		t.Fatalf("stored models = %#v, want %#v", storedModels, wantModels)
	}

	snapshot := fixture.manager.Current()
	if snapshot.Revision != beforeRevision+1 {
		t.Fatalf("snapshot revision = %d, want %d", snapshot.Revision, beforeRevision+1)
	}
	view, ok := snapshot.Groups[result.GroupID]
	if !ok || view.UpstreamURL != group.UpstreamURL ||
		view.InjectUsageOptions ||
		!reflect.DeepEqual(view.Protocols, wantProtocols) {
		t.Fatalf("snapshot group = %#v, exists=%t", view, ok)
	}
	settings, err := fixture.service.GetGroupSettings(t.Context(), result.GroupID)
	if err != nil {
		t.Fatal(err)
	}
	if len(settings.Overrides) != 0 ||
		settings.Effective.InjectUsageOptions {
		t.Fatalf("created group config/effective = %#v/%#v", settings.Overrides, settings.Effective)
	}
	if candidates := fixture.registry.CollectCandidates([]uint{result.GroupID}, nil, time.Time{}); len(candidates) != 2 {
		t.Fatalf("Registry candidates = %#v, want two", candidates)
	}
}

func TestCreateGroupRejectsDuplicateExternalModelWithoutMutation(t *testing.T) {
	fixture := newServiceFixture(t)
	beforeRevision := fixture.manager.Current().Revision
	beforeRegistry := fixture.registry.Snapshot()

	_, err := fixture.service.CreateGroup(t.Context(), GroupCreateRequest{
		UpstreamURL: "https://duplicate-external-model.example.com/v1",
		Protocols:   []protocol.Protocol{protocol.OpenAICompletions},
		Models: optionalGroupModels{
			Set: true,
			Values: []GroupModel{
				{ID: "provider-a", Alias: "public", AliasEnabled: true},
				{ID: "provider-b", Alias: "public", AliasEnabled: true},
			},
		},
		Keys: "sk-duplicate-external-model",
	})
	var apiErr *app_errors.APIError
	if !errors.As(err, &apiErr) || apiErr.Code != app_errors.ErrModelNameConflict.Code {
		t.Fatalf("CreateGroup() error = %#v, want MODEL_NAME_CONFLICT", err)
	}
	assertGroupCount(t, fixture.db, 0)
	var keyCount int64
	if err := fixture.db.Model(&models.UpstreamKey{}).Count(&keyCount).Error; err != nil {
		t.Fatal(err)
	}
	if keyCount != 0 {
		t.Fatalf("upstream key count = %d, want 0", keyCount)
	}
	if fixture.manager.Current().Revision != beforeRevision {
		t.Fatal("invalid models published a Snapshot")
	}
	if !reflect.DeepEqual(fixture.registry.Snapshot(), beforeRegistry) {
		t.Fatal("invalid models changed Registry")
	}

	result, err := fixture.service.CreateGroup(t.Context(), GroupCreateRequest{
		UpstreamURL: "https://case-sensitive-models.example.com/v1",
		Protocols:   []protocol.Protocol{protocol.OpenAICompletions},
		Models: optionalGroupModels{
			Set: true,
			Values: []GroupModel{
				{ID: "provider-a", Alias: "Public", AliasEnabled: true},
				{ID: "provider-b", Alias: "public", AliasEnabled: true},
			},
		},
		Keys: "sk-case-sensitive-models",
	})
	if err != nil {
		t.Fatalf("CreateGroup(case-distinct client names) error = %v", err)
	}
	if stored := loadCreatedGroupModels(t, fixture, result.GroupID); !reflect.DeepEqual(stored, []GroupModel{
		{ID: "provider-a", Alias: "Public"},
		{ID: "provider-b", Alias: "public"},
	}) {
		t.Fatalf("case-distinct stored models = %#v", stored)
	}
}

func TestCreateGroupReturnsUpstreamURLConflictWithoutMutation(t *testing.T) {
	fixture := newServiceFixture(t)
	first, err := fixture.service.CreateGroup(t.Context(), GroupCreateRequest{
		UpstreamURL: "https://api.example.com/v1",
		Protocols:   []protocol.Protocol{protocol.OpenAICompletions},
		Models:      optionalGroupModels{Set: true, Values: []GroupModel{}},
		Keys:        "sk-first",
	})
	if err != nil {
		t.Fatalf("initial CreateGroup() error = %v", err)
	}

	var existingKey models.UpstreamKey
	if err := fixture.db.Where("group_id = ?", first.GroupID).First(&existingKey).Error; err != nil {
		t.Fatalf("query existing key: %v", err)
	}
	beforeCiphertext := existingKey.KeyValue
	if count, ok := fixture.registry.IncrFailure(existingKey.ID); !ok || count != 1 {
		t.Fatalf("first failure count = %d, %t", count, ok)
	}
	beforeRevision := fixture.manager.Current().Revision
	beforeCandidates := fixture.registry.CollectCandidates([]uint{first.GroupID}, nil, time.Time{})

	_, err = fixture.service.CreateGroup(t.Context(), GroupCreateRequest{
		UpstreamURL: " https://API.example.com/v1/ ",
		Protocols:   []protocol.Protocol{protocol.Anthropic},
		Models:      optionalGroupModels{Set: true, Values: []GroupModel{}},
		Keys:        "sk-second",
	})

	var apiErr *app_errors.APIError
	if !errors.As(err, &apiErr) || apiErr.Code != app_errors.ErrUpstreamURLConflict.Code {
		t.Fatalf("CreateGroup() error = %#v", err)
	}
	data, ok := apiErr.Data.(UpstreamURLConflictData)
	if !ok || len(data.Groups) != 1 || data.Groups[0].ID != first.GroupID ||
		data.Groups[0].Name != first.GroupName {
		t.Fatalf("conflict data = %#v", apiErr.Data)
	}
	assertGroupCount(t, fixture.db, 1)
	if fixture.manager.Current().Revision != beforeRevision {
		t.Fatal("conflict published a snapshot")
	}
	if !reflect.DeepEqual(fixture.registry.CollectCandidates([]uint{first.GroupID}, nil, time.Time{}), beforeCandidates) {
		t.Fatal("conflict changed Registry")
	}
	if err := fixture.db.First(&existingKey, existingKey.ID).Error; err != nil {
		t.Fatalf("reload existing key: %v", err)
	}
	if existingKey.KeyValue != beforeCiphertext {
		t.Fatalf("ciphertext changed from %q to %q", beforeCiphertext, existingKey.KeyValue)
	}
	if count, ok := fixture.registry.IncrFailure(existingKey.ID); !ok || count != 2 {
		t.Fatalf("second failure count = %d, %t, want 2", count, ok)
	}
	var keyCount int64
	if err := fixture.db.Model(&models.UpstreamKey{}).Count(&keyCount).Error; err != nil {
		t.Fatalf("count upstream keys: %v", err)
	}
	if keyCount != 1 {
		t.Fatalf("upstream key count = %d, want 1", keyCount)
	}
}

func TestCreateGroupAllowsConfirmedDuplicateURLAsIndependentGroup(t *testing.T) {
	fixture := newServiceFixture(t)
	first, err := fixture.service.CreateGroup(t.Context(), GroupCreateRequest{
		UpstreamURL: "https://same.example.com/v1/",
		Protocols:   []protocol.Protocol{protocol.OpenAICompletions},
		Models: optionalGroupModels{
			Set: true, Values: []GroupModel{{ID: "gpt-4o"}},
		},
		Keys: "sk-first",
	})
	if err != nil {
		t.Fatalf("first CreateGroup() error = %v", err)
	}
	second, err := fixture.service.CreateGroup(t.Context(), GroupCreateRequest{
		UpstreamURL:            " HTTPS://SAME.example.com/v1 ",
		Protocols:              []protocol.Protocol{protocol.Anthropic},
		Models:                 optionalGroupModels{Set: true, Values: []GroupModel{{ID: "claude-sonnet"}}},
		Keys:                   "sk-second",
		ConfirmSameUpstreamURL: true,
	})
	if err != nil {
		t.Fatalf("confirmed CreateGroup() error = %v", err)
	}
	if first.GroupID == second.GroupID {
		t.Fatalf("confirmed groups share ID %d", first.GroupID)
	}
	if first.GroupName == second.GroupName {
		t.Fatalf("automatic names are not independent: %q", first.GroupName)
	}

	var groups []models.Group
	if err := fixture.db.Order("id ASC").Find(&groups).Error; err != nil {
		t.Fatalf("query groups: %v", err)
	}
	if len(groups) != 2 || groups[0].UpstreamURL != "https://same.example.com/v1" ||
		groups[1].UpstreamURL != groups[0].UpstreamURL {
		t.Fatalf("stored groups = %#v", groups)
	}
	assertStoredGroupProtocols(t, groups[0], []protocol.Protocol{protocol.OpenAICompletions})
	assertStoredGroupProtocols(t, groups[1], []protocol.Protocol{protocol.Anthropic})
	if !reflect.DeepEqual(loadCreatedGroupModels(t, fixture, first.GroupID), []GroupModel{{ID: "gpt-4o"}}) ||
		!reflect.DeepEqual(loadCreatedGroupModels(t, fixture, second.GroupID), []GroupModel{{ID: "claude-sonnet"}}) {
		t.Fatal("confirmed groups do not retain independent models")
	}

	_, err = fixture.service.CreateGroup(t.Context(), GroupCreateRequest{
		UpstreamURL: "https://same.example.com/v1",
		Protocols:   []protocol.Protocol{protocol.Gemini},
		Models:      optionalGroupModels{Set: true, Values: []GroupModel{}},
		Keys:        "sk-conflicting-third",
	})
	var apiErr *app_errors.APIError
	if !errors.As(err, &apiErr) || apiErr.Code != app_errors.ErrUpstreamURLConflict.Code {
		t.Fatalf("third CreateGroup() error = %#v, want URL conflict", err)
	}
	data, ok := apiErr.Data.(UpstreamURLConflictData)
	if !ok || !reflect.DeepEqual(data.Groups, []ExistingGroupSummary{
		{ID: first.GroupID, Name: first.GroupName},
		{ID: second.GroupID, Name: second.GroupName},
	}) {
		t.Fatalf("multiple conflict groups = %#v", apiErr.Data)
	}
}

func TestCreateGroupPersistsEncryptedKeysAndDoesNotDiscoverModels(t *testing.T) {
	fixture := newServiceFixture(t)
	discoveryCalls := 0
	fixture.service.dialects = dialect.NewSet(&recordingDiscoveryDialect{
		value: protocol.OpenAICompletions,
		listFn: func(_ context.Context, _, _ string, _ state.HeaderRules) ([]string, error) {
			discoveryCalls++
			return []string{"upstream-only"}, nil
		},
	})

	result, err := fixture.service.CreateGroup(t.Context(), GroupCreateRequest{
		UpstreamURL: "https://secure.example.com/v1/",
		Protocols:   []protocol.Protocol{protocol.OpenAICompletions},
		Models: optionalGroupModels{
			Set: true, Values: []GroupModel{{ID: "user-configured"}},
		},
		Keys: "sk-secret-a\nsk-secret-b",
	})
	if err != nil {
		t.Fatalf("CreateGroup() error = %v", err)
	}
	if discoveryCalls != 0 {
		t.Fatalf("Dialect.ListModels calls = %d, want 0", discoveryCalls)
	}
	var rows []models.UpstreamKey
	if err := fixture.db.Where("group_id = ?", result.GroupID).Order("id ASC").Find(&rows).Error; err != nil {
		t.Fatalf("query upstream keys: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("stored keys = %#v, want two", rows)
	}
	for index, plaintext := range []string{"sk-secret-a", "sk-secret-b"} {
		row := rows[index]
		if row.KeyValue == "" || row.KeyHash == "" || strings.Contains(row.KeyValue, plaintext) ||
			strings.Contains(row.KeyHash, plaintext) {
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

func TestCreateGroupPreservesUserGeminiModelPrefix(t *testing.T) {
	fixture := newServiceFixture(t)
	result, err := fixture.service.CreateGroup(t.Context(), GroupCreateRequest{
		UpstreamURL: "https://generativelanguage.googleapis.com/v1beta",
		Protocols:   []protocol.Protocol{protocol.Gemini},
		Models: optionalGroupModels{
			Set: true, Values: []GroupModel{{ID: " models/foo ", Alias: " user alias ", AliasEnabled: true}},
		},
		Keys: "gemini-secret",
	})
	if err != nil {
		t.Fatalf("CreateGroup() error = %v", err)
	}
	want := []GroupModel{{ID: "models/foo", Alias: "user alias"}}
	if stored := loadCreatedGroupModels(t, fixture, result.GroupID); !reflect.DeepEqual(stored, want) {
		t.Fatalf("stored models = %#v, want %#v", stored, want)
	}
	snapshot := fixture.manager.Current()
	targets := snapshot.Candidates[protocol.Gemini]["user alias"]
	if len(targets) != 1 || targets[0].UpstreamModelID != "models/foo" {
		t.Fatalf("Gemini snapshot targets = %#v", targets)
	}
	if _, exists := snapshot.Candidates[protocol.Gemini]["models/foo"]; exists {
		t.Fatal("aliased upstream model ID must not appear in the external candidate index")
	}
}

func TestCreateGroupPrivateHostLogContainsOnlyHostname(t *testing.T) {
	fixture := newServiceFixture(t)
	const (
		rawURL    = "http://127.0.0.1/base?tenant_secret=known-query-secret"
		plaintext = "known-upstream-key"
	)
	var logs bytes.Buffer
	previousOutput := logrus.StandardLogger().Out
	logrus.SetOutput(&logs)
	t.Cleanup(func() { logrus.SetOutput(previousOutput) })

	result, err := fixture.service.CreateGroup(t.Context(), GroupCreateRequest{
		UpstreamURL: rawURL,
		Protocols:   []protocol.Protocol{protocol.OpenAICompletions},
		Models:      optionalGroupModels{Set: true, Values: []GroupModel{}},
		Keys:        plaintext,
	})
	if err != nil {
		t.Fatalf("CreateGroup() error = %v", err)
	}
	var row models.UpstreamKey
	if err := fixture.db.Where("group_id = ?", result.GroupID).First(&row).Error; err != nil {
		t.Fatalf("query created key: %v", err)
	}
	logText := logs.String()
	if !strings.Contains(logText, "127.0.0.1") {
		t.Fatalf("private warning missing hostname: %s", logText)
	}
	for _, required := range []string{"[CONTROL]", "plane=control"} {
		if !strings.Contains(logText, required) {
			t.Fatalf("private warning missing %q: %s", required, logText)
		}
	}
	for _, forbidden := range []string{
		rawURL,
		"known-query-secret",
		plaintext,
		row.KeyValue,
		row.KeyHash,
		"signature",
	} {
		if strings.Contains(logText, forbidden) {
			t.Fatalf("private warning exposes %q: %s", forbidden, logText)
		}
	}
}

func TestCreateGroupRejectsNameCollisionWithoutMutation(t *testing.T) {
	fixture := newServiceFixture(t)
	name := "shared-name"
	first, err := fixture.service.CreateGroup(t.Context(), GroupCreateRequest{
		Name:        &name,
		UpstreamURL: "https://first.example.com",
		Protocols:   []protocol.Protocol{protocol.OpenAICompletions},
		Models:      optionalGroupModels{Set: true, Values: []GroupModel{}},
		Keys:        "sk-first",
	})
	if err != nil {
		t.Fatalf("initial CreateGroup() error = %v", err)
	}
	beforeRevision := fixture.manager.Current().Revision
	beforeCandidates := fixture.registry.CollectCandidates([]uint{first.GroupID}, nil, time.Time{})

	_, err = fixture.service.CreateGroup(t.Context(), GroupCreateRequest{
		Name:        &name,
		UpstreamURL: "https://second.example.com",
		Protocols:   []protocol.Protocol{protocol.Anthropic},
		Models:      optionalGroupModels{Set: true, Values: []GroupModel{}},
		Keys:        "sk-second",
	})
	var apiErr *app_errors.APIError
	if !errors.As(err, &apiErr) || apiErr.Code != app_errors.ErrDuplicateResource.Code ||
		apiErr.HTTPStatus != http.StatusConflict {
		t.Fatalf("name collision error = %#v", err)
	}
	assertGroupCount(t, fixture.db, 1)
	if fixture.manager.Current().Revision != beforeRevision {
		t.Fatal("name collision published a snapshot")
	}
	if !reflect.DeepEqual(fixture.registry.CollectCandidates([]uint{first.GroupID}, nil, time.Time{}), beforeCandidates) {
		t.Fatal("name collision changed Registry")
	}
}

func TestCreateGroupModelsPresenceRejectsNull(t *testing.T) {
	tests := []struct {
		name      string
		body      string
		wantSet   bool
		wantCount int
		wantError bool
	}{
		{
			name: "omitted",
			body: `{"upstream_url":"https://example.com","protocols":["openai-completions"],"keys":"sk-a"}`,
		},
		{
			name:    "empty",
			body:    `{"upstream_url":"https://example.com","protocols":["openai-completions"],"keys":"sk-a","models":[]}`,
			wantSet: true,
		},
		{
			name:      "values",
			body:      `{"upstream_url":"https://example.com","protocols":["openai-completions"],"keys":"sk-a","models":[{"id":" gpt-4o ","alias":" primary ","alias_enabled":true}]}`,
			wantSet:   true,
			wantCount: 1,
		},
		{
			name:      "missing alias enabled",
			body:      `{"upstream_url":"https://example.com","protocols":["openai-completions"],"keys":"sk-a","models":[{"id":"gpt-4o","alias":"primary"}]}`,
			wantError: true,
		},
		{
			name:      "null",
			body:      `{"upstream_url":"https://example.com","protocols":["openai-completions"],"keys":"sk-a","models":null}`,
			wantError: true,
		},
		{
			name:      "unknown model field",
			body:      `{"upstream_url":"https://example.com","protocols":["openai-completions"],"keys":"sk-a","models":[{"id":"gpt-4o","extra":true}]}`,
			wantError: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			decoder := json.NewDecoder(strings.NewReader(test.body))
			decoder.DisallowUnknownFields()
			var request GroupCreateRequest
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

	var modelValue optionalGroupModels
	if err := modelValue.UnmarshalJSON([]byte(`[{"id":"gpt-4o","alias":""}] []`)); err == nil {
		t.Fatal("UnmarshalJSON() accepted a trailing JSON value")
	}
}

func TestCreateGroupRequiresModelsAndAllowsEmptyArrayAtomically(t *testing.T) {
	fixture := newServiceFixture(t)
	beforeRevision := fixture.manager.Current().Revision
	_, err := fixture.service.CreateGroup(t.Context(), GroupCreateRequest{
		UpstreamURL: "https://empty-models.example.com",
		Protocols:   []protocol.Protocol{protocol.OpenAICompletions},
		Keys:        "sk-empty-models",
	})
	if !errors.Is(err, app_errors.ErrValidation) {
		t.Fatalf("CreateGroup(omitted models) error = %#v, want validation", err)
	}
	assertGroupCount(t, fixture.db, 0)
	if fixture.manager.Current().Revision != beforeRevision {
		t.Fatal("omitted models published a Snapshot")
	}

	result, err := fixture.service.CreateGroup(t.Context(), GroupCreateRequest{
		UpstreamURL: "https://empty-models.example.com",
		Protocols:   []protocol.Protocol{protocol.OpenAICompletions},
		Models:      optionalGroupModels{Set: true, Values: []GroupModel{}},
		Keys:        "sk-empty-models",
	})
	if err != nil {
		t.Fatalf("CreateGroup(empty models) error = %v", err)
	}
	if result.GroupID == 0 || result.KeysAdded != 1 || result.KeysDuplicated != 0 {
		t.Fatalf("CreateGroup(empty models) result = %#v", result)
	}
	if stored := loadCreatedGroupModels(t, fixture, result.GroupID); len(stored) != 0 {
		t.Fatalf("stored models = %#v, want empty", stored)
	}
	var group models.Group
	if err := fixture.db.First(&group, result.GroupID).Error; err != nil {
		t.Fatalf("query created Group: %v", err)
	}
	if string(group.Config) != `{}` {
		t.Fatalf("stored config = %s, want empty override", group.Config)
	}
	if fixture.manager.Current().Revision != beforeRevision+1 {
		t.Fatalf("snapshot revision = %d, want %d", fixture.manager.Current().Revision, beforeRevision+1)
	}
}

func TestCreateGroupRequiresSelectableProviderID(t *testing.T) {
	fixture := newServiceFixture(t)
	providerID := "private-company"
	request := GroupCreateRequest{
		ProviderID:  &providerID,
		UpstreamURL: "https://private-company.example/v1",
		Protocols:   []protocol.Protocol{protocol.OpenAICompletions},
		Models:      optionalGroupModels{Set: true, Values: []GroupModel{}},
		Keys:        "sk-private-company",
	}

	if _, err := fixture.service.CreateGroup(t.Context(), request); !errors.Is(err, app_errors.ErrValidation) {
		t.Fatalf("CreateGroup(unknown provider) error = %v, want ErrValidation", err)
	}
	assertGroupCount(t, fixture.db, 0)

	fixture.catalogRuntime.Publish(&catalog.Snapshot{Providers: map[string]catalog.Provider{
		providerID: {ID: providerID},
	}})
	result, err := fixture.service.CreateGroup(t.Context(), request)
	if err != nil {
		t.Fatalf("CreateGroup(catalog provider) error = %v", err)
	}
	var stored models.Group
	if err := fixture.db.First(&stored, result.GroupID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.ProviderID == nil || *stored.ProviderID != providerID {
		t.Fatalf("stored provider_id = %v, want %q", stored.ProviderID, providerID)
	}
}

func TestCreateGroupRejectsInvalidNormalizedInputs(t *testing.T) {
	fixture := newServiceFixture(t)
	blank := "   "
	tooLong := strings.Repeat("名", 86)
	controlName := "bad\nname"
	emptyModels := optionalGroupModels{Set: true, Values: []GroupModel{}}
	tests := []struct {
		name    string
		request GroupCreateRequest
	}{
		{name: "relative URL", request: GroupCreateRequest{UpstreamURL: "/v1", Protocols: []protocol.Protocol{protocol.OpenAICompletions}, Models: emptyModels, Keys: "sk"}},
		{name: "FTP URL", request: GroupCreateRequest{UpstreamURL: "ftp://example.com", Protocols: []protocol.Protocol{protocol.OpenAICompletions}, Models: emptyModels, Keys: "sk"}},
		{name: "userinfo URL", request: GroupCreateRequest{UpstreamURL: "https://user:pass@example.com", Protocols: []protocol.Protocol{protocol.OpenAICompletions}, Models: emptyModels, Keys: "sk"}},
		{name: "fragment URL", request: GroupCreateRequest{UpstreamURL: "https://example.com/#secret", Protocols: []protocol.Protocol{protocol.OpenAICompletions}, Models: emptyModels, Keys: "sk"}},
		{name: "missing host URL", request: GroupCreateRequest{UpstreamURL: "https:///v1", Protocols: []protocol.Protocol{protocol.OpenAICompletions}, Models: emptyModels, Keys: "sk"}},
		{name: "empty protocols", request: GroupCreateRequest{UpstreamURL: "https://example.com", Models: emptyModels, Keys: "sk"}},
		{name: "replaced completions protocol", request: GroupCreateRequest{UpstreamURL: "https://example.com", Protocols: []protocol.Protocol{"openai-chat-completions"}, Models: emptyModels, Keys: "sk"}},
		{name: "unknown protocol", request: GroupCreateRequest{UpstreamURL: "https://example.com", Protocols: []protocol.Protocol{"unknown"}, Models: emptyModels, Keys: "sk"}},
		{name: "empty keys", request: GroupCreateRequest{UpstreamURL: "https://example.com", Protocols: []protocol.Protocol{protocol.OpenAICompletions}, Models: emptyModels, Keys: " \n\t"}},
		{name: "too many keys", request: GroupCreateRequest{UpstreamURL: "https://example.com", Protocols: []protocol.Protocol{protocol.OpenAICompletions}, Models: emptyModels, Keys: groupCreateLines(1001)}},
		{name: "blank name", request: GroupCreateRequest{Name: &blank, UpstreamURL: "https://example.com", Protocols: []protocol.Protocol{protocol.OpenAICompletions}, Models: emptyModels, Keys: "sk"}},
		{name: "long name", request: GroupCreateRequest{Name: &tooLong, UpstreamURL: "https://example.com", Protocols: []protocol.Protocol{protocol.OpenAICompletions}, Models: emptyModels, Keys: "sk"}},
		{name: "control name", request: GroupCreateRequest{Name: &controlName, UpstreamURL: "https://example.com", Protocols: []protocol.Protocol{protocol.OpenAICompletions}, Models: emptyModels, Keys: "sk"}},
		{name: "blank model", request: GroupCreateRequest{UpstreamURL: "https://example.com", Protocols: []protocol.Protocol{protocol.OpenAICompletions}, Models: optionalGroupModels{Set: true, Values: []GroupModel{{ID: "   "}}}, Keys: "sk"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			beforeRevision := fixture.manager.Current().Revision
			_, err := fixture.service.CreateGroup(t.Context(), test.request)
			var apiErr *app_errors.APIError
			if !errors.As(err, &apiErr) || apiErr.Code != app_errors.ErrValidation.Code {
				t.Fatalf("CreateGroup() error = %#v", err)
			}
			assertGroupCount(t, fixture.db, 0)
			if fixture.manager.Current().Revision != beforeRevision {
				t.Fatal("invalid request published a snapshot")
			}
		})
	}
}

func TestCreateGroupPreservesApprovedURLNormalizationRules(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{name: "trim lower host and slash", raw: " HTTPS://API.Example.COM/v1/ ", want: "https://api.example.com/v1"},
		{name: "preserve fixed query", raw: "https://API.Example.com/base/?api-version=2026-01", want: "https://api.example.com/base?api-version=2026-01"},
		{name: "preserve internal double slash", raw: "https://api.example.com/a//b/", want: "https://api.example.com/a//b"},
		{name: "IPv6 without port", raw: "https://[::1]/v1/", want: "https://[::1]/v1"},
		{name: "IPv6 with port", raw: "http://[2001:db8::1]:8080/v1/", want: "http://[2001:db8::1]:8080/v1"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, _, err := normalizeUpstreamBaseURL(test.raw)
			if err != nil {
				t.Fatalf("normalizeUpstreamBaseURL(%q) error = %v", test.raw, err)
			}
			if got != test.want {
				t.Fatalf("normalizeUpstreamBaseURL(%q) = %q, want %q", test.raw, got, test.want)
			}
		})
	}
}

func TestCreateGroupPrivateHostClassificationUsesOnlyLiterals(t *testing.T) {
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

func TestCreateGroupAllowsExactlyOneThousandNonEmptyKeyLines(t *testing.T) {
	fixture := newServiceFixture(t)
	result, err := fixture.service.CreateGroup(t.Context(), GroupCreateRequest{
		UpstreamURL: "https://thousand.example.com",
		Protocols:   []protocol.Protocol{protocol.OpenAICompletions},
		Models:      optionalGroupModels{Set: true, Values: []GroupModel{}},
		Keys:        groupCreateLines(1000),
	})
	if err != nil {
		t.Fatalf("CreateGroup() error = %v", err)
	}
	if result.KeysAdded+result.KeysDuplicated != 1000 {
		t.Fatalf("key line count = %d, want 1000", result.KeysAdded+result.KeysDuplicated)
	}
}

func TestCreateGroupRollsBackWhenKeyEncryptionFails(t *testing.T) {
	fixture := newServiceFixture(t)
	seed, err := fixture.service.CreateGroup(t.Context(), GroupCreateRequest{
		UpstreamURL: "https://existing-rollback.example.com",
		Protocols:   []protocol.Protocol{protocol.OpenAICompletions},
		Models:      optionalGroupModels{Set: true, Values: []GroupModel{}},
		Keys:        "sk-existing-rollback",
	})
	if err != nil {
		t.Fatalf("seed CreateGroup() error = %v", err)
	}
	var existingKey models.UpstreamKey
	if err := fixture.db.Where("group_id = ?", seed.GroupID).First(&existingKey).Error; err != nil {
		t.Fatalf("query seeded key: %v", err)
	}
	if count, ok := fixture.registry.IncrFailure(existingKey.ID); !ok || count != 1 {
		t.Fatalf("first failure count = %d, %t", count, ok)
	}
	beforeCandidates := fixture.registry.CollectCandidates([]uint{seed.GroupID}, nil, time.Time{})
	fixture.service.encryption = groupCreateFailingEncryptService{Service: fixture.encryption}
	beforeRevision := fixture.manager.Current().Revision

	_, err = fixture.service.CreateGroup(t.Context(), GroupCreateRequest{
		UpstreamURL: "https://rollback.example.com",
		Protocols:   []protocol.Protocol{protocol.OpenAICompletions},
		Models: optionalGroupModels{
			Set: true, Values: []GroupModel{{ID: "must-not-persist"}},
		},
		Keys: "sk-must-not-persist",
	})
	if err == nil {
		t.Fatal("CreateGroup() error = nil, want encryption failure")
	}
	assertGroupCount(t, fixture.db, 1)
	if fixture.manager.Current().Revision != beforeRevision {
		t.Fatal("failed creation published a snapshot")
	}
	if candidates := fixture.registry.CollectCandidates([]uint{seed.GroupID}, nil, time.Time{}); !reflect.DeepEqual(candidates, beforeCandidates) {
		t.Fatalf("failed creation changed Registry: %#v", candidates)
	}
	if count, ok := fixture.registry.IncrFailure(existingKey.ID); !ok || count != 2 {
		t.Fatalf("second failure count = %d, %t, want 2", count, ok)
	}
}

type groupCreateFailingEncryptService struct {
	encryption.Service
}

func (groupCreateFailingEncryptService) Encrypt(string) (string, error) {
	return "", errors.New("forced encryption failure")
}

func loadCreatedGroupModels(t *testing.T, fixture serviceFixture, groupID uint) []GroupModel {
	t.Helper()
	var group models.Group
	if err := fixture.db.First(&group, groupID).Error; err != nil {
		t.Fatalf("query group %d: %v", groupID, err)
	}
	var result []GroupModel
	if err := json.Unmarshal(group.Models, &result); err != nil {
		t.Fatalf("decode group %d models: %v", groupID, err)
	}
	if result == nil {
		result = make([]GroupModel, 0)
	}
	return result
}

func assertStoredGroupProtocols(t *testing.T, group models.Group, want []protocol.Protocol) {
	t.Helper()
	var got []protocol.Protocol
	if err := json.Unmarshal(group.Protocols, &got); err != nil {
		t.Fatalf("decode group %d protocols: %v", group.ID, err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("group %d protocols = %#v, want %#v", group.ID, got, want)
	}
}

func groupCreateLines(count int) string {
	var builder strings.Builder
	for index := range count {
		builder.WriteString("sk-")
		builder.WriteString(strconv.Itoa(index))
		builder.WriteByte('\n')
	}
	return builder.String()
}
