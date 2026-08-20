package control

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"gpt-load/internal/platform/config"
	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/releasecheck"
)

type recordingReleaseUpdateChecker struct {
	update *releasecheck.Update
	err    error
	calls  int
	forces []bool
}

func (checker *recordingReleaseUpdateChecker) Check(_ context.Context, force bool) (*releasecheck.Update, error) {
	checker.calls++
	checker.forces = append(checker.forces, force)
	if checker.update == nil {
		return nil, checker.err
	}
	result := *checker.update
	return &result, checker.err
}

func TestSystemUpdateHTTPChecksOnDemandWithoutAffectingHome(t *testing.T) {
	initControlI18n(t)
	fixture := newServiceFixture(t)
	checker := &recordingReleaseUpdateChecker{update: &releasecheck.Update{
		Version:       "v2.0.0-beta.9",
		ReleaseURL:    "https://github.com/tbphp/gpt-load/releases/tag/v2.0.0-beta.9",
		PublishedAtMS: time.Date(2026, time.August, 20, 0, 0, 0, 0, time.UTC).UnixMilli(),
	}}
	server := NewServerWithReleaseUpdateChecker(
		&config.Config{AuthKey: "test-auth-key"},
		fixture.service,
		checker,
	)
	engine := gin.New()
	server.RegisterRoutes(engine)

	home := performHomeRequest(engine, "/api/home", "test-auth-key")
	if home.Code != http.StatusOK {
		t.Fatalf("GET /api/home = %d %s", home.Code, home.Body.String())
	}
	var homeEnvelope struct {
		Data map[string]json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(home.Body.Bytes(), &homeEnvelope); err != nil {
		t.Fatalf("decode home response: %v", err)
	}
	if _, exists := homeEnvelope.Data["update"]; exists || checker.calls != 0 {
		t.Fatalf("home update field/check calls = %v/%d, want absent/0", exists, checker.calls)
	}

	recorder := performHomeRequest(engine, "/api/system/update", "test-auth-key")
	if recorder.Code != http.StatusOK {
		t.Fatalf("GET /api/system/update = %d %s", recorder.Code, recorder.Body.String())
	}
	var envelope struct {
		Data systemUpdateResponse `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode update response: %v", err)
	}
	want := releaseUpdateResponse{
		Version:       checker.update.Version,
		ReleaseURL:    checker.update.ReleaseURL,
		PublishedAtMS: checker.update.PublishedAtMS,
	}
	if envelope.Data.Update == nil || *envelope.Data.Update != want || checker.calls != 1 ||
		len(checker.forces) != 1 || checker.forces[0] {
		t.Fatalf("update/calls = %#v/%d, want %#v/1", envelope.Data.Update, checker.calls, want)
	}
	assertManagementWireObject(t, envelope.Data, []string{"update"})
	assertManagementWireObject(t, *envelope.Data.Update, []string{
		"version", "release_url", "published_at_ms",
	})

	forced := performHomeRequest(engine, "/api/system/update?force=true", "test-auth-key")
	if forced.Code != http.StatusOK || checker.calls != 2 || len(checker.forces) != 2 || !checker.forces[1] {
		t.Fatalf("forced update = %d, calls=%d, forces=%v", forced.Code, checker.calls, checker.forces)
	}
}

func TestSystemUpdateHTTPReturnsNullForSuccessfulNoUpdate(t *testing.T) {
	initControlI18n(t)
	fixture := newServiceFixture(t)
	checker := &recordingReleaseUpdateChecker{}
	server := NewServerWithReleaseUpdateChecker(
		&config.Config{AuthKey: "test-auth-key"},
		fixture.service,
		checker,
	)
	engine := gin.New()
	server.RegisterRoutes(engine)

	recorder := performHomeRequest(engine, "/api/system/update", "test-auth-key")
	if recorder.Code != http.StatusOK || checker.calls != 1 || len(checker.forces) != 1 || checker.forces[0] {
		t.Fatalf("GET /api/system/update = %d %s, calls=%d", recorder.Code, recorder.Body.String(), checker.calls)
	}
	var envelope struct {
		Data map[string]json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if raw, exists := envelope.Data["update"]; !exists || string(raw) != "null" {
		t.Fatalf("update = %s, exists=%v, want null/true", raw, exists)
	}
}

func TestSystemUpdateHTTPHidesUpstreamFailureBehindBadGateway(t *testing.T) {
	initControlI18n(t)
	fixture := newServiceFixture(t)
	checker := &recordingReleaseUpdateChecker{err: errors.New("private upstream detail")}
	server := NewServerWithReleaseUpdateChecker(
		&config.Config{AuthKey: "test-auth-key"},
		fixture.service,
		checker,
	)
	engine := gin.New()
	server.RegisterRoutes(engine)

	recorder := performHomeRequest(engine, "/api/system/update", "test-auth-key")
	if recorder.Code != http.StatusBadGateway || checker.calls != 1 || len(checker.forces) != 1 || checker.forces[0] ||
		!strings.Contains(recorder.Body.String(), `"code":"`+app_errors.ErrBadGateway.Code+`"`) ||
		strings.Contains(recorder.Body.String(), "private upstream detail") {
		t.Fatalf("GET /api/system/update = %d %s, calls=%d", recorder.Code, recorder.Body.String(), checker.calls)
	}
}

func TestSystemUpdateHTTPRejectsAccessKeyAndInvalidQueryBeforeCheck(t *testing.T) {
	initControlI18n(t)
	fixture := newServiceFixture(t)
	accessKey, err := fixture.service.CreateAccessKey(t.Context(), AccessKeyCreateRequest{Name: "read only"})
	if err != nil {
		t.Fatalf("CreateAccessKey() error = %v", err)
	}
	checker := &recordingReleaseUpdateChecker{}
	server := NewServerWithReleaseUpdateChecker(
		&config.Config{AuthKey: "test-auth-key"},
		fixture.service,
		checker,
	)
	engine := gin.New()
	server.RegisterRoutes(engine)

	accessKeyResponse := performHomeRequest(engine, "/api/system/update", accessKey.Key)
	assertHomeHTTPError(t, accessKeyResponse, http.StatusForbidden, app_errors.ErrForbidden.Code)
	for _, path := range []string{
		"/api/system/update?refresh=1",
		"/api/system/update?force=maybe",
		"/api/system/update?force=true&force=false",
	} {
		queryResponse := performHomeRequest(engine, path, "test-auth-key")
		assertHomeHTTPError(t, queryResponse, http.StatusBadRequest, app_errors.ErrBadRequest.Code)
	}
	if checker.calls != 0 {
		t.Fatalf("rejected request check calls = %d, want 0", checker.calls)
	}
}
