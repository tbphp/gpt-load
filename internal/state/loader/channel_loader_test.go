package loader_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"gpt-load/internal/channel"
	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
	"gpt-load/internal/state"
	"gpt-load/internal/state/loader"
	"gpt-load/internal/storage/models"
)

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
		Status: models.CredentialStatusActive, UpdatedAtMS: 42,
	}
	mustCreate(t, db, &credential)

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
	if len(targets) != 1 || targets[0].GroupID != group.ID || targets[0].Mode != channel.RouteNative ||
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
		{GroupID: group.ID, Data: "cipher-one", Fingerprint: "fingerprint-one", Status: models.CredentialStatusActive, UpdatedAtMS: 0},
		{GroupID: group.ID, Data: "cipher-two", Fingerprint: "fingerprint-two", Status: models.CredentialStatusDisabled, WeightManual: &weight, UpdatedAtMS: 99},
	}
	for index := range credentials {
		mustCreate(t, db, &credentials[index])
	}

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
