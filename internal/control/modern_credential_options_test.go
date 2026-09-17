package control

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"gpt-load/internal/platform/config"
)

func TestModernCredentialOptionsPreserveExactAccountMemberships(t *testing.T) {
	t.Parallel()
	initControlI18n(t)
	fixture := newServiceFixture(t)
	firstGroup, first := createHomeSubscriptionCredential(t, fixture, "first", "shared", "same@example.com")
	secondGroup, second := createHomeSubscriptionCredential(t, fixture, "second", "shared", "same@example.com")
	otherGroup, other := createHomeSubscriptionCredential(t, fixture, "other", "different", "same@example.com")
	apiGroup := createGroupWithCredentials(t, fixture, "sk-credential-options-secret")
	engine := gin.New()
	NewServer(&config.Config{AuthKey: authTestKey}, fixture.service).RegisterRoutes(engine)
	recorder := performGroupCollectionRequest(engine, "/api/modern/credentials/options", "Bearer "+authTestKey)
	if recorder.Code != http.StatusOK {
		t.Fatalf("options status = %d, want 200", recorder.Code)
	}
	var envelope struct {
		Data struct {
			Items []struct {
				ID          uint   `json:"id"`
				Label       string `json:"label"`
				Memberships []struct {
					GroupID      uint `json:"group_id"`
					CredentialID uint `json:"credential_id"`
				} `json:"memberships"`
			} `json:"items"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if len(envelope.Data.Items) != 3 {
		t.Fatalf("got %d options, want 3 distinct identities", len(envelope.Data.Items))
	}
	for _, option := range envelope.Data.Items {
		switch option.ID {
		case first.ID:
			if option.Label != "same@example.com" || len(option.Memberships) != 2 ||
				option.Memberships[0].GroupID != firstGroup || option.Memberships[0].CredentialID != first.ID ||
				option.Memberships[1].GroupID != secondGroup || option.Memberships[1].CredentialID != second.ID {
				t.Fatalf("shared account memberships = %#v", option)
			}
		case other.ID:
			if len(option.Memberships) != 1 || option.Memberships[0].GroupID != otherGroup {
				t.Fatal("same email incorrectly merged different identities")
			}
		default:
			if len(option.Memberships) != 1 || option.Memberships[0].GroupID != apiGroup ||
				!strings.Contains(option.Label, "*") {
				t.Fatalf("API key is not masked: %#v", option)
			}
		}
	}
	if strings.Contains(recorder.Body.String(), "sk-credential-options-secret") {
		t.Fatal("options exposed an unmasked credential")
	}
	if recorder.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("credential options must not be cached by the browser")
	}
	readOnly, err := fixture.service.CreateAccessKey(t.Context(), AccessKeyCreateRequest{Name: "options readonly"})
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		path   string
		auth   string
		status int
	}{
		{"/api/modern/credentials/options", "", http.StatusUnauthorized},
		{"/api/modern/credentials/options", "Bearer " + readOnly.Key, http.StatusForbidden},
		{"/api/modern/credentials/options?q=secret", "Bearer " + authTestKey, http.StatusBadRequest},
	} {
		response := performGroupCollectionRequest(engine, test.path, test.auth)
		if response.Code != test.status {
			t.Fatalf("%s status = %d, want %d", test.path, response.Code, test.status)
		}
	}
}
