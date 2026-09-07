package control

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"gpt-load/internal/channel"
	"gpt-load/internal/platform/config"
)

func TestDownloadAllAPIKeysReturnsOneTextFileAcrossAllStatuses(t *testing.T) {
	t.Parallel()
	initControlI18n(t)
	fixture := newServiceFixture(t)
	lines := make([]string, 121)
	for index := range lines {
		lines[index] = fmt.Sprintf("synthetic-export-key-%03d", index)
	}
	groupID := createGroupForCredentialImport(t, fixture, strings.Join(lines, "\n"))
	refs := fixture.registry.CaptureActiveCredentialRefs([]uint{groupID})
	if _, err := fixture.service.BatchGroupCredentials(t.Context(), groupID, CredentialBatchRequest{
		Action: CredentialBatchDisable, CredentialIDs: []uint{refs[0].ID},
	}); err != nil {
		t.Fatal(err)
	}
	fixture.registry.SetBlacklisted(refs[1].ID)
	if _, err := fixture.service.CreateGroup(t.Context(), GroupCreateRequest{
		Name: stringPointer("other export group"), ChannelID: channel.OpenAI,
		ConnectionType: "api_key", Params: json.RawMessage(`{}`), Models: optionalGroupModels{Set: true},
		Credentials: "synthetic-other-group-key", ConfirmSameTarget: true,
	}); err != nil {
		t.Fatal(err)
	}
	server := NewServer(&config.Config{AuthKey: "synthetic-export-auth"}, fixture.service)
	engine := gin.New()
	server.RegisterRoutes(engine)
	response := serveCredentialRequest(t, engine, http.MethodPost,
		fmt.Sprintf("/api/groups/%d/credentials/download-all", groupID), "{}", "synthetic-export-auth", "")
	if response.Code != http.StatusOK {
		t.Fatalf("download status = %d, want 200", response.Code)
	}
	if response.Header().Get("Cache-Control") != "no-store" || response.Header().Get("Pragma") != "no-cache" {
		t.Fatal("download must disable caching")
	}
	var envelope struct {
		Data struct {
			CredentialCount int `json:"credential_count"`
			Files           []struct {
				Filename   string          `json:"filename"`
				Content    string          `json:"content"`
				Credential json.RawMessage `json:"credential"`
			} `json:"files"`
		} `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Data.CredentialCount != len(lines) || len(envelope.Data.Files) != 1 {
		t.Fatalf("download count=%d files=%d", envelope.Data.CredentialCount, len(envelope.Data.Files))
	}
	file := envelope.Data.Files[0]
	if !strings.HasSuffix(file.Filename, ".txt") || len(file.Credential) != 0 {
		t.Fatal("API Key download must contain only a TXT file")
	}
	if file.Content != strings.Join(lines, "\n")+"\n" {
		t.Fatal("download must contain every group credential once in ID order")
	}
	imported, err := fixture.service.ImportGroupCredentials(t.Context(), groupID, CredentialImportRequest{Credentials: file.Content})
	if err != nil || imported.CredentialsAdded != 0 || imported.CredentialsDuplicated != len(lines) {
		t.Fatalf("reimport added=%d duplicates=%d error=%v", imported.CredentialsAdded, imported.CredentialsDuplicated, err)
	}
}

func TestDownloadAllAPIKeysPreservesStructuredCredentials(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		channel    channel.ID
		params     string
		credential string
	}{
		{"azure", channel.AzureOpenAI, `{"endpoint":"https://resource.openai.azure.com"}`, `{"tenant_id":"tenant","client_secret":"synthetic-secret","client_id":"client"}`},
		{"bedrock", channel.AWSBedrock, `{"region":"us-east-1"}`, `{"access_key":"synthetic-access","secret_key":"synthetic-secret","session_token":"synthetic-session"}`},
		{"vertex", channel.GoogleVertex, `{}`, `{"service_account_json":"{\"type\":\"service_account\",\"project_id\":\"project-one\",\"client_email\":\"svc@example.iam.gserviceaccount.com\",\"private_key\":\"synthetic-private\\nkey\"}"}`},
		{"multiline_key", channel.OpenAI, `{}`, `{"api_key":"synthetic-line-one\nline-two"}`},
		{"json_prefix_key", channel.OpenAI, `{}`, `{"api_key":"{synthetic-key}"}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newServiceFixture(t)
			created, err := fixture.service.CreateGroup(t.Context(), GroupCreateRequest{
				Name: stringPointer(test.name), ChannelID: test.channel, Params: json.RawMessage(test.params),
				ConnectionType: "api_key", Models: optionalGroupModels{Set: true}, Credentials: test.credential,
			})
			if err != nil {
				t.Fatal(err)
			}
			download, err := fixture.service.DownloadAllGroupCredentials(t.Context(), created.GroupID)
			if err != nil {
				t.Fatal(err)
			}
			encoded, err := json.Marshal(download)
			if err != nil {
				t.Fatal(err)
			}
			var data struct {
				Files []struct {
					Content string `json:"content"`
				} `json:"files"`
			}
			if err := json.Unmarshal(encoded, &data); err != nil {
				t.Fatal(err)
			}
			if len(data.Files) != 1 {
				t.Fatalf("files=%d, want 1", len(data.Files))
			}
			content := data.Files[0].Content
			if strings.Count(content, "\n") != 1 || !json.Valid([]byte(strings.TrimSpace(content))) {
				t.Fatal("structured credential must occupy one complete JSON line")
			}
			imported, err := fixture.service.ImportGroupCredentials(t.Context(), created.GroupID, CredentialImportRequest{Credentials: content})
			if err != nil || imported.CredentialsAdded != 0 || imported.CredentialsDuplicated != 1 {
				t.Fatalf("round trip added=%d duplicates=%d error=%v", imported.CredentialsAdded, imported.CredentialsDuplicated, err)
			}
		})
	}
}
