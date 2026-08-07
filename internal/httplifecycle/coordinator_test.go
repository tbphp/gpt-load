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

	coordinator.BeginDataShutdown()
	if !errors.Is(context.Cause(ctx), ErrDataPlaneShutdown) {
		t.Fatalf("data request context cause = %v, want %v", context.Cause(ctx), ErrDataPlaneShutdown)
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
	coordinator.BeginDataShutdown()

	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, httptest.NewRequest("GET", "/health", nil))
	if recorder.Code != 503 {
		t.Fatalf("request status = %d, want 503", recorder.Code)
	}
}
