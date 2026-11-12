package mediastore

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
	"gorm.io/gorm"

	"gpt-load/internal/media"
)

// Store persists gateway video task bindings. It never stores credential
// material: rows reference credentials by ID only.
type Store struct {
	db  *gorm.DB
	now func() time.Time
	ttl time.Duration
}

func NewStore(db *gorm.DB) *Store {
	store := &Store{db: db, now: time.Now, ttl: media.DefaultTTL}
	if raw := strings.TrimSpace(os.Getenv("MEDIA_TASK_TTL")); raw != "" {
		if ttl, err := time.ParseDuration(raw); err == nil && ttl > 0 {
			store.ttl = ttl
		} else {
			logrus.WithField("value", raw).Warn("ignoring invalid MEDIA_TASK_TTL")
		}
	}
	return store
}

func (store *Store) SetClock(now func() time.Time) {
	if now != nil {
		store.now = now
	}
}

func (store *Store) SetTTL(ttl time.Duration) {
	if ttl > 0 {
		store.ttl = ttl
	}
}

func (store *Store) TTL() time.Duration {
	if store == nil || store.ttl <= 0 {
		return media.DefaultTTL
	}
	return store.ttl
}

func (store *Store) NewTask(accessKeyID uint, params media.NewTaskParams, idempotencyHash string) (media.Task, error) {
	now := store.currentTime()
	protocolName := params.Protocol
	if protocolName == "" {
		protocolName = "openai-videos"
	}
	operation := params.Operation
	if operation == "" {
		operation = "videos_create"
	}
	kind := params.Kind
	if kind == "" {
		kind = media.KindVideo
	}
	idPrefix := params.IDPrefix
	if idPrefix == "" {
		idPrefix = "media"
	}
	id, err := media.NewID(idPrefix)
	if err != nil {
		return media.Task{}, err
	}
	task := media.Task{
		ID: id, AccessKeyID: accessKeyID, Protocol: protocolName, Operation: operation, Kind: kind,
		ClientModel: params.ClientModel, Status: media.StatusQueued,
		Mode: params.Mode, Seconds: params.Seconds, Size: params.Size,
		AspectRatio: params.AspectRatio, Quality: params.Quality,
		FPS: params.FPS, Duration: params.Duration, WithAudio: params.WithAudio, ImageCount: params.ImageCount,
		CreatedAtMS: now.UnixMilli(), UpdatedAtMS: now.UnixMilli(),
		ExpiresAtMS: now.Add(store.TTL()).UnixMilli(),
	}
	if idempotencyHash != "" {
		task.IdempotencyKey = &idempotencyHash
	}
	return task, nil
}

// Reserve persists a pending task. When an idempotency key already exists, the
// existing row is returned and created is false.
func (store *Store) Reserve(ctx context.Context, task media.Task) (media.Task, bool, error) {
	if store == nil || store.db == nil {
		return media.Task{}, false, fmt.Errorf("media task store is unavailable")
	}
	err := store.db.WithContext(ctx).Create(&task).Error
	if err == nil {
		return task, true, nil
	}
	if duplicate := task.IdempotencyKey != nil && isDuplicateError(err); duplicate {
		existing, lookupErr := store.getByIdempotency(ctx, task.AccessKeyID, *task.IdempotencyKey)
		if lookupErr == nil {
			return existing, false, nil
		}
		if errors.Is(lookupErr, media.ErrNotFound) {
			// The unique index can still hold an expired row. Remove it and retry
			// once so an idempotency key remains reusable after its TTL elapses.
			if deleteErr := store.db.WithContext(ctx).
				Where("access_key_id = ? AND idempotency_key = ?", task.AccessKeyID, *task.IdempotencyKey).
				Delete(&media.Task{}).Error; deleteErr == nil {
				if createErr := store.db.WithContext(ctx).Create(&task).Error; createErr == nil {
					return task, true, nil
				}
			}
		}
	}
	return media.Task{}, false, err
}

// Get returns the task owned by the access key. Expired rows are treated as
// absent so the caller can return task_not_found.
func (store *Store) Get(ctx context.Context, accessKeyID uint, id string) (media.Task, error) {
	if store == nil || store.db == nil {
		return media.Task{}, fmt.Errorf("media task store is unavailable")
	}
	if id == "" {
		return media.Task{}, media.ErrNotFound
	}
	var task media.Task
	err := store.db.WithContext(ctx).
		Where("id = ? AND access_key_id = ? AND expires_at_ms > ?", id, accessKeyID, store.currentTime().UnixMilli()).
		First(&task).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return media.Task{}, media.ErrNotFound
	}
	if err != nil {
		return media.Task{}, err
	}
	return task, nil
}

func (store *Store) getByIdempotency(ctx context.Context, accessKeyID uint, hash string) (media.Task, error) {
	var task media.Task
	err := store.db.WithContext(ctx).
		Where("access_key_id = ? AND idempotency_key = ? AND expires_at_ms > ?", accessKeyID, hash, store.currentTime().UnixMilli()).
		First(&task).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return media.Task{}, media.ErrNotFound
	}
	if err != nil {
		return media.Task{}, err
	}
	return task, nil
}

func (store *Store) UpdateCreateResult(ctx context.Context, task media.Task) error {
	if store == nil || store.db == nil {
		return fmt.Errorf("media task store is unavailable")
	}
	task.UpdatedAtMS = store.currentTime().UnixMilli()
	return store.db.WithContext(ctx).Model(&media.Task{}).Where("id = ?", task.ID).Updates(map[string]any{
		"group_id":            task.GroupID,
		"credential_id":       task.CredentialID,
		"upstream_task_id":    task.UpstreamTaskID,
		"upstream_task_alias": task.UpstreamTaskAlias,
		"upstream_video_id":   task.UpstreamVideoID,
		"upstream_model":      task.UpstreamModel,
		"status":              task.Status,
		"progress":            task.Progress,
		"result_url":          task.ResultURL,
		"last_error_code":     task.LastErrorCode,
		"last_error_message":  task.LastErrorMessage,
		"updated_at_ms":       task.UpdatedAtMS,
	}).Error
}

func (store *Store) UpdateObservation(ctx context.Context, task media.Task) error {
	if store == nil || store.db == nil {
		return fmt.Errorf("media task store is unavailable")
	}
	task.UpdatedAtMS = store.currentTime().UnixMilli()
	return store.db.WithContext(ctx).Model(&media.Task{}).Where("id = ?", task.ID).Updates(map[string]any{
		"status":             task.Status,
		"progress":           task.Progress,
		"result_url":         task.ResultURL,
		"last_error_code":    task.LastErrorCode,
		"last_error_message": task.LastErrorMessage,
		"updated_at_ms":      task.UpdatedAtMS,
	}).Error
}

func (store *Store) MarkFailed(ctx context.Context, id, code, message string) error {
	if store == nil || store.db == nil {
		return fmt.Errorf("media task store is unavailable")
	}
	return store.db.WithContext(ctx).Model(&media.Task{}).Where("id = ?", id).Updates(map[string]any{
		"status":             media.StatusFailed,
		"last_error_code":    code,
		"last_error_message": message,
		"updated_at_ms":      store.currentTime().UnixMilli(),
	}).Error
}

func (store *Store) MarkCancelled(ctx context.Context, accessKeyID uint, id string) error {
	if store == nil || store.db == nil {
		return fmt.Errorf("media task store is unavailable")
	}
	result := store.db.WithContext(ctx).Model(&media.Task{}).
		Where("id = ? AND access_key_id = ? AND expires_at_ms > ?", id, accessKeyID, store.currentTime().UnixMilli()).
		Updates(map[string]any{
			"status":        media.StatusCancelled,
			"updated_at_ms": store.currentTime().UnixMilli(),
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return media.ErrNotFound
	}
	return nil
}

func (store *Store) DeleteExpired(ctx context.Context) (int64, error) {
	if store == nil || store.db == nil {
		return 0, fmt.Errorf("media task store is unavailable")
	}
	result := store.db.WithContext(ctx).
		Where("expires_at_ms <= ?", store.currentTime().UnixMilli()).
		Delete(&media.Task{})
	return result.RowsAffected, result.Error
}

// Cleanup implements the control runtime retention hook.
func (store *Store) Cleanup(ctx context.Context, now time.Time) error {
	if store == nil || store.db == nil {
		return nil
	}
	result := store.db.WithContext(ctx).Where("expires_at_ms <= ?", now.UnixMilli()).Delete(&media.Task{})
	return result.Error
}

func (store *Store) currentTime() time.Time {
	if store != nil && store.now != nil {
		return store.now()
	}
	return time.Now()
}

func isDuplicateError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return true
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "unique constraint") ||
		strings.Contains(message, "duplicate entry") ||
		strings.Contains(message, "duplicate key")
}
