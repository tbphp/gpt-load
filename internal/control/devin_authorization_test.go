package control

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"google.golang.org/protobuf/encoding/protowire"

	"gpt-load/internal/channel"
	"gpt-load/internal/storage/models"
)

func TestDevinAuthorizationAndImportUseEncryptedStages(t *testing.T) {
	previous := http.DefaultTransport
	defer func() { http.DefaultTransport = previous }()
	tokenCalls, statusCalls := 0, 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		var payload []byte
		switch request.Host + request.URL.Path {
		case "api.devin.ai/auth/cli/token":
			tokenCalls++
			var body map[string]string
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			if body["code"] != "authorized-code" || body["code_verifier"] == "" {
				t.Error("missing PKCE exchange")
			}
			payload = []byte(`{"token":"devin-session-token$secret"}`)
		case "server.codeium.com/exa.seat_management_pb.SeatManagementService/GetUserStatus":
			statusCalls++
			if request.Header.Get("Authorization") != "Basic devin-session-token$secret-devin-session-token$secret" {
				t.Error("missing account authentication")
			}
			user := protowire.AppendBytes(protowire.AppendTag(nil, 36, protowire.BytesType), []byte("verified-user"))
			user = protowire.AppendBytes(protowire.AppendTag(user, 7, protowire.BytesType), []byte("devin@example.test"))
			payload = protowire.AppendBytes(protowire.AppendTag(nil, 1, protowire.BytesType), user)
		default:
			t.Fatalf("unexpected endpoint: %s", request.Host+request.URL.Path)
		}
		_, _ = w.Write(payload)
	}))
	defer server.Close()
	transport := server.Client().Transport.(*http.Transport).Clone()
	transport.TLSClientConfig.ServerName = server.Certificate().DNSNames[0]
	transport.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
	}
	http.DefaultTransport = transport
	fixture := newServiceFixture(t)
	started, err := fixture.service.BeginCredentialAuthorization(t.Context(), channel.Devin)
	if err != nil {
		t.Fatal(err)
	}
	callback, err := url.Parse(started.AuthorizationURL)
	if err != nil {
		t.Fatal(err)
	}
	var row models.CredentialStage
	if err := fixture.db.Take(&row, "id = ?", started.StageID).Error; err != nil {
		t.Fatal(err)
	}
	if strings.Contains(row.EncryptedPayload, "verifier") || row.OAuthStateHash == nil {
		t.Fatal("unsafe pending authorization")
	}
	if _, err := fixture.service.CompleteCredentialAuthorization(t.Context(), "wrong-state", "authorized-code"); err == nil || tokenCalls != 0 {
		t.Fatal("state mismatch reached exchange")
	}
	ready, err := fixture.service.CompleteCredentialAuthorization(t.Context(), callback.Query().Get("state"), "authorized-code")
	if err != nil {
		t.Fatal(err)
	}
	if ready.Status != string(models.CredentialStageReady) || ready.Account.ExpiresAtMS != nil || ready.Account.EmailMask == "" {
		t.Fatal("incorrect ready-stage account")
	}
	created, err := fixture.service.CreateGroupIdempotent(t.Context(), "00000000-0000-4000-8000-000000000935", GroupCreateRequest{Name: stringPointer("Devin"), ChannelID: channel.Devin, ConnectionType: "subscription", Models: optionalGroupModels{Set: true, Values: []GroupModel{{ID: "swe-2"}}}, StagedCredentialIDs: []string{ready.StageID}})
	if err != nil {
		t.Fatal(err)
	}
	var credential models.Credential
	if err := fixture.db.Where("group_id = ?", created.GroupID).Take(&credential).Error; err != nil {
		t.Fatal(err)
	}
	if strings.Contains(credential.Data, "$secret") || credential.IdentityFingerprint == "verified-user" {
		t.Fatal("plaintext credential was persisted")
	}
	if err := fixture.db.Take(&row, "id = ?", ready.StageID).Error; err != nil {
		t.Fatal(err)
	}
	if row.Status != models.CredentialStageConsumed || row.EncryptedPayload != "" {
		t.Fatal("stage secret retained after consumption")
	}
	imported, err := fixture.service.ImportCredentialStage(t.Context(), channel.Devin, []byte(`{"type":"devin","api_key":"devin-session-token$secret","auth_kind":"oauth"}`))
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(imported)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encoded, []byte("$secret")) || imported.Status != string(models.CredentialStageReady) || tokenCalls != 1 || statusCalls != 2 {
		t.Fatal("invalid file import or duplicate authorization exchange")
	}
}
