package health

import (
	"sort"
	"time"
)

// StatsBucketCheckpoint is the serializable form of one minute bucket.
type StatsBucketCheckpoint struct {
	Minute  int64  `json:"minute"`
	Valid   bool   `json:"valid"`
	Success uint64 `json:"success"`
	Failure uint64 `json:"failure"`
	Problem uint64 `json:"problem"`
}

// StatsRuntimeCheckpoint contains the raw rolling-window state for one credential.
type StatsRuntimeCheckpoint struct {
	CredentialID        uint                    `json:"credential_id"`
	Buckets             []StatsBucketCheckpoint `json:"buckets"`
	ConsecutiveFailure  uint64                  `json:"consecutive_failure"`
	ConsecutiveProblem  uint64                  `json:"consecutive_problem"`
	LastEventAt         time.Time               `json:"last_event_at"`
	LastEventRecorded   bool                    `json:"last_event_recorded"`
	LastFailureCategory FailureCategory         `json:"last_failure_category"`
	LastStatusCode      int                     `json:"last_status_code"`
}

// CaptureRuntimeCheckpoint returns detached rolling-window state in stable
// credential order. It does not include request queues or access-key rate limits.
func (store *StatsStore) CaptureRuntimeCheckpoint() []StatsRuntimeCheckpoint {
	store.mu.Lock()
	checkpoints := make([]StatsRuntimeCheckpoint, 0, len(store.windows))
	for credentialID, window := range store.windows {
		buckets := make([]StatsBucketCheckpoint, 0, len(window.buckets))
		for _, bucket := range window.buckets {
			buckets = append(buckets, StatsBucketCheckpoint{
				Minute:  bucket.minute,
				Valid:   bucket.valid,
				Success: bucket.success,
				Failure: bucket.failure,
				Problem: bucket.problem,
			})
		}
		checkpoints = append(checkpoints, StatsRuntimeCheckpoint{
			CredentialID:        credentialID,
			Buckets:             buckets,
			ConsecutiveFailure:  window.consecutiveFailure,
			ConsecutiveProblem:  window.consecutiveProblem,
			LastEventAt:         window.lastEventAt,
			LastEventRecorded:   window.lastEventRecorded,
			LastFailureCategory: window.lastFailureCategory,
			LastStatusCode:      window.lastStatusCode,
		})
	}
	store.mu.Unlock()
	sort.Slice(checkpoints, func(i, j int) bool {
		return checkpoints[i].CredentialID < checkpoints[j].CredentialID
	})
	return checkpoints
}

// RestoreRuntimeCheckpoint replaces the supplied credentials' rolling-window state.
// Invalid credential IDs and malformed bucket lists are ignored because checkpoint
// data is explicitly best-effort.
func (store *StatsStore) RestoreRuntimeCheckpoint(checkpoints []StatsRuntimeCheckpoint) int {
	store.mu.Lock()
	defer store.mu.Unlock()

	restored := 0
	for _, checkpoint := range checkpoints {
		if checkpoint.CredentialID == 0 || len(checkpoint.Buckets) > statsBucketCount {
			continue
		}
		window := &credentialStatsWindow{
			consecutiveFailure:  checkpoint.ConsecutiveFailure,
			consecutiveProblem:  checkpoint.ConsecutiveProblem,
			lastEventAt:         checkpoint.LastEventAt,
			lastEventRecorded:   checkpoint.LastEventRecorded,
			lastFailureCategory: checkpoint.LastFailureCategory,
			lastStatusCode:      checkpoint.LastStatusCode,
		}
		for _, bucket := range checkpoint.Buckets {
			if !bucket.Valid {
				continue
			}
			slot := statsBucketSlot(bucket.Minute)
			window.buckets[slot] = statsBucket{
				minute:  bucket.Minute,
				valid:   true,
				success: bucket.Success,
				failure: bucket.Failure,
				problem: bucket.Problem,
			}
		}
		store.windows[checkpoint.CredentialID] = window
		restored++
	}
	return restored
}
