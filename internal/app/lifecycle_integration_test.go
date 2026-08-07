package app

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"testing"
	"time"

	"gpt-load/internal/httplifecycle"
	"gpt-load/internal/storage"

	"github.com/gin-gonic/gin"
)

func TestAppStopWaitsForTrackedControlHandler(t *testing.T) {
	db, err := storage.Open(":memory:")
	if err != nil {
		t.Fatalf("storage.Open() error = %v", err)
	}
	lifecycle := httplifecycle.NewCoordinator()
	engine, err := NewEngineWithLifecycle(lifecycle)
	if err != nil {
		t.Fatalf("NewEngineWithLifecycle() error = %v", err)
	}
	entered := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	engine.GET("/blocking", func(c *gin.Context) {
		close(entered)
		<-release
		c.Status(http.StatusNoContent)
	})
	application := NewApp(AppParams{
		Engine:           engine,
		Config:           testConfig(t),
		DB:               db,
		StartupBootstrap: startupBootstrapFunc(noopStartupBootstrap),
		RuntimeState:     runtimeStateLoaderFunc(func(context.Context) error { return nil }),
		ControlRuntime:   newControlRuntimeFake(nil, false),
		RequestLogs:      newRequestLogRuntimeFake(nil, nil),
		Lifecycle:        lifecycle,
	})
	t.Cleanup(func() { releaseOnce.Do(func() { close(release) }) })

	if err := application.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	requestDone := make(chan error, 1)
	go func() {
		response, requestErr := http.Get("http://" + application.Address() + "/blocking")
		if requestErr == nil {
			defer response.Body.Close()
			if response.StatusCode != http.StatusNoContent {
				requestErr = errors.New("unexpected blocking response status")
			}
		}
		requestDone <- requestErr
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("blocking handler was not entered")
	}

	stopDone := make(chan error, 1)
	go func() { stopDone <- application.Stop(context.Background()) }()
	select {
	case err := <-stopDone:
		t.Fatalf("Stop() returned before tracked handler released: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	releaseOnce.Do(func() { close(release) })
	if err := <-requestDone; err != nil {
		t.Fatalf("blocking request error = %v", err)
	}
	if err := <-stopDone; err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
}
