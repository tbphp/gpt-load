package main

import (
	"fmt"
	"time"
)

type serviceWaitStatus struct {
	state                   uint32
	checkPoint              uint32
	waitHint                uint32
	win32ExitCode           uint32
	serviceSpecificExitCode uint32
}

const (
	serviceWaitMinimumInterval = time.Second
	serviceWaitMaximumInterval = 10 * time.Second
	serviceWaitAbsoluteTimeout = 24 * time.Hour
)

func waitForServiceState(
	query func() (serviceWaitStatus, error),
	wanted uint32,
	stopped uint32,
	now func() time.Time,
	sleep func(time.Duration),
) error {
	startedAt := now()
	lastProgressAt := startedAt
	lastCheckPoint := uint32(0)
	for {
		status, err := query()
		if err != nil {
			return fmt.Errorf("query Windows service state: %w", err)
		}
		if status.state == wanted {
			return nil
		}
		if wanted != stopped && status.state == stopped {
			return fmt.Errorf(
				"Windows service stopped during startup with exit code %d/%d",
				status.win32ExitCode,
				status.serviceSpecificExitCode,
			)
		}

		currentTime := now()
		if currentTime.Sub(startedAt) > serviceWaitAbsoluteTimeout {
			return fmt.Errorf("timed out waiting for Windows service state %d", wanted)
		}

		waitHint := time.Duration(status.waitHint) * time.Millisecond
		if waitHint < serviceWaitMinimumInterval {
			waitHint = serviceWaitMinimumInterval
		}
		if status.checkPoint > lastCheckPoint {
			lastCheckPoint = status.checkPoint
			lastProgressAt = currentTime
		} else if currentTime.Sub(lastProgressAt) > waitHint {
			return fmt.Errorf("timed out waiting for Windows service state %d", wanted)
		}

		interval := waitHint / 10
		if interval < serviceWaitMinimumInterval {
			interval = serviceWaitMinimumInterval
		}
		if interval > serviceWaitMaximumInterval {
			interval = serviceWaitMaximumInterval
		}
		sleep(interval)
	}
}
