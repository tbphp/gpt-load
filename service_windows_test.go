//go:build windows

package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"golang.org/x/sys/windows/svc"
)

func TestWindowsServiceReportsPendingRunningAndGracefulStop(t *testing.T) {
	application := &fakeManagedApplication{serveErrors: make(chan error)}
	handler := &windowsServiceHandler{
		newRuntime: func() (*managedRuntime, error) {
			return &managedRuntime{application: application, shutdownTimeout: 2 * time.Second}, nil
		},
	}
	requests := make(chan svc.ChangeRequest, 2)
	changes := make(chan svc.Status, 8)
	result := make(chan [2]uint32, 1)
	go func() {
		specific, exitCode := handler.Execute(nil, requests, changes)
		value := uint32(0)
		if specific {
			value = 1
		}
		result <- [2]uint32{value, exitCode}
	}()

	startPending := readWindowsServiceStatus(t, changes)
	if startPending.State != svc.StartPending || startPending.CheckPoint == 0 || startPending.WaitHint == 0 {
		t.Fatalf("start pending status = %#v", startPending)
	}
	running := readWindowsServiceStatus(t, changes)
	if running.State != svc.Running ||
		running.Accepts&svc.AcceptStop == 0 ||
		running.Accepts&svc.AcceptShutdown == 0 ||
		running.Accepts&svc.AcceptPreShutdown == 0 {
		t.Fatalf("running status = %#v", running)
	}
	requests <- svc.ChangeRequest{Cmd: svc.Stop, CurrentStatus: running}
	stopPending := readWindowsServiceStatus(t, changes)
	if stopPending.State != svc.StopPending || stopPending.CheckPoint == 0 || stopPending.WaitHint < 10_000 {
		t.Fatalf("stop pending status = %#v", stopPending)
	}
	if got := <-result; got != [2]uint32{0, 0} {
		t.Fatalf("handler result = %v", got)
	}
}

func TestWindowsServiceAcceptsShutdownAndPreShutdown(t *testing.T) {
	for _, command := range []svc.Cmd{svc.Shutdown, svc.PreShutdown} {
		t.Run(commandName(command), func(t *testing.T) {
			application := &fakeManagedApplication{serveErrors: make(chan error)}
			handler := &windowsServiceHandler{
				newRuntime: func() (*managedRuntime, error) {
					return &managedRuntime{application: application, shutdownTimeout: time.Second}, nil
				},
			}
			requests := make(chan svc.ChangeRequest, 1)
			changes := make(chan svc.Status, 4)
			result := make(chan uint32, 1)
			go func() {
				_, exitCode := handler.Execute(nil, requests, changes)
				result <- exitCode
			}()
			_ = readWindowsServiceStatus(t, changes)
			running := readWindowsServiceStatus(t, changes)
			requests <- svc.ChangeRequest{Cmd: command, CurrentStatus: running}
			if status := readWindowsServiceStatus(t, changes); status.State != svc.StopPending {
				t.Fatalf("stop status = %#v", status)
			}
			if exitCode := <-result; exitCode != 0 {
				t.Fatalf("exit code = %d", exitCode)
			}
		})
	}
}

func TestWindowsServiceStartTimeoutReportsFailureWithoutSyntheticProgress(t *testing.T) {
	releaseStart := make(chan struct{})
	reported := make(chan error, 1)
	application := &fakeManagedApplication{
		start: func() error {
			<-releaseStart
			return nil
		},
		serveErrors: make(chan error),
	}
	handler := &windowsServiceHandler{
		startupTimeout: 20 * time.Millisecond,
		reportFailure:  func(err error) { reported <- err },
		newRuntime: func() (*managedRuntime, error) {
			return &managedRuntime{application: application, shutdownTimeout: time.Second}, nil
		},
	}
	requests := make(chan svc.ChangeRequest, 1)
	changes := make(chan svc.Status, 8)
	type handlerResult struct {
		specific bool
		code     uint32
	}
	result := make(chan handlerResult, 1)
	go func() {
		specific, code := handler.Execute(nil, requests, changes)
		result <- handlerResult{specific: specific, code: code}
	}()
	defer func() {
		close(releaseStart)
		requests <- svc.ChangeRequest{Cmd: svc.Stop}
	}()

	startPending := readWindowsServiceStatus(t, changes)
	if startPending.State != svc.StartPending {
		t.Fatalf("start status = %#v", startPending)
	}
	if got, want := startPending.WaitHint, durationMilliseconds(
		handler.startupTimeout+windowsServiceStartWaitBuffer,
	); got != want {
		t.Fatalf("start wait hint = %d, want %d", got, want)
	}
	select {
	case got := <-result:
		if !got.specific || got.code == 0 {
			t.Fatalf("handler result = %#v, want service-specific startup failure", got)
		}
	case <-time.After(time.Second):
		t.Fatal("Windows service startup did not time out")
	}
	select {
	case status := <-changes:
		t.Fatalf("unexpected synthetic startup progress = %#v", status)
	default:
	}
	select {
	case err := <-reported:
		if err == nil {
			t.Fatal("reported startup timeout error = nil")
		}
	case <-time.After(time.Second):
		t.Fatal("startup timeout was not reported")
	}
}

func TestWindowsServiceExpectedStopReportsCleanupErrorWithoutFailureExit(t *testing.T) {
	stopErr := errors.New("shutdown cleanup failed")
	reported := make(chan error, 1)
	application := &fakeManagedApplication{
		stop:        func(context.Context) error { return stopErr },
		serveErrors: make(chan error),
	}
	handler := &windowsServiceHandler{
		reportFailure: func(err error) { reported <- err },
		newRuntime: func() (*managedRuntime, error) {
			return &managedRuntime{application: application, shutdownTimeout: time.Second}, nil
		},
	}
	requests := make(chan svc.ChangeRequest, 1)
	changes := make(chan svc.Status, 4)
	type handlerResult struct {
		specific bool
		code     uint32
	}
	result := make(chan handlerResult, 1)
	go func() {
		specific, code := handler.Execute(nil, requests, changes)
		result <- handlerResult{specific: specific, code: code}
	}()

	_ = readWindowsServiceState(t, changes, svc.Running)
	requests <- svc.ChangeRequest{Cmd: svc.Stop}
	_ = readWindowsServiceState(t, changes, svc.StopPending)
	if got := <-result; got != (handlerResult{}) {
		t.Fatalf("handler result = %#v, want clean service exit", got)
	}
	select {
	case err := <-reported:
		if !errors.Is(err, stopErr) {
			t.Fatalf("reported shutdown error = %v, want %v", err, stopErr)
		}
	case <-time.After(time.Second):
		t.Fatal("shutdown cleanup error was not reported")
	}
	if err := handler.result(); err != nil {
		t.Fatalf("handler result error = %v, want nil for an expected stop", err)
	}
}

func TestWindowsServiceDoesNotReportSyntheticStopProgress(t *testing.T) {
	releaseStop := make(chan struct{})
	application := &fakeManagedApplication{
		stop: func(context.Context) error {
			<-releaseStop
			return nil
		},
		serveErrors: make(chan error),
	}
	handler := &windowsServiceHandler{
		newRuntime: func() (*managedRuntime, error) {
			return &managedRuntime{application: application, shutdownTimeout: 5 * time.Second}, nil
		},
	}
	requests := make(chan svc.ChangeRequest, 1)
	changes := make(chan svc.Status, 4)
	done := make(chan struct{})
	go func() {
		handler.Execute(nil, requests, changes)
		close(done)
	}()
	defer func() {
		select {
		case <-releaseStop:
		default:
			close(releaseStop)
		}
	}()

	_ = readWindowsServiceState(t, changes, svc.Running)
	requests <- svc.ChangeRequest{Cmd: svc.Stop}
	_ = readWindowsServiceState(t, changes, svc.StopPending)
	select {
	case status := <-changes:
		t.Fatalf("unexpected synthetic stop progress = %#v", status)
	case <-time.After(50 * time.Millisecond):
	}
	close(releaseStop)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Windows service handler did not stop")
	}
}

func TestWindowsServiceStopsAndReturnsUnexpectedServeError(t *testing.T) {
	serveErrors := make(chan error, 1)
	stopCalls := make(chan context.Context, 1)
	application := &fakeManagedApplication{serveErrors: serveErrors, stopCalls: stopCalls}
	handler := &windowsServiceHandler{
		newRuntime: func() (*managedRuntime, error) {
			return &managedRuntime{application: application, shutdownTimeout: time.Second}, nil
		},
	}
	requests := make(chan svc.ChangeRequest, 1)
	changes := make(chan svc.Status, 4)
	type handlerResult struct {
		specific bool
		code     uint32
	}
	result := make(chan handlerResult, 1)
	go func() {
		specific, code := handler.Execute(nil, requests, changes)
		result <- handlerResult{specific: specific, code: code}
	}()
	_ = readWindowsServiceStatus(t, changes)
	_ = readWindowsServiceStatus(t, changes)
	serveErr := errors.New("HTTP accept loop failed")
	serveErrors <- serveErr
	if status := readWindowsServiceStatus(t, changes); status.State != svc.StopPending {
		t.Fatalf("stop status = %#v", status)
	}
	if got := <-result; !got.specific || got.code == 0 {
		t.Fatalf("handler result = %#v", got)
	}
	if !errors.Is(handler.result(), serveErr) {
		t.Fatalf("handler error = %v, want %v", handler.result(), serveErr)
	}
}

func TestWindowsServiceStopWaitHintIncludesGraceBuffer(t *testing.T) {
	if got, want := windowsServiceStopWaitHint(17*time.Second), uint32(22_000); got != want {
		t.Fatalf("stop wait hint = %d, want %d", got, want)
	}
	if got := windowsServiceStopWaitHint(time.Second); got != 10_000 {
		t.Fatalf("minimum stop wait hint = %d, want 10000", got)
	}
}

func readWindowsServiceStatus(t *testing.T, changes <-chan svc.Status) svc.Status {
	t.Helper()
	select {
	case status := <-changes:
		return status
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for Windows service status")
		return svc.Status{}
	}
}

func readWindowsServiceState(
	t *testing.T,
	changes <-chan svc.Status,
	wanted svc.State,
) svc.Status {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		select {
		case status := <-changes:
			if status.State == wanted {
				return status
			}
		case <-deadline:
			t.Fatalf("timed out waiting for Windows service state %d", wanted)
			return svc.Status{}
		}
	}
}

func commandName(command svc.Cmd) string {
	switch command {
	case svc.Shutdown:
		return "shutdown"
	case svc.PreShutdown:
		return "preshutdown"
	default:
		return "unknown"
	}
}
