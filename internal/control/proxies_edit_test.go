package control

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"gpt-load/internal/platform/config"
)

func TestProxyNamesAreOptionalAndSortByDisplayedIdentity(t *testing.T) {
	t.Parallel()
	fixture := newServiceFixture(t)
	first, err := fixture.service.SaveProxy(t.Context(), 0, ProxySaveRequest{URL: "http://b.example:8080"})
	if err != nil || first.Name != "" {
		t.Fatalf("create unnamed proxy: name=%q error=%v", first.Name, err)
	}
	second, err := fixture.service.SaveProxy(t.Context(), 0, ProxySaveRequest{Name: "   ", URL: "socks5://a.example:1080"})
	if err != nil || second.Name != "" {
		t.Fatalf("create whitespace-named proxy: name=%q error=%v", second.Name, err)
	}
	if _, err := fixture.service.SaveProxy(t.Context(), first.ID, ProxySaveRequest{Name: "named"}); err != nil {
		t.Fatal(err)
	}
	cleared, err := fixture.service.SaveProxy(t.Context(), first.ID, ProxySaveRequest{Name: ""})
	if err != nil || cleared.Name != "" || cleared.DisplayURL != first.DisplayURL {
		t.Fatal("clearing the name did not preserve the connection")
	}
	third, err := fixture.service.SaveProxy(t.Context(), 0, ProxySaveRequest{Name: "a-named", URL: "http://c.example:8080"})
	if err != nil {
		t.Fatal(err)
	}
	list, err := fixture.service.ListProxies(t.Context(), ProxyListQuery{Sort: "name", PageSize: 20})
	if err != nil || len(list.Items) != 3 {
		t.Fatalf("list proxy identities: count=%d error=%v", len(list.Items), err)
	}
	if list.Items[0].ID != third.ID || list.Items[1].ID != first.ID || list.Items[2].ID != second.ID {
		t.Fatal("name sorting did not fall back to the displayed address")
	}
}

func TestProxyEditorRevealRequiresAdminAndDoesNotExposeListSecrets(t *testing.T) {
	initControlI18n(t)
	fixture := newServiceFixture(t)
	const password = "proxy-editor-fixture-password"
	const endpoint = "socks5://fixture-user:" + password + "@proxy.example:1080"
	item, err := fixture.service.SaveProxy(t.Context(), 0, ProxySaveRequest{URL: endpoint})
	if err != nil {
		t.Fatal(err)
	}
	accessKey, err := fixture.service.CreateAccessKey(t.Context(), AccessKeyCreateRequest{Name: "proxy-readonly-fixture"})
	if err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	server := NewServer(&config.Config{AuthKey: authTestKey}, fixture.service)
	server.logger = newControlJSONLogger(&logs)
	engine := gin.New()
	server.RegisterRoutes(engine)
	request := func(method, path, token string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, path, nil)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		recorder := httptest.NewRecorder()
		engine.ServeHTTP(recorder, req)
		return recorder
	}
	path := fmt.Sprintf("/api/proxies/%d/reveal", item.ID)
	for _, denied := range []struct {
		token  string
		status int
	}{{"", http.StatusUnauthorized}, {accessKey.Key, http.StatusForbidden}} {
		response := request(http.MethodPost, path, denied.token)
		if response.Code != denied.status || strings.Contains(response.Body.String(), password) {
			t.Fatalf("unauthorized reveal: status=%d, want %d", response.Code, denied.status)
		}
	}
	response := request(http.MethodPost, path, authTestKey)
	if response.Code != http.StatusOK {
		t.Fatalf("admin reveal status=%d", response.Code)
	}
	if response.Header().Get("Cache-Control") != "no-store" || response.Header().Get("Pragma") != "no-cache" {
		t.Fatal("proxy editor secrets can be cached")
	}
	var envelope struct {
		Data ProxyRevealResult `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Data.ID != item.ID || envelope.Data.Name != "" || envelope.Data.URL != endpoint {
		t.Fatal("proxy editor did not receive the original editable connection")
	}
	list := request(http.MethodGet, "/api/proxies", authTestKey)
	if list.Code != http.StatusOK || strings.Contains(list.Body.String(), password) || strings.Contains(item.DisplayURL, password) {
		t.Fatal("proxy list or save response disclosed authentication data")
	}
	if strings.Contains(logs.String(), password) {
		t.Fatal("proxy reveal audit disclosed authentication data")
	}
	var revealed bool
	for _, event := range controlEventsNamed(decodeControlJSONLogs(t, logs.Bytes()), "mutation") {
		if event["operation"] == "proxy_reveal" && event["resource_locator"] == fmt.Sprintf("proxy:%d", item.ID) {
			revealed = true
		}
	}
	if !revealed {
		t.Fatal("proxy reveal is missing its audit identity")
	}
}
