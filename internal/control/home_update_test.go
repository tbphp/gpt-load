package control

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"gpt-load/internal/platform/config"
	"gpt-load/internal/releasecheck"
)

type staticReleaseUpdateReader struct {
	update *releasecheck.Update
	calls  int
}

func (reader *staticReleaseUpdateReader) Snapshot() *releasecheck.Update {
	reader.calls++
	if reader.update == nil {
		return nil
	}
	result := *reader.update
	return &result
}

func TestHomeHTTPExposesConfirmedReleaseUpdateToAdmin(t *testing.T) {
	initControlI18n(t)
	fixture := newServiceFixture(t)
	reader := &staticReleaseUpdateReader{update: &releasecheck.Update{
		Version:       "v2.0.0-beta.8",
		ReleaseURL:    "https://github.com/tbphp/gpt-load/releases/tag/v2.0.0-beta.8",
		PublishedAtMS: time.Date(2026, time.August, 19, 13, 9, 53, 0, time.UTC).UnixMilli(),
	}}
	server := NewServerWithReleaseUpdateReader(
		&config.Config{AuthKey: "test-auth-key"},
		fixture.service,
		reader,
	)
	engine := gin.New()
	server.RegisterRoutes(engine)

	recorder := performHomeRequest(engine, "/api/home", "test-auth-key")
	if recorder.Code != http.StatusOK {
		t.Fatalf("GET /api/home = %d %s", recorder.Code, recorder.Body.String())
	}
	var envelope struct {
		Data struct {
			Update *homeUpdateResponse `json:"update"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	want := homeUpdateResponse{
		Version:       reader.update.Version,
		ReleaseURL:    reader.update.ReleaseURL,
		PublishedAtMS: reader.update.PublishedAtMS,
	}
	if envelope.Data.Update == nil || *envelope.Data.Update != want || reader.calls != 1 {
		t.Fatalf("update/calls = %#v/%d, want %#v/1", envelope.Data.Update, reader.calls, want)
	}
	assertManagementWireObject(t, *envelope.Data.Update, []string{
		"version", "release_url", "published_at_ms",
	})
}

func TestHomeHTTPHidesReleaseUpdateFromAccessKey(t *testing.T) {
	initControlI18n(t)
	fixture := newServiceFixture(t)
	accessKey, err := fixture.service.CreateAccessKey(t.Context(), AccessKeyCreateRequest{Name: "read only"})
	if err != nil {
		t.Fatalf("CreateAccessKey() error = %v", err)
	}
	reader := &staticReleaseUpdateReader{update: &releasecheck.Update{
		Version:       "v2.0.1",
		ReleaseURL:    "https://github.com/tbphp/gpt-load/releases/tag/v2.0.1",
		PublishedAtMS: time.Now().UnixMilli(),
	}}
	server := NewServerWithReleaseUpdateReader(
		&config.Config{AuthKey: "test-auth-key"},
		fixture.service,
		reader,
	)
	engine := gin.New()
	server.RegisterRoutes(engine)

	recorder := performHomeRequest(engine, "/api/home", accessKey.Key)
	if recorder.Code != http.StatusOK {
		t.Fatalf("GET /api/home = %d %s", recorder.Code, recorder.Body.String())
	}
	var envelope struct {
		Data struct {
			Update *homeUpdateResponse `json:"update"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if envelope.Data.Update != nil || reader.calls != 0 {
		t.Fatalf("AccessKey update/calls = %#v/%d, want nil/0", envelope.Data.Update, reader.calls)
	}
}

func TestHomeHTTPUsesNullWhenNoReleaseUpdateIsConfirmed(t *testing.T) {
	initControlI18n(t)
	fixture := newServiceFixture(t)
	reader := &staticReleaseUpdateReader{}
	server := NewServerWithReleaseUpdateReader(
		&config.Config{AuthKey: "test-auth-key"},
		fixture.service,
		reader,
	)
	engine := gin.New()
	server.RegisterRoutes(engine)

	recorder := performHomeRequest(engine, "/api/home", "test-auth-key")
	if recorder.Code != http.StatusOK || !json.Valid(recorder.Body.Bytes()) {
		t.Fatalf("GET /api/home = %d %s", recorder.Code, recorder.Body.String())
	}
	var envelope struct {
		Data map[string]json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if raw, exists := envelope.Data["update"]; !exists || string(raw) != "null" || reader.calls != 1 {
		t.Fatalf("update/calls = %s/%d, want null/1", raw, reader.calls)
	}
}
