package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"gpt-load/internal/platform/config"
	"gpt-load/internal/platform/encryption"
	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/platform/version"
	"gpt-load/internal/state"
	"gpt-load/internal/state/loader"
	"gpt-load/internal/storage"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"
	"gorm.io/gorm"
)

type runtimeStateLoaderFunc func(context.Context) error

func (f runtimeStateLoaderFunc) Load(ctx context.Context) error {
	return f(ctx)
}

type startupBootstrapFunc func(context.Context) error

func (f startupBootstrapFunc) EnsureInitialState(ctx context.Context) error {
	return f(ctx)
}

func noopStartupBootstrap(context.Context) error {
	return nil
}

type requestLogRuntimeFake struct {
	startFunc  func() error
	stopFunc   func(context.Context) error
	emitFunc   func()
	startCalls atomic.Int32
	stopCalls  atomic.Int32
}

func newRequestLogRuntimeFake(
	startFunc func() error,
	stopFunc func(context.Context) error,
) *requestLogRuntimeFake {
	return &requestLogRuntimeFake{
		startFunc: startFunc,
		stopFunc:  stopFunc,
	}
}

func (fake *requestLogRuntimeFake) Start() error {
	fake.startCalls.Add(1)
	if fake.startFunc == nil {
		return nil
	}
	return fake.startFunc()
}

func (fake *requestLogRuntimeFake) Stop(ctx context.Context) error {
	fake.stopCalls.Add(1)
	if fake.stopFunc == nil {
		return nil
	}
	return fake.stopFunc(ctx)
}

func (fake *requestLogRuntimeFake) Emit() {
	if fake.emitFunc != nil {
		fake.emitFunc()
	}
}

type closeErrorListener struct {
	net.Listener
	closeErr error
}

func (listener *closeErrorListener) Close() error {
	return errors.Join(listener.Listener.Close(), listener.closeErr)
}

func mustNewEngine(t *testing.T) *gin.Engine {
	t.Helper()
	engine, err := NewEngine()
	if err != nil {
		t.Fatalf("NewEngine() error = %v", err)
	}
	return engine
}

type controlRuntimeFake struct {
	ready          <-chan struct{}
	started        chan struct{}
	orderViolation chan struct{}
	canceled       chan struct{}
	release        chan struct{}
	stopped        chan struct{}
	releaseOnce    sync.Once
}

func receiveTestSignal[T any](t *testing.T, signal <-chan T, name string) T {
	t.Helper()
	select {
	case value := <-signal:
		return value
	case <-time.After(time.Second):
		t.Fatalf("timed out after 1s waiting for %s", name)
		var zero T
		return zero
	}
}

func newControlRuntimeFake(ready <-chan struct{}, blockAfterCancel bool) *controlRuntimeFake {
	fake := &controlRuntimeFake{
		ready:          ready,
		started:        make(chan struct{}),
		orderViolation: make(chan struct{}, 1),
		canceled:       make(chan struct{}),
		stopped:        make(chan struct{}),
	}
	if blockAfterCancel {
		fake.release = make(chan struct{})
	}
	return fake
}

func (f *controlRuntimeFake) Run(ctx context.Context) {
	if f.ready != nil {
		select {
		case <-f.ready:
		default:
			f.orderViolation <- struct{}{}
		}
	}
	close(f.started)
	<-ctx.Done()
	close(f.canceled)
	if f.release != nil {
		<-f.release
	}
	close(f.stopped)
}

func (f *controlRuntimeFake) Release() {
	if f.release != nil {
		f.releaseOnce.Do(func() { close(f.release) })
	}
}

func TestNewEngineRecoversWithoutLoggingCredentials(t *testing.T) {
	previousMode := gin.Mode()
	gin.SetMode(gin.DebugMode)
	t.Cleanup(func() { gin.SetMode(previousMode) })

	var logs bytes.Buffer
	previousGinErrorWriter := gin.DefaultErrorWriter
	previousLogOutput := logrus.StandardLogger().Out
	gin.DefaultErrorWriter = &logs
	logrus.SetOutput(&logs)
	t.Cleanup(func() {
		gin.DefaultErrorWriter = previousGinErrorWriter
		logrus.SetOutput(previousLogOutput)
	})

	engine := mustNewEngine(t)
	engine.GET("/panic", func(*gin.Context) {
		panic("panic-secret")
	})

	request := httptest.NewRequest(http.MethodGet, "/panic?token=query-secret", nil)
	request.Header.Set("Authorization", "Bearer authorization-secret")
	request.Header.Set("X-Api-Key", "x-api-key-secret")
	request.Header.Set("Api-Key", "api-key-secret")
	request.Header.Set("X-Goog-Api-Key", "google-api-key-secret")
	recorder := httptest.NewRecorder()

	engine.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("panic status = %d, want %d", recorder.Code, http.StatusInternalServerError)
	}
	var body struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode panic response: %v; body = %q", err, recorder.Body.String())
	}
	if body.Code != app_errors.ErrInternalServer.Code || body.Message != app_errors.ErrInternalServer.Message {
		t.Fatalf("panic response = %#v", body)
	}
	if gin.Mode() != gin.ReleaseMode {
		t.Fatalf("gin mode = %q, want %q", gin.Mode(), gin.ReleaseMode)
	}

	for _, secret := range []string{
		"panic-secret",
		"query-secret",
		"authorization-secret",
		"x-api-key-secret",
		"api-key-secret",
		"google-api-key-secret",
	} {
		if strings.Contains(logs.String(), secret) {
			t.Fatalf("recovery logs contain secret %q:\n%s", secret, logs.String())
		}
	}
}

func TestNewEngineServesHealth(t *testing.T) {
	engine := mustNewEngine(t)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/health", nil)

	engine.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("GET /health status = %d, want 200", recorder.Code)
	}
	var body map[string]string
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode health response: %v", err)
	}
	if body["status"] != "ok" || body["version"] != version.Version {
		t.Fatalf("health response = %#v", body)
	}
}

func TestNewEngineDoesNotTrustForwardingHeaders(t *testing.T) {
	engine := mustNewEngine(t)
	engine.GET("/client-ip", func(c *gin.Context) {
		c.String(http.StatusOK, c.ClientIP())
	})

	request := httptest.NewRequest(http.MethodGet, "/client-ip", nil)
	request.RemoteAddr = "198.51.100.24:41000"
	request.Header.Set("X-Forwarded-For", "203.0.113.9")
	request.Header.Set("X-Real-IP", "203.0.113.10")
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)

	if recorder.Body.String() != "198.51.100.24" {
		t.Fatalf("ClientIP() = %q, want direct peer", recorder.Body.String())
	}
}

func TestAppStartMigratesDatabaseAndServesHTTP(t *testing.T) {
	db, err := storage.Open(":memory:")
	if err != nil {
		t.Fatalf("storage.Open() error = %v", err)
	}
	keyService, err := encryption.NewService("test-master-key")
	if err != nil {
		t.Fatalf("encryption.NewService() error = %v", err)
	}
	manager, runtimeState := newTestRuntimeState(db)

	application := NewApp(AppParams{
		Engine:           mustNewEngine(t),
		Config:           testConfig(t),
		Encryption:       keyService,
		DB:               db,
		StartupBootstrap: startupBootstrapFunc(noopStartupBootstrap),
		RuntimeState:     runtimeState,
		ControlRuntime:   newControlRuntimeFake(nil, false),
		RequestLogs:      newRequestLogRuntimeFake(nil, nil),
	})
	cleanupApp(t, application)
	if err := application.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	snapshot := manager.Current()
	if snapshot == nil || snapshot.Revision != 1 {
		t.Fatalf("runtime snapshot = %#v, want revision 1", snapshot)
	}
	if application.httpServer.ReadTimeout != 2*time.Second {
		t.Fatalf("ReadTimeout = %s, want 2s", application.httpServer.ReadTimeout)
	}
	if application.httpServer.ReadHeaderTimeout != 30*time.Second {
		t.Fatalf("ReadHeaderTimeout = %s, want 30s", application.httpServer.ReadHeaderTimeout)
	}
	if application.httpServer.IdleTimeout != 3*time.Second {
		t.Fatalf("IdleTimeout = %s, want 3s", application.httpServer.IdleTimeout)
	}
	if application.httpServer.WriteTimeout != 0 {
		t.Fatalf("WriteTimeout = %s, want 0 for streaming responses", application.httpServer.WriteTimeout)
	}
	for _, table := range []string{
		"groups", "upstream_keys", "access_keys", "request_logs", "usage_stats",
		"model_prices", "system_settings", "jobs", "schema_info",
	} {
		if !db.Migrator().HasTable(table) {
			t.Errorf("Start() did not migrate table %q", table)
		}
	}

	response, err := http.Get("http://" + application.Address() + "/health")
	if err != nil {
		t.Fatalf("GET running /health: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("running /health status = %d", response.StatusCode)
	}
}

func TestAppStartBootstrapsAfterMigrationBeforeRuntimeLoad(t *testing.T) {
	db, err := storage.Open(":memory:")
	if err != nil {
		t.Fatalf("storage.Open() error = %v", err)
	}
	keyService, err := encryption.NewService("test-master-key")
	if err != nil {
		t.Fatalf("encryption.NewService() error = %v", err)
	}

	var order []string
	loadErr := errors.New("stop before listen")
	application := NewApp(AppParams{
		Engine:     mustNewEngine(t),
		Config:     testConfig(t),
		Encryption: keyService,
		DB:         db,
		StartupBootstrap: startupBootstrapFunc(func(context.Context) error {
			for _, table := range []string{"groups", "access_keys", "system_settings"} {
				if !db.Migrator().HasTable(table) {
					return errors.New(table + " table does not exist")
				}
			}
			order = append(order, "bootstrap")
			return nil
		}),
		RuntimeState: runtimeStateLoaderFunc(func(context.Context) error {
			order = append(order, "load")
			return loadErr
		}),
		ControlRuntime: newControlRuntimeFake(nil, false),
		RequestLogs:    newRequestLogRuntimeFake(nil, nil),
	})
	cleanupApp(t, application)

	if err := application.Start(); !errors.Is(err, loadErr) {
		t.Fatalf("Start() error = %v, want wrapped runtime state error", err)
	}
	if want := []string{"bootstrap", "load"}; !slices.Equal(order, want) {
		t.Fatalf("startup order = %#v, want %#v", order, want)
	}
}

func TestAppStartRejectsBootstrapFailureBeforeRuntimeLoadAndListen(t *testing.T) {
	db, err := storage.Open(":memory:")
	if err != nil {
		t.Fatalf("storage.Open() error = %v", err)
	}
	keyService, err := encryption.NewService("test-master-key")
	if err != nil {
		t.Fatalf("encryption.NewService() error = %v", err)
	}

	bootstrapErr := errors.New("bootstrap failed")
	loadCalled := false
	application := NewApp(AppParams{
		Engine:     mustNewEngine(t),
		Config:     testConfig(t),
		Encryption: keyService,
		DB:         db,
		StartupBootstrap: startupBootstrapFunc(func(context.Context) error {
			return bootstrapErr
		}),
		RuntimeState: runtimeStateLoaderFunc(func(context.Context) error {
			loadCalled = true
			return errors.New("unexpected runtime load")
		}),
		ControlRuntime: newControlRuntimeFake(nil, false),
		RequestLogs:    newRequestLogRuntimeFake(nil, nil),
	})
	cleanupApp(t, application)

	err = application.Start()
	if !errors.Is(err, bootstrapErr) {
		t.Fatalf("Start() error = %v, want wrapped bootstrap error", err)
	}
	if !strings.Contains(err.Error(), "bootstrap initial state") {
		t.Fatalf("Start() error = %q, want bootstrap context", err)
	}
	if loadCalled {
		t.Fatal("runtime loader ran after bootstrap failure")
	}
	if application.Address() != "" || application.httpServer != nil ||
		application.listener != nil {
		t.Fatal("application listened after bootstrap failure")
	}
}

func TestAppStartRejectsRuntimeStateLoadFailureBeforeListen(t *testing.T) {
	db, err := storage.Open(":memory:")
	if err != nil {
		t.Fatalf("storage.Open() error = %v", err)
	}
	keyService, err := encryption.NewService("test-master-key")
	if err != nil {
		t.Fatalf("encryption.NewService() error = %v", err)
	}

	loadErr := errors.New("corrupt runtime config")
	application := NewApp(AppParams{
		Engine:           mustNewEngine(t),
		Config:           testConfig(t),
		Encryption:       keyService,
		DB:               db,
		StartupBootstrap: startupBootstrapFunc(noopStartupBootstrap),
		ControlRuntime:   newControlRuntimeFake(nil, false),
		RequestLogs:      newRequestLogRuntimeFake(nil, nil),
		RuntimeState: runtimeStateLoaderFunc(func(context.Context) error {
			return loadErr
		}),
	})
	cleanupApp(t, application)

	err = application.Start()
	if !errors.Is(err, loadErr) {
		t.Fatalf("Start() error = %v, want wrapped runtime state error", err)
	}
	if !strings.Contains(err.Error(), "load runtime state") {
		t.Fatalf("Start() error = %q, want runtime state context", err)
	}
	if got := application.Address(); got != "" {
		t.Fatalf("Address() = %q after failed load, want empty", got)
	}
	if application.httpServer != nil {
		t.Fatalf("httpServer = %#v after failed load, want nil", application.httpServer)
	}
	if application.listener != nil {
		t.Fatalf("listener = %#v after failed load, want nil", application.listener)
	}
}

func TestAppReportsUnexpectedHTTPServeFailure(t *testing.T) {
	db, err := storage.Open(":memory:")
	if err != nil {
		t.Fatalf("storage.Open() error = %v", err)
	}
	keyService, err := encryption.NewService("test-master-key")
	if err != nil {
		t.Fatalf("encryption.NewService() error = %v", err)
	}
	_, runtimeState := newTestRuntimeState(db)

	application := NewApp(AppParams{
		Engine:           mustNewEngine(t),
		Config:           testConfig(t),
		Encryption:       keyService,
		DB:               db,
		StartupBootstrap: startupBootstrapFunc(noopStartupBootstrap),
		RuntimeState:     runtimeState,
		ControlRuntime:   newControlRuntimeFake(nil, false),
		RequestLogs:      newRequestLogRuntimeFake(nil, nil),
	})
	cleanupApp(t, application)
	if err := application.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	if err := application.listener.Close(); err != nil {
		t.Fatalf("close live listener: %v", err)
	}
	select {
	case err := <-application.ServeErrors():
		if err == nil || !strings.Contains(err.Error(), "serve HTTP") {
			t.Fatalf("ServeErrors() = %v, want wrapped HTTP Serve failure", err)
		}
	case <-time.After(time.Second):
		t.Fatal("unexpected HTTP Serve failure was not propagated")
	}
}

func TestAppStartsControlRuntimeAfterInitialization(t *testing.T) {
	db, err := storage.Open(":memory:")
	if err != nil {
		t.Fatalf("storage.Open() error = %v", err)
	}
	keyService, err := encryption.NewService("test-master-key")
	if err != nil {
		t.Fatalf("encryption.NewService() error = %v", err)
	}
	loaded := make(chan struct{})
	runtime := newControlRuntimeFake(loaded, false)
	application := NewApp(AppParams{
		Engine:           mustNewEngine(t),
		Config:           testConfig(t),
		Encryption:       keyService,
		DB:               db,
		StartupBootstrap: startupBootstrapFunc(noopStartupBootstrap),
		RuntimeState: runtimeStateLoaderFunc(func(context.Context) error {
			close(loaded)
			return nil
		}),
		ControlRuntime: runtime,
		RequestLogs:    newRequestLogRuntimeFake(nil, nil),
	})
	cleanupApp(t, application)

	if err := application.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	receiveTestSignal(t, runtime.started, "control runtime start")
	select {
	case <-runtime.orderViolation:
		t.Fatal("control runtime started before runtime state loading completed")
	default:
	}
	if got := application.Address(); got == "" {
		t.Fatal("Address() is empty after Start() returned")
	}
}

func TestAppDoesNotStartControlRuntimeWhenLoadFails(t *testing.T) {
	db, err := storage.Open(":memory:")
	if err != nil {
		t.Fatalf("storage.Open() error = %v", err)
	}
	keyService, err := encryption.NewService("test-master-key")
	if err != nil {
		t.Fatalf("encryption.NewService() error = %v", err)
	}
	loadErr := errors.New("corrupt runtime config")
	runtime := newControlRuntimeFake(nil, false)
	application := NewApp(AppParams{
		Engine:           mustNewEngine(t),
		Config:           testConfig(t),
		Encryption:       keyService,
		DB:               db,
		StartupBootstrap: startupBootstrapFunc(noopStartupBootstrap),
		RuntimeState:     runtimeStateLoaderFunc(func(context.Context) error { return loadErr }),
		ControlRuntime:   runtime,
		RequestLogs:      newRequestLogRuntimeFake(nil, nil),
	})
	cleanupApp(t, application)

	if err := application.Start(); !errors.Is(err, loadErr) {
		t.Fatalf("Start() error = %v, want wrapped load error", err)
	}
	select {
	case <-runtime.started:
		t.Fatal("control runtime started after runtime state loading failed")
	default:
	}
}

func TestAppDoesNotStartControlRuntimeWhenListenFails(t *testing.T) {
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen on occupied test address: %v", err)
	}
	t.Cleanup(func() { _ = occupied.Close() })

	db, err := storage.Open(":memory:")
	if err != nil {
		t.Fatalf("storage.Open() error = %v", err)
	}
	keyService, err := encryption.NewService("test-master-key")
	if err != nil {
		t.Fatalf("encryption.NewService() error = %v", err)
	}
	cfg := testConfig(t)
	cfg.Server.Port = occupied.Addr().(*net.TCPAddr).Port
	runtime := newControlRuntimeFake(nil, false)
	application := NewApp(AppParams{
		Engine:           mustNewEngine(t),
		Config:           cfg,
		Encryption:       keyService,
		DB:               db,
		StartupBootstrap: startupBootstrapFunc(noopStartupBootstrap),
		RuntimeState:     runtimeStateLoaderFunc(func(context.Context) error { return nil }),
		ControlRuntime:   runtime,
		RequestLogs:      newRequestLogRuntimeFake(nil, nil),
	})
	cleanupApp(t, application)

	if err := application.Start(); err == nil || !strings.Contains(err.Error(), "listen on") {
		t.Fatalf("Start() error = %v, want listen failure", err)
	}
	select {
	case <-runtime.started:
		t.Fatal("control runtime started after listening failed")
	default:
	}
}

func TestAppStopCancelsAndWaitsForControlRuntime(t *testing.T) {
	db, err := storage.Open(":memory:")
	if err != nil {
		t.Fatalf("storage.Open() error = %v", err)
	}
	keyService, err := encryption.NewService("test-master-key")
	if err != nil {
		t.Fatalf("encryption.NewService() error = %v", err)
	}
	runtime := newControlRuntimeFake(nil, true)
	application := NewApp(AppParams{
		Engine:           mustNewEngine(t),
		Config:           testConfig(t),
		Encryption:       keyService,
		DB:               db,
		StartupBootstrap: startupBootstrapFunc(noopStartupBootstrap),
		RuntimeState:     runtimeStateLoaderFunc(func(context.Context) error { return nil }),
		ControlRuntime:   runtime,
		RequestLogs:      newRequestLogRuntimeFake(nil, nil),
	})
	cleanupApp(t, application)
	t.Cleanup(runtime.Release)
	if err := application.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	receiveTestSignal(t, runtime.started, "control runtime start")

	stopResult := make(chan error, 1)
	go func() { stopResult <- application.Stop(context.Background()) }()
	receiveTestSignal(t, runtime.canceled, "control runtime cancellation")
	select {
	case err := <-stopResult:
		t.Fatalf("Stop() returned before control runtime stopped: %v", err)
	default:
	}
	runtime.Release()
	receiveTestSignal(t, runtime.stopped, "control runtime stop")
	if err := receiveTestSignal(t, stopResult, "application stop result"); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
}

func TestAppStopHonorsDeadlineWhileWaitingForControlRuntime(t *testing.T) {
	db, err := storage.Open(":memory:")
	if err != nil {
		t.Fatalf("storage.Open() error = %v", err)
	}
	keyService, err := encryption.NewService("test-master-key")
	if err != nil {
		t.Fatalf("encryption.NewService() error = %v", err)
	}
	runtime := newControlRuntimeFake(nil, true)
	application := NewApp(AppParams{
		Engine:           mustNewEngine(t),
		Config:           testConfig(t),
		Encryption:       keyService,
		DB:               db,
		StartupBootstrap: startupBootstrapFunc(noopStartupBootstrap),
		RuntimeState:     runtimeStateLoaderFunc(func(context.Context) error { return nil }),
		ControlRuntime:   runtime,
		RequestLogs:      newRequestLogRuntimeFake(nil, nil),
	})
	cleanupApp(t, application)
	t.Cleanup(runtime.Release)
	if err := application.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	receiveTestSignal(t, runtime.started, "control runtime start")
	address := application.Address()
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("db.DB() error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err = application.Stop(ctx)
	if !errors.Is(err, context.Canceled) || !strings.Contains(err.Error(), "wait for control runtime") {
		t.Fatalf("Stop() error = %v, want wrapped control runtime context error", err)
	}
	receiveTestSignal(t, runtime.canceled, "control runtime cancellation")

	connection, dialErr := net.DialTimeout("tcp", address, time.Second)
	if dialErr == nil {
		_ = connection.Close()
		t.Fatalf("HTTP listener at %s still accepts connections after Stop() returned", address)
	}
	if pingErr := sqlDB.PingContext(context.Background()); pingErr == nil || pingErr.Error() != "sql: database is closed" {
		t.Fatalf("sql.DB PingContext() error = %v, want database closed", pingErr)
	}

	runtime.Release()
	receiveTestSignal(t, runtime.stopped, "control runtime stop")
}

func TestAppStartsRequestLogAfterListenBeforeHTTPServe(t *testing.T) {
	db, err := storage.Open(":memory:")
	if err != nil {
		t.Fatalf("storage.Open() error = %v", err)
	}
	keyService, err := encryption.NewService("test-master-key")
	if err != nil {
		t.Fatalf("encryption.NewService() error = %v", err)
	}

	boundListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("bind test listener: %v", err)
	}
	t.Cleanup(func() { _ = boundListener.Close() })
	boundAddress := boundListener.Addr().String()
	cfg := testConfig(t)

	requestLogsStarted := make(chan struct{})
	controlRuntime := newControlRuntimeFake(requestLogsStarted, false)
	var application *App
	requestLogs := newRequestLogRuntimeFake(func() error {
		connection, dialErr := net.DialTimeout("tcp", boundAddress, time.Second)
		if dialErr != nil {
			return fmt.Errorf("listener was not bound before request logs started: %w", dialErr)
		}
		if closeErr := connection.Close(); closeErr != nil {
			return fmt.Errorf("close startup probe: %w", closeErr)
		}
		if application.httpServer != nil || application.runtimeCancel != nil ||
			application.runtimeDone != nil {
			return errors.New("HTTP or control runtime state was exposed before request logs started")
		}
		close(requestLogsStarted)
		return nil
	}, nil)

	application = NewApp(AppParams{
		Engine:           mustNewEngine(t),
		Config:           cfg,
		Encryption:       keyService,
		DB:               db,
		StartupBootstrap: startupBootstrapFunc(noopStartupBootstrap),
		RuntimeState:     runtimeStateLoaderFunc(func(context.Context) error { return nil }),
		ControlRuntime:   controlRuntime,
		RequestLogs:      requestLogs,
	})
	application.listen = func(network, address string) (net.Listener, error) {
		if network != "tcp" || address != net.JoinHostPort(cfg.Server.Host, "0") {
			return nil, fmt.Errorf("listen arguments = %q %q", network, address)
		}
		return boundListener, nil
	}
	cleanupApp(t, application)

	if err := application.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if requestLogs.startCalls.Load() != 1 {
		t.Fatalf("RequestLogs.Start() calls = %d, want 1", requestLogs.startCalls.Load())
	}
	receiveTestSignal(t, controlRuntime.started, "control runtime start")
	select {
	case <-controlRuntime.orderViolation:
		t.Fatal("control runtime started before request logs")
	default:
	}

	response, err := http.Get("http://" + application.Address() + "/health")
	if err != nil {
		t.Fatalf("GET running /health: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("running /health status = %d", response.StatusCode)
	}
}

func TestAppRequestLogStartFailureClosesListenerWithoutServing(t *testing.T) {
	db, err := storage.Open(":memory:")
	if err != nil {
		t.Fatalf("storage.Open() error = %v", err)
	}
	keyService, err := encryption.NewService("test-master-key")
	if err != nil {
		t.Fatalf("encryption.NewService() error = %v", err)
	}

	boundListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("bind test listener: %v", err)
	}
	t.Cleanup(func() { _ = boundListener.Close() })
	boundAddress := boundListener.Addr().String()
	cfg := testConfig(t)
	startErr := errors.New("request log worker failed")
	closeErr := errors.New("listener close failed")
	requestLogs := newRequestLogRuntimeFake(func() error { return startErr }, nil)
	controlRuntime := newControlRuntimeFake(nil, false)
	application := NewApp(AppParams{
		Engine:           mustNewEngine(t),
		Config:           cfg,
		Encryption:       keyService,
		DB:               db,
		StartupBootstrap: startupBootstrapFunc(noopStartupBootstrap),
		RuntimeState:     runtimeStateLoaderFunc(func(context.Context) error { return nil }),
		ControlRuntime:   controlRuntime,
		RequestLogs:      requestLogs,
	})
	application.listen = func(network, address string) (net.Listener, error) {
		if network != "tcp" || address != net.JoinHostPort(cfg.Server.Host, "0") {
			return nil, fmt.Errorf("listen arguments = %q %q", network, address)
		}
		return &closeErrorListener{
			Listener: boundListener,
			closeErr: closeErr,
		}, nil
	}
	cleanupApp(t, application)

	err = application.Start()
	if !errors.Is(err, startErr) || !strings.Contains(err.Error(), "start request logs") {
		t.Fatalf("Start() error = %v, want wrapped request log error", err)
	}
	if !errors.Is(err, closeErr) ||
		!strings.Contains(err.Error(), "close listener after request log startup failure") {
		t.Fatalf("Start() error = %v, want joined listener close error", err)
	}
	if requestLogs.startCalls.Load() != 1 {
		t.Fatalf("RequestLogs.Start() calls = %d, want 1", requestLogs.startCalls.Load())
	}
	if application.Address() != "" || application.httpServer != nil ||
		application.listener != nil || application.runtimeCancel != nil ||
		application.runtimeDone != nil {
		t.Fatal("failed Start left application runtime state")
	}
	select {
	case <-controlRuntime.started:
		t.Fatal("control runtime started after request log startup failed")
	default:
	}

	rebound, err := net.Listen("tcp", boundAddress)
	if err != nil {
		t.Fatalf("request log startup failure leaked listener: %v", err)
	}
	if err := rebound.Close(); err != nil {
		t.Fatalf("close rebound listener: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("db.DB() error = %v", err)
	}
	if err := sqlDB.Ping(); err != nil {
		t.Fatalf("database closed during failed Start: %v", err)
	}
}

func TestAppStopDrainsRequestLogAfterLastHandlerEmitBeforeDatabaseClose(t *testing.T) {
	db, err := storage.Open(":memory:")
	if err != nil {
		t.Fatalf("storage.Open() error = %v", err)
	}
	keyService, err := encryption.NewService("test-master-key")
	if err != nil {
		t.Fatalf("encryption.NewService() error = %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("db.DB() error = %v", err)
	}

	handlerEntered := make(chan struct{})
	releaseHandler := make(chan struct{})
	handlerEmitted := make(chan struct{})
	requestLogsStopped := make(chan struct{})
	var releaseHandlerOnce sync.Once
	var stopOnce sync.Once
	var requestLogStopErr error

	requestLogs := newRequestLogRuntimeFake(nil, func(context.Context) error {
		stopOnce.Do(func() {
			select {
			case <-handlerEmitted:
			default:
				requestLogStopErr = errors.New("request logs stopped before handler emitted")
				close(requestLogsStopped)
				return
			}
			if pingErr := sqlDB.Ping(); pingErr != nil {
				requestLogStopErr = fmt.Errorf("database closed before request logs stopped: %w", pingErr)
			}
			close(requestLogsStopped)
		})
		return requestLogStopErr
	})
	requestLogs.emitFunc = func() { close(handlerEmitted) }
	engine := mustNewEngine(t)
	engine.GET("/blocking", func(c *gin.Context) {
		close(handlerEntered)
		<-releaseHandler
		requestLogs.Emit()
		c.Status(http.StatusNoContent)
	})
	controlRuntime := newControlRuntimeFake(nil, false)
	application := NewApp(AppParams{
		Engine:           engine,
		Config:           testConfig(t),
		Encryption:       keyService,
		DB:               db,
		StartupBootstrap: startupBootstrapFunc(noopStartupBootstrap),
		RuntimeState:     runtimeStateLoaderFunc(func(context.Context) error { return nil }),
		ControlRuntime:   controlRuntime,
		RequestLogs:      requestLogs,
	})
	cleanupApp(t, application)
	t.Cleanup(func() { releaseHandlerOnce.Do(func() { close(releaseHandler) }) })

	if err := application.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	receiveTestSignal(t, controlRuntime.started, "control runtime start")

	requestResult := make(chan error, 1)
	go func() {
		response, requestErr := http.Get("http://" + application.Address() + "/blocking")
		if requestErr == nil {
			requestErr = response.Body.Close()
			if response.StatusCode != http.StatusNoContent {
				requestErr = fmt.Errorf("status = %d, want %d", response.StatusCode, http.StatusNoContent)
			}
		}
		requestResult <- requestErr
	}()
	receiveTestSignal(t, handlerEntered, "blocking handler entry")

	stopResult := make(chan error, 1)
	go func() { stopResult <- application.Stop(context.Background()) }()
	receiveTestSignal(t, controlRuntime.canceled, "control runtime cancellation")
	select {
	case <-requestLogsStopped:
		t.Fatal("request logs stopped while HTTP handler was in flight")
	default:
	}
	select {
	case err := <-stopResult:
		t.Fatalf("Stop() returned while HTTP handler was in flight: %v", err)
	default:
	}

	releaseHandlerOnce.Do(func() { close(releaseHandler) })
	if err := receiveTestSignal(t, requestResult, "blocking request result"); err != nil {
		t.Fatalf("blocking request error = %v", err)
	}
	receiveTestSignal(t, handlerEmitted, "handler final emit")
	receiveTestSignal(t, requestLogsStopped, "request log stop")
	if err := receiveTestSignal(t, stopResult, "application stop result"); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	if pingErr := sqlDB.Ping(); pingErr == nil {
		t.Fatal("database remained open after Stop() returned")
	}
}

func TestAppStopDeadlineJoinsRequestLogErrorAndClosesDatabase(t *testing.T) {
	db, err := storage.Open(":memory:")
	if err != nil {
		t.Fatalf("storage.Open() error = %v", err)
	}
	keyService, err := encryption.NewService("test-master-key")
	if err != nil {
		t.Fatalf("encryption.NewService() error = %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("db.DB() error = %v", err)
	}

	requestLogErr := errors.New("request log drain failed")
	requestLogsStopped := make(chan struct{})
	var stopOnce sync.Once
	var requestLogStopErr error
	requestLogs := newRequestLogRuntimeFake(nil, func(context.Context) error {
		stopOnce.Do(func() {
			if pingErr := sqlDB.Ping(); pingErr != nil {
				requestLogStopErr = fmt.Errorf("database closed before request log drain: %w", pingErr)
			} else {
				requestLogStopErr = requestLogErr
			}
			close(requestLogsStopped)
		})
		return requestLogStopErr
	})
	controlRuntime := newControlRuntimeFake(nil, true)
	application := NewApp(AppParams{
		Engine:           mustNewEngine(t),
		Config:           testConfig(t),
		Encryption:       keyService,
		DB:               db,
		StartupBootstrap: startupBootstrapFunc(noopStartupBootstrap),
		RuntimeState:     runtimeStateLoaderFunc(func(context.Context) error { return nil }),
		ControlRuntime:   controlRuntime,
		RequestLogs:      requestLogs,
	})
	t.Cleanup(func() {
		ctx, cleanupCancel := context.WithTimeout(context.Background(), time.Second)
		defer cleanupCancel()
		if cleanupErr := application.Stop(ctx); cleanupErr != nil &&
			!errors.Is(cleanupErr, requestLogErr) {
			t.Errorf("cleanup Stop() error = %v", cleanupErr)
		}
	})
	t.Cleanup(controlRuntime.Release)

	if err := application.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	receiveTestSignal(t, controlRuntime.started, "control runtime start")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err = application.Stop(ctx)
	if !errors.Is(err, context.Canceled) ||
		!strings.Contains(err.Error(), "wait for control runtime") {
		t.Fatalf("Stop() error = %v, want wrapped control runtime deadline", err)
	}
	if !errors.Is(err, requestLogErr) || !strings.Contains(err.Error(), "stop request logs") {
		t.Fatalf("Stop() error = %v, want joined request log error", err)
	}
	receiveTestSignal(t, requestLogsStopped, "request log stop after deadline")
	if pingErr := sqlDB.Ping(); pingErr == nil {
		t.Fatal("database remained open after deadline Stop()")
	}

	controlRuntime.Release()
	receiveTestSignal(t, controlRuntime.stopped, "control runtime stop")
}

func newTestRuntimeState(db *gorm.DB) (*state.Manager, *loader.Loader) {
	manager := state.NewManager()
	registry := state.NewKeyRegistry()
	return manager, loader.New(db, manager, registry)
}

func cleanupApp(t *testing.T, application *App) {
	t.Helper()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := application.Stop(ctx); err != nil {
			t.Errorf("Stop() error = %v", err)
		}
	})
}

func testConfig(t *testing.T) *config.Config {
	t.Helper()
	return &config.Config{
		Server: config.ServerConfig{
			Host:                    "127.0.0.1",
			Port:                    0,
			GracefulShutdownTimeout: 1,
			ReadTimeout:             2,
			IdleTimeout:             3,
		},
		DataDir:       t.TempDir(),
		DatabaseDSN:   ":memory:",
		EncryptionKey: "test-master-key",
		AuthKey:       "test-auth-key",
		Log: config.LogConfig{
			Level:  "info",
			Format: "text",
		},
	}
}
