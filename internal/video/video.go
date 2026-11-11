// Package video defines the gateway-owned lifecycle of asynchronous video
// generation tasks. It deliberately owns no credential material: task records
// reference credentials by ID only.
package video

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	ObjectVideo = "video"

	StatusQueued     = "queued"
	StatusInProgress = "in_progress"
	StatusCompleted  = "completed"
	StatusFailed     = "failed"
	StatusCancelled  = "cancelled"

	DefaultMode        = "text"
	DefaultSeconds     = "5"
	DefaultSize        = "720P"
	DefaultAspectRatio = "16:9"

	MaxImages = 8
	MaxAudios = 3
	MaxVideos = 1

	DefaultTTL = 24 * time.Hour
)

var ErrNotFound = errors.New("video task not found")

// TaskStore is the gateway-owned persistence contract for video task bindings.
type TaskStore interface {
	NewTask(accessKeyID uint, request CreateRequest, idempotencyHash string) (Task, error)
	Reserve(ctx context.Context, task Task) (Task, bool, error)
	Get(ctx context.Context, accessKeyID uint, id string) (Task, error)
	UpdateCreateResult(ctx context.Context, task Task) error
	UpdateObservation(ctx context.Context, task Task) error
	Delete(ctx context.Context, id string) error
}

// ValidationError identifies one invalid create field. It is safe to return to
// clients: it never contains upstream data or credential material.
type ValidationError struct {
	Param   string
	Message string
}

func (e *ValidationError) Error() string {
	if e == nil {
		return "invalid video request"
	}
	return e.Message
}

// VideoReference is one reference video input.
type VideoReference struct {
	URL          string
	StartSeconds *float64
	RequireAudio *bool
}

// CreateRequest is the validated view of a video creation request. The
// normalized body is what the gateway sends upstream: it carries explicit
// defaults and drops empty media fields for the selected mode.
type CreateRequest struct {
	Model       string
	Prompt      string
	Mode        string
	Seconds     string
	Size        string
	AspectRatio string
	N           int
	FirstFrame  string
	LastFrame   string
	Images      []string
	Audios      []string
	Videos      []VideoReference

	NormalizedBody []byte
}

// UpstreamResponse is the provider-neutral view of one upstream video object.
type UpstreamResponse struct {
	ID           string
	TaskID       string
	VideoID      string
	Status       string
	Model        string
	URL          string
	Progress     *int
	CreatedAt    *int64
	ErrorMessage string
	ErrorCode    string
	Raw          json.RawMessage
}

// Task is the persisted binding between a stable gateway task ID and the
// upstream task plus the credential that created it.
type Task struct {
	ID                string  `gorm:"type:varchar(40);primaryKey;not null"`
	AccessKeyID       uint    `gorm:"not null;default:0;uniqueIndex:idx_video_tasks_idempotency,priority:1"`
	GroupID           uint    `gorm:"not null;default:0"`
	CredentialID      uint    `gorm:"not null;default:0"`
	UpstreamTaskID    string  `gorm:"type:varchar(255);not null;default:''"`
	UpstreamTaskAlias string  `gorm:"type:varchar(255);not null;default:''"`
	UpstreamVideoID   string  `gorm:"type:varchar(255);not null;default:''"`
	UpstreamModel     string  `gorm:"type:varchar(255);not null;default:''"`
	ClientModel       string  `gorm:"type:varchar(255);not null;default:''"`
	Status            string  `gorm:"type:varchar(32);not null;default:'queued'"`
	Mode              string  `gorm:"type:varchar(32);not null;default:'text'"`
	Seconds           string  `gorm:"type:varchar(8);not null;default:'5'"`
	Size              string  `gorm:"type:varchar(16);not null;default:'720P'"`
	AspectRatio       string  `gorm:"type:varchar(16);not null;default:'16:9'"`
	ResultURL         string  `gorm:"type:text;not null;default:''"`
	LastErrorCode     string  `gorm:"type:varchar(64);not null;default:''"`
	LastErrorMessage  string  `gorm:"type:text;not null;default:''"`
	IdempotencyKey    *string `gorm:"type:varchar(64);uniqueIndex:idx_video_tasks_idempotency,priority:2"`
	Progress          int     `gorm:"not null;default:0"`
	CreatedAtMS       int64   `gorm:"column:created_at_ms;not null;default:0"`
	UpdatedAtMS       int64   `gorm:"column:updated_at_ms;not null;default:0"`
	ExpiresAtMS       int64   `gorm:"column:expires_at_ms;not null;default:0;index:idx_video_tasks_expires"`
}

// TableName keeps the persisted table name independent of the Go package name.
func (Task) TableName() string { return "video_tasks" }

// UpstreamIDs returns the client-visible upstream identifiers retained on the
// task. The polling identifier is always available; the raw fields are kept
// only when the provider returned them.
func (task Task) UpstreamIDs() (taskID, videoID string) {
	taskID = task.UpstreamTaskAlias
	if taskID == "" {
		taskID = task.UpstreamTaskID
	}
	videoID = task.UpstreamVideoID
	return taskID, videoID
}

// Response is the OpenAI Videos-compatible gateway response.
type Response struct {
	ID        string    `json:"id"`
	Object    string    `json:"object"`
	Model     string    `json:"model,omitempty"`
	Status    string    `json:"status"`
	Progress  int       `json:"progress"`
	CreatedAt int64     `json:"created_at"`
	ExpiresAt *int64    `json:"expires_at,omitempty"`
	URL       string    `json:"url,omitempty"`
	TaskID    string    `json:"task_id,omitempty"`
	VideoID   string    `json:"video_id,omitempty"`
	Error     *APIError `json:"error,omitempty"`
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
	Error APIError `json:"error"`
}

// NewID returns a stable gateway task identifier with a `video_` prefix.
func NewID() (string, error) {
	random := make([]byte, 12)
	if _, err := rand.Read(random); err != nil {
		return "", fmt.Errorf("generate video task id: %w", err)
	}
	return "video_" + hex.EncodeToString(random), nil
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

// ParseCreateRequest validates one create body and returns the normalized
// upstream body. Unknown provider fields are preserved.
func ParseCreateRequest(body []byte) (CreateRequest, error) {
	if !json.Valid(body) {
		return CreateRequest{}, &ValidationError{Param: "", Message: "Request body must be a valid JSON object."}
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	var raw map[string]json.RawMessage
	if err := decoder.Decode(&raw); err != nil || raw == nil {
		return CreateRequest{}, &ValidationError{Param: "", Message: "Request body must be a valid JSON object."}
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); err == nil {
		return CreateRequest{}, &ValidationError{Param: "", Message: "Request body must contain exactly one JSON object."}
	}

	request := CreateRequest{N: 1, Mode: DefaultMode, Seconds: DefaultSeconds, Size: DefaultSize, AspectRatio: DefaultAspectRatio}
	model, present, err := stringField(raw, "model", true)
	if err != nil {
		return CreateRequest{}, err
	}
	if present {
		request.Model = model
	}
	prompt, present, err := stringField(raw, "prompt", false)
	if err != nil {
		return CreateRequest{}, err
	}
	if present {
		request.Prompt = prompt
	}
	if request.Model == "" || strings.TrimSpace(request.Model) != request.Model {
		return CreateRequest{}, &ValidationError{Param: "model", Message: "model must be a non-empty string."}
	}
	if request.Prompt == "" || strings.TrimSpace(request.Prompt) == "" {
		return CreateRequest{}, &ValidationError{Param: "prompt", Message: "prompt must be a non-empty string."}
	}

	if request.Mode, present, err = stringField(raw, "mode", false); err != nil {
		return CreateRequest{}, err
	}
	if !present || request.Mode == "" {
		request.Mode = DefaultMode
	}
	switch request.Mode {
	case "text", "keyframe", "reference":
	default:
		return CreateRequest{}, &ValidationError{Param: "mode", Message: "mode must be one of text, keyframe, or reference."}
	}

	if request.Seconds, present, err = stringField(raw, "seconds", false); err != nil {
		return CreateRequest{}, err
	}
	if !present || request.Seconds == "" {
		request.Seconds = DefaultSeconds
	} else {
		seconds, parseErr := strconv.Atoi(request.Seconds)
		if parseErr != nil || seconds < 4 || seconds > 12 {
			return CreateRequest{}, &ValidationError{Param: "seconds", Message: "seconds must be a string between \"4\" and \"12\"."}
		}
	}

	if request.Size, present, err = stringField(raw, "size", false); err != nil {
		return CreateRequest{}, err
	}
	if !present || request.Size == "" {
		request.Size = DefaultSize
	}
	switch request.Size {
	case "720P", "1080P", "1K", "2K":
	default:
		return CreateRequest{}, &ValidationError{Param: "size", Message: "size must be one of 720P, 1080P, 1K, or 2K."}
	}

	if request.AspectRatio, present, err = stringField(raw, "aspect_ratio", false); err != nil {
		return CreateRequest{}, err
	}
	if !present || request.AspectRatio == "" {
		request.AspectRatio = DefaultAspectRatio
	}
	if len(request.AspectRatio) > 16 || strings.ContainsAny(request.AspectRatio, "\r\n\x00") {
		return CreateRequest{}, &ValidationError{Param: "aspect_ratio", Message: "aspect_ratio is invalid."}
	}

	if n, present, err := integerField(raw, "n"); err != nil {
		return CreateRequest{}, err
	} else if present {
		if n != 1 {
			return CreateRequest{}, &ValidationError{Param: "n", Message: "n must be 1."}
		}
		request.N = int(n)
	}
	if _, _, err := integerField(raw, "seed"); err != nil {
		return CreateRequest{}, err
	}

	if request.FirstFrame, present, err = stringField(raw, "first_frame", false); err != nil {
		return CreateRequest{}, err
	}
	if request.FirstFrame, err = normalizeMediaURL(request.FirstFrame, "first_frame"); err != nil {
		return CreateRequest{}, err
	}
	if request.LastFrame, present, err = stringField(raw, "last_frame", false); err != nil {
		return CreateRequest{}, err
	}
	if request.LastFrame, err = normalizeMediaURL(request.LastFrame, "last_frame"); err != nil {
		return CreateRequest{}, err
	}
	if request.Images, err = stringArrayField(raw, "images", MaxImages); err != nil {
		return CreateRequest{}, err
	}
	if request.Audios, err = stringArrayField(raw, "audios", MaxAudios); err != nil {
		return CreateRequest{}, err
	}
	if request.Videos, err = videoReferenceField(raw, "videos"); err != nil {
		return CreateRequest{}, err
	}

	switch request.Mode {
	case "text":
		if len(request.Images) > 0 || len(request.Audios) > 0 || len(request.Videos) > 0 ||
			request.FirstFrame != "" || request.LastFrame != "" {
			return CreateRequest{}, &ValidationError{
				Param: "mode", Message: "mode=text does not accept first_frame, last_frame, images, audios, or videos.",
			}
		}
	case "keyframe":
		if len(request.Images) > 0 || len(request.Audios) > 0 || len(request.Videos) > 0 {
			return CreateRequest{}, &ValidationError{
				Param: "mode", Message: "mode=keyframe does not accept images, audios, or videos.",
			}
		}
		if request.FirstFrame == "" && request.LastFrame == "" {
			return CreateRequest{}, &ValidationError{
				Param: "first_frame", Message: "mode=keyframe requires first_frame or last_frame.",
			}
		}
	case "reference":
		if request.FirstFrame != "" || request.LastFrame != "" {
			return CreateRequest{}, &ValidationError{
				Param: "mode", Message: "mode=reference does not accept first_frame or last_frame.",
			}
		}
		if len(request.Images) == 0 && len(request.Audios) == 0 && len(request.Videos) == 0 {
			return CreateRequest{}, &ValidationError{
				Param: "images", Message: "mode=reference requires at least one of images, audios, or videos.",
			}
		}
	}

	normalized, err := normalizeCreateBody(raw, request)
	if err != nil {
		return CreateRequest{}, err
	}
	request.NormalizedBody = normalized
	return request, nil
}

func normalizeCreateBody(raw map[string]json.RawMessage, request CreateRequest) ([]byte, error) {
	object := make(map[string]json.RawMessage, len(raw)+5)
	for key, value := range raw {
		object[key] = value
	}
	encoded := func(value any) (json.RawMessage, error) {
		encodedValue, err := json.Marshal(value)
		if err != nil {
			return nil, fmt.Errorf("encode normalized video request: %w", err)
		}
		return encodedValue, nil
	}
	for key, value := range map[string]any{
		"model":        request.Model,
		"prompt":       request.Prompt,
		"mode":         request.Mode,
		"seconds":      request.Seconds,
		"size":         request.Size,
		"aspect_ratio": request.AspectRatio,
		"n":            request.N,
	} {
		valueJSON, err := encoded(value)
		if err != nil {
			return nil, err
		}
		object[key] = valueJSON
	}
	for _, key := range []string{"first_frame", "last_frame", "images", "audios", "videos"} {
		if _, exists := object[key]; !exists {
			continue
		}
		if (key == "first_frame" || key == "last_frame") && request.Mode == "reference" {
			delete(object, key)
			continue
		}
		if key == "images" || key == "audios" || key == "videos" {
			if request.Mode != "reference" {
				delete(object, key)
			} else if emptyRawArray(object[key]) {
				delete(object, key)
			}
		}
	}
	normalized, err := json.Marshal(object)
	if err != nil {
		return nil, fmt.Errorf("encode normalized video request: %w", err)
	}
	return normalized, nil
}

func emptyRawArray(value json.RawMessage) bool {
	var items []json.RawMessage
	return json.Unmarshal(value, &items) == nil && len(items) == 0
}

func stringField(raw map[string]json.RawMessage, field string, required bool) (string, bool, error) {
	value, exists := raw[field]
	if !exists || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
		if required {
			return "", false, &ValidationError{Param: field, Message: field + " is required."}
		}
		return "", false, nil
	}
	var text string
	if err := json.Unmarshal(value, &text); err != nil {
		return "", true, &ValidationError{Param: field, Message: field + " must be a string."}
	}
	return text, true, nil
}

func integerField(raw map[string]json.RawMessage, field string) (int64, bool, error) {
	value, exists := raw[field]
	if !exists || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
		return 0, false, nil
	}
	var number json.Number
	if err := json.Unmarshal(value, &number); err != nil {
		return 0, true, &ValidationError{Param: field, Message: field + " must be an integer."}
	}
	parsed, err := number.Int64()
	if err != nil {
		return 0, true, &ValidationError{Param: field, Message: field + " must be an integer."}
	}
	return parsed, true, nil
}

func normalizeMediaURL(value, field string) (string, error) {
	if value == "" {
		return "", nil
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" || parsed.User != nil ||
		(parsed.Scheme != "http" && parsed.Scheme != "https") {
		return "", &ValidationError{Param: field, Message: field + " must be a public http or https URL."}
	}
	return value, nil
}

func stringArrayField(raw map[string]json.RawMessage, field string, maximum int) ([]string, error) {
	value, exists := raw[field]
	if !exists || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
		return nil, nil
	}
	var items []string
	if err := json.Unmarshal(value, &items); err != nil {
		return nil, &ValidationError{Param: field, Message: field + " must be an array of strings."}
	}
	if len(items) > maximum {
		return nil, &ValidationError{Param: field, Message: fmt.Sprintf("%s accepts at most %d items.", field, maximum)}
	}
	result := make([]string, 0, len(items))
	for _, item := range items {
		normalized, err := normalizeMediaURL(item, field)
		if err != nil {
			return nil, err
		}
		if normalized != "" {
			result = append(result, normalized)
		}
	}
	return result, nil
}

func videoReferenceField(raw map[string]json.RawMessage, field string) ([]VideoReference, error) {
	value, exists := raw[field]
	if !exists || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
		return nil, nil
	}
	var items []map[string]json.RawMessage
	if err := json.Unmarshal(value, &items); err != nil {
		return nil, &ValidationError{Param: field, Message: field + " must be an array of objects."}
	}
	if len(items) > MaxVideos {
		return nil, &ValidationError{Param: field, Message: fmt.Sprintf("%s accepts at most %d item.", field, MaxVideos)}
	}
	result := make([]VideoReference, 0, len(items))
	for index, item := range items {
		reference := VideoReference{}
		rawURL, ok := item["url"]
		if !ok {
			return nil, &ValidationError{Param: field, Message: fmt.Sprintf("%s[%d].url is required.", field, index)}
		}
		if err := json.Unmarshal(rawURL, &reference.URL); err != nil {
			return nil, &ValidationError{Param: field, Message: fmt.Sprintf("%s[%d].url must be a string.", field, index)}
		}
		if reference.URL == "" {
			return nil, &ValidationError{Param: field, Message: fmt.Sprintf("%s[%d].url is required.", field, index)}
		}
		normalized, err := normalizeMediaURL(reference.URL, field)
		if err != nil {
			return nil, err
		}
		reference.URL = normalized
		if start, ok := item["start_seconds"]; ok && !bytes.Equal(bytes.TrimSpace(start), []byte("null")) {
			var number float64
			if err := json.Unmarshal(start, &number); err != nil || number < 0 {
				return nil, &ValidationError{Param: field, Message: fmt.Sprintf("%s[%d].start_seconds must be a non-negative number.", field, index)}
			}
			reference.StartSeconds = &number
		}
		if requireAudio, ok := item["require_audio"]; ok && !bytes.Equal(bytes.TrimSpace(requireAudio), []byte("null")) {
			var value bool
			if err := json.Unmarshal(requireAudio, &value); err != nil {
				return nil, &ValidationError{Param: field, Message: fmt.Sprintf("%s[%d].require_audio must be a boolean.", field, index)}
			}
			reference.RequireAudio = &value
		}
		result = append(result, reference)
	}
	return result, nil
}

// ParseUpstreamResponse decodes the fields the gateway normalizes. It accepts
// provider-specific identifiers; malformed optional fields are ignored.
func ParseUpstreamResponse(body []byte) (UpstreamResponse, error) {
	if len(bytes.TrimSpace(body)) == 0 || !json.Valid(body) {
		return UpstreamResponse{}, errors.New("upstream video response is not a JSON object")
	}
	var raw struct {
		ID        any             `json:"id"`
		TaskID    any             `json:"task_id"`
		VideoID   any             `json:"video_id"`
		Status    string          `json:"status"`
		Model     string          `json:"model"`
		URL       string          `json:"url"`
		Progress  *int            `json:"progress"`
		CreatedAt *int64          `json:"created_at"`
		Error     json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return UpstreamResponse{}, fmt.Errorf("decode upstream video response: %w", err)
	}
	result := UpstreamResponse{
		ID: stringValue(raw.ID), TaskID: stringValue(raw.TaskID), VideoID: stringValue(raw.VideoID),
		Status: raw.Status, Model: raw.Model, URL: raw.URL, Progress: raw.Progress, CreatedAt: raw.CreatedAt,
		Raw: append(json.RawMessage(nil), body...),
	}
	if len(raw.Error) > 0 && !bytes.Equal(bytes.TrimSpace(raw.Error), []byte("null")) {
		var errorObject struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		}
		if json.Unmarshal(raw.Error, &errorObject) == nil {
			result.ErrorCode, result.ErrorMessage = errorObject.Code, errorObject.Message
		}
	}
	return result, nil
}

func stringValue(value any) string {
	if text, ok := value.(string); ok {
		return text
	}
	return ""
}

// SelectUpstreamTaskID returns the identifier used for retrieve/content/delete
// requests. Agnes prefers video_id; OpenAI-shaped providers prefer id then
// task_id then video_id.
func SelectUpstreamTaskID(channelID string, response UpstreamResponse) string {
	if channelID == "agnes" {
		for _, candidate := range []string{response.VideoID, response.TaskID, response.ID} {
			if candidate != "" {
				return candidate
			}
		}
		return ""
	}
	for _, candidate := range []string{response.ID, response.TaskID, response.VideoID} {
		if candidate != "" {
			return candidate
		}
	}
	return ""
}

// NormalizeStatus maps provider statuses onto the five gateway values.
func NormalizeStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "", "queued", "submitted":
		return StatusQueued
	case "pending", "processing", "running", "in_progress", "in-progress":
		return StatusInProgress
	case "done", "completed", "complete", "succeeded", "success":
		return StatusCompleted
	case "error", "failed", "failure":
		return StatusFailed
	case "cancelled", "canceled", "cancelling", "canceling":
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

// ApplyUpstreamObservation updates a task from one upstream status response.
func ApplyUpstreamObservation(task Task, response UpstreamResponse) Task {
	if response.Progress != nil {
		task.Progress = ClampProgress(*response.Progress)
	}
	if task.Status == StatusCompleted {
		task.Progress = 100
	}
	if response.URL != "" {
		task.ResultURL = response.URL
	}
	if status := strings.TrimSpace(response.Status); status != "" {
		task.Status = NormalizeStatus(status)
	}
	task.LastErrorCode = response.ErrorCode
	task.LastErrorMessage = response.ErrorMessage
	if (task.Status == StatusFailed || task.Status == StatusCancelled) && task.LastErrorCode == "" {
		task.LastErrorCode = "upstream_error"
	}
	return task
}

// TaskResponse renders the client view of one persisted task.
func TaskResponse(task Task, now time.Time, includeURL bool) Response {
	response := Response{
		ID: task.ID, Object: ObjectVideo, Model: task.ClientModel, Status: task.Status,
		Progress: task.Progress, CreatedAt: task.CreatedAtMS / 1000,
	}
	if task.ExpiresAtMS > 0 {
		expires := task.ExpiresAtMS / 1000
		response.ExpiresAt = &expires
	}
	if includeURL && task.Status == StatusCompleted {
		if task.ResultURL != "" {
			response.URL = task.ResultURL
		} else {
			response.URL = "/v1/videos/" + task.ID + "/content"
		}
	}
	if task.Status == StatusFailed || task.Status == StatusCancelled {
		message := task.LastErrorMessage
		if message == "" {
			message = "Upstream video task did not complete."
		}
		code := task.LastErrorCode
		if code == "" {
			code = "upstream_error"
		}
		response.Error = &APIError{Message: message, Type: "upstream_error", Code: code}
	}
	return response
}

// CreateResponse renders the creation response, including upstream IDs when
// the provider supplied them.
func CreateResponse(task Task) Response {
	response := TaskResponse(task, time.UnixMilli(task.CreatedAtMS), false)
	response.Status = StatusQueued
	response.Progress = 0
	taskID, videoID := task.UpstreamIDs()
	response.TaskID = taskID
	response.VideoID = videoID
	return response
}
