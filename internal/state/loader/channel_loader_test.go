package loader_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"gpt-load/internal/channel"
	"gpt-load/internal/execution"
	"gpt-load/internal/platform/encryption"
	"gpt-load/internal/protocol"
	"gpt-load/internal/state"
	"gpt-load/internal/state/loader"
	"gpt-load/internal/storage/models"
	subscriptionproviders "gpt-load/internal/subscription/providers"
	subscriptionruntime "gpt-load/internal/subscription/runtime"
)

func TestValidatedLoaderRejectsWrongEncryptionKeyBeforePublishing(t *testing.T) {
	t.Parallel()

	db := openMigratedDatabase(t)
	group := models.Group{
		Name: "validated-startup", ChannelID: string(channel.OpenAI), Params: models.JSON(`{}`),
		Models: models.JSON(`[]`), Overrides: models.JSON(`{}`), Enabled: true,
	}
	mustCreate(t, db, &group)
	correct, err := encryption.NewService("correct-master-key")
	if err != nil {
		t.Fatal(err)
	}
	canonical := `{"api_key":"startup-secret"}`
	ciphertext, err := correct.Encrypt(canonical)
	if err != nil {
		t.Fatal(err)
	}
	mustCreate(t, db, &models.Credential{
		GroupID: group.ID, Data: ciphertext, Fingerprint: correct.Hash(canonical),
		Status: models.CredentialStatusActive,
	})
	wrong, err := encryption.NewService("wrong-master-key")
	if err != nil {
		t.Fatal(err)
	}
	manager := state.NewManager()
	registry := state.NewCredentialRegistry()
	channels, subscriptions := testSubscriptionRuntime(t)
	err = loader.NewWithCredentialValidation(
		db, manager, registry, channels, subscriptions, wrong,
	).Load(t.Context())
	if err == nil {
		t.Fatal("Load() error = nil, want credential decryption failure")
	}
	if manager.Current() != nil || len(registry.Snapshot()) != 0 {
		t.Fatal("invalid credentials were published before startup validation")
	}
	if strings.Contains(err.Error(), "startup-secret") || strings.Contains(err.Error(), ciphertext) {
		t.Fatalf("Load() error leaked credential material: %v", err)
	}
}

func TestValidatedLoaderRejectsInvalidStoredCredentialShape(t *testing.T) {
	t.Parallel()

	db := openMigratedDatabase(t)
	group := models.Group{
		Name: "invalid-shape", ChannelID: string(channel.OpenAI), Params: models.JSON(`{}`),
		Models: models.JSON(`[]`), Overrides: models.JSON(`{}`), Enabled: true,
	}
	mustCreate(t, db, &group)
	service, err := encryption.NewService("shape-master-key")
	if err != nil {
		t.Fatal(err)
	}
	invalid := `{"token":"must-not-leak"}`
	ciphertext, err := service.Encrypt(invalid)
	if err != nil {
		t.Fatal(err)
	}
	mustCreate(t, db, &models.Credential{
		GroupID: group.ID, Data: ciphertext, Fingerprint: service.Hash(invalid),
		Status: models.CredentialStatusActive,
	})
	manager := state.NewManager()
	registry := state.NewCredentialRegistry()
	channels, subscriptions := testSubscriptionRuntime(t)
	err = loader.NewWithCredentialValidation(
		db, manager, registry, channels, subscriptions, service,
	).Load(t.Context())
	if err == nil {
		t.Fatal("Load() error = nil, want stored credential schema failure")
	}
	if manager.Current() != nil || len(registry.Snapshot()) != 0 {
		t.Fatal("invalid credential shape was published")
	}
	if strings.Contains(err.Error(), "must-not-leak") || strings.Contains(err.Error(), ciphertext) {
		t.Fatalf("Load() error leaked credential material: %v", err)
	}
}

func testSubscriptionRuntime(t *testing.T) (*channel.Registry, *subscriptionruntime.Runtime) {
	t.Helper()
	channels := channel.NewRegistry()
	runtime, err := subscriptionruntime.NewRuntime(channels, subscriptionproviders.Implementations()...)
	if err != nil {
		t.Fatal(err)
	}
	return channels, runtime
}

func TestBuildCompileInputMapsChannelAndCredentialMetadata(t *testing.T) {
	t.Parallel()

	db := openMigratedDatabase(t)
	group := models.Group{
		Name: "compatible", ChannelID: string(channel.OpenAICompatible),
		Params:    models.JSON(`{"base_url":"https://proxy.example/v1/"}`),
		Models:    models.JSON(`[{"id":"upstream-model","alias":"public-model"}]`),
		Overrides: models.JSON(`{}`), Enabled: true,
	}
	mustCreate(t, db, &group)
	credential := models.Credential{
		GroupID: group.ID, Data: "encrypted-channel-secret", Fingerprint: "fingerprint-one",
		Status: models.CredentialStatusActive,
	}
	mustCreate(t, db, &credential)
	if err := db.Table("credentials").Where("id = ?", credential.ID).
		UpdateColumn("secret_version", 42).Error; err != nil {
		t.Fatal(err)
	}
	credential.SecretVersion = 42

	input, err := loader.BuildCompileInput(t.Context(), db, channel.NewRegistry())
	if err != nil {
		t.Fatalf("BuildCompileInput() error = %v", err)
	}
	if len(input.Groups) != 1 || input.Groups[0].ChannelID != channel.OpenAICompatible ||
		string(input.Groups[0].Params) != `{"base_url":"https://proxy.example/v1/"}` {
		t.Fatalf("CompileInput.Groups = %#v", input.Groups)
	}
	if len(input.Credentials) != 1 {
		t.Fatalf("CompileInput.Credentials = %#v", input.Credentials)
	}
	metadata := input.Credentials[0]
	if metadata.ID != credential.ID || metadata.GroupID != group.ID || metadata.Version != 42 ||
		metadata.IdentityGeneration == 0 || metadata.Fingerprint != credential.Fingerprint {
		t.Fatalf("credential metadata = %#v", metadata)
	}
	if strings.Contains(fmt.Sprintf("%#v", input), credential.Data) {
		t.Fatal("CompileInput contains encrypted credential data")
	}

	snapshot, err := state.Compile(input)
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	targets := snapshot.ExecutionCandidates[protocol.OpenAIResponses][execution.OperationResponsesCreate]["public-model"]
	if len(targets) != 1 || targets[0].GroupID != group.ID || targets[0].Mode != channel.RouteConverted ||
		targets[0].UpstreamModelID != "upstream-model" {
		t.Fatalf("Responses candidates = %#v", targets)
	}
}

func TestBuildGroupCredentialEntriesUsesStableCredentialIdentity(t *testing.T) {
	t.Parallel()

	db := openMigratedDatabase(t)
	group := models.Group{
		Name: "credentials", ChannelID: string(channel.OpenAI), Params: models.JSON(`{}`),
		Models: models.JSON(`[]`), Overrides: models.JSON(`{}`), Enabled: true,
	}
	mustCreate(t, db, &group)
	weight := 9
	credentials := []models.Credential{
		{GroupID: group.ID, Data: "cipher-one", Fingerprint: "fingerprint-one", Status: models.CredentialStatusActive},
		{GroupID: group.ID, Data: "cipher-two", Fingerprint: "fingerprint-two", Status: models.CredentialStatusDisabled, WeightManual: &weight, SecretVersion: 99},
	}
	for index := range credentials {
		mustCreate(t, db, &credentials[index])
	}
	if err := db.Table("credentials").Where("id = ?", credentials[1].ID).
		UpdateColumn("secret_version", 99).Error; err != nil {
		t.Fatal(err)
	}
	credentials[1].SecretVersion = 99

	entries, err := loader.BuildGroupCredentialEntries(t.Context(), db, group.ID)
	if err != nil {
		t.Fatalf("BuildGroupCredentialEntries() error = %v", err)
	}
	if len(entries) != 2 || entries[0].ID != credentials[0].ID || entries[1].ID != credentials[1].ID {
		t.Fatalf("entries = %#v", entries)
	}
	if entries[0].Version == 0 || entries[0].IdentityGeneration == 0 ||
		entries[0].Fingerprint != credentials[0].Fingerprint || entries[0].EncryptedValue != credentials[0].Data {
		t.Fatalf("first entry identity = %#v", entries[0])
	}
	if entries[1].Version != 99 || entries[1].IdentityGeneration == entries[0].IdentityGeneration ||
		entries[1].Status != state.CredentialStatusDisabled || entries[1].WeightManual == nil || *entries[1].WeightManual != weight {
		t.Fatalf("second entry identity = %#v", entries[1])
	}
}

func TestBuildGroupCredentialEntriesChangesIdentityWhenExecutionTargetChanges(t *testing.T) {
	t.Parallel()

	db := openMigratedDatabase(t)
	group := models.Group{
		Name: "target-identity", ChannelID: string(channel.OpenAICompatible),
		Params: models.JSON(`{"base_url":"https://old.example/v1"}`),
		Models: models.JSON(`[]`), Overrides: models.JSON(`{}`), Enabled: true,
	}
	mustCreate(t, db, &group)
	credential := models.Credential{
		GroupID: group.ID, Data: "cipher", Fingerprint: "same-fingerprint",
		Status: models.CredentialStatusActive,
	}
	mustCreate(t, db, &credential)

	before, err := loader.BuildGroupCredentialEntries(t.Context(), db, group.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&models.Group{}).Where("id = ?", group.ID).
		Update("params", models.JSON(`{"base_url":"https://new.example/v1"}`)).Error; err != nil {
		t.Fatal(err)
	}
	after, err := loader.BuildGroupCredentialEntries(t.Context(), db, group.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(before) != 1 || len(after) != 1 ||
		before[0].IdentityGeneration == after[0].IdentityGeneration {
		t.Fatalf("identity generation before=%#v after=%#v", before, after)
	}

	registry := state.NewCredentialRegistry()
	before[0].Blacklisted = true
	before[0].FailureCount = 3
	if err := registry.ReplaceCredentials(before); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.ReconcileGroup(group.ID, after); err != nil {
		t.Fatal(err)
	}
	entries, err := registry.SnapshotGroupCredentialEntriesExact(group.ID, []uint{after[0].ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Blacklisted || entries[0].FailureCount != 0 {
		t.Fatalf("target change retained old health state: %#v", entries)
	}
}

func TestSubscriptionTokenRefreshKeepsIdentityGeneration(t *testing.T) {
	t.Parallel()

	db := openMigratedDatabase(t)
	group := models.Group{
		Name: "subscription-refresh", ChannelID: string(channel.Codex),
		ConnectionType: models.ConnectionTypeSubscription,
		Params:         models.JSON(`{}`), Models: models.JSON(`[]`), Overrides: models.JSON(`{}`), Enabled: true,
	}
	mustCreate(t, db, &group)
	credential := models.Credential{
		GroupID: group.ID, Data: "old-cipher", Fingerprint: "old-secret-fingerprint",
		IdentityFingerprint: "stable-account-fingerprint", SecretVersion: 7,
		Status: models.CredentialStatusActive,
	}
	mustCreate(t, db, &credential)

	before, err := loader.BuildGroupCredentialEntries(t.Context(), db, group.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&models.Credential{}).Where("id = ?", credential.ID).Updates(map[string]any{
		"data": "new-cipher", "fingerprint": "new-secret-fingerprint", "secret_version": 8,
	}).Error; err != nil {
		t.Fatal(err)
	}
	after, err := loader.BuildGroupCredentialEntries(t.Context(), db, group.ID)
	if err != nil {
		t.Fatal(err)
	}
	if before[0].IdentityGeneration != after[0].IdentityGeneration {
		t.Fatalf("identity changed across token refresh: before=%d after=%d",
			before[0].IdentityGeneration, after[0].IdentityGeneration)
	}
	if before[0].Version != 7 || after[0].Version != 8 {
		t.Fatalf("secret versions = %d -> %d, want 7 -> 8", before[0].Version, after[0].Version)
	}
}

func TestLoaderUsesCredentialsAndKeepsCiphertextOutOfSnapshot(t *testing.T) {
	t.Parallel()

	db := openMigratedDatabase(t)
	group := models.Group{
		Name: "runtime", ChannelID: string(channel.Anthropic), Params: models.JSON(`{}`),
		Models:    models.JSON(`[{"id":"claude-upstream","alias":"claude-public"}]`),
		Overrides: models.JSON(`{}`), Enabled: true,
	}
	mustCreate(t, db, &group)
	credential := models.Credential{
		GroupID: group.ID, Data: "new-credential-cipher", Fingerprint: "new-fingerprint",
		Status: models.CredentialStatusActive,
	}
	mustCreate(t, db, &credential)
	manager := state.NewManager()
	registry := state.NewCredentialRegistry()
	if err := loader.New(db, manager, registry, channel.NewRegistry()).Load(t.Context()); err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	candidates := registry.CollectCredentialCandidates([]uint{group.ID}, nil, time.Time{})
	if len(candidates) != 1 || candidates[0].ID != credential.ID || candidates[0].Version == 0 || candidates[0].IdentityGeneration == 0 {
		t.Fatalf("credential candidates = %#v", candidates)
	}
	if got, ok := registry.EncryptedCredentialData(credential.ID); !ok || got != credential.Data {
		t.Fatalf("EncryptedValue(%d) = %q, %t", credential.ID, got, ok)
	}
	snapshot := manager.Current()
	if snapshot == nil {
		t.Fatal("Current() = nil")
	}
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatalf("json.Marshal(snapshot) error = %v", err)
	}
	if strings.Contains(string(encoded), credential.Data) || strings.Contains(fmt.Sprintf("%#v", snapshot), credential.Data) {
		t.Fatal("snapshot exposes encrypted credential data")
	}
}
