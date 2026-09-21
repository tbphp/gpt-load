package control

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"

	"gpt-load/internal/channel"
	"gpt-load/internal/platform/config"
	app_errors "gpt-load/internal/platform/errors"
	stateloader "gpt-load/internal/state/loader"
	"gpt-load/internal/storage/models"
)

// createChannelGroup builds one API key group on an explicit channel so channel
// switch tests can start from any stored parameter shape.
func createChannelGroup(
	t *testing.T,
	fixture serviceFixture,
	channelID channel.ID,
	params string,
	credentials string,
) uint {
	t.Helper()
	name := fmt.Sprintf("channel-group-%d", testIdempotencySequence.Add(1))
	result, err := fixture.service.CreateGroup(t.Context(), GroupCreateRequest{
		Name: &name, ChannelID: channelID, Params: json.RawMessage(params),
		Models:      optionalGroupModels{Set: true, Values: []GroupModel{{ID: "gpt-4o"}}},
		Credentials: credentials, ConnectionType: "api_key",
	})
	if err != nil {
		t.Fatalf("CreateGroup(%q) error = %v", channelID, err)
	}
	return result.GroupID
}

func mustLoadGroup(t *testing.T, fixture serviceFixture, groupID uint) models.Group {
	t.Helper()
	group, err := loadGroupRow(fixture.db, groupID)
	if err != nil {
		t.Fatalf("loadGroupRow(%d) error = %v", groupID, err)
	}
	return group
}

func TestUpdateGroupChannelKeepsOperatorConfiguredBaseURL(t *testing.T) {
	t.Parallel()
	fixture := newServiceFixture(t)
	groupID := createChannelGroup(
		t, fixture, channel.OpenAICompatible,
		`{"base_url":"https://relay.example/v1"}`, "sk-keep-base-url",
	)

	got, err := fixture.service.UpdateGroupChannel(t.Context(), groupID, GroupChannelUpdateRequest{
		ChannelID: channel.OpenAI,
	})
	if err != nil {
		t.Fatalf("UpdateGroupChannel() error = %v", err)
	}
	if got.ChannelID != channel.OpenAI {
		t.Fatalf("response channel = %q, want %q", got.ChannelID, channel.OpenAI)
	}
	if string(got.Params) != `{"base_url":"https://relay.example/v1"}` {
		t.Fatalf("response params = %s, want operator base URL preserved", got.Params)
	}
	stored := mustLoadGroup(t, fixture, groupID)
	if stored.ChannelID != string(channel.OpenAI) ||
		string(stored.Params) != `{"base_url":"https://relay.example/v1"}` {
		t.Fatalf("stored group = %q %s", stored.ChannelID, stored.Params)
	}
}

func TestUpdateGroupChannelKeepsABaseURLThatMatchesTheChannelPreset(t *testing.T) {
	t.Parallel()
	fixture := newServiceFixture(t)
	fixedBaseURL, ok := fixture.channelRegistry.FixedBaseURL(channel.ZhipuAI)
	if !ok {
		t.Fatalf("FixedBaseURL(%q) is unavailable", channel.ZhipuAI)
	}
	groupID := createChannelGroup(
		t, fixture, channel.ZhipuAI,
		fmt.Sprintf(`{"base_url":%q}`, fixedBaseURL), "sk-keep-preset",
	)

	got, err := fixture.service.UpdateGroupChannel(t.Context(), groupID, GroupChannelUpdateRequest{
		ChannelID: channel.OpenAI,
	})
	if err != nil {
		t.Fatalf("UpdateGroupChannel() error = %v", err)
	}
	// A stored address is the operator's, even when it equals a channel preset:
	// nothing distinguishes a prefilled value from a typed one.
	if string(got.Params) != fmt.Sprintf(`{"base_url":%q}`, fixedBaseURL) {
		t.Fatalf("response params = %s, want the stored address kept", got.Params)
	}
}

func TestUpdateGroupChannelKeepsTheAddressWhenTheTargetRequiresOne(t *testing.T) {
	t.Parallel()
	fixture := newServiceFixture(t)
	groupID := createChannelGroup(
		t, fixture, channel.OpenAI,
		`{"base_url":"https://api.openai.com/v1"}`, "sk-required-target",
	)

	// Dropping the address here would leave a channel whose base URL is
	// mandatory with nothing to validate, failing a switch the operator can do.
	got, err := fixture.service.UpdateGroupChannel(t.Context(), groupID, GroupChannelUpdateRequest{
		ChannelID: channel.NewAPI,
	})
	if err != nil {
		t.Fatalf("UpdateGroupChannel() error = %v", err)
	}
	if string(got.Params) != `{"base_url":"https://api.openai.com/v1"}` {
		t.Fatalf("response params = %s, want the stored address kept", got.Params)
	}
}

func TestUpdateGroupChannelDropsParamsTheTargetChannelDoesNotDeclare(t *testing.T) {
	t.Parallel()
	fixture := newServiceFixture(t)
	groupID := createChannelGroup(
		t, fixture, channel.AzureOpenAI,
		`{"endpoint":"https://contoso.openai.azure.com"}`, "sk-drop-endpoint",
	)

	got, err := fixture.service.UpdateGroupChannel(t.Context(), groupID, GroupChannelUpdateRequest{
		ChannelID: channel.OpenAI,
	})
	if err != nil {
		t.Fatalf("UpdateGroupChannel() error = %v", err)
	}
	if string(got.Params) != `{}` {
		t.Fatalf("response params = %s, want the unknown field dropped", got.Params)
	}
}

func TestUpdateGroupChannelRejectsMissingRequiredTargetParam(t *testing.T) {
	t.Parallel()
	fixture := newServiceFixture(t)
	groupID := createChannelGroup(t, fixture, channel.OpenAI, `{}`, "sk-missing-required")

	_, err := fixture.service.UpdateGroupChannel(t.Context(), groupID, GroupChannelUpdateRequest{
		ChannelID: channel.NewAPI,
	})
	if !errors.Is(err, app_errors.ErrValidation) {
		t.Fatalf("UpdateGroupChannel() error = %v, want %v", err, app_errors.ErrValidation)
	}
	stored := mustLoadGroup(t, fixture, groupID)
	if stored.ChannelID != string(channel.OpenAI) {
		t.Fatalf("stored channel = %q, want the rejected switch to leave it unchanged", stored.ChannelID)
	}
}

func TestUpdateGroupChannelRejectsCredentialsTheTargetChannelCannotAccept(t *testing.T) {
	t.Parallel()
	fixture := newServiceFixture(t)
	groupID := createChannelGroup(t, fixture, channel.OpenAI, `{}`, "sk-incompatible")

	_, err := fixture.service.UpdateGroupChannel(t.Context(), groupID, GroupChannelUpdateRequest{
		ChannelID: channel.GoogleVertex,
	})
	var apiErr *app_errors.APIError
	if !errors.As(err, &apiErr) || apiErr.Code != app_errors.ErrValidation.Code {
		t.Fatalf("UpdateGroupChannel() error = %v, want %v", err, app_errors.ErrValidation)
	}
	// The operator needs to know which stored credential blocks the switch.
	data, ok := apiErr.Data.(credentialValidationData)
	if !ok || data.Entry != 1 {
		t.Fatalf("error data = %#v, want the first credential entry", apiErr.Data)
	}
	stored := mustLoadGroup(t, fixture, groupID)
	if stored.ChannelID != string(channel.OpenAI) {
		t.Fatalf("stored channel = %q, want the rejected switch to leave it unchanged", stored.ChannelID)
	}
}

func TestUpdateGroupChannelRejectsSubscriptionChannels(t *testing.T) {
	t.Parallel()
	fixture := newServiceFixture(t)
	groupID := createChannelGroup(t, fixture, channel.OpenAI, `{}`, "sk-subscription-target")

	_, err := fixture.service.UpdateGroupChannel(t.Context(), groupID, GroupChannelUpdateRequest{
		ChannelID: channel.Claude,
	})
	if !errors.Is(err, app_errors.ErrValidation) {
		t.Fatalf("UpdateGroupChannel(subscription target) error = %v, want %v", err, app_errors.ErrValidation)
	}
}

func TestUpdateGroupChannelRejectsUnknownChannel(t *testing.T) {
	t.Parallel()
	fixture := newServiceFixture(t)
	groupID := createChannelGroup(t, fixture, channel.OpenAI, `{}`, "sk-unknown-channel")

	_, err := fixture.service.UpdateGroupChannel(t.Context(), groupID, GroupChannelUpdateRequest{
		ChannelID: "does-not-exist",
	})
	if !errors.Is(err, app_errors.ErrValidation) {
		t.Fatalf("UpdateGroupChannel(unknown) error = %v, want %v", err, app_errors.ErrValidation)
	}
}

func TestUpdateGroupChannelRejectsNoOpSwitch(t *testing.T) {
	t.Parallel()
	fixture := newServiceFixture(t)
	groupID := createChannelGroup(t, fixture, channel.OpenAI, `{}`, "sk-same-channel")

	_, err := fixture.service.UpdateGroupChannel(t.Context(), groupID, GroupChannelUpdateRequest{
		ChannelID: channel.OpenAI,
	})
	if !errors.Is(err, app_errors.ErrValidation) {
		t.Fatalf("UpdateGroupChannel(same channel) error = %v, want %v", err, app_errors.ErrValidation)
	}
}

func TestUpdateGroupChannelResetsValidationProtocolForTheTargetChannel(t *testing.T) {
	t.Parallel()
	fixture := newServiceFixture(t)
	groupID := createChannelGroup(
		t, fixture, channel.OpenAICompatible,
		`{"base_url":"https://relay.example/v1"}`, "sk-validation-protocol",
	)
	before := mustLoadGroup(t, fixture, groupID)
	if before.ValidationProtocol == nil {
		t.Fatal("fixture group has no validation protocol to re-derive")
	}

	got, err := fixture.service.UpdateGroupChannel(t.Context(), groupID, GroupChannelUpdateRequest{
		ChannelID: channel.Anthropic,
	})
	if err != nil {
		t.Fatalf("UpdateGroupChannel() error = %v", err)
	}
	stored := mustLoadGroup(t, fixture, groupID)
	if stored.ValidationProtocol == nil {
		t.Fatal("stored validation protocol = nil, want a protocol the target channel probes")
	}
	target, err := fixture.channelRegistry.Resolve(channel.Anthropic, json.RawMessage(stored.Params))
	if err != nil {
		t.Fatalf("Resolve(%q) error = %v", channel.Anthropic, err)
	}
	supported := availableValidationProtocols(target)
	found := false
	for _, candidate := range supported {
		if string(candidate) == *stored.ValidationProtocol {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("stored validation protocol = %q, want one of %v", *stored.ValidationProtocol, supported)
	}
	if got.ValidationProtocol == nil || string(*got.ValidationProtocol) != *stored.ValidationProtocol {
		t.Fatalf("response validation protocol = %v, want %q", got.ValidationProtocol, *stored.ValidationProtocol)
	}
}

func TestUpdateGroupChannelRebuildsCredentialIdentityGeneration(t *testing.T) {
	t.Parallel()
	fixture := newServiceFixture(t)
	groupID := createChannelGroup(
		t, fixture, channel.OpenAICompatible,
		`{"base_url":"https://relay.example/v1"}`, "sk-identity-generation",
	)
	before, err := stateloader.BuildGroupCredentialEntriesWithProxy(
		t.Context(), fixture.db, groupID, fixture.encryption,
	)
	if err != nil || len(before) != 1 {
		t.Fatalf("BuildGroupCredentialEntriesWithProxy() = %d entries, error = %v", len(before), err)
	}

	if _, err := fixture.service.UpdateGroupChannel(t.Context(), groupID, GroupChannelUpdateRequest{
		ChannelID: channel.OpenAI,
	}); err != nil {
		t.Fatalf("UpdateGroupChannel() error = %v", err)
	}

	after, err := stateloader.BuildGroupCredentialEntriesWithProxy(
		t.Context(), fixture.db, groupID, fixture.encryption,
	)
	if err != nil || len(after) != 1 {
		t.Fatalf("BuildGroupCredentialEntriesWithProxy() = %d entries, error = %v", len(after), err)
	}
	if after[0].ID != before[0].ID || after[0].Fingerprint != before[0].Fingerprint {
		t.Fatalf("credential row changed: %#v -> %#v", before[0], after[0])
	}
	if after[0].IdentityGeneration == before[0].IdentityGeneration {
		t.Fatalf("identity generation = %d, want the channel switch to rebuild it", after[0].IdentityGeneration)
	}
	ref, ok := fixture.registry.CredentialRef(after[0].ID)
	if !ok || ref.IdentityGeneration != after[0].IdentityGeneration {
		t.Fatalf("registry ref = %#v, want identity generation %d", ref, after[0].IdentityGeneration)
	}
}

func TestUpdateGroupChannelKeepsStoredCredentialsUntouched(t *testing.T) {
	t.Parallel()
	fixture := newServiceFixture(t)
	groupID := createChannelGroup(
		t, fixture, channel.OpenAICompatible,
		`{"base_url":"https://relay.example/v1"}`, "sk-credentials-untouched",
	)
	var before []models.Credential
	if err := fixture.db.Where("group_id = ?", groupID).Order("id ASC").Find(&before).Error; err != nil {
		t.Fatalf("query credentials: %v", err)
	}

	if _, err := fixture.service.UpdateGroupChannel(t.Context(), groupID, GroupChannelUpdateRequest{
		ChannelID: channel.OpenAI,
	}); err != nil {
		t.Fatalf("UpdateGroupChannel() error = %v", err)
	}

	var after []models.Credential
	if err := fixture.db.Where("group_id = ?", groupID).Order("id ASC").Find(&after).Error; err != nil {
		t.Fatalf("query credentials: %v", err)
	}
	if len(after) != len(before) {
		t.Fatalf("credential count = %d, want %d", len(after), len(before))
	}
	for index := range before {
		if after[index].ID != before[index].ID ||
			after[index].Data != before[index].Data ||
			after[index].Fingerprint != before[index].Fingerprint ||
			after[index].SecretVersion != before[index].SecretVersion ||
			after[index].Status != before[index].Status {
			t.Fatalf("credential %d changed: %#v -> %#v", before[index].ID, before[index], after[index])
		}
	}
}

func TestUpdateGroupChannelReportsSameTargetConflict(t *testing.T) {
	t.Parallel()
	fixture := newServiceFixture(t)
	existing := createChannelGroup(
		t, fixture, channel.OpenAI,
		`{"base_url":"https://shared.example/v1"}`, "sk-conflict-existing",
	)
	groupID := createChannelGroup(
		t, fixture, channel.OpenAICompatible,
		`{"base_url":"https://shared.example/v1"}`, "sk-conflict-source",
	)

	_, err := fixture.service.UpdateGroupChannel(t.Context(), groupID, GroupChannelUpdateRequest{
		ChannelID: channel.OpenAI,
	})
	if err == nil {
		t.Fatal("UpdateGroupChannel() error = nil, want a same-target conflict")
	}
	var apiErr *app_errors.APIError
	if !errors.As(err, &apiErr) || apiErr.Code != app_errors.ErrChannelTargetConflict.Code {
		t.Fatalf("UpdateGroupChannel() error = %v, want %v", err, app_errors.ErrChannelTargetConflict)
	}

	// The operator confirms the shared target and the switch proceeds.
	if _, err := fixture.service.UpdateGroupChannel(t.Context(), groupID, GroupChannelUpdateRequest{
		ChannelID: channel.OpenAI, ConfirmSameTarget: true,
	}); err != nil {
		t.Fatalf("UpdateGroupChannel(confirmed) error = %v", err)
	}
	if stored := mustLoadGroup(t, fixture, groupID); stored.ChannelID != string(channel.OpenAI) {
		t.Fatalf("stored channel = %q, want %q", stored.ChannelID, channel.OpenAI)
	}
	if stored := mustLoadGroup(t, fixture, existing); stored.ChannelID != string(channel.OpenAI) {
		t.Fatalf("existing group changed: %q", stored.ChannelID)
	}
}

func TestUpdateGroupChannelPublishesTheTargetChannelSnapshot(t *testing.T) {
	t.Parallel()
	fixture := newServiceFixture(t)
	groupID := createChannelGroup(
		t, fixture, channel.OpenAICompatible,
		`{"base_url":"https://relay.example/v1"}`, "sk-snapshot",
	)

	if _, err := fixture.service.UpdateGroupChannel(t.Context(), groupID, GroupChannelUpdateRequest{
		ChannelID: channel.OpenAI,
	}); err != nil {
		t.Fatalf("UpdateGroupChannel() error = %v", err)
	}

	snapshot := fixture.manager.Current()
	if snapshot == nil {
		t.Fatal("runtime snapshot = nil")
	}
	view, ok := snapshot.GroupCatalog[groupID]
	if !ok || view.ChannelID != channel.OpenAI {
		t.Fatalf("snapshot group catalog = %#v, want channel %q", view, channel.OpenAI)
	}
}

func TestUpdateGroupChannelRejectsUnknownGroup(t *testing.T) {
	t.Parallel()
	fixture := newServiceFixture(t)

	_, err := fixture.service.UpdateGroupChannel(t.Context(), 4242, GroupChannelUpdateRequest{
		ChannelID: channel.OpenAI,
	})
	if !errors.Is(err, app_errors.ErrResourceNotFound) {
		t.Fatalf("UpdateGroupChannel(missing group) error = %v, want %v", err, app_errors.ErrResourceNotFound)
	}
}

func TestMigrateGroupParamsKeepsSharedKeysAndDropsTheRest(t *testing.T) {
	t.Parallel()
	registry := channel.NewRegistry()
	fixedBaseURL, ok := registry.FixedBaseURL(channel.ZhipuAI)
	if !ok {
		t.Fatalf("FixedBaseURL(%q) is unavailable", channel.ZhipuAI)
	}
	cases := []struct {
		name    string
		to      channel.ID
		current string
		want    string
	}{
		{
			name:    "operator base URL survives",
			to:      channel.OpenAI,
			current: `{"base_url":"https://relay.example/v1"}`,
			want:    `{"base_url":"https://relay.example/v1"}`,
		},
		{
			name:    "a base URL matching the previous preset is still the operator's",
			to:      channel.OpenAI,
			current: fmt.Sprintf(`{"base_url":%q}`, fixedBaseURL),
			want:    fmt.Sprintf(`{"base_url":%q}`, fixedBaseURL),
		},
		{
			name:    "unknown key is dropped",
			to:      channel.OpenAI,
			current: `{"endpoint":"https://contoso.openai.azure.com"}`,
			want:    `{}`,
		},
		{
			name:    "target default fills a declared key",
			to:      channel.GoogleVertex,
			current: `{"base_url":"https://relay.example/v1"}`,
			want:    `{"location":"global"}`,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			got, err := migrateGroupParams(
				registry, testCase.to, models.JSON(testCase.current),
			)
			if err != nil {
				t.Fatalf("migrateGroupParams() error = %v", err)
			}
			if string(got) != testCase.want {
				t.Fatalf("migrateGroupParams() = %s, want %s", got, testCase.want)
			}
		})
	}
}

func TestMigrateGroupParamsRejectsAMissingRequiredTargetKey(t *testing.T) {
	t.Parallel()
	registry := channel.NewRegistry()

	_, err := migrateGroupParams(registry, channel.NewAPI, models.JSON(`{}`))
	if !errors.Is(err, app_errors.ErrValidation) {
		t.Fatalf("migrateGroupParams() error = %v, want %v", err, app_errors.ErrValidation)
	}
}

func TestGroupChannelHTTPSwitchesTheStoredChannel(t *testing.T) {
	t.Parallel()
	initControlI18n(t)
	fixture := newServiceFixture(t)
	groupID := createChannelGroup(
		t, fixture, channel.OpenAICompatible,
		`{"base_url":"https://relay.example/v1"}`, "sk-http-switch",
	)
	engine := gin.New()
	NewServer(&config.Config{AuthKey: "test-auth-key"}, fixture.service).RegisterRoutes(engine)
	path := "/api/groups/" + stringGroupID(groupID) + "/channel"

	recorder := serveGroupSettingsRequest(
		t, engine, http.MethodPut, path, "test-auth-key", `{"channel_id":"openai"}`,
	)
	if recorder.Code != http.StatusOK {
		t.Fatalf("PUT channel = %d %s, want 200", recorder.Code, recorder.Body.String())
	}
	if stored := mustLoadGroup(t, fixture, groupID); stored.ChannelID != string(channel.OpenAI) {
		t.Fatalf("stored channel = %q, want %q", stored.ChannelID, channel.OpenAI)
	}
}

func TestGroupChannelHTTPRejectsStrictJSONAndUnauthorizedWithoutMutation(t *testing.T) {
	t.Parallel()
	initControlI18n(t)
	fixture := newServiceFixture(t)
	groupID := createChannelGroup(
		t, fixture, channel.OpenAICompatible,
		`{"base_url":"https://relay.example/v1"}`, "sk-http-strict",
	)
	engine := gin.New()
	NewServer(&config.Config{AuthKey: "test-auth-key"}, fixture.service).RegisterRoutes(engine)
	path := "/api/groups/" + stringGroupID(groupID) + "/channel"
	beforeRevision := fixture.manager.Current().Revision

	for _, body := range []string{
		`{"unknown":true}`,
		`{"channel_id":"openai","channel_id":"anthropic"}`,
		`{"channel_id":""}`,
		`{"channel_id":"claude"}`,
		`{"channel_id":"openai_compatible"}`,
		`{"channel_id":"openai","params":{"base_url":"https://injected.example/v1"}}`,
	} {
		recorder := serveGroupSettingsRequest(t, engine, http.MethodPut, path, "test-auth-key", body)
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("PUT channel body %s = %d %s, want 400", body, recorder.Code, recorder.Body.String())
		}
	}
	unauthorized := serveGroupSettingsRequest(t, engine, http.MethodPut, path, "", `{"channel_id":"openai"}`)
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized PUT channel = %d %s", unauthorized.Code, unauthorized.Body.String())
	}
	if got := fixture.manager.Current().Revision; got != beforeRevision {
		t.Fatalf("rejected HTTP channel updates revision = %d, want %d", got, beforeRevision)
	}
	if stored := mustLoadGroup(t, fixture, groupID); stored.ChannelID != string(channel.OpenAICompatible) {
		t.Fatalf("stored channel = %q, want it unchanged", stored.ChannelID)
	}
}

func TestGroupChannelHTTPMissingGroupDoesNotMutate(t *testing.T) {
	t.Parallel()
	initControlI18n(t)
	fixture := newServiceFixture(t)
	engine := gin.New()
	NewServer(&config.Config{AuthKey: "test-auth-key"}, fixture.service).RegisterRoutes(engine)
	beforeRevision := fixture.manager.Current().Revision

	missing := serveGroupSettingsRequest(
		t, engine, http.MethodPut, "/api/groups/999/channel", "test-auth-key", `{"channel_id":"openai"}`,
	)
	if missing.Code != http.StatusNotFound {
		t.Fatalf("missing group PUT channel = %d %s, want 404", missing.Code, missing.Body.String())
	}
	if got := fixture.manager.Current().Revision; got != beforeRevision {
		t.Fatalf("missing group published revision %d, want %d", got, beforeRevision)
	}
}
