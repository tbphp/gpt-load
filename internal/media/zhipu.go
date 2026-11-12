package media

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	ProtocolOpenAIVideos = "openai-videos"
	ProtocolZhipuMedia   = "zhipu-media"

	OperationZhipuImagesGenerate    = "zhipu_images_generate"
	OperationZhipuImagesCreateAsync = "zhipu_images_create_async"
	OperationZhipuVideosCreate      = "zhipu_videos_create"
	OperationZhipuMediaRetrieve     = "zhipu_media_retrieve"
	OperationZhipuMediaCancel       = "zhipu_media_cancel"

	ZhipuTaskProcessing = "PROCESSING"
)

// ZhipuResult is one normalized image or video result object.
type ZhipuResult struct {
	URL           string `json:"url,omitempty"`
	CoverImageURL string `json:"cover_image_url,omitempty"`
	B64JSON       string `json:"b64_json,omitempty"`
}

// ZhipuUpstream is the provider-neutral view of a Zhipu create or async-result
// response.
type ZhipuUpstream struct {
	ID          string
	RequestID   string
	Model       string
	TaskStatus  string
	ImageResult []ZhipuResult
	Data        []ZhipuResult
	VideoResult []ZhipuResult
	ErrorCode   string
	ErrorMsg    string
	Raw         json.RawMessage
}

// ZhipuModelKind classifies known Zhipu media model families.
func ZhipuModelKind(model string) string {
	lower := strings.ToLower(strings.TrimSpace(model))
	switch {
	case strings.Contains(lower, "cogvideo"), strings.Contains(lower, "vidu"), strings.Contains(lower, "video"):
		return KindVideo
	case strings.Contains(lower, "cogview"), strings.Contains(lower, "glm-image"), strings.Contains(lower, "image"):
		return KindImage
	default:
		return ""
	}
}

func knownZhipuChatModel(model string) bool {
	lower := strings.ToLower(strings.TrimSpace(model))
	return strings.HasPrefix(lower, "glm-4") || strings.HasPrefix(lower, "glm-3") ||
		strings.HasPrefix(lower, "charglm") || strings.HasPrefix(lower, "embedding-")
}

// ParseZhipuImageRequest validates a sync or async CogView request.
func ParseZhipuImageRequest(body []byte, async bool) (MediaCreate, error) {
	if !json.Valid(body) {
		return MediaCreate{}, &ValidationError{Param: "", Message: "Request body must be a valid JSON object."}
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil || raw == nil {
		return MediaCreate{}, &ValidationError{Param: "", Message: "Request body must be a valid JSON object."}
	}
	model, present, err := stringField(raw, "model", true)
	if err != nil || !present || strings.TrimSpace(model) == "" {
		return MediaCreate{}, &ValidationError{Param: "model", Message: "model must be a non-empty string."}
	}
	prompt, _, err := stringField(raw, "prompt", false)
	if err != nil {
		return MediaCreate{}, err
	}
	if prompt == "" || strings.TrimSpace(prompt) == "" {
		return MediaCreate{}, &ValidationError{Param: "prompt", Message: "prompt must be a non-empty string."}
	}
	if ZhipuModelKind(model) == KindVideo || knownZhipuChatModel(model) {
		return MediaCreate{}, &ValidationError{Param: "model", Message: "model does not support image generation."}
	}
	size, _, err := stringField(raw, "size", false)
	if err != nil {
		return MediaCreate{}, err
	}
	if size != "" {
		if err := validateZhipuImageSize(size); err != nil {
			return MediaCreate{}, err
		}
	}
	quality, _, err := stringField(raw, "quality", false)
	if err != nil {
		return MediaCreate{}, err
	}
	switch quality {
	case "", "standard", "hd":
	default:
		return MediaCreate{}, &ValidationError{Param: "quality", Message: "quality must be standard or hd."}
	}
	if _, _, err := stringField(raw, "user_id", false); err != nil {
		return MediaCreate{}, err
	}
	normalized := append([]byte(nil), body...)
	operation := OperationZhipuImagesGenerate
	if async {
		operation = OperationZhipuImagesCreateAsync
	}
	return MediaCreate{
		Protocol: ProtocolZhipuMedia, Operation: operation, Kind: KindImage,
		ClientModel: model, Prompt: prompt, Size: size, Quality: quality,
		ImageCount: 1, NormalizedBody: normalized,
	}, nil
}

// ParseZhipuVideoRequest validates a CogVideoX request.
func ParseZhipuVideoRequest(body []byte) (MediaCreate, error) {
	if !json.Valid(body) {
		return MediaCreate{}, &ValidationError{Param: "", Message: "Request body must be a valid JSON object."}
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil || raw == nil {
		return MediaCreate{}, &ValidationError{Param: "", Message: "Request body must be a valid JSON object."}
	}
	model, present, err := stringField(raw, "model", true)
	if err != nil || !present || strings.TrimSpace(model) == "" {
		return MediaCreate{}, &ValidationError{Param: "model", Message: "model must be a non-empty string."}
	}
	prompt, _, err := stringField(raw, "prompt", false)
	if err != nil {
		return MediaCreate{}, err
	}
	if prompt == "" || strings.TrimSpace(prompt) == "" {
		return MediaCreate{}, &ValidationError{Param: "prompt", Message: "prompt must be a non-empty string."}
	}
	if ZhipuModelKind(model) == KindImage || knownZhipuChatModel(model) {
		return MediaCreate{}, &ValidationError{Param: "model", Message: "model does not support video generation."}
	}
	imageURL, _, err := stringField(raw, "image_url", false)
	if err != nil {
		return MediaCreate{}, err
	}
	if imageURL != "" {
		if !validZhipuImageInput(imageURL) {
			return MediaCreate{}, &ValidationError{Param: "image_url", Message: "image_url must be an http/https URL or a data:image base64 URL."}
		}
	}
	size, _, err := stringField(raw, "size", false)
	if err != nil {
		return MediaCreate{}, err
	}
	if size != "" {
		if err := validateZhipuVideoSize(size); err != nil {
			return MediaCreate{}, err
		}
	}
	fps, present, err := integerField(raw, "fps")
	if err != nil {
		return MediaCreate{}, err
	}
	if present && fps != 30 && fps != 60 {
		return MediaCreate{}, &ValidationError{Param: "fps", Message: "fps must be 30 or 60."}
	}
	quality, _, err := stringField(raw, "quality", false)
	if err != nil {
		return MediaCreate{}, err
	}
	switch quality {
	case "", "speed", "quality":
	default:
		return MediaCreate{}, &ValidationError{Param: "quality", Message: "quality must be speed or quality."}
	}
	duration, present, err := integerField(raw, "duration")
	if err != nil {
		return MediaCreate{}, err
	}
	if present && duration != 5 && duration != 10 {
		return MediaCreate{}, &ValidationError{Param: "duration", Message: "duration must be 5 or 10."}
	}
	withAudio := false
	if audio, ok := raw["with_audio"]; ok && !bytes.Equal(bytes.TrimSpace(audio), []byte("null")) {
		if err := json.Unmarshal(audio, &withAudio); err != nil {
			return MediaCreate{}, &ValidationError{Param: "with_audio", Message: "with_audio must be a boolean."}
		}
	}
	normalized := append([]byte(nil), body...)
	return MediaCreate{
		Protocol: ProtocolZhipuMedia, Operation: OperationZhipuVideosCreate, Kind: KindVideo,
		ClientModel: model, Prompt: prompt, Size: size, Quality: quality,
		FPS: int(fps), Duration: int(duration), WithAudio: withAudio,
		ImageCount: 1, NormalizedBody: normalized,
	}, nil
}

func validateZhipuImageSize(raw string) error {
	width, height, err := parseZhipuSize(raw)
	if err != nil {
		return err
	}
	if officialZhipuSize(width, height, "1024x1024", "768x1344", "864x1152", "1344x768", "1152x864", "1440x720", "720x1440") {
		return nil
	}
	if width < 512 || height < 512 || width > 2048 || height > 2048 || width%16 != 0 || height%16 != 0 {
		return &ValidationError{Param: "size", Message: "size must be an official CogView size or a WxH between 512 and 2048 divisible by 16."}
	}
	if width*height > 1<<21 {
		return &ValidationError{Param: "size", Message: "size exceeds the 2^21 pixel limit."}
	}
	return nil
}

func validateZhipuVideoSize(raw string) error {
	width, height, err := parseZhipuSize(raw)
	if err != nil {
		return err
	}
	if officialZhipuSize(width, height, "720x480", "1024x1024", "1280x960", "960x1280", "1920x1080", "1080x1920", "2048x1080", "3840x2160") {
		return nil
	}
	if width < 256 || height < 256 || width > 4096 || height > 4096 || width%16 != 0 || height%16 != 0 {
		return &ValidationError{Param: "size", Message: "size must be an official video size or a WxH between 256 and 4096 divisible by 16."}
	}
	return nil
}

func officialZhipuSize(width, height int, sizes ...string) bool {
	candidate := fmt.Sprintf("%dx%d", width, height)
	for _, size := range sizes {
		if size == candidate {
			return true
		}
	}
	return false
}

func parseZhipuSize(raw string) (int, int, error) {
	parts := strings.Split(strings.ToLower(strings.TrimSpace(raw)), "x")
	if len(parts) != 2 {
		return 0, 0, &ValidationError{Param: "size", Message: "size must use the WxH format."}
	}
	width, errW := strconv.Atoi(parts[0])
	height, errH := strconv.Atoi(parts[1])
	if errW != nil || errH != nil || width <= 0 || height <= 0 {
		return 0, 0, &ValidationError{Param: "size", Message: "size must use the WxH format."}
	}
	return width, height, nil
}

func validZhipuImageInput(value string) bool {
	if strings.HasPrefix(value, "data:image/") {
		return strings.Contains(value, ";base64,")
	}
	parsed, err := url.Parse(value)
	return err == nil && parsed.Host != "" && parsed.User == nil &&
		(parsed.Scheme == "http" || parsed.Scheme == "https")
}

// ParseZhipuResponse decodes the fields the gateway normalizes from a Zhipu
// create or async-result response.
func ParseZhipuResponse(body []byte) (ZhipuUpstream, error) {
	if len(bytes.TrimSpace(body)) == 0 || !json.Valid(body) {
		return ZhipuUpstream{}, fmt.Errorf("upstream Zhipu response is not a JSON object")
	}
	var raw struct {
		ID          string          `json:"id"`
		RequestID   string          `json:"request_id"`
		Model       string          `json:"model"`
		TaskStatus  string          `json:"task_status"`
		ImageResult []ZhipuResult   `json:"image_result"`
		Data        []ZhipuResult   `json:"data"`
		VideoResult []ZhipuResult   `json:"video_result"`
		Error       json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return ZhipuUpstream{}, fmt.Errorf("decode Zhipu response: %w", err)
	}
	result := ZhipuUpstream{
		ID: raw.ID, RequestID: raw.RequestID, Model: raw.Model, TaskStatus: raw.TaskStatus,
		ImageResult: raw.ImageResult, Data: raw.Data, VideoResult: raw.VideoResult,
		Raw: append(json.RawMessage(nil), body...),
	}
	if len(raw.Error) > 0 && !bytes.Equal(bytes.TrimSpace(raw.Error), []byte("null")) {
		var errorObject struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		}
		if json.Unmarshal(raw.Error, &errorObject) == nil {
			result.ErrorCode, result.ErrorMsg = errorObject.Code, errorObject.Message
		}
	}
	return result, nil
}

// SelectZhipuTaskID returns the identifier accepted by /async-result/{id}.
func SelectZhipuTaskID(response ZhipuUpstream) string {
	if response.ID != "" {
		return response.ID
	}
	return response.RequestID
}

// ZhipuObservationResult returns the first usable result URL for a task kind.
func ZhipuObservationResult(task Task, response ZhipuUpstream) (resultURL, errorCode, errorMessage string) {
	results := response.ImageResult
	if len(results) == 0 {
		results = response.Data
	}
	if task.Kind == KindVideo {
		results = response.VideoResult
	}
	for _, result := range results {
		if result.URL != "" {
			resultURL = result.URL
			break
		}
	}
	return resultURL, response.ErrorCode, response.ErrorMsg
}

// ApplyZhipuObservation updates a task from one async-result response.
func ApplyZhipuObservation(task Task, response ZhipuUpstream) Task {
	resultURL, code, message := ZhipuObservationResult(task, response)
	return ApplyStatusObservation(task, response.TaskStatus, nil, resultURL, code, message)
}

// ZhipuCreateResponse renders the gateway create response for an async task.
func ZhipuCreateResponse(task Task) map[string]any {
	status := task.Status
	if status == "" {
		status = StatusInProgress
	}
	response := map[string]any{
		"model": task.ClientModel, "id": task.ID, "request_id": task.UpstreamTaskAlias,
		"task_status": status, "status": status,
	}
	if task.UpstreamTaskID != "" && task.UpstreamTaskID != task.UpstreamTaskAlias {
		response["upstream_id"] = task.UpstreamTaskID
	}
	if task.Status == StatusFailed || task.Status == StatusCancelled {
		message := task.LastErrorMessage
		if message == "" {
			message = "Upstream media task did not complete."
		}
		code := task.LastErrorCode
		if code == "" {
			code = "upstream_error"
		}
		response["error"] = APIError{Message: message, Type: "upstream_error", Code: code}
	}
	return response
}

// ZhipuTaskResponse renders the gateway async-result response.
func ZhipuTaskResponse(task Task, response ZhipuUpstream) map[string]any {
	status := task.Status
	result := map[string]any{
		"id": task.ID, "model": task.ClientModel, "task_status": status, "status": status,
		"request_id": task.UpstreamTaskAlias,
	}
	if task.ID == "" {
		delete(result, "id")
	}
	if task.Kind == KindVideo {
		if len(response.VideoResult) > 0 {
			result["video_result"] = response.VideoResult
		}
	} else {
		results := response.ImageResult
		if len(results) == 0 {
			results = response.Data
		}
		if len(results) > 0 {
			result["image_result"] = results
			result["data"] = results
		}
	}
	if task.Status == StatusFailed || task.Status == StatusCancelled {
		message := task.LastErrorMessage
		if message == "" {
			message = "Upstream media task did not complete."
		}
		code := task.LastErrorCode
		if code == "" {
			code = "upstream_error"
		}
		result["error"] = APIError{Message: message, Type: "upstream_error", Code: code}
	}
	return result
}

// ZhipuSyncImageResponse normalizes a sync image response and requires a URL.
func ZhipuSyncImageResponse(body []byte) ([]byte, error) {
	parsed, err := ParseZhipuResponse(body)
	if err != nil {
		return nil, err
	}
	results := parsed.Data
	if len(results) == 0 {
		results = parsed.ImageResult
	}
	hasPayload := false
	for _, result := range results {
		if result.URL != "" || result.B64JSON != "" {
			hasPayload = true
			break
		}
	}
	if !hasPayload {
		return nil, fmt.Errorf("upstream image response contains no URL or base64 payload")
	}
	response := map[string]any{"created": time.Now().Unix(), "data": results}
	if parsed.Model != "" {
		response["model"] = parsed.Model
	}
	return json.Marshal(response)
}
