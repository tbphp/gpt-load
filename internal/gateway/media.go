package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"
	"golang.org/x/net/proxy"

	"gpt-load/internal/channel"
	"gpt-load/internal/dialect"
	"gpt-load/internal/execution"
	"gpt-load/internal/health"
	"gpt-load/internal/media"
	"gpt-load/internal/outboundproxy"
	"gpt-load/internal/protocol"
	"gpt-load/internal/scheduler"
	"gpt-load/internal/state"
	"gpt-load/internal/telemetry"
	"gpt-load/internal/usage"
)

const (
	mediaUpstreamMessageKey   = "gpt-load.media.upstream-message"
	mediaTaskIDKey            = "gpt-load.media.task-id"
	mediaErrorEnvelopeKey     = "gpt-load.media.error-envelope"
	openAIVideoDefaultBaseURL = "https://api.openai.com/v1"
	maxVideoErrorBodyBytes    = int64(64 << 10)
)

type mediaRequestState struct {
	store       media.TaskStore
	protocol    protocol.Protocol
	operation   execution.Operation
	kind        string
	clientModel string
	accessKeyID uint
	create      *media.MediaCreate
	createHash  string
	reservation media.Task
	reserved    bool
	finalized   bool
	binding     media.Task
	pinned      bool
	delete      bool
	handled     bool
	sync        bool
}

func (handler *Handler) prepareMediaRequest(
	ginContext *gin.Context,
	snapshot *state.ConfigSnapshot,
	accessKey state.AccessKeyView,
	selectedProtocol protocol.Protocol,
	metadata dialect.RequestMetadata,
	parsed *dialect.ParsedRequest,
	recorder *requestRecorder,
) (*mediaRequestState, dialect.RequestMetadata, *dialect.ParsedRequest, *reason) {
	if len(accessKey.Filters.Protocols) > 0 {
		if _, allowed := accessKey.Filters.Protocols[selectedProtocol]; !allowed {
			if selectedProtocol == protocol.ZhipuMedia {
				return nil, metadata, parsed, &reasonZhipuProtocolNotAllowed
			}
			return nil, metadata, parsed, &reasonVideoProtocolNotAllowed
		}
	}
	if handler == nil || handler.mediaTasks == nil {
		return nil, metadata, parsed, &reasonMediaInternal
	}
	if metadata.Operation == execution.OperationZhipuImagesGenerate {
		create := metadata.MediaCreate
		if create == nil {
			return nil, metadata, parsed, &reasonMediaInternal
		}
		state := &mediaRequestState{
			store: handler.mediaTasks, protocol: selectedProtocol, operation: metadata.Operation,
			kind: create.Kind, clientModel: create.ClientModel, create: create, sync: true,
		}
		if recorder != nil {
			recorder.setClientModel(create.ClientModel)
			recorder.setOperation(metadata.Operation)
			recorder.setUsageApplicable(false)
			recorder.setMediaMetadata(syncMediaObservation(state))
		}
		return state, metadata, parsed, nil
	}
	if metadata.Operation == execution.OperationVideosCreate ||
		metadata.Operation == execution.OperationZhipuImagesCreateAsync ||
		metadata.Operation == execution.OperationZhipuVideosCreate {
		create := metadata.MediaCreate
		if create == nil {
			return nil, metadata, parsed, &reasonMediaInternal
		}
		hash, err := media.IdempotencyHash(accessKey.ID, ginContext.GetHeader("Idempotency-Key"))
		if err != nil {
			return nil, metadata, parsed, invalidMediaRequestReason(err)
		}
		state := &mediaRequestState{
			store: handler.mediaTasks, protocol: selectedProtocol, operation: metadata.Operation,
			kind: create.Kind, clientModel: create.ClientModel, accessKeyID: accessKey.ID,
			create: create, createHash: hash,
		}
		idPrefix := "media"
		if selectedProtocol == protocol.OpenAIVideos {
			idPrefix = "video"
		} else if selectedProtocol == protocol.ZhipuMedia {
			idPrefix = "zhipu"
		}
		task, err := state.store.NewTask(accessKey.ID, taskParams(create, idPrefix), hash)
		if err != nil {
			return nil, metadata, parsed, &reasonMediaInternal
		}
		if recorder != nil {
			recorder.setClientModel(create.ClientModel)
			recorder.setOperation(metadata.Operation)
			recorder.setUsageApplicable(false)
			recorder.setMediaMetadata(observationForCreateTask(task))
		}
		task, created, err := state.store.Reserve(ginContext.Request.Context(), task)
		if err != nil {
			return nil, metadata, parsed, &reasonMediaInternal
		}
		if !created {
			if recorder != nil {
				recorder.setMediaMetadata(observationForExistingTask(task, "create"))
				recorder.completeLocalSuccess(http.StatusOK, task.UpstreamModel)
			}
			handler.writeMediaJSON(ginContext, http.StatusOK, handler.mediaCreateResponse(task))
			state.handled = true
			return state, metadata, parsed, nil
		}
		state.reservation, state.reserved = task, true
		return state, metadata, parsed, nil
	}

	if metadata.MediaTaskID == "" {
		return nil, metadata, parsed, &reasonMediaTaskNotFound
	}
	ginContext.Set(mediaTaskIDKey, metadata.MediaTaskID)
	task, err := handler.mediaTasks.Get(ginContext.Request.Context(), accessKey.ID, metadata.MediaTaskID)
	if errors.Is(err, media.ErrNotFound) {
		return nil, metadata, parsed, &reasonMediaTaskNotFound
	}
	if err != nil {
		return nil, metadata, parsed, &reasonMediaInternal
	}
	if len(accessKey.Filters.Models) > 0 {
		if _, allowed := accessKey.Filters.Models[task.ClientModel]; !allowed {
			return nil, metadata, parsed, &reasonMediaTaskNotFound
		}
	}
	if metadata.Operation == execution.OperationZhipuMediaCancel {
		if recorder != nil {
			recorder.setClientModel(task.ClientModel)
			recorder.setOperation(metadata.Operation)
			recorder.setUsageApplicable(false)
			recorder.setMediaMetadata(observationForExistingTask(task, "cancel"))
		}
		return nil, metadata, parsed, &reasonMediaCancelNotSupported
	}
	group, ok := snapshot.Groups[task.GroupID]
	if !ok {
		return nil, metadata, parsed, &reasonMediaCredentialUnavailable
	}
	ref, ok := handler.registry.CredentialRef(task.CredentialID)
	if !ok || ref.GroupID != task.GroupID || !handler.mediaCredentialAvailable(task.GroupID, task.CredentialID) {
		return nil, metadata, parsed, &reasonMediaCredentialUnavailable
	}
	if task.UpstreamTaskID == "" {
		return nil, metadata, parsed, &reasonMediaCredentialUnavailable
	}
	if _, supported := group.ResolvedTarget.Mode(selectedProtocol, metadata.Operation); !supported {
		switch metadata.Operation {
		case execution.OperationVideosDelete, execution.OperationZhipuMediaCancel:
			return nil, metadata, parsed, &reasonMediaCancelNotSupported
		case execution.OperationVideosContent:
			return nil, metadata, parsed, &reasonMediaContentNotSupported
		default:
			return nil, metadata, parsed, &reasonMediaCredentialUnavailable
		}
	}
	state := &mediaRequestState{
		store: handler.mediaTasks, protocol: selectedProtocol, operation: metadata.Operation,
		kind: task.Kind, clientModel: task.ClientModel, binding: task, pinned: true,
	}
	phase := mediaPhase(metadata.Operation)
	if recorder != nil {
		recorder.setClientModel(task.ClientModel)
		recorder.setOperation(metadata.Operation)
		recorder.setUsageApplicable(false)
		recorder.setMediaMetadata(observationForExistingTask(task, phase))
	}
	switch metadata.Operation {
	case execution.OperationVideosContent:
		state.handled = true
		updateDebugHeaders(ginContext.Writer.Header(), group.Name, 1)
		if failure := handler.handleOpenAIVideoContent(ginContext, snapshot, accessKey, group, ref, task, recorder); failure != nil {
			return nil, metadata, parsed, failure
		}
		return state, metadata, parsed, nil
	case execution.OperationVideosDelete, execution.OperationZhipuMediaCancel:
		state.delete = true
	case execution.OperationVideosRetrieve, execution.OperationZhipuMediaRetrieve:
	default:
		return nil, metadata, parsed, &reasonMediaCredentialUnavailable
	}
	parsed.Method = ginContext.Request.Method
	if selectedProtocol == protocol.ZhipuMedia {
		parsed.Path = "/v1/async-result/" + url.PathEscape(task.UpstreamTaskID)
	} else {
		parsed.Path = "/v1/videos/" + url.PathEscape(task.UpstreamTaskID)
	}
	parsed.RawQuery = ""
	parsed.Body = nil
	if parsed.Header != nil {
		parsed.Header.Del("Content-Type")
		parsed.Header.Del("Content-Length")
	}
	return state, metadata, parsed, nil
}

func taskParams(create *media.MediaCreate, idPrefix string) media.NewTaskParams {
	if create == nil {
		return media.NewTaskParams{IDPrefix: idPrefix}
	}
	return media.NewTaskParams{
		IDPrefix: idPrefix, Protocol: create.Protocol, Operation: create.Operation, Kind: create.Kind,
		ClientModel: create.ClientModel, Mode: create.Mode, Seconds: create.Seconds,
		Size: create.Size, AspectRatio: create.AspectRatio, Quality: create.Quality,
		FPS: create.FPS, Duration: create.Duration, WithAudio: create.WithAudio, ImageCount: create.ImageCount,
	}
}

func mediaIDPrefix(value protocol.Protocol) string {
	switch value {
	case protocol.OpenAIVideos:
		return "video"
	case protocol.ZhipuMedia:
		return "zhipu"
	default:
		return "media"
	}
}

func (handler *Handler) mediaCreateResponse(task media.Task) any {
	if task.Protocol == media.ProtocolZhipuMedia {
		return media.ZhipuCreateResponse(task)
	}
	return media.OpenAIVideoCreateResponse(task)
}

func mediaWriteContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
}

func invalidMediaRequestReason(err error) *reason {
	value := reason{Status: http.StatusBadRequest, Code: "invalid_request_error", Message: "Invalid media request."}
	var validation *media.ValidationError
	if errors.As(err, &validation) {
		value.Message = validation.Message
	}
	return &value
}

func (handler *Handler) mediaCredentialAvailable(groupID, credentialID uint) bool {
	if handler == nil || handler.registry == nil {
		return false
	}
	candidates := handler.registry.CollectCredentialCandidates([]uint{groupID}, nil, handler.now())
	for _, candidate := range candidates {
		if candidate.ID == credentialID {
			return true
		}
	}
	return false
}

func (handler *Handler) cleanupMediaReservation(state *mediaRequestState) {
	if state == nil || handler == nil || handler.mediaTasks == nil ||
		!state.reserved || state.finalized || state.reservation.ID == "" {
		return
	}
	if err := handler.mediaTasks.MarkFailed(context.Background(), state.reservation.ID, "create_failed", "The media task was not created."); err != nil {
		utilsLogMedia(handler.logger, logrus.WarnLevel, map[string]any{
			"event": "media_task_reservation_cleanup_failed", "media_task_id": state.reservation.ID, "error": err.Error(),
		}, "Failed to mark an incomplete media task reservation as failed")
	}
}

func (handler *Handler) finalizeMediaCreate(
	ctx context.Context,
	state *mediaRequestState,
	selection scheduler.Selection,
	ref state.CredentialRef,
	result UpstreamResult,
	clientModel string,
	upstreamModel string,
) (UpstreamResult, *reason) {
	if state == nil || state.store == nil {
		return result, &reasonMediaInternal
	}
	if state.sync {
		return handler.finalizeZhipuSyncImage(state, result)
	}
	var (
		upstreamTaskID string
		alias          string
		videoID        string
		resultURL      string
		errorCode      string
		errorMessage   string
		reportedModel  string
		status         string
		progress       *int
	)
	switch state.protocol {
	case protocol.ZhipuMedia:
		response, err := media.ParseZhipuResponse(result.Body)
		if err != nil {
			return result, &reasonUpstreamProtocol
		}
		upstreamTaskID = media.SelectZhipuTaskID(response)
		alias = response.RequestID
		reportedModel = response.Model
		status = response.TaskStatus
		resultURL, errorCode, errorMessage = media.ZhipuObservationResult(media.Task{Kind: state.kind}, response)
	default:
		response, err := media.ParseOpenAIVideoResponse(result.Body)
		if err != nil {
			return result, &reasonUpstreamProtocol
		}
		upstreamTaskID = media.SelectOpenAIVideoTaskID(string(selection.ChannelID), response)
		alias, videoID = response.TaskID, response.VideoID
		reportedModel, status, progress = response.Model, response.Status, response.Progress
		resultURL, errorCode, errorMessage = response.URL, response.ErrorCode, response.ErrorMessage
	}
	if upstreamTaskID == "" {
		return result, &reasonUpstreamProtocol
	}
	task := state.reservation
	if task.ID == "" {
		if state.accessKeyID == 0 {
			return result, &reasonMediaInternal
		}
		created, err := state.store.NewTask(state.accessKeyID, taskParams(state.create, mediaIDPrefix(state.protocol)), state.createHash)
		if err != nil {
			return result, &reasonMediaInternal
		}
		task, _, err = state.store.Reserve(ctx, created)
		if err != nil {
			return result, &reasonMediaInternal
		}
		state.reservation = task
	}
	task.GroupID = selection.GroupID
	task.CredentialID = ref.ID
	task.Protocol = string(state.protocol)
	task.Operation = string(state.operation)
	task.Kind = state.kind
	task.UpstreamTaskID = upstreamTaskID
	task.UpstreamTaskAlias = alias
	task.UpstreamVideoID = videoID
	task.UpstreamModel = upstreamModel
	task.ClientModel = clientModel
	task.Status = media.NormalizeStatus(status)
	if strings.TrimSpace(status) == "" {
		if state.protocol == protocol.ZhipuMedia {
			task.Status = media.StatusInProgress
		} else {
			task.Status = media.StatusQueued
		}
	}
	if progress != nil {
		task.Progress = media.ClampProgress(*progress)
	}
	if task.Status == media.StatusCompleted {
		task.Progress = 100
	}
	if resultURL != "" {
		task.ResultURL = resultURL
	}
	task.LastErrorCode = errorCode
	task.LastErrorMessage = errorMessage
	if err := state.store.UpdateCreateResult(ctx, task); err != nil {
		_ = state.store.MarkFailed(ctx, task.ID, "storage_error", "The media task binding could not be persisted.")
		return result, &reasonMediaInternal
	}
	state.reservation = task
	state.finalized = true
	body, err := json.Marshal(handler.mediaCreateResponse(task))
	if err != nil {
		return result, &reasonMediaInternal
	}
	result.StatusCode = http.StatusOK
	result.Body = body
	result.ClassificationBody = body
	result.Header = mediaJSONHeaders(result.Header)
	result.UpstreamReportedModel = reportedModel
	return result, nil
}

func (handler *Handler) finalizeZhipuSyncImage(state *mediaRequestState, result UpstreamResult) (UpstreamResult, *reason) {
	body, err := media.ZhipuSyncImageResponse(result.Body)
	if err != nil {
		return result, &reasonUpstreamProtocol
	}
	state.finalized = true
	result.StatusCode = http.StatusOK
	result.Body = body
	result.ClassificationBody = body
	result.Header = mediaJSONHeaders(result.Header)
	return result, nil
}

func (handler *Handler) finalizeMediaResource(
	ctx context.Context,
	state *mediaRequestState,
	result UpstreamResult,
) (UpstreamResult, *reason) {
	if result.StatusCode == http.StatusNotFound {
		return result, &reasonMediaTaskNotFound
	}
	if state == nil || state.store == nil {
		return result, &reasonMediaInternal
	}
	if result.StatusCode < http.StatusOK || result.StatusCode >= http.StatusMultipleChoices {
		return result, nil
	}
	task := state.binding
	var body []byte
	var err error
	if state.delete {
		task.Status = media.StatusCancelled
		if state.protocol == protocol.ZhipuMedia {
			body, err = json.Marshal(map[string]any{
				"id": task.ID, "object": "media_task", "cancelled": true, "task_status": media.StatusCancelled,
			})
		} else {
			body, err = json.Marshal(struct {
				ID      string `json:"id"`
				Object  string `json:"object"`
				Deleted bool   `json:"deleted"`
			}{ID: task.ID, Object: media.ObjectVideo, Deleted: true})
		}
	} else if state.protocol == protocol.ZhipuMedia {
		if len(bytes.TrimSpace(result.Body)) > 0 {
			response, parseErr := media.ParseZhipuResponse(result.Body)
			if parseErr != nil {
				return result, &reasonUpstreamProtocol
			}
			task = media.ApplyZhipuObservation(task, response)
			body, err = json.Marshal(media.ZhipuTaskResponse(task, response))
		} else {
			body, err = json.Marshal(media.ZhipuTaskResponse(task, media.ZhipuUpstream{}))
		}
	} else {
		if len(bytes.TrimSpace(result.Body)) > 0 {
			response, parseErr := media.ParseOpenAIVideoResponse(result.Body)
			if parseErr != nil {
				return result, &reasonUpstreamProtocol
			}
			task = media.ApplyUpstreamObservation(task, response)
		}
		body, err = json.Marshal(media.OpenAIVideoTaskResponse(task, handler.now(), true))
	}
	if err != nil {
		return result, &reasonMediaInternal
	}
	if err := state.store.UpdateObservation(ctx, task); err != nil {
		return result, &reasonMediaInternal
	}
	state.binding = task
	if state.delete {
		result.StatusCode = http.StatusOK
	}
	result.Body = body
	result.ClassificationBody = body
	result.Header = mediaJSONHeaders(result.Header)
	return result, nil
}

func mediaJSONHeaders(source http.Header) http.Header {
	headers := source.Clone()
	if headers == nil {
		headers = make(http.Header)
	}
	headers.Del("Content-Encoding")
	headers.Del("Content-Length")
	headers.Set("Content-Type", "application/json; charset=utf-8")
	return headers
}

func (handler *Handler) writeMediaJSON(ginContext *gin.Context, status int, value any) {
	body, err := json.Marshal(value)
	if err != nil {
		_ = handler.writeMediaError(ginContext, http.StatusInternalServerError, "internal_error", "server_error", "", "Media response could not be encoded.")
		return
	}
	if err := handler.writeBufferedResponse(ginContext, status, http.Header{
		"Content-Type": {"application/json; charset=utf-8"},
	}, body); err != nil {
		utilsLogMedia(handler.logger, logrus.WarnLevel, map[string]any{
			"event": "media_response_write_failed", "error": err.Error(),
		}, "Failed to write media response")
	}
}

func (handler *Handler) rememberMediaUpstreamError(ginContext *gin.Context, result UpstreamResult) {
	if result.ExecutionError != nil && result.ExecutionError.Summary != "" {
		ginContext.Set(mediaUpstreamMessageKey, result.ExecutionError.Summary)
	}
	if result.UpstreamRequestID != "" {
		ginContext.Header("X-Request-Id", result.UpstreamRequestID)
	}
	if retryAfter := result.Header.Get("Retry-After"); retryAfter != "" {
		ginContext.Header("Retry-After", retryAfter)
	}
}

func (handler *Handler) completeMediaUpstreamFailure(
	ginContext *gin.Context,
	recorder *requestRecorder,
	operation execution.Operation,
	result UpstreamResult,
) {
	handler.rememberMediaUpstreamError(ginContext, result)
	value := mediaUpstreamFailureReason(operation, result.StatusCode)
	handler.completeReason(ginContext, recorder, value)
}

func mediaUpstreamFailureReason(operation execution.Operation, statusCode int) reason {
	switch statusCode {
	case http.StatusBadRequest:
		return reasonInvalidProtocolRequest
	case http.StatusNotFound:
		if isMediaCreateOperation(operation) {
			return reasonMediaModelNotFound
		}
		return reasonMediaTaskNotFound
	case http.StatusTooManyRequests:
		return reasonUpstreamRateLimited
	case http.StatusNotImplemented, http.StatusMethodNotAllowed:
		if isMediaCancelOperation(operation) {
			return reasonMediaCancelNotSupported
		}
		return reasonMediaContentNotSupported
	default:
		return reasonUpstreamProtocol
	}
}

func (handler *Handler) handleOpenAIVideoContent(
	ginContext *gin.Context,
	snapshot *state.ConfigSnapshot,
	accessKey state.AccessKeyView,
	group state.GroupView,
	ref state.CredentialRef,
	task media.Task,
	recorder *requestRecorder,
) *reason {
	started := handler.now()
	upstreamModel := task.UpstreamModel
	selection := scheduler.Selection{
		CredentialID: ref.ID, GroupID: group.ID, ChannelID: group.ChannelID,
		Group: group, RouteMode: channel.RouteNative, UpstreamModelID: &upstreamModel,
	}
	if recorder != nil {
		recorder.freezeNextAttemptPricing(handler.freezeAttemptPricing(
			selection,
			dialect.RequestMetadata{Operation: execution.OperationVideosContent},
			false,
			accessKey.PriceMultiplier,
		))
	}
	encrypted, active := handler.registry.ActiveEncryptedCredentialDataIfMatch(ref)
	if !active {
		return &reasonMediaCredentialUnavailable
	}
	decrypted, err := handler.encryption.Decrypt(encrypted)
	if err != nil {
		return &reasonMediaCredentialUnavailable
	}
	credential, err := normalizeChannelCredential(
		handler.channels, handler.subscriptions, group.ChannelID, group.ConnectionType, decrypted,
	)
	if err != nil {
		return &reasonMediaCredentialUnavailable
	}
	effectiveProxy, _, err := resolveAttemptProxy(handler.encryption, group.Proxy, ref)
	if err != nil {
		return &reasonMediaCredentialUnavailable
	}
	client, err := newMediaHTTPClient(effectiveProxy, group.Timeouts, false)
	if err != nil {
		return &reasonMediaCredentialUnavailable
	}
	defer client.CloseIdleConnections()
	ctx := ginContext.Request.Context()

	sourceURL, authenticated, task, failure := handler.openAIVideoContentSource(ctx, client, group, task, credential.apiKey)
	if failure != nil {
		handler.recordOpenAIVideoContentAttempt(recorder, group, ref, task, 0, "", started, failure)
		return failure
	}
	mediaClient := client
	if !authenticated {
		blockPrivate := !handler.allowPrivateVideoURLs
		if blockPrivate {
			if err := validatePublicVideoURL(ctx, sourceURL); err != nil {
				value := reasonUpstreamConnect
				handler.recordOpenAIVideoContentAttempt(recorder, group, ref, task, 0, "", started, &value)
				return &value
			}
		}
		mediaClient, err = newMediaHTTPClient(effectiveProxy, group.Timeouts, blockPrivate)
		if err != nil {
			return &reasonMediaCredentialUnavailable
		}
		defer mediaClient.CloseIdleConnections()
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, sourceURL, nil)
	if err != nil {
		value := reasonUpstreamProtocol
		handler.recordOpenAIVideoContentAttempt(recorder, group, ref, task, 0, "", started, &value)
		return &value
	}
	if authenticated {
		request.Header.Set("Authorization", "Bearer "+credential.apiKey)
		applyVideoHeaderRules(request.Header, group.HeaderRules, credential.apiKey)
	}
	copyVideoRangeHeaders(request.Header, ginContext.Request.Header)
	request.Header.Set("Accept-Encoding", "identity")
	response, err := mediaClient.Do(request)
	if err != nil {
		value := reasonUpstreamConnect
		if isTimeoutError(err) {
			value = reasonUpstreamTimeout
		}
		handler.recordOpenAIVideoContentAttempt(recorder, group, ref, task, 0, "", started, &value)
		return &value
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound {
		handler.recordOpenAIVideoContentAttempt(recorder, group, ref, task, response.StatusCode, response.Header.Get("X-Request-Id"), started, &reasonMediaTaskNotFound)
		return &reasonMediaTaskNotFound
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		body, _ := io.ReadAll(io.LimitReader(response.Body, maxVideoErrorBodyBytes))
		message := videoUpstreamErrorMessage(body)
		ginContext.Set(mediaUpstreamMessageKey, message)
		value := reasonUpstreamProtocol
		if response.StatusCode == http.StatusTooManyRequests {
			value = reasonUpstreamRateLimited
		}
		handler.recordOpenAIVideoContentAttempt(recorder, group, ref, task, response.StatusCode, response.Header.Get("X-Request-Id"), started, &value)
		return &value
	}

	copyVideoContentHeaders(ginContext.Writer.Header(), response.Header)
	ginContext.Status(response.StatusCode)
	ginContext.Writer.WriteHeaderNow()
	buffer := make([]byte, 64<<10)
	if _, err := io.CopyBuffer(ginContext.Writer, response.Body, buffer); err != nil {
		utilsLogMedia(handler.logger, logrus.WarnLevel, map[string]any{
			"event": "video_content_stream_failed", "video_task_id": task.ID, "error": err.Error(),
		}, "Video content stream ended early")
	}
	if flusher, ok := ginContext.Writer.(http.Flusher); ok {
		flusher.Flush()
	}
	handler.recordOpenAIVideoContentAttempt(recorder, group, ref, task, response.StatusCode, response.Header.Get("X-Request-Id"), started, nil)
	if recorder != nil {
		recorder.updateMediaMetadata(task.Status, task.UpstreamTaskID)
	}
	return nil
}

func (handler *Handler) openAIVideoContentSource(
	ctx context.Context,
	client *http.Client,
	group state.GroupView,
	task media.Task,
	apiKey string,
) (sourceURL string, authenticated bool, updated media.Task, failure *reason) {
	if group.ChannelID == channel.Agnes {
		if task.Status == media.StatusCompleted && validVideoMediaURL(task.ResultURL) {
			return task.ResultURL, false, task, nil
		}
		retrieveURL, err := agnesVideoRetrieveURL(group, task)
		if err != nil {
			value := reasonMediaContentNotSupported
			return "", false, task, &value
		}
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, retrieveURL, nil)
		if err != nil {
			value := reasonUpstreamProtocol
			return "", false, task, &value
		}
		request.Header.Set("Authorization", "Bearer "+apiKey)
		applyVideoHeaderRules(request.Header, group.HeaderRules, apiKey)
		request.Header.Set("Accept", "application/json")
		response, err := client.Do(request)
		if err != nil {
			value := reasonUpstreamConnect
			if isTimeoutError(err) {
				value = reasonUpstreamTimeout
			}
			return "", false, task, &value
		}
		defer response.Body.Close()
		body, _ := io.ReadAll(io.LimitReader(response.Body, maxVideoErrorBodyBytes))
		if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
			value := reasonUpstreamProtocol
			return "", false, task, &value
		}
		parsed, err := media.ParseOpenAIVideoResponse(body)
		if err != nil || !validVideoMediaURL(parsed.URL) {
			value := reasonUpstreamProtocol
			return "", false, task, &value
		}
		task = media.ApplyUpstreamObservation(task, parsed)
		if handler.mediaTasks != nil {
			task.Status = media.StatusCompleted
			task.Progress = 100
			task.ResultURL = parsed.URL
			_ = handler.mediaTasks.UpdateObservation(ctx, task)
		}
		return parsed.URL, false, task, nil
	}
	contentURL, err := openAIVideoContentURL(group, task)
	if err != nil {
		value := reasonMediaContentNotSupported
		return "", false, task, &value
	}
	return contentURL, true, task, nil
}

func (handler *Handler) recordOpenAIVideoContentAttempt(
	recorder *requestRecorder,
	group state.GroupView,
	ref state.CredentialRef,
	task media.Task,
	statusCode int,
	requestID string,
	started time.Time,
	failure *reason,
) {
	if recorder == nil {
		return
	}
	upstreamModel := task.UpstreamModel
	selection := scheduler.Selection{
		CredentialID: ref.ID, GroupID: group.ID, ChannelID: group.ChannelID,
		Group: group, RouteMode: channel.RouteNative, UpstreamModelID: &upstreamModel,
	}
	dispatch := execution.DispatchNotSent
	if statusCode > 0 {
		dispatch = execution.DispatchMaybeSent
	}
	result := UpstreamResult{
		StatusCode: statusCode, DispatchState: dispatch, ResponseStarted: statusCode > 0,
		UpstreamProtocol: protocol.OpenAIVideos, UpstreamRequestID: requestID,
		Usage: usage.Result{State: usage.StateNotApplicable},
	}
	decision := health.Decision{
		Category: health.FailureCategoryOK, Origin: execution.ErrorOriginUpstream,
		Scope: execution.ErrorScopeRequest,
	}
	if failure != nil {
		decision.Category = health.FailureCategoryUpstreamHostError
		if failure.Status == http.StatusNotFound {
			decision.Category = health.FailureCategoryModelUnavailable
		}
		result.ErrorSummary = failure.Message
	}
	completed := handler.now()
	index := recorder.recordAttempt(selection, nil, result, decision, started, completed)
	if failure != nil {
		recorder.completeReason(*failure)
		return
	}
	recorder.completeResponse(result, decision, upstreamModel, index)
}

func newMediaHTTPClient(
	effective outboundproxy.Effective,
	timeouts state.TimeoutConfig,
	blockPrivate bool,
) (*http.Client, error) {
	normalized, err := outboundproxy.NormalizeEffective(effective)
	if err != nil {
		return nil, fmt.Errorf("normalize video proxy: %w", err)
	}
	dialer := &net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second}
	transport := &http.Transport{
		DialContext:           dialer.DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          20,
		MaxIdleConnsPerHost:   4,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   15 * time.Second,
		ExpectContinueTimeout: time.Second,
		DisableCompression:    true,
	}
	if timeouts.FirstByte > 0 {
		transport.ResponseHeaderTimeout = timeouts.FirstByte
	}
	switch normalized.Config.Mode {
	case outboundproxy.ModeDirect:
		transport.Proxy = nil
	case outboundproxy.ModeEnvironment:
		transport.Proxy = http.ProxyFromEnvironment
	case outboundproxy.ModeCustom:
		proxyURL, parseErr := url.Parse(normalized.Config.URL)
		if parseErr != nil || proxyURL.Hostname() == "" {
			return nil, fmt.Errorf("invalid video proxy endpoint")
		}
		if proxyURL.Scheme == "socks5" {
			dialer, dialErr := proxy.FromURL(proxyURL, &net.Dialer{Timeout: 15 * time.Second})
			contextDialer, ok := dialer.(proxy.ContextDialer)
			if dialErr != nil || !ok {
				return nil, fmt.Errorf("invalid video socks proxy")
			}
			transport.DialContext = contextDialer.DialContext
			transport.Proxy = nil
		} else {
			transport.Proxy = http.ProxyURL(proxyURL)
		}
	default:
		return nil, fmt.Errorf("invalid video proxy mode")
	}
	timeout := timeouts.Request
	if timeout <= 0 {
		timeout = 30 * time.Minute
	}
	return &http.Client{
		Transport: transport, Timeout: timeout,
		CheckRedirect: func(request *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return errors.New("too many video redirects")
			}
			if request.URL.Scheme != "http" && request.URL.Scheme != "https" {
				return errors.New("invalid video redirect scheme")
			}
			if blockPrivate {
				return validatePublicVideoURL(request.Context(), request.URL.String())
			}
			if len(via) > 0 && !strings.EqualFold(request.URL.Host, via[0].URL.Host) {
				return errors.New("video content redirect changed host")
			}
			return nil
		},
	}, nil
}

func validatePublicVideoURL(ctx context.Context, rawURL string) error {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Hostname() == "" || parsed.User != nil ||
		(parsed.Scheme != "http" && parsed.Scheme != "https") {
		return errors.New("video result URL is not a public http or https URL")
	}
	addresses, err := net.DefaultResolver.LookupIPAddr(ctx, parsed.Hostname())
	if err != nil || len(addresses) == 0 {
		return errors.New("video result URL could not be resolved")
	}
	for _, address := range addresses {
		if blockedVideoIP(address.IP) {
			return errors.New("video result URL resolves to a private address")
		}
	}
	return nil
}

func blockedVideoIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	if ip4 := ip.To4(); ip4 != nil {
		if ip4[0] == 100 && ip4[1] >= 64 && ip4[1] <= 127 {
			return true // RFC 6598 carrier-grade NAT
		}
		ip = ip4
	}
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsUnspecified() || ip.IsMulticast()
}

func applyVideoHeaderRules(headers http.Header, rules state.HeaderRules, apiKey string) {
	for name, value := range rules.Set {
		headers.Set(name, strings.ReplaceAll(value, "${API_KEY}", apiKey))
	}
	for _, name := range rules.Remove {
		headers.Del(name)
	}
}

func copyVideoRangeHeaders(target, source http.Header) {
	for _, name := range []string{"Range", "If-Range", "If-None-Match", "If-Modified-Since"} {
		if value := source.Get(name); value != "" {
			target.Set(name, value)
		}
	}
}

func copyVideoContentHeaders(target, source http.Header) {
	for _, name := range []string{
		"Content-Type", "Content-Length", "Content-Range", "Accept-Ranges",
		"ETag", "Last-Modified", "Cache-Control", "X-Request-Id",
	} {
		if value := source.Get(name); value != "" {
			target.Set(name, value)
		}
	}
}

func validVideoMediaURL(value string) bool {
	parsed, err := url.Parse(value)
	return err == nil && parsed.Host != "" && parsed.User == nil &&
		(parsed.Scheme == "http" || parsed.Scheme == "https")
}

func videoUpstreamErrorMessage(body []byte) string {
	var envelope struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
		Message string `json:"message"`
	}
	if json.Unmarshal(body, &envelope) == nil {
		if envelope.Error.Message != "" {
			return envelope.Error.Message
		}
		if envelope.Message != "" {
			return envelope.Message
		}
	}
	return "Upstream video request failed."
}

func openAIVideoContentURL(group state.GroupView, task media.Task) (string, error) {
	resourceID := url.PathEscape(task.UpstreamTaskID)
	switch group.ResolvedTarget.ProviderKind {
	case channel.ProviderOpenAI:
		if base, err := groupBaseURL(group); err == nil {
			return base + "/videos/" + resourceID + "/content", nil
		}
		return openAIVideoDefaultBaseURL + "/videos/" + resourceID + "/content", nil
	case channel.ProviderOpenAICompatible:
		base, err := groupBaseURL(group)
		if err != nil {
			return "", err
		}
		return base + "/videos/" + resourceID + "/content", nil
	case channel.ProviderMultiProtocolGateway:
		base, err := groupBaseURL(group)
		if err != nil {
			return "", err
		}
		return base + "/v1/videos/" + resourceID + "/content", nil
	default:
		return "", fmt.Errorf("video content is not supported for this channel")
	}
}

func agnesVideoRetrieveURL(group state.GroupView, task media.Task) (string, error) {
	base, err := groupBaseURL(group)
	if err != nil {
		return "", err
	}
	parsed, err := url.Parse(base)
	if err != nil || parsed.Hostname() == "" {
		return "", fmt.Errorf("invalid Agnes base URL")
	}
	path := strings.TrimRight(parsed.Path, "/")
	path = strings.TrimSuffix(path, "/v1")
	parsed.Path = path
	parsed.RawPath = ""
	query := url.Values{}
	query.Set("video_id", task.UpstreamTaskID)
	if task.UpstreamModel != "" {
		query.Set("model_name", task.UpstreamModel)
	}
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}

func groupBaseURL(group state.GroupView) (string, error) {
	var target struct {
		BaseURL string `json:"base_url"`
	}
	if len(group.ResolvedTarget.TargetConfig) == 0 {
		return "", fmt.Errorf("channel base URL is required")
	}
	if err := json.Unmarshal(group.ResolvedTarget.TargetConfig, &target); err != nil {
		return "", fmt.Errorf("decode channel base URL: %w", err)
	}
	base := strings.TrimRight(strings.TrimSpace(target.BaseURL), "/")
	if base == "" {
		return "", fmt.Errorf("channel base URL is required")
	}
	return base, nil
}

func utilsLogMedia(logger *logrus.Logger, level logrus.Level, fields map[string]any, message string) {
	if logger == nil {
		return
	}
	entry := logger.WithFields(logrus.Fields{})
	for key, value := range fields {
		entry = entry.WithField(key, value)
	}
	entry.Log(level, message)
}

func videoUnusualActivity(body []byte) bool {
	text := strings.ToLower(string(body))
	return strings.Contains(text, "unusual activity") ||
		strings.Contains(text, "restricted") ||
		strings.Contains(text, "quota")
}

func observationForCreateTask(task media.Task) *telemetry.MediaObservation {
	return &telemetry.MediaObservation{
		GatewayTaskID: task.ID, Protocol: task.Protocol, Operation: task.Operation, Kind: task.Kind,
		Model: task.ClientModel, Mode: task.Mode, Seconds: task.Seconds, Size: task.Size,
		AspectRatio: task.AspectRatio, Quality: task.Quality, FPS: task.FPS, Duration: task.Duration,
		WithAudio: task.WithAudio, ImageCount: task.ImageCount, Phase: "create", Status: task.Status,
	}
}

func syncMediaObservation(state *mediaRequestState) *telemetry.MediaObservation {
	observation := &telemetry.MediaObservation{
		Protocol: string(state.protocol), Operation: string(state.operation), Kind: state.kind,
		Model: state.clientModel, Phase: "create", Status: media.StatusInProgress,
	}
	if state.create != nil {
		observation.Size = state.create.Size
		observation.Quality = state.create.Quality
		observation.ImageCount = state.create.ImageCount
	}
	return observation
}

func observationForExistingTask(task media.Task, phase string) *telemetry.MediaObservation {
	return &telemetry.MediaObservation{
		GatewayTaskID: task.ID, UpstreamTaskID: task.UpstreamTaskID,
		Protocol: task.Protocol, Operation: task.Operation, Kind: task.Kind,
		Model: task.ClientModel, Mode: task.Mode, Seconds: task.Seconds,
		Size: task.Size, AspectRatio: task.AspectRatio, Quality: task.Quality,
		FPS: task.FPS, Duration: task.Duration, WithAudio: task.WithAudio,
		ImageCount: task.ImageCount, Phase: phase, Status: task.Status,
	}
}

func mediaPhase(operation execution.Operation) string {
	switch operation {
	case execution.OperationVideosCreate, execution.OperationZhipuImagesGenerate,
		execution.OperationZhipuImagesCreateAsync, execution.OperationZhipuVideosCreate:
		return "create"
	case execution.OperationVideosRetrieve, execution.OperationZhipuMediaRetrieve:
		return "retrieve"
	case execution.OperationVideosContent:
		return "content"
	case execution.OperationVideosDelete, execution.OperationZhipuMediaCancel:
		return "cancel"
	default:
		return ""
	}
}

func (handler *Handler) mediaResultHasTaskID(selectedProtocol protocol.Protocol, selection scheduler.Selection, result UpstreamResult) bool {
	if selectedProtocol == protocol.ZhipuMedia {
		response, err := media.ParseZhipuResponse(result.Body)
		return err == nil && media.SelectZhipuTaskID(response) != ""
	}
	response, err := media.ParseOpenAIVideoResponse(result.Body)
	if err != nil {
		return false
	}
	return media.SelectOpenAIVideoTaskID(string(selection.ChannelID), response) != ""
}

func isMediaProtocol(value protocol.Protocol) bool {
	return value == protocol.OpenAIVideos || value == protocol.ZhipuMedia
}

func isMediaCreateOperation(operation execution.Operation) bool {
	switch operation {
	case execution.OperationVideosCreate, execution.OperationZhipuImagesGenerate,
		execution.OperationZhipuImagesCreateAsync, execution.OperationZhipuVideosCreate:
		return true
	default:
		return false
	}
}

func isMediaCancelOperation(operation execution.Operation) bool {
	return operation == execution.OperationVideosDelete || operation == execution.OperationZhipuMediaCancel
}

func (handler *Handler) resolveMediaImageProtocol(
	selected protocol.Protocol,
	path string,
	body []byte,
	snapshot *state.ConfigSnapshot,
) (protocol.Protocol, string) {
	if selected != protocol.OpenAIImages || path != "/v1/images/generations" {
		return selected, ""
	}
	requested := jsonStringField(body, "model")
	if requested == "" {
		return selected, ""
	}
	if canonical, ok := canonicalZhipuModel(snapshot, execution.OperationZhipuImagesGenerate, requested); ok {
		return protocol.ZhipuMedia, canonical
	}
	return selected, ""
}

func canonicalZhipuModel(snapshot *state.ConfigSnapshot, operation execution.Operation, requested string) (string, bool) {
	for _, group := range snapshot.Groups {
		if group.ChannelID != channel.ZhipuAI {
			continue
		}
		if _, ok := group.ResolvedTarget.Mode(protocol.ZhipuMedia, operation); !ok {
			continue
		}
		for _, model := range group.Models {
			if strings.EqualFold(model.ExternalName(), requested) {
				return model.ExternalName(), true
			}
		}
	}
	return "", false
}

func jsonStringField(body []byte, field string) string {
	var object map[string]json.RawMessage
	if json.Unmarshal(body, &object) != nil {
		return ""
	}
	value, ok := object[field]
	if !ok {
		return ""
	}
	var text string
	if json.Unmarshal(value, &text) != nil {
		return ""
	}
	return text
}

func rewriteJSONModel(body []byte, model string) []byte {
	var object map[string]json.RawMessage
	if json.Unmarshal(body, &object) != nil {
		return body
	}
	encoded, err := json.Marshal(model)
	if err != nil {
		return body
	}
	object["model"] = encoded
	rewritten, err := json.Marshal(object)
	if err != nil {
		return body
	}
	return rewritten
}
