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
		pendingInterval: time.Second,
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
				pendingInterval: time.Second,
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

func TestWindowsServiceAdvancesStartPendingCheckpoint(t *testing.T) {
	releaseStart := make(chan struct{})
	application := &fakeManagedApplication{
		start: func() error {
			<-releaseStart
			return nil
		},
		serveErrors: make(chan error),
	}
	handler := &windowsServiceHandler{
		pendingInterval: 5 * time.Millisecond,
		newRuntime: func() (*managedRuntime, error) {
			return &managedRuntime{application: application, shutdownTimeout: time.Second}, nil
		},
	}
	requests := make(chan svc.ChangeRequest, 1)
	changes := make(chan svc.Status, 8)
	done := make(chan struct{})
	go func() {
		handler.Execute(nil, requests, changes)
		close(done)
	}()
	first := readWindowsServiceStatus(t, changes)
	second := readWindowsServiceStatus(t, changes)
	if second.State != svc.StartPending || second.CheckPoint <= first.CheckPoint {
		t.Fatalf("pending checkpoints = %d then %d", first.CheckPoint, second.CheckPoint)
	}
	close(releaseStart)
	running := readWindowsServiceState(t, changes, svc.Running)
	requests <- svc.ChangeRequest{Cmd: svc.Stop, CurrentStatus: running}
	_ = readWindowsServiceState(t, changes, svc.StopPending)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Windows service handler did not stop")
	}
}

func TestWindowsServiceStopsAndReturnsUnexpectedServeError(t *testing.T) {
	serveErrors := make(chan error, 1)
	stopCalls := make(chan context.Context, 1)
	application := &fakeManagedApplication{serveErrors: serveErrors, stopCalls: stopCalls}
	handler := &windowsServiceHandler{
		pendingInterval: time.Second,
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
