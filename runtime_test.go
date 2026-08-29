package main

import (
	"context"
	"errors"
	"testing"
	"time"
)

type fakeManagedApplication struct {
	start       func() error
	startErr    error
	stop        func(context.Context) error
	serveErrors chan error
	stopCalls   chan context.Context
}

func (application *fakeManagedApplication) Start() error {
	if application.start != nil {
		return application.start()
	}
	return application.startErr
}

func (application *fakeManagedApplication) Stop(ctx context.Context) error {
	if application.stopCalls != nil {
		application.stopCalls <- ctx
	}
	if application.stop != nil {
		return application.stop(ctx)
	}
	return nil
}

func (application *fakeManagedApplication) ServeErrors() <-chan error {
	return application.serveErrors
}

func TestManagedRuntimeCleansUpAfterStartFailure(t *testing.T) {
	startErr := errors.New("startup failed")
	stopCalls := make(chan context.Context, 1)
	runtime := &managedRuntime{
		application: &fakeManagedApplication{
			startErr:  startErr,
			stopCalls: stopCalls,
		},
		shutdownTimeout: 10 * time.Second,
	}

	err := runtime.start()
	if !errors.Is(err, startErr) {
		t.Fatalf("start() error = %v, want %v", err, startErr)
	}
	select {
	case cleanupCtx := <-stopCalls:
		deadline, ok := cleanupCtx.Deadline()
		if !ok || time.Until(deadline) > time.Second {
			t.Fatalf("startup cleanup deadline = %v, want at most one second", deadline)
		}
	default:
		t.Fatal("start() did not clean up after failure")
	}
}

func TestManagedRuntimeUsesConfiguredShutdownDeadline(t *testing.T) {
	const timeout = 250 * time.Millisecond
	stopCalls := make(chan context.Context, 1)
	runtime := &managedRuntime{
		application:     &fakeManagedApplication{stopCalls: stopCalls},
		shutdownTimeout: timeout,
	}

	if err := runtime.stop(nil); err != nil {
		t.Fatalf("stop() error = %v", err)
	}
	ctx := <-stopCalls
	deadline, ok := ctx.Deadline()
	if !ok {
		t.Fatal("shutdown context has no deadline")
	}
	remaining := time.Until(deadline)
	if remaining <= 0 || remaining > timeout {
		t.Fatalf("shutdown deadline remaining = %v, want within %v", remaining, timeout)
	}
}

func TestManagedRuntimeCancelsShutdownWhenForced(t *testing.T) {
	stopStarted := make(chan struct{})
	runtime := &managedRuntime{
		application: &fakeManagedApplication{stop: func(ctx context.Context) error {
			close(stopStarted)
			<-ctx.Done()
			return context.Cause(ctx)
		}},
		shutdownTimeout: time.Minute,
	}
	force := make(chan struct{})
	result := make(chan error, 1)
	go func() { result <- runtime.stop(force) }()

	<-stopStarted
	close(force)
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("forced stop error = %v, want context canceled", err)
	}
}
