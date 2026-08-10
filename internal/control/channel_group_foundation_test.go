package control

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"gpt-load/internal/channel"
	"gpt-load/internal/platform/config"
	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/storage/models"
)

func TestServiceUsesInjectedChannelRegistryAndListsSafeDescriptors(t *testing.T) {
	t.Parallel()

	fixture := newServiceFixture(t)
	if fixture.service.channelRegistry != fixture.channelRegistry {
		t.Fatal("Service did not retain the injected ChannelRegistry")
	}

	result, err := fixture.service.ListChannels(context.Background(), "openai")
	if err != nil {
		t.Fatalf("ListChannels() error = %v", err)
	}
	if result.Total != 3 || len(result.Items) != 3 ||
		result.Items[0].ID != channel.OpenAI || result.Items[1].ID != channel.AzureOpenAI ||
		result.Items[2].ID != channel.OpenAICompatible {
		t.Fatalf("ListChannels(openai) = %#v", result)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	text := strings.ToLower(string(encoded))
	implementationName := "bif" + "rost"
	if strings.Contains(text, "target_config") || strings.Contains(text, "catalog_provider_id") ||
		strings.Contains(text, implementationName) || strings.Contains(text, "credential_value") {
		t.Fatalf("channel descriptors leaked internal execution data: %s", encoded)
	}
	common, err := fixture.service.ListChannels(context.Background(), "deep seek")
	if err != nil || common.Total != 1 || len(common.Items) != 1 ||
		common.Items[0].ID != channel.DeepSeek || len(common.Items[0].ParamFields) != 1 ||
		common.Items[0].ParamFields[0].Key != "base_url" || common.Items[0].ParamFields[0].Required {
		t.Fatalf("ListChannels(deep seek) = %#v, %v", common, err)
	}
	commonJSON, err := json.Marshal(common)
	if err != nil || strings.Contains(strings.ToLower(string(commonJSON)), "deepseek.com") {
		t.Fatalf("common preset leaked fixed target: %s, %v", commonJSON, err)
	}
}

func TestParseChannelQueryIsStrict(t *testing.T) {
	t.Parallel()

	if got, apiErr := parseChannelQuery("q=%20claude%20", false); apiErr != nil || got != "claude" {
		t.Fatalf("parseChannelQuery() = %q, %v", got, apiErr)
	}
	for _, raw := range []string{"unknown=1", "q=a&q=b"} {
		if _, apiErr := parseChannelQuery(raw, false); apiErr != app_errors.ErrBadRequest {
			t.Fatalf("parseChannelQuery(%q) error = %v, want bad request", raw, apiErr)
		}
	}
	if _, apiErr := parseChannelQuery("", true); apiErr != app_errors.ErrBadRequest {
		t.Fatalf("parseChannelQuery(force query) error = %v, want bad request", apiErr)
	}
}

func TestChannelsHTTPIsAuthenticatedAndStrict(t *testing.T) {
	initControlI18n(t)
	fixture := newServiceFixture(t)
	engine := gin.New()
	NewServer(&config.Config{AuthKey: "channel-auth"}, fixture.service).RegisterRoutes(engine)

	request := httptest.NewRequest(http.MethodGet, "/api/channels?q=deep%20seek", nil)
	request.Header.Set("Authorization", "Bearer channel-auth")
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)
	body := recorder.Body.String()
	if recorder.Code != http.StatusOK || !strings.Contains(body, `"channel_id":"deepseek"`) {
		t.Fatalf("authenticated channels response = %d %s", recorder.Code, recorder.Body.String())
	}
	if strings.Contains(body, `"param_fields":null`) ||
		strings.Contains(body, `"credential_fields":null`) ||
		strings.Contains(body, `"client_protocols":null`) {
		t.Fatalf("channels response must encode descriptor collections as arrays: %s", body)
	}

	for name, path := range map[string]string{
		"unauthenticated": "/api/channels",
		"unknown query":   "/api/channels?unknown=1",
	} {
		t.Run(name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodGet, path, nil)
			if name != "unauthenticated" {
				request.Header.Set("Authorization", "Bearer channel-auth")
			}
			engine.ServeHTTP(recorder, request)
			if name == "unauthenticated" && recorder.Code != http.StatusUnauthorized {
				t.Fatalf("response = %d %s, want 401", recorder.Code, recorder.Body.String())
			}
			if name == "unknown query" && recorder.Code != http.StatusBadRequest {
				t.Fatalf("response = %d %s, want 400", recorder.Code, recorder.Body.String())
			}
		})
	}

	for _, path := range []string{
		"/api/provider-suggestions",
		"/api/providers/openai/models",
	} {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, path, nil)
		request.Header.Set("Authorization", "Bearer channel-auth")
		engine.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusNotFound {
			t.Fatalf("retired provider catalog route %q = %d %s, want 404", path, recorder.Code, recorder.Body.String())
		}
	}
}

func TestGroupCreateWireAcceptsOnlyChannelContract(t *testing.T) {
	t.Parallel()

	var request GroupCreateRequest
	valid := []byte(`{
		"channel_id":"openai_compatible",
		"params":{"base_url":"https://proxy.example/v1"},
		"models":[],
		"credentials":"sk-one",
		"confirm_same_target":true
	}`)
	if err := decodeStrictControlJSONObject(valid, &request); err != nil {
		t.Fatalf("decode channel GroupCreateRequest: %v", err)
	}
	if request.ChannelID != channel.OpenAICompatible || request.Credentials != "sk-one" || !request.ConfirmSameTarget {
		t.Fatalf("GroupCreateRequest = %#v", request)
	}
	for _, legacy := range []string{"keys", "provider_id", "upstream_url", "protocols", "confirm_same_upstream_url"} {
		body := []byte(`{"channel_id":"openai","models":[],"credentials":"sk-one","` + legacy + `":null}`)
		if err := decodeStrictControlJSONObject(body, &GroupCreateRequest{}); err == nil {
			t.Fatalf("legacy field %q was accepted", legacy)
		}
	}
}

func TestCreateChannelGroupPersistsCanonicalCredentialsAndPublishes(t *testing.T) {
	fixture := newServiceFixture(t)
	fixture.service.now = func() time.Time { return time.UnixMilli(1_786_000_000_000) }
	beforeRevision := fixture.manager.Current().Revision

	result, err := fixture.service.CreateGroup(t.Context(), GroupCreateRequest{
		Name:      stringPointer(" channel group "),
		ChannelID: channel.OpenAICompatible,
		Params:    json.RawMessage(`{"base_url":" HTTPS://Proxy.Example/v1/ "}`),
		Models: optionalGroupModels{Set: true, Values: []GroupModel{
			{ID: " provider-model ", Alias: " public ", AliasEnabled: true},
		}},
		Credentials: " sk-one \n sk-one\nsk-two\n",
	})
	if err != nil {
		t.Fatalf("CreateGroup() error = %v", err)
	}
	if result.CredentialsAdded != 2 || result.CredentialsDuplicated != 1 {
		t.Fatalf("CreateGroup() result = %#v", result)
	}
	encodedResult, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("json.Marshal(result) error = %v", err)
	}
	var resultFields map[string]json.RawMessage
	if err := json.Unmarshal(encodedResult, &resultFields); err != nil {
		t.Fatalf("json.Unmarshal(result) error = %v", err)
	}
	if len(resultFields) != 4 || resultFields["credentials_added"] == nil ||
		resultFields["credentials_duplicated"] == nil || resultFields["keys_added"] != nil {
		t.Fatalf("result fields = %s", encodedResult)
	}

	var group models.Group
	if err := fixture.db.First(&group, result.GroupID).Error; err != nil {
		t.Fatalf("load group: %v", err)
	}
	if group.ChannelID != string(channel.OpenAICompatible) || string(group.Params) != `{"base_url":"https://proxy.example/v1"}` {
		t.Fatalf("stored group = %#v", group)
	}
	var credentials []models.Credential
	if err := fixture.db.Where("group_id = ?", group.ID).Order("id ASC").Find(&credentials).Error; err != nil {
		t.Fatalf("load credentials: %v", err)
	}
	if len(credentials) != 2 {
		t.Fatalf("credentials = %#v, want two", credentials)
	}
	wantCanonical := []string{`{"api_key":"sk-one"}`, `{"api_key":"sk-two"}`}
	for index, row := range credentials {
		plaintext, decryptErr := fixture.encryption.Decrypt(row.Data)
		if decryptErr != nil {
			t.Fatalf("decrypt credential %d: %v", row.ID, decryptErr)
		}
		if plaintext != wantCanonical[index] || row.Fingerprint != fixture.encryption.Hash(wantCanonical[index]) ||
			row.UpdatedAtMS < 1 {
			t.Fatalf("credential %d = plaintext %q row %#v", index, plaintext, row)
		}
	}
	snapshot := fixture.manager.Current()
	view, ok := snapshot.Groups[group.ID]
	if snapshot.Revision != beforeRevision+1 || !ok || view.ChannelID != channel.OpenAICompatible ||
		string(view.Params) != `{"base_url":"https://proxy.example/v1"}` {
		t.Fatalf("published snapshot = revision %d group %#v exists=%t", snapshot.Revision, view, ok)
	}
	refs := fixture.registry.CaptureActiveCredentialRefs([]uint{group.ID})
	if len(refs) != 2 || refs[0].Version == 0 || refs[0].IdentityGeneration == 0 || refs[0].Fingerprint == "" {
		t.Fatalf("credential refs = %#v", refs)
	}
	if got := loadCreatedGroupModels(t, fixture, group.ID); !reflect.DeepEqual(got, []GroupModel{{ID: "provider-model", Alias: "public"}}) {
		t.Fatalf("stored models = %#v", got)
	}
}

func TestCreateChannelGroupUsesChannelAndCanonicalParamsForSimilarity(t *testing.T) {
	fixture := newServiceFixture(t)
	first, err := fixture.service.CreateGroup(t.Context(), GroupCreateRequest{
		ChannelID:   channel.OpenAICompatible,
		Params:      json.RawMessage(`{"base_url":"HTTPS://Same.Example/v1/"}`),
		Models:      optionalGroupModels{Set: true, Values: []GroupModel{}},
		Credentials: "first",
	})
	if err != nil {
		t.Fatalf("first CreateGroup() error = %v", err)
	}
	_, err = fixture.service.CreateGroup(t.Context(), GroupCreateRequest{
		ChannelID:   channel.OpenAICompatible,
		Params:      json.RawMessage(`{"base_url":"https://same.example/v1"}`),
		Models:      optionalGroupModels{Set: true, Values: []GroupModel{}},
		Credentials: "second",
	})
	var apiErr *app_errors.APIError
	if !errors.As(err, &apiErr) || apiErr.Code != app_errors.ErrChannelTargetConflict.Code {
		t.Fatalf("same target error = %v", err)
	}
	data, ok := apiErr.Data.(SameTargetConflictData)
	if !ok || len(data.Groups) != 1 || data.Groups[0].ID != first.GroupID {
		t.Fatalf("same target conflict data = %#v", apiErr.Data)
	}

	confirmed, err := fixture.service.CreateGroup(t.Context(), GroupCreateRequest{
		ChannelID:         channel.OpenAICompatible,
		Params:            json.RawMessage(`{"base_url":"https://same.example/v1"}`),
		Models:            optionalGroupModels{Set: true, Values: []GroupModel{}},
		Credentials:       "second",
		ConfirmSameTarget: true,
	})
	if err != nil || confirmed.GroupID == 0 {
		t.Fatalf("confirmed CreateGroup() = %#v, %v", confirmed, err)
	}

	otherChannel, err := fixture.service.CreateGroup(t.Context(), GroupCreateRequest{
		Name:        stringPointer("same URL other channel"),
		ChannelID:   channel.AnthropicCompatible,
		Params:      json.RawMessage(`{"base_url":"https://same.example/v1"}`),
		Models:      optionalGroupModels{Set: true, Values: []GroupModel{}},
		Credentials: "third",
	})
	if err != nil || otherChannel.GroupID == 0 {
		t.Fatalf("other-channel CreateGroup() = %#v, %v", otherChannel, err)
	}
}

func TestCreateChannelGroupIdempotencyReplaysCredentialCounts(t *testing.T) {
	fixture := newServiceFixture(t)
	request := GroupCreateRequest{
		Name:        stringPointer("idempotent channel"),
		ChannelID:   channel.OpenAI,
		Params:      json.RawMessage(`{}`),
		Models:      optionalGroupModels{Set: true, Values: []GroupModel{}},
		Credentials: " repeated \nrepeated\n",
	}
	const idempotencyKey = "328f47a2-9c35-4d6e-8b1a-1234567890ab"
	first, err := fixture.service.CreateGroupIdempotent(t.Context(), idempotencyKey, request)
	if err != nil {
		t.Fatalf("first CreateGroupIdempotent() error = %v", err)
	}
	replayed, err := fixture.service.CreateGroupIdempotent(t.Context(), idempotencyKey, request)
	if err != nil {
		t.Fatalf("replay CreateGroupIdempotent() error = %v", err)
	}
	if !reflect.DeepEqual(replayed, first) || first.CredentialsAdded != 1 || first.CredentialsDuplicated != 1 {
		t.Fatalf("first/replayed = %#v / %#v", first, replayed)
	}
	var credentials int64
	if err := fixture.db.Model(&models.Credential{}).Where("group_id = ?", first.GroupID).Count(&credentials).Error; err != nil {
		t.Fatalf("count credentials: %v", err)
	}
	if credentials != 1 || len(fixture.registry.CaptureActiveCredentialRefs([]uint{first.GroupID})) != 1 {
		t.Fatalf("credential state = db %d registry %#v", credentials, fixture.registry.Snapshot())
	}
}

func TestChannelGroupSettingsExposeImmutableChannelAndEditableParams(t *testing.T) {
	fixture := newServiceFixture(t)
	created, err := fixture.service.CreateGroup(t.Context(), GroupCreateRequest{
		Name:        stringPointer("settings channel"),
		ChannelID:   channel.OpenAICompatible,
		Params:      json.RawMessage(`{"base_url":"https://first.example/v1"}`),
		Models:      optionalGroupModels{Set: true, Values: []GroupModel{}},
		Credentials: "settings-key",
	})
	if err != nil {
		t.Fatalf("CreateGroup() error = %v", err)
	}
	settings, err := fixture.service.GetGroupSettings(t.Context(), created.GroupID)
	if err != nil {
		t.Fatalf("GetGroupSettings() error = %v", err)
	}
	if settings.ChannelID != channel.OpenAICompatible || string(settings.Params) != `{"base_url":"https://first.example/v1"}` {
		t.Fatalf("settings = %#v", settings)
	}
	encoded, err := json.Marshal(settings)
	if err != nil {
		t.Fatalf("json.Marshal(settings) error = %v", err)
	}
	text := string(encoded)
	for _, legacy := range []string{"provider_id", "upstream_url", "protocols"} {
		if strings.Contains(text, `"`+legacy+`"`) {
			t.Fatalf("settings leaked legacy field %q: %s", legacy, text)
		}
	}

	updated, err := fixture.service.UpdateGroupSettings(t.Context(), created.GroupID, GroupSettingsUpdateRequest{
		Params: optionalField[json.RawMessage]{
			Set: true, Value: json.RawMessage(`{"base_url":" HTTPS://Second.Example/v1/ "}`),
		},
	})
	if err != nil {
		t.Fatalf("UpdateGroupSettings(params) error = %v", err)
	}
	if updated.ChannelID != channel.OpenAICompatible || string(updated.Params) != `{"base_url":"https://second.example/v1"}` {
		t.Fatalf("updated settings = %#v", updated)
	}

	for _, body := range []string{
		`{"channel_id":"anthropic"}`,
		`{"upstream_url":"https://legacy.example/v1"}`,
		`{"protocols":["anthropic"]}`,
		`{"provider_id":"anthropic"}`,
	} {
		if err := decodeStrictControlJSONObject([]byte(body), &GroupSettingsUpdateRequest{}); err == nil {
			t.Fatalf("legacy or immutable settings body accepted: %s", body)
		}
	}
}

func TestChannelGroupCollectionDetailAndOptionsUseChannelCredentialContract(t *testing.T) {
	fixture := newServiceFixture(t)
	created, err := fixture.service.CreateGroup(t.Context(), GroupCreateRequest{
		Name:      stringPointer("collection channel"),
		ChannelID: channel.OpenAICompatible,
		Params:    json.RawMessage(`{"base_url":"https://collection.example/v1"}`),
		Models: optionalGroupModels{Set: true, Values: []GroupModel{
			{ID: "provider-model", Alias: "public-model", AliasEnabled: true},
		}},
		Credentials: "collection-key",
	})
	if err != nil {
		t.Fatalf("CreateGroup() error = %v", err)
	}
	legacyCredentialQueries := 0
	var credentialSelects []string
	const callbackName = "test:channel_collection_no_legacy_credentials"
	if err := fixture.db.Callback().Query().Before("gorm:query").Register(callbackName, func(tx *gorm.DB) {
		if tx.Statement.Table == "upstream_keys" {
			legacyCredentialQueries++
		}
		if tx.Statement.Table == "credentials" {
			credentialSelects = append([]string(nil), tx.Statement.Selects...)
		}
	}); err != nil {
		t.Fatalf("register query observer: %v", err)
	}
	t.Cleanup(func() { _ = fixture.db.Callback().Query().Remove(callbackName) })

	collection, err := fixture.service.ListGroupCollection(t.Context(), GroupCollectionQuery{
		Page: 1, PageSize: 20,
	})
	if err != nil || len(collection.Items) != 1 {
		t.Fatalf("ListGroupCollection() = %#v, %v", collection, err)
	}
	if legacyCredentialQueries != 0 {
		t.Fatalf("legacy upstream key queries = %d, want 0", legacyCredentialQueries)
	}
	if want := []string{"id", "group_id", "fingerprint", "status", "weight_manual", "updated_at_ms"}; !reflect.DeepEqual(credentialSelects, want) {
		t.Fatalf("credential SELECT columns = %#v, want %#v", credentialSelects, want)
	}
	item := collection.Items[0]
	if item.ChannelID != channel.OpenAICompatible || string(item.Params) != `{"base_url":"https://collection.example/v1"}` ||
		item.CredentialCounts.Total != 1 || item.CredentialCounts.Available != 1 {
		t.Fatalf("collection item = %#v", item)
	}
	assertNoLegacyGroupFields(t, item)

	summary, err := fixture.service.GetGroupSummary(t.Context(), created.GroupID)
	if err != nil || summary.ChannelID != channel.OpenAICompatible ||
		string(summary.Params) != `{"base_url":"https://collection.example/v1"}` || summary.CredentialCount != 1 {
		t.Fatalf("GetGroupSummary() = %#v, %v", summary, err)
	}
	assertNoLegacyGroupFields(t, summary)

	options, err := fixture.service.ListGroupOptions(t.Context())
	if err != nil || len(options) != 1 || options[0].ChannelID != channel.OpenAICompatible ||
		string(options[0].Params) != `{"base_url":"https://collection.example/v1"}` ||
		!reflect.DeepEqual(options[0].Models, []string{"public-model"}) {
		t.Fatalf("ListGroupOptions() = %#v, %v", options, err)
	}
	assertNoLegacyGroupFields(t, options[0])

	if err := fixture.service.DeleteGroup(t.Context(), created.GroupID); err != nil {
		t.Fatalf("DeleteGroup() error = %v", err)
	}
	var credentials int64
	if err := fixture.db.Model(&models.Credential{}).Where("group_id = ?", created.GroupID).Count(&credentials).Error; err != nil {
		t.Fatalf("count credentials after delete: %v", err)
	}
	if credentials != 0 || len(fixture.registry.CaptureActiveCredentialRefs([]uint{created.GroupID})) != 0 {
		t.Fatalf("deleted credential state = db %d registry %#v", credentials, fixture.registry.Snapshot())
	}
	if _, exists := fixture.manager.Current().Groups[created.GroupID]; exists {
		t.Fatalf("deleted group %d remains in snapshot", created.GroupID)
	}
}

func assertNoLegacyGroupFields(t *testing.T, value any) {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	for _, legacy := range []string{"provider_id", "upstream_url", "protocols", "key_count", "key_counts"} {
		if strings.Contains(string(encoded), `"`+legacy+`"`) {
			t.Fatalf("response leaked legacy field %q: %s", legacy, encoded)
		}
	}
}
