package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

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
	Keys  []state.KeyRuntimeCheckpoint    `json:"keys,omitempty"`
	Stats []health.StatsRuntimeCheckpoint `json:"stats,omitempty"`
}

// FileRuntimeStateCheckpoint stores the small, disposable runtime checkpoint
// in DATA_DIR. The startup path consumes the file before parsing it so a
// malformed or partially written file cannot be retried on every restart.
type FileRuntimeStateCheckpoint struct {
	path       string
	registry   *state.KeyRegistry
	stats      *health.StatsStore
	removeFile func(string) error
}

func NewFileRuntimeStateCheckpoint(
	dataDir string,
	registry *state.KeyRegistry,
	stats *health.StatsStore,
) *FileRuntimeStateCheckpoint {
	return &FileRuntimeStateCheckpoint{
		path:       filepath.Join(dataDir, runtimeStateCheckpointFileName),
		registry:   registry,
		stats:      stats,
		removeFile: os.Remove,
	}
}

// Restore consumes the checkpoint file if present. Failures are returned to
// the lifecycle owner, which continues startup with database-backed state.
func (checkpoint *FileRuntimeStateCheckpoint) Restore(ctx context.Context) error {
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
		checkpoint.registry.RestoreRuntimeCheckpoint(document.Keys)
	}
	if checkpoint.stats != nil {
		checkpoint.stats.RestoreRuntimeCheckpoint(document.Stats)
	}
	return nil
}

// Save serializes the current runtime state and writes it directly to the
// fixed checkpoint file. A failed write is removed best-effort and returned so
// the lifecycle owner can log it without blocking shutdown.
func (checkpoint *FileRuntimeStateCheckpoint) Save(ctx context.Context) error {
	if err := contextError(ctx); err != nil {
		return err
	}
	document := runtimeStateCheckpointDocument{}
	if checkpoint.registry != nil {
		document.Keys = checkpoint.registry.CaptureRuntimeCheckpoint()
	}
	if checkpoint.stats != nil {
		document.Stats = checkpoint.stats.CaptureRuntimeCheckpoint()
	}
	payload, err := json.Marshal(document)
	if err != nil {
		return err
	}
	if err := os.WriteFile(checkpoint.path, payload, 0o600); err != nil {
		_ = os.Remove(checkpoint.path)
		return err
	}
	return nil
}

func contextError(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	return ctx.Err()
}
