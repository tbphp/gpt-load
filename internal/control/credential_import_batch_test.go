package control

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"gpt-load/internal/channel"
	"gpt-load/internal/channel/modules"
	"gpt-load/internal/outboundproxy"
	"gpt-load/internal/platform/config"
	"gpt-load/internal/storage/models"
	subscriptionproviders "gpt-load/internal/subscription/providers"
	"gpt-load/internal/subscription/providers/claude"
	"gpt-load/internal/subscription/providers/codex"
	subscriptionruntime "gpt-load/internal/subscription/runtime"
)

// 同一账号来自不同格式时仍只暂存一次，其他条目的错误不阻止有效账号。
func TestCredentialImportBatchKeepsPartialResultsAndDeduplicatesIdentity(t *testing.T) {
	t.Parallel()
	fixture := newServiceFixture(t)
	server := NewServer(&config.Config{AuthKey: "batch-test-auth"}, fixture.service)
	engine := gin.New()
	server.RegisterRoutes(engine)
	raw := `[
		{"type":"codex","access_token":"batch-access-a","refresh_token":"batch-refresh-a","account_id":"batch-account-a","email":"one@example.com","expired":"2035-01-01T00:00:00Z"},
		{"auth_mode":"chatgpt","tokens":{"access_token":"batch-access-b","refresh_token":"batch-refresh-b","account_id":"batch-account-a"}},
		{"type":"claude","access_token":"batch-access-c","refresh_token":"batch-refresh-c","account_uuid":"batch-account-c","expired":"2035-01-01T00:00:00Z"},
		42
	]`
	response := serveCredentialImportBatch(t, engine, "/api/credential-stages/import-batch", "codex", raw, "")
	if response.Code != http.StatusOK {
		t.Fatalf("batch status = %d, want 200", response.Code)
	}
	var result struct {
		Data struct {
			Items []struct {
				Index     int                    `json:"index"`
				Format    string                 `json:"format"`
				Status    string                 `json:"status"`
				ErrorCode string                 `json:"error_code"`
				Stage     *CredentialStageResult `json:"stage"`
			} `json:"items"`
		} `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	items := result.Data.Items
	if len(items) != 4 {
		t.Fatalf("items = %d, want 4", len(items))
	}
	if items[0].Status != "ready" || items[0].Format != "cpa" || items[0].Stage == nil || items[0].Stage.StageID == "" {
		t.Fatal("CPA account was not staged with its format")
	}
	if items[1].Status != "skipped" || items[1].Format != "codex" || items[1].ErrorCode != "duplicate_account" || items[1].Stage != nil {
		t.Fatal("native copy of the same account was not deduplicated")
	}
	if items[2].Status != "skipped" || items[2].ErrorCode != "channel_mismatch" || items[2].Stage != nil {
		t.Fatal("other channel account was not explicitly skipped")
	}
	if items[3].Status != "failed" || items[3].ErrorCode == "" || items[3].Stage != nil {
		t.Fatal("invalid sibling did not retain its failure")
	}
	for index, item := range items {
		if item.Index != index+1 {
			t.Fatalf("item index = %d, want %d", item.Index, index+1)
		}
	}
	for _, secret := range []string{"batch-access-", "batch-refresh-", "batch-account-"} {
		if strings.Contains(response.Body.String(), secret) {
			t.Fatal("batch result leaked credential material")
		}
	}
	var ready int64
	if err := fixture.db.Model(&models.CredentialStage{}).Where("status = ?", models.CredentialStageReady).Count(&ready).Error; err != nil {
		t.Fatal(err)
	}
	if ready != 1 {
		t.Fatalf("ready stages = %d, want 1", ready)
	}
	if response.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("batch result is missing no-store")
	}
}

func TestCredentialImportBatchRejectsInvalidContainerBeforeStaging(t *testing.T) {
	t.Parallel()
	for _, raw := range []string{
		`{"type":"sub2api-data","version":99,"accounts":[]}`,
		`{"type":"codex","type":"claude","access_token":"secret","refresh_token":"secret"}`,
		`{"accounts":`,
	} {
		t.Run(fmt.Sprintf("bytes-%d", len(raw)), func(t *testing.T) {
			fixture := newServiceFixture(t)
			server := NewServer(&config.Config{AuthKey: "batch-test-auth"}, fixture.service)
			engine := gin.New()
			server.RegisterRoutes(engine)
			response := serveCredentialImportBatch(t, engine, "/api/credential-stages/import-batch", "codex", raw, "")
			if response.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400", response.Code)
			}
			var count int64
			if err := fixture.db.Model(&models.CredentialStage{}).Count(&count).Error; err != nil || count != 0 {
				t.Fatalf("stages/error = %d/%v", count, err)
			}
		})
	}
}

func TestOriginalCredentialImportAcceptsNativeCodexAndKeepsSingularResponse(t *testing.T) {
	t.Parallel()
	fixture := newServiceFixture(t)
	server := NewServer(&config.Config{AuthKey: "batch-test-auth"}, fixture.service)
	engine := gin.New()
	server.RegisterRoutes(engine)
	raw := `{"auth_mode":"chatgpt","tokens":{"access_token":"native-access","refresh_token":"native-refresh","account_id":"native-account"}}`
	response := serveCredentialImportBatch(t, engine, "/api/credential-stages/import", "codex", raw, "")
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.Code)
	}
	var result struct {
		Data CredentialStageResult `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil || result.Data.StageID == "" {
		t.Fatalf("singular stage response missing: %v", err)
	}
}

func TestSub2APIImportExportsCanonicalCredentialAndReimportKeepsIdentity(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name        string
		channel     channel.ID
		platform    string
		credentials string
		extra       string
	}{
		{
			name: "codex", channel: channel.Codex, platform: "openai",
			credentials: `{"access_token":"export-access","refresh_token":"export-refresh","chatgpt_account_id":"export-account","expires_at":"2035-01-01T00:00:00Z"}`,
			extra:       `{}`,
		},
		{
			name: "claude", channel: channel.Claude, platform: "anthropic",
			credentials: `{"access_token":"export-access","refresh_token":"export-refresh","expires_at":"2051222400"}`,
			extra:       `{"account_uuid":"export-account","org_uuid":"export-org","email_address":"export@example.com"}`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			fixture := newServiceFixture(t)
			raw := []byte(fmt.Sprintf(`{"exported_at":"2026-09-08T00:00:00Z","proxies":[],"accounts":[{"platform":%q,"type":"oauth","credentials":%s,"extra":%s,"concurrency":2,"priority":9}]}`, test.platform, test.credentials, test.extra))
			batch, err := fixture.service.ImportCredentialBatch(t.Context(), test.channel, raw, 0, nil)
			if err != nil || len(batch.Items) != 1 || batch.Items[0].Stage == nil {
				t.Fatalf("import result/error = %#v/%v", batch, err)
			}
			stage := batch.Items[0].Stage
			created, err := fixture.service.CreateGroup(t.Context(), GroupCreateRequest{
				Name: stringPointer("sub2api round trip"), ChannelID: test.channel,
				ConnectionType: models.ConnectionTypeSubscription,
				Models:         optionalGroupModels{Set: true}, StagedCredentialIDs: []string{stage.StageID},
			})
			if err != nil {
				t.Fatal(err)
			}
			var credential models.Credential
			if err := fixture.db.Where("group_id = ?", created.GroupID).Take(&credential).Error; err != nil {
				t.Fatal(err)
			}
			exported, err := fixture.service.DownloadGroupCredential(t.Context(), created.GroupID, credential.ID)
			if err != nil {
				t.Fatal(err)
			}
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(exported.Credential, &fields); err != nil {
				t.Fatal(err)
			}
			for _, unwanted := range []string{"platform", "credentials", "extra", "priority", "concurrency", "client_id", "format", "expires_at"} {
				if _, exists := fields[unwanted]; exists {
					t.Fatalf("source field %s leaked into export", unwanted)
				}
			}
			if fields["access_token"] == nil || fields["refresh_token"] == nil || fields["expired"] == nil {
				t.Fatal("canonical export lost credential fields")
			}
			reimported, err := fixture.service.ImportCredentialBatch(t.Context(), test.channel, exported.Credential, created.GroupID, nil)
			if err != nil || len(reimported.Items) != 1 || reimported.Items[0].Stage == nil || reimported.Items[0].Format != "cpa" {
				t.Fatalf("reimport result/error = %#v/%v", reimported, err)
			}
			connected, err := fixture.service.ConnectGroupCredentials(t.Context(), created.GroupID, []string{reimported.Items[0].Stage.StageID})
			if err != nil || connected.CredentialsAdded != 0 || connected.CredentialsDuplicated != 1 {
				t.Fatalf("reimport identity changed: result/error = %#v/%v", connected, err)
			}
		})
	}
}

func TestCredentialImportBatchUsesGroupNetworkAndRequiresManagementAuth(t *testing.T) {
	t.Parallel()
	fixture := newServiceFixture(t)
	proxy := outboundproxy.Config{Mode: outboundproxy.ModeCustom, URL: "http://batch-proxy.example:8080"}
	encoded, err := outboundproxy.Encode(proxy)
	if err != nil {
		t.Fatal(err)
	}
	encrypted, err := fixture.encryption.Encrypt(encoded)
	if err != nil {
		t.Fatal(err)
	}
	group := models.Group{
		Name: "batch network", ChannelID: string(channel.Codex), ConnectionType: models.ConnectionTypeSubscription,
		Params: models.JSON(`{}`), Models: models.JSON(`[]`), ProxyConfig: &encrypted, Enabled: true,
	}
	if err := fixture.db.Create(&group).Error; err != nil {
		t.Fatal(err)
	}
	batch, err := fixture.service.ImportCredentialBatch(t.Context(), channel.Codex,
		[]byte(`{"tokens":{"access_token":"network-access","refresh_token":"network-refresh","account_id":"network-account"}}`), group.ID, nil)
	if err != nil || len(batch.Items) != 1 || batch.Items[0].Stage == nil {
		t.Fatalf("batch result/error = %#v/%v", batch, err)
	}
	row, err := fixture.service.loadCredentialStage(t.Context(), batch.Items[0].Stage.StageID)
	if err != nil {
		t.Fatal(err)
	}
	network, err := fixture.service.credentialStageNetworkContext(t.Context(), row)
	if err != nil || network.Proxy.Config != proxy || network.Proxy.Source != outboundproxy.SourceGroup {
		t.Fatalf("stage network/error = %#v/%v", network, err)
	}
	initControlI18n(t)
	engine := gin.New()
	NewServer(&config.Config{AuthKey: "batch-test-auth"}, fixture.service).RegisterRoutes(engine)
	request := httptest.NewRequest(http.MethodPost, "/api/credential-stages/import-batch", strings.NewReader("not multipart"))
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized batch status = %d", response.Code)
	}
}

type rotatingBatchImporter struct {
	subscriptionruntime.BrowserAuthorizationDriver
	calls   int
	failure error
}

func (driver *rotatingBatchImporter) ImportCredential(context.Context, []byte) (subscriptionruntime.Credential, error) {
	driver.calls++
	if driver.failure != nil {
		return subscriptionruntime.Credential{}, driver.failure
	}
	if driver.calls > 1 {
		return subscriptionruntime.Credential{}, &codex.TokenEndpointError{StatusCode: 400, Code: "refresh_token_reused"}
	}
	return driver.Parse([]byte(`{"type":"codex","access_token":"rotated-access","refresh_token":"rotated-refresh","account_id":"rotated-account","expired":"2035-01-01T00:00:00Z"}`))
}

func TestCredentialImportBatchNeverReusesRefreshTokenAcrossFormats(t *testing.T) {
	t.Parallel()
	for _, firstFails := range []bool{false, true} {
		t.Run(fmt.Sprintf("first-fails-%t", firstFails), func(t *testing.T) {
			fixture := newServiceFixture(t)
			implementations := subscriptionproviders.Implementations()
			base := implementations[0].Drivers[0].(subscriptionruntime.BrowserAuthorizationDriver)
			importer := &rotatingBatchImporter{BrowserAuthorizationDriver: base}
			if firstFails {
				importer.failure = context.DeadlineExceeded
			}
			implementations[0].Drivers[0] = importer
			var err error
			fixture.service.subscriptions, err = subscriptionruntime.NewRuntime(fixture.service.channelRegistry, implementations...)
			if err != nil {
				t.Fatal(err)
			}
			raw := []byte(`[
				{"auth_mode":"chatgpt","tokens":{"refresh_token":"same-original-refresh"}},
				{"platform":"openai","type":"oauth","credentials":{"refresh_token":"same-original-refresh","email":"same@example.com"}}
			]`)
			batch, err := fixture.service.ImportCredentialBatch(t.Context(), channel.Codex, raw, 0, nil)
			if err != nil || len(batch.Items) != 2 {
				t.Fatalf("batch/error = %#v/%v", batch, err)
			}
			if importer.calls != 1 {
				t.Fatalf("same refresh token was submitted %d times, want 1", importer.calls)
			}
			if firstFails {
				if batch.Items[1].Status != "failed" || batch.Items[1].ErrorCode != batch.Items[0].ErrorCode {
					t.Fatal("duplicate of an uncertain refresh must retain the first failure")
				}
			} else if batch.Items[1].Status != "skipped" || batch.Items[1].ErrorCode != "duplicate_account" {
				t.Fatal("duplicate refresh token was not skipped")
			}
		})
	}
}

func TestCredentialImportBatchPreservesBootstrapFailureMeaning(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name string
		err  error
		code string
	}{
		{"token throttled", &codex.TokenEndpointError{StatusCode: 429}, "upstream_unavailable"},
		{"profile unavailable", &claude.UpstreamHTTPError{StatusCode: 503}, "upstream_unavailable"},
		{"refresh rejected", &codex.TokenEndpointError{StatusCode: 400, Code: "invalid_grant"}, "reauthorization_required"},
		{"identity changed", codex.ErrCredentialIdentityChanged, "reauthorization_required"},
		{"provider timeout while batch is active", fmt.Errorf("provider request: %w", context.DeadlineExceeded), "upstream_unavailable"},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newServiceFixture(t)
			implementations := subscriptionproviders.Implementations()
			base := implementations[0].Drivers[0].(subscriptionruntime.BrowserAuthorizationDriver)
			implementations[0].Drivers[0] = &rotatingBatchImporter{BrowserAuthorizationDriver: base, failure: test.err}
			var err error
			fixture.service.subscriptions, err = subscriptionruntime.NewRuntime(fixture.service.channelRegistry, implementations...)
			if err != nil {
				t.Fatal(err)
			}
			batch, err := fixture.service.ImportCredentialBatch(t.Context(), channel.Codex, []byte(`{"tokens":{"refresh_token":"failure-refresh"}}`), 0, nil)
			if err != nil || len(batch.Items) != 1 || batch.Items[0].ErrorCode != test.code {
				t.Fatalf("batch/error = %#v/%v, want %s", batch, err, test.code)
			}
		})
	}
}

type deadlineBatchImporter struct {
	subscriptionruntime.BrowserAuthorizationDriver
	waitDuringImport bool
	calls            int
}

func (driver *deadlineBatchImporter) ImportCredential(ctx context.Context, _ []byte) (subscriptionruntime.Credential, error) {
	driver.calls++
	if driver.calls == 1 {
		return driver.Parse([]byte(`{"type":"codex","access_token":"ready-access","refresh_token":"ready-refresh","account_id":"ready-account","expired":"2035-01-01T00:00:00Z"}`))
	}
	if driver.waitDuringImport {
		<-ctx.Done()
		return subscriptionruntime.Credential{}, fmt.Errorf("import request: %w", ctx.Err())
	}
	return driver.Parse([]byte(`{"type":"codex","access_token":"expired-access","refresh_token":"expired-refresh","account_id":"waiting-account","expired":"2000-01-01T00:00:00Z"}`))
}

func TestCredentialImportBatchDeadlinePreservesCompletedAndPendingResults(t *testing.T) {
	t.Parallel()
	for _, phase := range []string{"import", "refresh"} {
		t.Run(phase, func(t *testing.T) {
			t.Parallel()
			fixture := newServiceFixture(t)
			implementations := subscriptionproviders.Implementations()
			var importer *deadlineBatchImporter
			for _, registration := range implementations {
				for index, driver := range registration.Drivers {
					if driver.ID() == modules.CodexSubscriptionDriver {
						importer = &deadlineBatchImporter{
							BrowserAuthorizationDriver: driver.(subscriptionruntime.BrowserAuthorizationDriver),
							waitDuringImport:           phase == "import",
						}
						registration.Drivers[index] = importer
					}
				}
			}
			if importer == nil {
				t.Fatal("Codex driver not found")
			}
			var err error
			fixture.service.subscriptions, err = subscriptionruntime.NewRuntime(fixture.service.channelRegistry, implementations...)
			if err != nil {
				t.Fatal(err)
			}
			refreshCalls := 0
			fixture.service.refreshSubscriptionCredential = func(ctx context.Context, _ channel.ID, _ subscriptionruntime.Credential) (subscriptionruntime.Credential, error) {
				refreshCalls++
				<-ctx.Done()
				return subscriptionruntime.Credential{}, fmt.Errorf("refresh request: %w", ctx.Err())
			}
			raw := []byte(`[{"tokens":{"refresh_token":"first-refresh"}},{"tokens":{"refresh_token":"second-refresh"}},{"tokens":{"refresh_token":"third-refresh"}}]`)
			// 缩短父请求期限，实际等到正在处理的请求被取消，避免等待默认 30 秒。
			ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
			defer cancel()
			batch, err := fixture.service.ImportCredentialBatch(ctx, channel.Codex, raw, 0, nil)
			if err != nil || len(batch.Items) != 3 {
				t.Fatalf("batch/error = %#v/%v", batch, err)
			}
			if importer.calls != 2 || (phase == "refresh" && refreshCalls != 1) || (phase == "import" && refreshCalls != 0) {
				t.Fatalf("unexpected import/refresh calls = %d/%d", importer.calls, refreshCalls)
			}
			if batch.Items[0].Status != "ready" || batch.Items[0].Stage == nil {
				t.Fatal("deadline discarded a completed account")
			}
			for _, item := range batch.Items[1:] {
				if item.Status != "failed" || item.ErrorCode != "import_timeout" || item.Stage != nil {
					t.Errorf("item %d status/error = %s/%s, want failed/import_timeout", item.Index, item.Status, item.ErrorCode)
				}
			}
			var count int64
			if err := fixture.db.Model(&models.CredentialStage{}).Count(&count).Error; err != nil || count != 1 {
				t.Fatalf("persisted stages/error = %d/%v, want one completed stage", count, err)
			}
		})
	}
}

func TestCredentialImportFilesDeduplicateBeforeRefreshAndReusePreparedResults(t *testing.T) {
	t.Parallel()
	fixture := newServiceFixture(t)
	implementations := subscriptionproviders.Implementations()
	base := implementations[0].Drivers[0].(subscriptionruntime.BrowserAuthorizationDriver)
	importer := &rotatingBatchImporter{BrowserAuthorizationDriver: base}
	implementations[0].Drivers[0] = importer
	var err error
	fixture.service.subscriptions, err = subscriptionruntime.NewRuntime(fixture.service.channelRegistry, implementations...)
	if err != nil {
		t.Fatal(err)
	}
	initControlI18n(t)
	engine := gin.New()
	NewServer(&config.Config{AuthKey: "batch-test-auth"}, fixture.service).RegisterRoutes(engine)
	files := []string{
		`{"tokens":{"refresh_token":"original-shared-refresh"}}`,
		`{"platform":"openai","type":"oauth","credentials":{"refresh_token":"original-shared-refresh","email":"same@example.com"}}`,
	}
	requestFiles := func(prepared []string) []struct {
		FileIndex int    `json:"file_index"`
		ImportID  string `json:"import_id"`
		Status    string `json:"status"`
		ErrorCode string `json:"error_code"`
	} {
		t.Helper()
		var body bytes.Buffer
		writer := multipart.NewWriter(&body)
		if err := writer.WriteField("channel_id", "codex"); err != nil {
			t.Fatal(err)
		}
		if len(prepared) > 0 {
			raw, err := json.Marshal(prepared)
			if err != nil {
				t.Fatal(err)
			}
			if err := writer.WriteField("prepared_import_ids", string(raw)); err != nil {
				t.Fatal(err)
			}
		}
		for index, raw := range files {
			part, err := writer.CreateFormFile("file", fmt.Sprintf("file-%d.json", index))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := part.Write([]byte(raw)); err != nil {
				t.Fatal(err)
			}
		}
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
		request := httptest.NewRequest(http.MethodPost, "/api/credential-stages/import-batch", &body)
		request.Header.Set("Authorization", "Bearer batch-test-auth")
		request.Header.Set("Content-Type", writer.FormDataContentType())
		response := httptest.NewRecorder()
		engine.ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("multi-file status = %d, want 200", response.Code)
		}
		var result struct {
			Data struct {
				Items []struct {
					FileIndex int    `json:"file_index"`
					ImportID  string `json:"import_id"`
					Status    string `json:"status"`
					ErrorCode string `json:"error_code"`
				} `json:"items"`
			} `json:"data"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		return result.Data.Items
	}
	items := requestFiles(nil)
	if len(items) != 2 || items[0].FileIndex != 1 || items[1].FileIndex != 2 || len(items[0].ImportID) != 64 ||
		items[0].Status != "ready" || items[1].ErrorCode != "duplicate_account" || importer.calls != 1 {
		t.Fatalf("cross-file results/calls = %#v/%d", items, importer.calls)
	}
	items = requestFiles([]string{items[0].ImportID})
	if len(items) != 2 || items[0].ErrorCode != "already_prepared" || items[1].ErrorCode != "already_prepared" || importer.calls != 1 {
		t.Fatalf("retry results/calls = %#v/%d", items, importer.calls)
	}
}

func serveCredentialImportBatch(t *testing.T, engine *gin.Engine, path, channelID, raw, groupID string) *httptest.ResponseRecorder {
	t.Helper()
	initControlI18n(t)
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.WriteField("channel_id", channelID); err != nil {
		t.Fatal(err)
	}
	if groupID != "" {
		if err := writer.WriteField("group_id", groupID); err != nil {
			t.Fatal(err)
		}
	}
	part, err := writer.CreateFormFile("file", "accounts.json")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write([]byte(raw)); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, path, &body)
	request.Header.Set("Authorization", "Bearer batch-test-auth")
	request.Header.Set("Content-Type", writer.FormDataContentType())
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	return response
}
