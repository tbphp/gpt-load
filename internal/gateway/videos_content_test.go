package gateway

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/gin-gonic/gin"

	"gpt-load/internal/channel"
	"gpt-load/internal/dialect"
	"gpt-load/internal/health"
	"gpt-load/internal/media"
	"gpt-load/internal/mediastore"
	"gpt-load/internal/protocol"
	"gpt-load/internal/state"
	"gpt-load/internal/testutil/encryptiontest"
	"gpt-load/internal/testutil/sqlitetest"
)

func newVideoContentFixture(t *testing.T, channelID channel.ID, upstreamURL string) (*gin.Engine, *mediastore.Store, *Handler) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	keyService := encryptiontest.Service(t, "video-content-test-master-key")
	manager := state.NewManager()
	if _, err := manager.Publish(state.CompileInput{
		ChannelRegistry: channel.NewRegistry(),
		Groups: []state.GroupConfig{{
			ConnectionType: "api_key", ID: 1, Name: "video", ChannelID: channelID,
			Params: json.RawMessage(`{"base_url":"` + upstreamURL + `/v1"}`),
			Models: []state.ModelConfig{{ID: "video-model"}}, Enabled: true,
		}},
		Credentials: []state.CredentialConfig{{
			ID: 1, GroupID: 1, Status: state.CredentialStatusActive, Version: 1, IdentityGeneration: 1, Fingerprint: "credential-1",
		}},
		AccessKeys: []state.AccessKeyConfig{{
			ID: 1, Name: "video-client", KeyHash: keyService.Hash("gl-video"), Status: state.AccessKeyStatusActive,
			Filters: state.FilterSet{Protocols: map[protocol.Protocol]struct{}{protocol.OpenAIVideos: {}}},
		}},
	}); err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	registry := state.NewCredentialRegistry()
	encrypted, err := keyService.Encrypt(`{"api_key":"key-one"}`)
	if err != nil {
		t.Fatal(err)
	}
	if err := registry.ReplaceCredentials([]state.CredentialEntry{{
		ID: 1, GroupID: 1, Version: 1, IdentityGeneration: 1, Fingerprint: "credential-1",
		Status: state.CredentialStatusActive, EncryptedValue: encrypted,
	}}); err != nil {
		t.Fatal(err)
	}
	store := mediastore.NewStore(sqlitetest.OpenMigrated(t))
	handler := NewHandler(
		manager, registry, keyService, &scriptedForwarder{}, dialect.NewSet(dialect.NewOpenAIVideos()),
		health.NewStatsStore(), health.NewMutationCoordinator(), nil, nil, nil,
	)
	handler.ConfigureMediaTasks(store)
	engine := gin.New()
	bindGatewayRoutesForTest(t, engine, handler)
	return engine, store, handler
}

func seedVideoTask(t *testing.T, store *mediastore.Store) media.Task {
	t.Helper()
	request := media.NewTaskParams{Protocol: media.ProtocolOpenAIVideos, Operation: "videos_create", Kind: media.KindVideo, ClientModel: "video-model", Mode: "text", Seconds: "5", Size: "720P", AspectRatio: "16:9"}
	task, err := store.NewTask(1, request, "")
	if err != nil {
		t.Fatal(err)
	}
	task, _, err = store.Reserve(context.Background(), task)
	if err != nil {
		t.Fatal(err)
	}
	task.GroupID, task.CredentialID = 1, 1
	task.UpstreamTaskID, task.UpstreamModel = "task_up_content", "upstream-video"
	task.Status = media.StatusInProgress
	if err := store.UpdateCreateResult(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	return task
}

func TestVideoContentProxiesBinaryRangeWithPinnedCredential(t *testing.T) {
	var calls atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		calls.Add(1)
		if request.URL.Path != "/v1/videos/task_up_content/content" {
			t.Errorf("upstream path = %q", request.URL.Path)
		}
		if request.Header.Get("Authorization") != "Bearer key-one" {
			t.Errorf("upstream authorization = %q", request.Header.Get("Authorization"))
		}
		if request.Header.Get("Range") != "bytes=0-3" {
			t.Errorf("upstream range = %q", request.Header.Get("Range"))
		}
		writer.Header().Set("Content-Type", "video/mp4")
		writer.Header().Set("Content-Range", "bytes 0-3/8")
		writer.Header().Set("Accept-Ranges", "bytes")
		writer.WriteHeader(http.StatusPartialContent)
		_, _ = io.WriteString(writer, "abcd")
	}))
	defer upstream.Close()
	engine, store, _ := newVideoContentFixture(t, channel.OpenAICompatible, upstream.URL)
	task := seedVideoTask(t, store)

	request := httptest.NewRequest(http.MethodGet, "/v1/videos/"+task.ID+"/content", nil)
	request.Header.Set("Authorization", "Bearer gl-video")
	request.Header.Set("Range", "bytes=0-3")
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusPartialContent {
		t.Fatalf("content status = %d body = %s", recorder.Code, recorder.Body.String())
	}
	if recorder.Body.String() != "abcd" || recorder.Header().Get("Content-Type") != "video/mp4" ||
		recorder.Header().Get("Content-Range") != "bytes 0-3/8" {
		t.Fatalf("content response = %q headers = %#v", recorder.Body.String(), recorder.Header())
	}
	if calls.Load() != 1 {
		t.Fatalf("upstream calls = %d", calls.Load())
	}
}

func TestVideoContentUsesResultURLWithoutCredential(t *testing.T) {
	var calls atomic.Int64
	mediaServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		calls.Add(1)
		if request.Header.Get("Authorization") != "" {
			t.Errorf("signed media URL received authorization %q", request.Header.Get("Authorization"))
		}
		writer.Header().Set("Content-Type", "video/mp4")
		_, _ = io.WriteString(writer, "signed")
	}))
	defer mediaServer.Close()
	engine, store, handler := newVideoContentFixture(t, channel.Agnes, "http://127.0.0.1:1")
	handler.allowPrivateVideoURLs = true
	task := seedVideoTask(t, store)
	task.Status, task.Progress, task.ResultURL = media.StatusCompleted, 100, mediaServer.URL
	if err := store.UpdateObservation(context.Background(), task); err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodGet, "/v1/videos/"+task.ID+"/content", nil)
	request.Header.Set("Authorization", "Bearer gl-video")
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || recorder.Body.String() != "signed" {
		t.Fatalf("content status = %d body = %q", recorder.Code, recorder.Body.String())
	}
	if calls.Load() != 1 {
		t.Fatalf("media calls = %d", calls.Load())
	}
}

func TestVideoContentBlocksPrivateResultURL(t *testing.T) {
	engine, store, _ := newVideoContentFixture(t, channel.Agnes, "http://127.0.0.1:1")
	task := seedVideoTask(t, store)
	task.Status, task.Progress = media.StatusCompleted, 100
	task.ResultURL = "http://169.254.169.254/latest/meta-data"
	if err := store.UpdateObservation(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/v1/videos/"+task.ID+"/content", nil)
	request.Header.Set("Authorization", "Bearer gl-video")
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadGateway {
		t.Fatalf("status = %d body = %s", recorder.Code, recorder.Body.String())
	}
	if strings.Contains(recorder.Body.String(), "meta-data") {
		t.Fatalf("private metadata leaked: %s", recorder.Body.String())
	}
}
