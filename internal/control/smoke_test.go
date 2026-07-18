package control_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"
	"gorm.io/gorm"

	"gpt-load/internal/app"
	"gpt-load/internal/container"
	"gpt-load/internal/platform/i18n"
	"gpt-load/internal/platform/redact"
	"gpt-load/internal/state"
	"gpt-load/internal/storage"
	"gpt-load/internal/storage/models"
	"gpt-load/internal/storage/store"
	"gpt-load/internal/testutil/fakeupstream"
)

func TestM1S6NoUISmoke(t *testing.T) {
	const (
		authKey     = "s6-control-auth-distinctive-value"
		upstreamKey = "sk-s6-upstream-distinctive-credential"
		model       = "gpt-4o"
	)
	upstream := fakeupstream.New(
		fakeupstream.Step{Status: http.StatusOK, Fixture: "openai/models.json"},
		fakeupstream.Step{Status: http.StatusOK, Fixture: "openai/success.json"},
	)
	defer upstream.Close()

	t.Setenv("AUTH_KEY", authKey)
	t.Setenv("DATA_DIR", t.TempDir())
	t.Setenv("DATABASE_DSN", ":memory:")
	t.Setenv("ENCRYPTION_KEY", "s6-smoke-encryption-master-key")
	t.Setenv("REDIS_DSN", "")
	if err := i18n.Init(); err != nil {
		t.Fatalf("i18n.Init() error = %v", err)
	}

	dependencyContainer, err := container.BuildContainer()
	if err != nil {
		t.Fatalf("BuildContainer() error = %v", err)
	}

	logger := logrus.StandardLogger()
	previousOutput := logger.Out
	previousHooks := logger.ReplaceHooks(make(logrus.LevelHooks))
	var logs bytes.Buffer
	logger.SetOutput(&logs)
	t.Cleanup(func() {
		logger.SetOutput(previousOutput)
		logger.ReplaceHooks(previousHooks)
	})

	err = dependencyContainer.Invoke(func(
		engine *gin.Engine,
		db *gorm.DB,
		runtimeState app.RuntimeStateLoader,
		registry *state.KeyRegistry,
		storageStore store.Store,
		runtimeRedactor *redact.Redactor,
	) {
		logger.AddHook(redact.NewHook(runtimeRedactor))
		t.Cleanup(func() {
			_ = storageStore.Close()
			if sqlDB, dbErr := db.DB(); dbErr == nil {
				_ = sqlDB.Close()
			}
		})
		if err := storage.AutoMigrate(db); err != nil {
			t.Fatalf("AutoMigrate() error = %v", err)
		}
		if err := runtimeState.Load(context.Background()); err != nil {
			t.Fatalf("runtimeState.Load() error = %v", err)
		}

		responses := make(map[string]string)
		invalid := serveJSON(t, engine, http.MethodPost, "/api/import", authKey, map[string]any{
			"upstream_url": "not-a-url", "protocols": []string{"openai"}, "keys": upstreamKey,
		})
		responses["error"] = invalid.Body.String()
		if invalid.Code != http.StatusBadRequest {
			t.Fatalf("invalid import = %d %s, want 400", invalid.Code, invalid.Body.String())
		}

		importRecorder := serveJSON(t, engine, http.MethodPost, "/api/import", authKey, map[string]any{
			"upstream_url": upstream.URL, "protocols": []string{"openai"}, "name": "s6-smoke", "keys": upstreamKey,
		})
		responses["import"] = importRecorder.Body.String()
		if importRecorder.Code != http.StatusOK {
			t.Fatalf("import = %d %s", importRecorder.Code, importRecorder.Body.String())
		}
		var imported struct {
			Data struct {
				GroupID       uint     `json:"group_id"`
				KeysAdded     int      `json:"keys_added"`
				ModelsFetched bool     `json:"models_fetched"`
				Models        []string `json:"models"`
			} `json:"data"`
		}
		decodeSmokeResponse(t, importRecorder, &imported)
		if imported.Data.GroupID == 0 || imported.Data.KeysAdded != 1 || !imported.Data.ModelsFetched ||
			len(imported.Data.Models) != 2 || imported.Data.Models[0] != model {
			t.Fatalf("import data = %#v", imported.Data)
		}

		groupsRecorder := serveJSON(t, engine, http.MethodGet, "/api/groups", authKey, nil)
		responses["groups"] = groupsRecorder.Body.String()
		if groupsRecorder.Code != http.StatusOK {
			t.Fatalf("groups = %d %s", groupsRecorder.Code, groupsRecorder.Body.String())
		}
		var groups struct {
			Data []struct {
				ID       uint `json:"id"`
				KeyCount int  `json:"key_count"`
			} `json:"data"`
		}
		decodeSmokeResponse(t, groupsRecorder, &groups)
		if len(groups.Data) != 1 || groups.Data[0].ID != imported.Data.GroupID || groups.Data[0].KeyCount != 1 {
			t.Fatalf("groups data = %#v", groups.Data)
		}

		createRecorder := serveJSON(t, engine, http.MethodPost, "/api/access-keys", authKey, map[string]any{
			"name": "s6-client",
			"filters": map[string]any{
				"groups": []uint{imported.Data.GroupID}, "protocols": []string{"openai"}, "models": []string{model},
			},
		})
		responses["create_access_key"] = createRecorder.Body.String()
		if createRecorder.Code != http.StatusOK {
			t.Fatalf("create AccessKey = %d %s", createRecorder.Code, createRecorder.Body.String())
		}
		var created struct {
			Data struct {
				Key string `json:"key"`
			} `json:"data"`
		}
		decodeSmokeResponse(t, createRecorder, &created)
		if !regexp.MustCompile(`^gl-[0-9a-f]{32}$`).MatchString(created.Data.Key) {
			t.Fatalf("generated AccessKey has invalid format")
		}

		listRecorder := serveJSON(t, engine, http.MethodGet, "/api/access-keys", authKey, nil)
		responses["list_access_keys"] = listRecorder.Body.String()
		if listRecorder.Code != http.StatusOK || !strings.Contains(listRecorder.Body.String(), created.Data.Key) {
			t.Fatalf("list AccessKeys = %d %s", listRecorder.Code, listRecorder.Body.String())
		}

		chatRecorder := serveJSON(t, engine, http.MethodPost, "/v1/chat/completions", created.Data.Key, map[string]any{
			"model": model, "messages": []map[string]string{{"role": "user", "content": "ping"}},
		})
		responses["gateway"] = chatRecorder.Body.String()
		if chatRecorder.Code != http.StatusOK || !strings.Contains(chatRecorder.Body.String(), `"content":"pong"`) {
			t.Fatalf("gateway = %d %s", chatRecorder.Code, chatRecorder.Body.String())
		}
		if chatRecorder.Header().Get("X-GPTLoad-Attempts") != "1" {
			t.Fatalf("X-GPTLoad-Attempts = %q, want 1", chatRecorder.Header().Get("X-GPTLoad-Attempts"))
		}

		requests := upstream.Requests()
		if len(requests) != 2 || requests[0].Method != http.MethodGet || requests[0].Path != "/v1/models" ||
			requests[1].Method != http.MethodPost || requests[1].Path != "/v1/chat/completions" {
			t.Fatalf("upstream requests = %#v", requests)
		}
		for index, request := range requests {
			if request.Headers.Get("Authorization") != "Bearer "+upstreamKey {
				t.Fatalf("upstream request %d credential was not injected", index)
			}
		}
		if strings.Contains(string(requests[1].Body), created.Data.Key) {
			t.Fatal("gateway forwarded downstream AccessKey in request body")
		}

		var upstreamRow models.UpstreamKey
		if err := db.First(&upstreamRow).Error; err != nil {
			t.Fatalf("query UpstreamKey: %v", err)
		}
		var accessRow models.AccessKey
		if err := db.First(&accessRow).Error; err != nil {
			t.Fatalf("query AccessKey: %v", err)
		}
		if upstreamRow.KeyValue == upstreamKey || accessRow.KeyValue == created.Data.Key {
			t.Fatal("database stored a plaintext credential")
		}
		if _, ok := registry.EncryptedValue(upstreamRow.ID); !ok {
			t.Fatal("imported upstream key is absent from KeyRegistry")
		}

		secrets := []string{authKey, upstreamKey, created.Data.Key, upstreamRow.KeyValue, upstreamRow.KeyHash, accessRow.KeyValue, accessRow.KeyHash}
		for name, body := range responses {
			allowedAccessKey := name == "create_access_key" || name == "list_access_keys"
			for secretIndex, secret := range secrets {
				if secret == created.Data.Key && allowedAccessKey {
					continue
				}
				if secret != "" && strings.Contains(body, secret) {
					t.Fatalf("%s response exposes secret index %d", name, secretIndex)
				}
			}
		}
		for secretIndex, secret := range secrets {
			if secret != "" && strings.Contains(logs.String(), secret) {
				t.Fatalf("logs expose secret index %d", secretIndex)
			}
		}
		databaseText := fmt.Sprintf("%#v %#v", upstreamRow, accessRow)
		for _, plaintext := range []string{authKey, upstreamKey, created.Data.Key} {
			if strings.Contains(databaseText, plaintext) {
				t.Fatalf("database contains plaintext credential")
			}
		}
	})
	if err != nil {
		t.Fatalf("resolve S6 smoke dependencies: %v", err)
	}
}

func serveJSON(
	t *testing.T,
	engine *gin.Engine,
	method, path, bearer string,
	payload any,
) *httptest.ResponseRecorder {
	t.Helper()
	var body *bytes.Reader
	if payload == nil {
		body = bytes.NewReader(nil)
	} else {
		encoded, err := json.Marshal(payload)
		if err != nil {
			t.Fatalf("marshal request payload: %v", err)
		}
		body = bytes.NewReader(encoded)
	}
	request := httptest.NewRequest(method, path, body)
	request.Header.Set("Authorization", "Bearer "+bearer)
	if payload != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)
	return recorder
}

func decodeSmokeResponse(t *testing.T, recorder *httptest.ResponseRecorder, target any) {
	t.Helper()
	if err := json.Unmarshal(recorder.Body.Bytes(), target); err != nil {
		t.Fatalf("decode response %d %s: %v", recorder.Code, recorder.Body.String(), err)
	}
}
