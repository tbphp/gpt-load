package main

import (
	"strings"
	"testing"
	"time"
)

func TestWaitForServiceStateAllowsProgressBeyondTwoMinutes(t *testing.T) {
	base := time.Unix(0, 0)
	current := base
	query := func() (serviceWaitStatus, error) {
		elapsed := current.Sub(base)
		switch {
		case elapsed >= 210*time.Second:
			return serviceWaitStatus{state: 2}, nil
		case elapsed >= 200*time.Second:
			return serviceWaitStatus{state: 3, checkPoint: 3, waitHint: 180_000}, nil
		case elapsed >= 100*time.Second:
			return serviceWaitStatus{state: 3, checkPoint: 2, waitHint: 180_000}, nil
		default:
			return serviceWaitStatus{state: 3, checkPoint: 1, waitHint: 180_000}, nil
		}
	}

	err := waitForServiceState(query, 2, 1, func() time.Time { return current }, func(wait time.Duration) {
		current = current.Add(wait)
	})
	if err != nil {
		t.Fatalf("waitForServiceState() error = %v", err)
	}
	if current.Sub(base) <= 2*time.Minute {
		t.Fatalf("wait duration = %s, want progress beyond two minutes", current.Sub(base))
	}
}

func TestWaitForServiceStateTimesOutWhenCheckpointStops(t *testing.T) {
	base := time.Unix(0, 0)
	current := base
	query := func() (serviceWaitStatus, error) {
		return serviceWaitStatus{state: 3, checkPoint: 7, waitHint: 5_000}, nil
	}

	err := waitForServiceState(query, 2, 1, func() time.Time { return current }, func(wait time.Duration) {
		current = current.Add(wait)
	})
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("waitForServiceState() error = %v, want progress timeout", err)
	}
	if elapsed := current.Sub(base); elapsed > 10*time.Second {
		t.Fatalf("stalled service detected after %s, want WaitHint-based timeout", elapsed)
	}
}

func TestWaitForServiceStateReportsStartupExitCodes(t *testing.T) {
	err := waitForServiceState(
		func() (serviceWaitStatus, error) {
			return serviceWaitStatus{
				state:                   1,
				win32ExitCode:           1066,
				serviceSpecificExitCode: 23,
			}, nil
		},
		4,
		1,
		time.Now,
		func(time.Duration) {},
	)
	if err == nil || !strings.Contains(err.Error(), "1066/23") {
		t.Fatalf("waitForServiceState() error = %v, want startup exit codes", err)
	}
}
