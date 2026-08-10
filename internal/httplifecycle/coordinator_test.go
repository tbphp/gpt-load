package httplifecycle

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func TestCoordinatorCancelsDataPlaneRequestsAndWaitsForAllHandlers(t *testing.T) {
	coordinator := NewCoordinator()
	request := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	ctx, release, accepted := coordinator.BindData(request)
	if !accepted {
		t.Fatal("BindData() rejected an active request")
	}

	coordinator.BeginShutdown()
	if !errors.Is(context.Cause(ctx), ErrServerShutdown) {
		t.Fatalf("data request context cause = %v, want %v", context.Cause(ctx), ErrServerShutdown)
	}
	waitCtx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	if err := coordinator.Wait(waitCtx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Wait() error = %v, want deadline exceeded", err)
	}

	release()
	if err := coordinator.Wait(context.Background()); err != nil {
		t.Fatalf("Wait() after release error = %v", err)
	}
	if _, _, accepted := coordinator.BindData(request); accepted {
		t.Fatal("BindData() accepted a request after shutdown began")
	}
}

func TestCoordinatorTrackMiddlewareRejectsNewRequestsAfterShutdown(t *testing.T) {
	coordinator := NewCoordinator()
	engine := gin.New()
	engine.Use(coordinator.TrackAll())
	engine.GET("/health", func(c *gin.Context) { c.Status(204) })
	coordinator.BeginShutdown()

	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, httptest.NewRequest("GET", "/health", nil))
	if recorder.Code != 503 {
		t.Fatalf("request status = %d, want 503", recorder.Code)
	}
}

func TestCoordinatorBeginShutdownCancelsTrackedControlRequest(t *testing.T) {
	coordinator := NewCoordinator()
	engine := gin.New()
	engine.Use(coordinator.TrackAll())
	entered := make(chan struct{})
	canceled := make(chan error, 1)
	engine.GET("/api/blocking", func(c *gin.Context) {
		close(entered)
		<-c.Request.Context().Done()
		canceled <- context.Cause(c.Request.Context())
	})

	done := make(chan struct{})
	go func() {
		engine.ServeHTTP(
			httptest.NewRecorder(),
			httptest.NewRequest("GET", "/api/blocking", nil),
		)
		close(done)
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("tracked control request was not entered")
	}

	coordinator.BeginShutdown()
	select {
	case cause := <-canceled:
		if !errors.Is(cause, ErrServerShutdown) {
			t.Fatalf("control request context cause = %v, want %v", cause, ErrServerShutdown)
		}
	case <-time.After(time.Second):
		t.Fatal("tracked control request was not canceled")
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("tracked control request did not return after cancellation")
	}
}
