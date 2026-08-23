package control

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"gpt-load/internal/channel"
	"gpt-load/internal/outboundproxy"
	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/storage/models"
)

func TestGroupAndCredentialProxyUseFinalPrecedenceAndEncryptedStorage(t *testing.T) {
	fixture := newServiceFixture(t)
	runtime := &recordingCredentialRuntimeExecutor{}
	fixture.service.executor = runtime
	groupID := createGroupWithCredentials(t, fixture, "sk-proxy-test")

	if _, err := fixture.service.UpdateSettings(t.Context(), SettingsUpdateRequest{
		Settings: map[string]json.RawMessage{outboundproxy.SystemSettingKey: json.RawMessage(
			`{"mode":"custom","url":"http://global-user:global-password@global-proxy.example.com:8080"}`,
		)},
	}); err != nil {
		t.Fatalf("set global proxy: %v", err)
	}
	group, err := fixture.service.GetGroupSettings(t.Context(), groupID)
	if err != nil {
		t.Fatal(err)
	}
	if group.Proxy.ConfiguredMode != outboundproxy.ModeInherit || group.Proxy.EffectiveSource != outboundproxy.SourceGlobal {
		t.Fatalf("inherited group proxy = %#v", group.Proxy)
	}

	group, err = fixture.service.UpdateGroupSettings(t.Context(), groupID, GroupSettingsUpdateRequest{
		Proxy: optionalField[outboundproxy.Config]{Set: true, Value: outboundproxy.Config{
			Mode: outboundproxy.ModeCustom,
			URL:  "socks5://group-user:group-password@group-proxy.example.com:1080",
		}},
	})
	if err != nil {
		t.Fatalf("UpdateGroupSettings(proxy) error = %v", err)
	}
	if group.Proxy.EffectiveSource != outboundproxy.SourceGroup ||
		group.Proxy.DisplayURL != "socks5://group-user:******@group-proxy.example.com:1080" {
		t.Fatalf("group proxy = %#v", group.Proxy)
	}

	credentials, err := fixture.service.ListGroupCredentials(t.Context(), groupID, CredentialCollectionQuery{Page: 1, PageSize: 20})
	if err != nil {
		t.Fatal(err)
	}
	if len(credentials.Items) != 1 || credentials.Items[0].Proxy.EffectiveSource != outboundproxy.SourceGroup {
		t.Fatalf("inherited credential proxy = %#v", credentials.Items)
	}
	credentialID := credentials.Items[0].CredentialID
	secretVersion := credentials.Items[0].SecretVersion

	updated, err := fixture.service.UpdateGroupCredential(t.Context(), groupID, credentialID, CredentialUpdateRequest{
		Proxy: optionalField[outboundproxy.Config]{Set: true, Value: outboundproxy.Config{Mode: outboundproxy.ModeDirect}},
	})
	if err != nil {
		t.Fatalf("UpdateGroupCredential(proxy) error = %v", err)
	}
	if updated.Proxy.ConfiguredMode != outboundproxy.ModeDirect ||
		updated.Proxy.EffectiveMode != outboundproxy.ModeDirect ||
		updated.Proxy.EffectiveSource != outboundproxy.SourceCredential {
		t.Fatalf("credential proxy = %#v", updated.Proxy)
	}
	if updated.SecretVersion != secretVersion {
		t.Fatalf("proxy update changed secret version %d -> %d", secretVersion, updated.SecretVersion)
	}
	if got := runtime.retiredCredentialIDs(); len(got) != 1 || got[0] != credentialID {
		t.Fatalf("retired credential runtimes = %v, want [%d]", got, credentialID)
	}
	ref, ok := fixture.registry.CredentialRef(credentialID)
	if !ok || ref.EncryptedProxy == "" || ref.ProxyFingerprint == "" {
		t.Fatalf("credential registry proxy identity = %#v, %t", ref, ok)
	}

	var groupRow models.Group
	if err := fixture.db.Take(&groupRow, groupID).Error; err != nil {
		t.Fatal(err)
	}
	var credentialRow models.Credential
	if err := fixture.db.Take(&credentialRow, credentialID).Error; err != nil {
		t.Fatal(err)
	}
	for name, ciphertext := range map[string]*string{
		"group": groupRow.ProxyConfig, "credential": credentialRow.ProxyConfig,
	} {
		if ciphertext == nil || strings.Contains(*ciphertext, "password") || strings.Contains(*ciphertext, "proxy.example.com") {
			t.Fatalf("%s proxy is not encrypted: %v", name, ciphertext)
		}
	}

	reset, err := fixture.service.UpdateGroupCredential(t.Context(), groupID, credentialID, CredentialUpdateRequest{
		Proxy: optionalField[outboundproxy.Config]{Set: true, Null: true},
	})
	if err != nil {
		t.Fatalf("reset credential proxy: %v", err)
	}
	if reset.Proxy.ConfiguredMode != outboundproxy.ModeInherit || reset.Proxy.EffectiveSource != outboundproxy.SourceGroup {
		t.Fatalf("reset credential proxy = %#v", reset.Proxy)
	}
	ref, ok = fixture.registry.CredentialRef(credentialID)
	if !ok || ref.EncryptedProxy != "" || ref.ProxyFingerprint != "" {
		t.Fatalf("reset credential registry proxy identity = %#v, %t", ref, ok)
	}
}

func TestGroupAndCredentialProxyRejectInvalidConfigWithoutMutation(t *testing.T) {
	fixture := newServiceFixture(t)
	groupID := createGroupWithCredentials(t, fixture, "sk-invalid-proxy-test")
	credentials, err := fixture.service.ListGroupCredentials(t.Context(), groupID, CredentialCollectionQuery{Page: 1, PageSize: 20})
	if err != nil {
		t.Fatal(err)
	}
	credentialID := credentials.Items[0].CredentialID

	if _, err := fixture.service.UpdateGroupSettings(t.Context(), groupID, GroupSettingsUpdateRequest{
		Proxy: optionalField[outboundproxy.Config]{Set: true, Value: outboundproxy.Config{Mode: outboundproxy.ModeCustom, URL: "ftp://proxy.example.com"}},
	}); err == nil {
		t.Fatal("UpdateGroupSettings accepted invalid proxy")
	}
	if _, err := fixture.service.UpdateGroupCredential(t.Context(), groupID, credentialID, CredentialUpdateRequest{
		Proxy: optionalField[outboundproxy.Config]{Set: true, Value: outboundproxy.Config{Mode: outboundproxy.ModeInherit}},
	}); err == nil {
		t.Fatal("UpdateGroupCredential accepted inherit object")
	}

	var groupRow models.Group
	if err := fixture.db.Take(&groupRow, groupID).Error; err != nil {
		t.Fatal(err)
	}
	var credentialRow models.Credential
	if err := fixture.db.Take(&credentialRow, credentialID).Error; err != nil {
		t.Fatal(err)
	}
	if groupRow.ProxyConfig != nil || credentialRow.ProxyConfig != nil {
		t.Fatalf("invalid proxy mutated rows: %v/%v", groupRow.ProxyConfig, credentialRow.ProxyConfig)
	}
}

func TestUnsupportedChannelRejectsManagedGroupAndCredentialProxy(t *testing.T) {
	fixture := newServiceFixture(t)
	name := "azure-without-managed-proxy"
	created, err := fixture.service.CreateGroup(t.Context(), GroupCreateRequest{
		Name: &name, ChannelID: channel.AzureOpenAI,
		ConnectionType: models.ConnectionTypeAPIKey,
		Params:         json.RawMessage(`{"endpoint":"https://resource.openai.azure.com"}`),
		Models:         optionalGroupModels{Set: true, Values: []GroupModel{{ID: "gpt-4o"}}},
		Credentials:    "azure-key",
	})
	if err != nil {
		t.Fatalf("CreateGroup() error = %v", err)
	}

	proxy := optionalField[outboundproxy.Config]{Set: true, Value: outboundproxy.Config{
		Mode: outboundproxy.ModeCustom,
		URL:  "http://proxy.example.com:8080",
	}}
	if _, err := fixture.service.UpdateGroupSettings(t.Context(), created.GroupID, GroupSettingsUpdateRequest{
		Proxy: proxy,
	}); !errors.Is(err, app_errors.ErrValidation) {
		t.Fatalf("UpdateGroupSettings() error = %v, want ErrValidation", err)
	}
	credentials, err := fixture.service.ListGroupCredentials(
		t.Context(), created.GroupID, CredentialCollectionQuery{Page: 1, PageSize: 20},
	)
	if err != nil || len(credentials.Items) != 1 {
		t.Fatalf("ListGroupCredentials() = %#v, %v", credentials, err)
	}
	if _, err := fixture.service.UpdateGroupCredential(
		t.Context(), created.GroupID, credentials.Items[0].CredentialID,
		CredentialUpdateRequest{Proxy: proxy},
	); !errors.Is(err, app_errors.ErrValidation) {
		t.Fatalf("UpdateGroupCredential() error = %v, want ErrValidation", err)
	}

	withProxy := GroupCreateRequest{
		Name: new(string), ChannelID: channel.AzureOpenAI,
		ConnectionType: models.ConnectionTypeAPIKey,
		Params:         json.RawMessage(`{"endpoint":"https://another.openai.azure.com"}`),
		Models:         optionalGroupModels{Set: true, Values: []GroupModel{{ID: "gpt-4o"}}},
		Credentials:    "another-azure-key",
		Proxy:          proxy,
	}
	*withProxy.Name = "azure-with-managed-proxy"
	if _, err := fixture.service.CreateGroup(t.Context(), withProxy); !errors.Is(err, app_errors.ErrValidation) {
		t.Fatalf("CreateGroup(proxy) error = %v, want ErrValidation", err)
	}
}
