package control

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"

	"gpt-load/internal/catalog"
	"gpt-load/internal/platform/config"
)

func TestModelPriceSyncRouteSanitizesFailure(t *testing.T) {
	// 不标记 t.Parallel()：本测试劫持了全局 logrus 输出/格式，与其他并行测试同时运行会互相覆盖断言。
	initControlI18n(t)
	fixture := newServiceFixture(t)
	const rawFailure = "secret upstream response body"
	client := catalogSyncClientFunc(func(context.Context, catalog.Metadata) (catalog.SyncResult, error) {
		return catalog.SyncResult{}, errors.New(rawFailure)
	})
	coordinator := newCatalogSyncCoordinator(fixture.service, client, "unused", catalog.Metadata{
		CheckedAtMillis: 100, SuccessfulFetchAtMillis: 90,
	}, true)
	coordinator.now = func() time.Time { return time.UnixMilli(250) }
	var serviceLogs bytes.Buffer
	previousOutput := logrus.StandardLogger().Out
	logrus.SetOutput(&serviceLogs)
	t.Cleanup(func() { logrus.SetOutput(previousOutput) })
	engine := gin.New()
	NewServer(&config.Config{AuthKey: authTestKey}, fixture.service).RegisterRoutes(engine)

	syncResponse := serveModelPriceSyncRequest(engine, authTestKey)
	if syncResponse.Code != http.StatusBadGateway ||
		strings.Contains(syncResponse.Body.String(), rawFailure) {
		t.Fatalf("sync failure = %d %s", syncResponse.Code, syncResponse.Body.String())
	}
	var envelope struct {
		Code string            `json:"code"`
		Data CatalogSyncStatus `json:"data"`
	}
	if err := json.Unmarshal(syncResponse.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Code != "BAD_GATEWAY" || envelope.Data.Trigger != CatalogSyncManual ||
		envelope.Data.CheckedAtMS != 250 || envelope.Data.SuccessfulFetchAtMS != 90 ||
		envelope.Data.ErrorCode != "catalog_sync_failed" || envelope.Data.NotModified ||
		envelope.Data.Skipped {
		t.Fatalf("sync failure envelope = %#v", envelope)
	}
	assertControlLogExcludes(t, serviceLogs.String(), rawFailure)
}

func serveModelPriceSyncRequest(engine *gin.Engine, authKey string) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/model-prices/sync", nil)
	request.Header.Set("Authorization", "Bearer "+authKey)
	request.Header.Set("Accept-Language", "en-US")
	engine.ServeHTTP(recorder, request)
	return recorder
}
