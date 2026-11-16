package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"gpt-load/internal/platform/securefile"
	"os"
	"path/filepath"
	"sync"

	"gpt-load/internal/health"
	"gpt-load/internal/state"
)

const runtimeStateCheckpointFileName = "runtime-state.checkpoint.json"

// RuntimeStateCheckpoint persists best-effort mutable runtime state between
// process runs. Implementations must not become a startup or shutdown blocker.
type RuntimeStateCheckpoint interface {
	Restore(context.Context) error
	Save(context.Context) error
}

type runtimeStateCheckpointDocument struct {
	Credentials []state.CredentialRuntimeCheckpoint `json:"credentials,omitempty"`
	Stats       []health.StatsRuntimeCheckpoint     `json:"stats,omitempty"`
	Scheduling  *state.SchedulingCheckpoint         `json:"scheduling,omitempty"`
	Responses   []state.ResponseBinding             `json:"responses,omitempty"`
}

// FileRuntimeStateCheckpoint stores the small, disposable runtime checkpoint
// in DATA_DIR. The startup path consumes the file before parsing it so a
// malformed or partially written file cannot be retried on every restart.
type FileRuntimeStateCheckpoint struct {
	mu               sync.Mutex
	path             string
	registry         *state.CredentialRegistry
	stats            *health.StatsStore
	responseBindings *state.ResponseBindings
	removeFile       func(string) error
}

func NewFileRuntimeStateCheckpoint(
	dataDir string,
	registry *state.CredentialRegistry,
	stats *health.StatsStore,
	responseBindings *state.ResponseBindings,
) *FileRuntimeStateCheckpoint {
	return &FileRuntimeStateCheckpoint{
		path:             filepath.Join(dataDir, runtimeStateCheckpointFileName),
		registry:         registry,
		stats:            stats,
		responseBindings: responseBindings,
		removeFile:       os.Remove,
	}
}

// Restore consumes the checkpoint file if present. Failures are returned to
// the lifecycle owner, which continues startup with database-backed state.
func (checkpoint *FileRuntimeStateCheckpoint) Restore(ctx context.Context) error {
	checkpoint.mu.Lock()
	defer checkpoint.mu.Unlock()
	if err := contextError(ctx); err != nil {
		return err
	}
	raw, err := os.ReadFile(checkpoint.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read runtime state checkpoint: %w", err)
	}
	if err := checkpoint.removeFile(checkpoint.path); err != nil {
		return fmt.Errorf("consume runtime state checkpoint: %w", err)
	}

	var document runtimeStateCheckpointDocument
	if err := json.Unmarshal(raw, &document); err != nil {
		return fmt.Errorf("decode runtime state checkpoint: %w", err)
	}
	if checkpoint.registry != nil {
		checkpoint.registry.RestoreRuntimeCheckpoint(document.Credentials)
		if document.Scheduling != nil {
			checkpoint.registry.SchedulingState().RestoreCheckpoint(*document.Scheduling)
		}
	}
	if checkpoint.stats != nil {
		checkpoint.stats.RestoreRuntimeCheckpoint(document.Stats)
	}
	if checkpoint.responseBindings != nil {
		if err := checkpoint.responseBindings.RestoreCheckpoint(document.Responses); err != nil {
			return err
		}
	}
	return nil
}

// Save publishes complete runtime state through a secure sibling temporary file.
func (checkpoint *FileRuntimeStateCheckpoint) Save(ctx context.Context) error {
	checkpoint.mu.Lock()
	defer checkpoint.mu.Unlock()
	if err := contextError(ctx); err != nil {
		return err
	}
	document := runtimeStateCheckpointDocument{}
	if checkpoint.registry != nil {
		document.Credentials = checkpoint.registry.CaptureRuntimeCheckpoint()
		scheduling := checkpoint.registry.SchedulingState().CaptureCheckpoint()
		document.Scheduling = &scheduling
	}
	if checkpoint.stats != nil {
		document.Stats = checkpoint.stats.CaptureRuntimeCheckpoint()
	}
	if checkpoint.responseBindings != nil {
		document.Responses = checkpoint.responseBindings.CaptureCheckpoint()
	}
	payload, err := json.Marshal(document)
	if err != nil {
		return err
	}
	return securefile.WriteAtomic(checkpoint.path, payload)
}

func contextError(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	return ctx.Err()
}
