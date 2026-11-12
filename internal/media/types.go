// Package media owns gateway-side asynchronous media task bindings shared by
// OpenAI Videos and Zhipu image/video protocols.
package media

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

const (
	KindImage = "image"
	KindVideo = "video"

	StatusQueued     = "queued"
	StatusInProgress = "in_progress"
	StatusCompleted  = "completed"
	StatusFailed     = "failed"
	StatusCancelled  = "cancelled"

	DefaultTTL = 24 * time.Hour
)

var ErrNotFound = errors.New("media task not found")

// TaskStore is the gateway-owned persistence contract for media task bindings.
type TaskStore interface {
	NewTask(accessKeyID uint, params NewTaskParams, idempotencyHash string) (Task, error)
	Reserve(ctx context.Context, task Task) (Task, bool, error)
	Get(ctx context.Context, accessKeyID uint, id string) (Task, error)
	UpdateCreateResult(ctx context.Context, task Task) error
	UpdateObservation(ctx context.Context, task Task) error
	MarkFailed(ctx context.Context, id, code, message string) error
}

// ValidationError identifies one invalid request field and is safe to return
// to clients.
type ValidationError struct {
	Param   string
	Message string
}

func (e *ValidationError) Error() string {
	if e == nil {
		return "invalid media request"
	}
	return e.Message
}

// MediaCreate is the provider-neutral validated creation shape. Protocol
// dialects fill it; the gateway uses it to reserve a binding and emit metadata.
type MediaCreate struct {
	Protocol       string
	Operation      string
	Kind           string
	ClientModel    string
	Prompt         string
	Mode           string
	Seconds        string
	Size           string
	AspectRatio    string
	Quality        string
	FPS            int
	Duration       int
	WithAudio      bool
	ImageCount     int
	NormalizedBody []byte
}

// NewTaskParams is the persisted subset of one creation request.
type NewTaskParams struct {
	IDPrefix    string
	Protocol    string
	Operation   string
	Kind        string
	ClientModel string
	Mode        string
	Seconds     string
	Size        string
	AspectRatio string
	Quality     string
	FPS         int
	Duration    int
	WithAudio   bool
	ImageCount  int
}

// Task is the persisted binding between a gateway task ID and the upstream
// task plus the credential that created it. It intentionally stores no
// credential material.
type Task struct {
	ID                string  `gorm:"type:varchar(40);primaryKey;not null"`
	AccessKeyID       uint    `gorm:"not null;default:0;uniqueIndex:idx_media_tasks_idempotency,priority:1"`
	GroupID           uint    `gorm:"not null;default:0"`
	CredentialID      uint    `gorm:"not null;default:0"`
	Protocol          string  `gorm:"type:varchar(32);not null;default:'openai-videos'"`
	Operation         string  `gorm:"type:varchar(48);not null;default:'videos_create'"`
	Kind              string  `gorm:"type:varchar(16);not null;default:'video';check:chk_media_tasks_kind,kind IN ('image','video')"`
	UpstreamTaskID    string  `gorm:"type:varchar(255);not null;default:''"`
	UpstreamTaskAlias string  `gorm:"type:varchar(255);not null;default:''"`
	UpstreamVideoID   string  `gorm:"type:varchar(255);not null;default:''"`
	UpstreamModel     string  `gorm:"type:varchar(255);not null;default:''"`
	ClientModel       string  `gorm:"type:varchar(255);not null;default:''"`
	Status            string  `gorm:"type:varchar(32);not null;default:'queued';check:chk_media_tasks_status,status IN ('queued','in_progress','completed','failed','cancelled')"`
	Mode              string  `gorm:"type:varchar(32);not null;default:''"`
	Seconds           string  `gorm:"type:varchar(8);not null;default:''"`
	Size              string  `gorm:"type:varchar(32);not null;default:''"`
	AspectRatio       string  `gorm:"type:varchar(16);not null;default:''"`
	Quality           string  `gorm:"type:varchar(16);not null;default:''"`
	FPS               int     `gorm:"not null;default:0"`
	Duration          int     `gorm:"not null;default:0"`
	WithAudio         bool    `gorm:"not null;default:false"`
	ImageCount        int     `gorm:"not null;default:0"`
	ResultURL         string  `gorm:"type:text;not null;default:''"`
	LastErrorCode     string  `gorm:"type:varchar(64);not null;default:''"`
	LastErrorMessage  string  `gorm:"type:text;not null;default:''"`
	IdempotencyKey    *string `gorm:"type:varchar(64);uniqueIndex:idx_media_tasks_idempotency,priority:2"`
	Progress          int     `gorm:"not null;default:0;check:chk_media_tasks_progress,progress >= 0 AND progress <= 100"`
	CreatedAtMS       int64   `gorm:"column:created_at_ms;not null;default:0"`
	UpdatedAtMS       int64   `gorm:"column:updated_at_ms;not null;default:0"`
	ExpiresAtMS       int64   `gorm:"column:expires_at_ms;not null;default:0;index:idx_media_tasks_expires"`
}

func (Task) TableName() string { return "media_tasks" }

// UpstreamIDs returns the raw upstream identifiers retained on the task.
func (task Task) UpstreamIDs() (taskID, videoID string) {
	taskID = task.UpstreamTaskAlias
	if taskID == "" {
		taskID = task.UpstreamTaskID
	}
	videoID = task.UpstreamVideoID
	return taskID, videoID
}

// APIError is the OpenAI-style error object.
type APIError struct {
	Message string `json:"message"`
	Type    string `json:"type"`
	Param   string `json:"param"`
	Code    string `json:"code"`
}

// ErrorEnvelope is the top-level OpenAI-style error response.
type ErrorEnvelope struct {
	Error  APIError `json:"error"`
	TaskID string   `json:"task_id,omitempty"`
}

// NewID returns a stable gateway task identifier with the supplied prefix.
func NewID(prefix string) (string, error) {
	random := make([]byte, 12)
	if _, err := rand.Read(random); err != nil {
		return "", fmt.Errorf("generate media task id: %w", err)
	}
	if prefix == "" {
		prefix = "media"
	}
	return prefix + "_" + hex.EncodeToString(random), nil
}

// IdempotencyHash binds one idempotency key to the access key that supplied it.
func IdempotencyHash(accessKeyID uint, key string) (string, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return "", nil
	}
	if len(key) > 255 || strings.ContainsAny(key, "\r\n\x00") {
		return "", &ValidationError{Param: "Idempotency-Key", Message: "Idempotency-Key is invalid."}
	}
	sum := sha256.Sum256([]byte(strconv.FormatUint(uint64(accessKeyID), 10) + "\n" + key))
	return hex.EncodeToString(sum[:]), nil
}

// NormalizeStatus maps provider statuses onto the five gateway values.
func NormalizeStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "", "queued", "submitted":
		return StatusQueued
	case "pending", "processing", "running", "in_progress", "in-progress", "processing.":
		return StatusInProgress
	case "done", "completed", "complete", "succeeded", "success":
		return StatusCompleted
	case "error", "failed", "fail", "failure":
		return StatusFailed
	case "cancelled", "canceled", "cancelling", "canceling", "cancelled.":
		return StatusCancelled
	default:
		return StatusInProgress
	}
}

// ClampProgress keeps provider progress inside the documented 0..100 range.
func ClampProgress(progress int) int {
	if progress < 0 {
		return 0
	}
	if progress > 100 {
		return 100
	}
	return progress
}

// ApplyStatusObservation updates one task from a normalized status.
func ApplyStatusObservation(task Task, status string, progress *int, resultURL, errorCode, errorMessage string) Task {
	if progress != nil {
		task.Progress = ClampProgress(*progress)
	}
	if resultURL != "" {
		task.ResultURL = resultURL
	}
	if status = strings.TrimSpace(status); status != "" {
		task.Status = NormalizeStatus(status)
	}
	if task.Status == StatusCompleted {
		task.Progress = 100
	}
	task.LastErrorCode = errorCode
	task.LastErrorMessage = errorMessage
	if (task.Status == StatusFailed || task.Status == StatusCancelled) && task.LastErrorCode == "" {
		task.LastErrorCode = "upstream_error"
	}
	return task
}
