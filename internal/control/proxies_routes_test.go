package control

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"gpt-load/internal/channel"
	"gpt-load/internal/outboundproxy"
	"gpt-load/internal/platform/config"
	"gpt-load/internal/platform/i18n"
	"gpt-load/internal/storage/models"
)

func TestProxyRouteIDUsesCanonicalSafePlatformRange(t *testing.T) {
	t.Parallel()
	valid := []string{"1"}
	if strconv.IntSize == 64 {
		valid = append(valid, "4294967296", "9007199254740991")
	}
	for _, value := range valid {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Params = gin.Params{{Key: "id", Value: value}}
		id, err := proxyID(c)
		if err != nil || strconv.FormatUint(uint64(id), 10) != value {
			t.Errorf("valid proxy ID %q: id=%d err=%v", value, id, err)
		}
	}
	for _, value := range []string{"", "0", "-1", "+1", "01", "9007199254740992", "raw-secret"} {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Params = gin.Params{{Key: "id", Value: value}}
		if _, err := proxyID(c); err == nil {
			t.Errorf("accepted invalid proxy ID %q", value)
		}
	}
}

func TestProxyUpdateAuditIdentifiesCatalogEntry(t *testing.T) {
	initControlI18n(t)
	fixture := newServiceFixture(t)
	item, err := fixture.service.SaveProxy(t.Context(), 0, ProxySaveRequest{Name: "before", URL: "http://audit-proxy.example:8080"})
	if err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	server := NewServer(&config.Config{AuthKey: authTestKey}, fixture.service)
	server.logger = newControlJSONLogger(&logs)
	engine := gin.New()
	server.RegisterRoutes(engine)
	request := httptest.NewRequest(http.MethodPut, fmt.Sprintf("/api/proxies/%d", item.ID), strings.NewReader(`{"name":"after"}`))
	request.Header.Set("Authorization", "Bearer "+authTestKey)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("update status=%d body=%s", response.Code, response.Body.String())
	}
	events := controlEventsNamed(decodeControlJSONLogs(t, logs.Bytes()), "mutation")
	if len(events) != 1 || events[0]["resource_locator"] != fmt.Sprintf("proxy:%d", item.ID) {
		t.Fatalf("proxy update audit did not identify the entry: %#v", events)
	}
}

func TestManagedProxyRoutesDeduplicateAndPublishFallback(t *testing.T) {
	if err := i18n.Init(); err != nil {
		t.Fatal(err)
	}
	fixture := newServiceFixture(t)
	groupID := createGroupWithCredentials(t, fixture, "sk-managed-proxy-fixture")
	engine := gin.New()
	NewServer(&config.Config{AuthKey: "test-auth-key"}, fixture.service).RegisterRoutes(engine)
	call := func(method, path, body string, status int) json.RawMessage {
		t.Helper()
		request := httptest.NewRequest(method, path, strings.NewReader(body))
		request.Header.Set("Authorization", "Bearer test-auth-key")
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		engine.ServeHTTP(response, request)
		if response.Code != status {
			t.Fatalf("%s %s: got %d, want %d: %s", method, path, response.Code, status, response.Body.String())
		}
		var envelope struct {
			Data json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
			t.Fatal(err)
		}
		return envelope.Data
	}
	data := call("POST", "/api/proxies", `{"name":"shared","url":"http://proxy.example:8080"}`, http.StatusOK)
	var proxy struct {
		ID uint `json:"id"`
	}
	if err := json.Unmarshal(data, &proxy); err != nil {
		t.Fatal(err)
	}
	call("POST", "/api/proxies", `{"name":"duplicate","url":"http://PROXY.EXAMPLE:8080/"}`, http.StatusConflict)
	_, err := fixture.service.UpdateGroupSettings(t.Context(), groupID, GroupSettingsUpdateRequest{Proxy: optionalField[outboundproxy.Config]{Set: true, Value: outboundproxy.Config{Mode: outboundproxy.ModeCustom, ProxyID: proxy.ID}}})
	if err != nil {
		t.Fatal(err)
	}
	if fixture.manager.Current().Groups[groupID].Proxy.Config.URL != "http://proxy.example:8080" {
		t.Fatal("reference was not published")
	}
	for _, action := range []string{"disable", "enable", "delete"} {
		call("POST", "/api/proxies/batch", fmt.Sprintf(`{"ids":[%d],"action":%q}`, proxy.ID, action), http.StatusOK)
		view, err := fixture.service.GetGroupSettings(t.Context(), groupID)
		if err != nil {
			t.Fatal(err)
		}
		if view.Proxy.ProxyID != proxy.ID {
			t.Fatal("mutation cleared proxy reference")
		}
		if action == "enable" {
			if view.Proxy.EffectiveSource != outboundproxy.SourceGroup {
				t.Fatal("enable did not restore reference")
			}
		} else if view.Proxy.EffectiveMode != outboundproxy.ModeDirect {
			t.Fatal("unavailable proxy did not inherit")
		}
	}
}

func TestProxyProbeUsesDisabledProxyAndPreservesCancellation(t *testing.T) {
	fixture := newServiceFixture(t)
	var hits atomic.Int32
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.URL.Host != "unresolvable-proxy-probe.invalid" {
			t.Error("probe did not use explicit proxy target")
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer proxy.Close()
	item, err := fixture.service.SaveProxy(t.Context(), 0, ProxySaveRequest{Name: "probe", URL: proxy.URL})
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.service.BatchProxies(t.Context(), ProxyBatchRequest{IDs: []uint{item.ID}, Action: "disable"}); err != nil {
		t.Fatal(err)
	}
	result, err := fixture.service.TestProxy(t.Context(), item.ID, "http://unresolvable-proxy-probe.invalid/test")
	if err != nil || result.LastTestStatusCode == nil || *result.LastTestStatusCode != 204 || hits.Load() != 1 || result.Enabled {
		t.Fatalf("disabled proxy test failed: result=%+v err=%v hits=%d", result, err, hits.Load())
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := fixture.service.TestProxy(ctx, item.ID, "http://unresolvable-proxy-probe.invalid/test"); err == nil {
		t.Fatal("cancelled probe succeeded")
	}
	var persisted models.Proxy
	if err := fixture.db.Take(&persisted, item.ID).Error; err != nil {
		t.Fatal(err)
	}
	if persisted.LastTestAtMS == nil || *persisted.LastTestAtMS != *result.LastTestAtMS {
		t.Fatal("cancel replaced completed test result")
	}
}

func TestSharedProxyMutationRefreshesCredentialRegistry(t *testing.T) {
	fixture := newServiceFixture(t)
	groupID := createGroupWithCredentials(t, fixture, "sk-proxy-registry-fixture")
	item, err := fixture.service.SaveProxy(t.Context(), 0, ProxySaveRequest{Name: "shared", URL: "http://registry-proxy.example:8080"})
	if err != nil {
		t.Fatal(err)
	}
	rows, err := fixture.service.ListGroupCredentials(t.Context(), groupID, CredentialCollectionQuery{Page: 1, PageSize: 20})
	if err != nil {
		t.Fatal(err)
	}
	id := rows.Items[0].CredentialID
	_, err = fixture.service.UpdateGroupCredential(t.Context(), groupID, id, CredentialUpdateRequest{Proxy: optionalField[outboundproxy.Config]{Set: true, Value: outboundproxy.Config{Mode: outboundproxy.ModeCustom, ProxyID: item.ID}}})
	if err != nil {
		t.Fatal(err)
	}
	before, ok := fixture.registry.CredentialRef(id)
	if !ok || before.EncryptedProxy == "" {
		t.Fatal("managed credential proxy not expanded")
	}
	for _, action := range []string{"disable", "enable", "delete"} {
		if err := fixture.service.BatchProxies(t.Context(), ProxyBatchRequest{IDs: []uint{item.ID}, Action: action}); err != nil {
			t.Fatal(err)
		}
		ref, ok := fixture.registry.CredentialRef(id)
		if !ok || (ref.EncryptedProxy != "") != (action == "enable") {
			t.Fatalf("%s did not refresh registry", action)
		}
		if ref.Version != before.Version {
			t.Fatal("proxy operation changed credential secret version")
		}
	}
}

func TestProxyTestURLSettingDoesNotPublishDataPlaneConfiguration(t *testing.T) {
	fixture := newServiceFixture(t)
	before := fixture.manager.Current()
	if err := fixture.service.SaveProxyTestURL(t.Context(), "https://probe.example/test"); err != nil {
		t.Fatal(err)
	}
	if fixture.manager.Current() != before {
		t.Fatal("test preference republished data plane snapshot")
	}
	list, err := fixture.service.ListProxies(t.Context(), ProxyListQuery{})
	if err != nil || list.TestURL != "https://probe.example/test" {
		t.Fatalf("saved test URL = %q, %v", list.TestURL, err)
	}
}

func TestProxyProbeCancellationStopsInFlightRequest(t *testing.T) {
	fixture := newServiceFixture(t)
	started := make(chan struct{})
	stopped := make(chan struct{})
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(started); <-r.Context().Done(); close(stopped) }))
	defer proxy.Close()
	item, err := fixture.service.SaveProxy(t.Context(), 0, ProxySaveRequest{Name: "cancel", URL: proxy.URL})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := fixture.service.TestProxy(ctx, item.ID, "http://probe.invalid/test"); done <- err }()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("probe did not start")
	}
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("cancelled test succeeded")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("probe ignored cancellation")
	}
	select {
	case <-stopped:
	case <-time.After(3 * time.Second):
		t.Fatal("upstream remained connected")
	}
}

func TestLegacyProxyWritesUseManagedReferences(t *testing.T) {
	fixture := newServiceFixture(t)
	groupID := createGroupWithCredentials(t, fixture, "managed-proxy-legacy-credential")
	_, err := fixture.service.UpdateSettings(t.Context(), SettingsUpdateRequest{Settings: map[string]json.RawMessage{
		outboundproxy.SystemSettingKey: json.RawMessage(`{"mode":"custom","url":"http://proxy.example:8080"}`),
	}})
	if err != nil {
		t.Fatal(err)
	}
	var rows []models.Proxy
	if err := fixture.db.Find(&rows).Error; err != nil || len(rows) != 1 {
		t.Fatalf("global URL did not become a managed proxy: count=%d err=%v", len(rows), err)
	}
	globalID := rows[0].ID
	group, err := fixture.service.UpdateGroupSettings(t.Context(), groupID, GroupSettingsUpdateRequest{Proxy: optionalField[outboundproxy.Config]{
		Set: true, Value: outboundproxy.Config{Mode: outboundproxy.ModeCustom, URL: "http://PROXY.EXAMPLE:08080/"},
	}})
	if err != nil || group.Proxy.ProxyID != globalID {
		t.Fatalf("group URL was not deduplicated: id=%d err=%v", group.Proxy.ProxyID, err)
	}
	credentials, err := fixture.service.ListGroupCredentials(t.Context(), groupID, CredentialCollectionQuery{Page: 1, PageSize: 20})
	if err != nil {
		t.Fatal(err)
	}
	credential, err := fixture.service.UpdateGroupCredential(t.Context(), groupID, credentials.Items[0].CredentialID, CredentialUpdateRequest{Proxy: optionalField[outboundproxy.Config]{
		Set: true, Value: outboundproxy.Config{Mode: outboundproxy.ModeCustom, URL: "http://proxy.example:8080"},
	}})
	if err != nil || credential.Proxy.ProxyID != globalID {
		t.Fatalf("credential URL was not deduplicated: id=%d err=%v", credential.Proxy.ProxyID, err)
	}
	list, err := fixture.service.ListProxies(t.Context(), ProxyListQuery{})
	if err != nil || len(list.Items) != 1 || !list.Items[0].Global || list.Items[0].GroupCount != 1 || list.Items[0].CredentialCount != 1 {
		t.Fatalf("direct reference counts: %+v err=%v", list.Items, err)
	}
}

func TestProxyImportDistinguishesAuthenticationAndNames(t *testing.T) {
	fixture := newServiceFixture(t)
	result, err := fixture.service.ImportProxies(t.Context(), "http://user:first@proxy.example:8080\nhttp://user:first@PROXY.EXAMPLE:08080/\nhttp://user:second@proxy.example:8080\nhttps://unsupported.example\n")
	if err != nil || result.Imported != 2 || result.Duplicates != 1 || len(result.InvalidLines) != 1 || result.InvalidLines[0] != 4 {
		t.Fatalf("import result: %+v, %v", result, err)
	}
	list, err := fixture.service.ListProxies(t.Context(), ProxyListQuery{})
	if err != nil || len(list.Items) != 2 {
		t.Fatalf("list count=%d err=%v", len(list.Items), err)
	}
	if list.Items[0].Name == list.Items[1].Name {
		t.Fatal("automatic names do not distinguish proxies at the same address")
	}
	for _, item := range list.Items {
		if strings.Contains(item.DisplayURL, "first") || strings.Contains(item.DisplayURL, "second") {
			t.Fatal("proxy list exposed authentication")
		}
	}
}

func TestManagedProxyGroupCreateIdempotency(t *testing.T) {
	fixture := newServiceFixture(t)
	item, err := fixture.service.SaveProxy(t.Context(), 0, ProxySaveRequest{Name: "idempotent", URL: "http://proxy.example:8080"})
	if err != nil {
		t.Fatal(err)
	}
	request := GroupCreateRequest{
		ChannelID: channel.OpenAICompatible, ConnectionType: "api_key",
		Params: json.RawMessage(`{"base_url":"https://proxy-idempotent.example"}`),
		Models: optionalGroupModels{Set: true, Values: []GroupModel{}}, Credentials: "fixture",
		Proxy: optionalField[outboundproxy.Config]{Set: true, Value: outboundproxy.Config{Mode: outboundproxy.ModeCustom, ProxyID: item.ID}},
	}
	const key = "368f47a2-9c35-4d6e-8b1a-1234567890ab"
	created, err := fixture.service.CreateGroupIdempotent(t.Context(), key, request)
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.service.BatchProxies(t.Context(), ProxyBatchRequest{IDs: []uint{item.ID}, Action: "disable"}); err != nil {
		t.Fatal(err)
	}
	replayed, err := fixture.service.CreateGroupIdempotent(t.Context(), key, request)
	if err != nil || replayed.GroupID != created.GroupID {
		t.Fatalf("proxy state changed idempotent replay: group=%d err=%v", replayed.GroupID, err)
	}
	request.ConfirmSameTarget = true
	if _, err := fixture.service.CreateGroupIdempotent(t.Context(), "468f47a2-9c35-4d6e-8b1a-1234567890ab", request); err == nil {
		t.Fatal("new group accepted a disabled selection")
	}
}
