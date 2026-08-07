// Package httplifecycle coordinates HTTP handler draining with data-plane
// cancellation during process shutdown.
package httplifecycle

import (
	"context"
	"errors"
	"net/http"
	"sync"

	"github.com/gin-gonic/gin"
)

var ErrDataPlaneShutdown = errors.New("data plane request canceled for shutdown")

// Coordinator tracks all HTTP handlers and keeps cancellation handles for
// data-plane handlers. It is intentionally process-local and single-instance.
type Coordinator struct {
	mu       sync.Mutex
	closing  bool
	active   int
	zero     chan struct{}
	dataStop map[*http.Request]context.CancelCauseFunc
}

func NewCoordinator() *Coordinator {
	zero := make(chan struct{})
	close(zero)
	return &Coordinator{
		zero:     zero,
		dataStop: make(map[*http.Request]context.CancelCauseFunc),
	}
}

// TrackAll returns global Gin middleware that counts every in-flight handler,
// including system, control, data, and embedded web UI requests.
func (coordinator *Coordinator) TrackAll() gin.HandlerFunc {
	return func(c *gin.Context) {
		if coordinator == nil || !coordinator.enter() {
			c.AbortWithStatus(http.StatusServiceUnavailable)
			return
		}
		defer coordinator.leave()
		c.Next()
	}
}

// BindData derives a data-plane request context and registers a cancellation
// handle. The caller must invoke the returned release function exactly once.
func (coordinator *Coordinator) BindData(request *http.Request) (
	context.Context,
	func(),
	bool,
) {
	if request == nil {
		return context.Background(), func() {}, false
	}
	parent := request.Context()
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithCancelCause(parent)
	if coordinator == nil {
		cancel(nil)
		return ctx, func() {}, false
	}

	coordinator.mu.Lock()
	if coordinator.closing {
		coordinator.mu.Unlock()
		cancel(ErrDataPlaneShutdown)
		return ctx, func() {}, false
	}
	coordinator.addActiveLocked()
	coordinator.dataStop[request] = cancel
	coordinator.mu.Unlock()
	stopBodyClose := context.AfterFunc(ctx, func() {
		if request.Body != nil {
			_ = request.Body.Close()
		}
	})

	var once sync.Once
	release := func() {
		once.Do(func() {
			stopBodyClose()
			cancel(nil)
			coordinator.mu.Lock()
			delete(coordinator.dataStop, request)
			coordinator.leaveLocked()
			coordinator.mu.Unlock()
		})
	}
	return ctx, release, true
}

// BeginDataShutdown rejects new data-plane requests and cancels every data
// request already registered with the coordinator.
func (coordinator *Coordinator) BeginDataShutdown() {
	if coordinator == nil {
		return
	}
	coordinator.mu.Lock()
	if coordinator.closing {
		coordinator.mu.Unlock()
		return
	}
	coordinator.closing = true
	stops := make([]context.CancelCauseFunc, 0, len(coordinator.dataStop))
	for _, cancel := range coordinator.dataStop {
		stops = append(stops, cancel)
	}
	coordinator.mu.Unlock()
	for _, cancel := range stops {
		cancel(ErrDataPlaneShutdown)
	}
}

// Wait waits until all tracked handlers and data-plane registrations have
// released, or until the supplied context ends.
func (coordinator *Coordinator) Wait(ctx context.Context) error {
	if coordinator == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	coordinator.mu.Lock()
	zero := coordinator.zero
	coordinator.mu.Unlock()
	select {
	case <-zero:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (coordinator *Coordinator) enter() bool {
	coordinator.mu.Lock()
	defer coordinator.mu.Unlock()
	if coordinator.closing {
		return false
	}
	coordinator.addActiveLocked()
	return true
}

func (coordinator *Coordinator) leave() {
	coordinator.mu.Lock()
	coordinator.leaveLocked()
	coordinator.mu.Unlock()
}

func (coordinator *Coordinator) addActiveLocked() {
	if coordinator.active == 0 {
		coordinator.zero = make(chan struct{})
	}
	coordinator.active++
}

func (coordinator *Coordinator) leaveLocked() {
	if coordinator.active == 0 {
		return
	}
	coordinator.active--
	if coordinator.active == 0 {
		close(coordinator.zero)
	}
}
