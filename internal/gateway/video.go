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
	"gpt-load/internal/outboundproxy"
	"gpt-load/internal/protocol"
	"gpt-load/internal/scheduler"
	"gpt-load/internal/state"
	"gpt-load/internal/telemetry"
	"gpt-load/internal/usage"
	"gpt-load/internal/video"
)

const (
	videoUpstreamMessageKey   = "gpt-load.video.upstream-message"
	openAIVideoDefaultBaseURL = "https://api.openai.com/v1"
	maxVideoErrorBodyBytes    = int64(64 << 10)
)

type videoRequestState struct {
	store       video.TaskStore
	create      *video.CreateRequest
	createHash  string
	reservation video.Task
	reserved    bool
	finalized   bool
	binding     video.Task
	pinned      bool
	delete      bool
	handled     bool
}

func (handler *Handler) prepareVideoRequest(
	ginContext *gin.Context,
	snapshot *state.ConfigSnapshot,
	accessKey state.AccessKeyView,
	metadata dialect.RequestMetadata,
	parsed *dialect.ParsedRequest,
	recorder *requestRecorder,
) (*videoRequestState, dialect.RequestMetadata, *dialect.ParsedRequest, *reason) {
	if len(accessKey.Filters.Protocols) > 0 {
		if _, allowed := accessKey.Filters.Protocols[protocol.OpenAIVideos]; !allowed {
			return nil, metadata, parsed, &reasonVideoProtocolNotAllowed
		}
	}
	if handler == nil || handler.videoTasks == nil {
		return nil, metadata, parsed, &reasonVideoInternal
	}
	if metadata.Operation == execution.OperationVideosCreate {
		create := metadata.VideoCreate
		if create == nil {
			parsedRequest, err := video.ParseCreateRequest(parsed.Body)
			if err != nil {
				return nil, metadata, parsed, invalidVideoRequestReason(err)
			}
			create = &parsedRequest
			metadata.VideoCreate = create
		}
		hash, err := video.IdempotencyHash(accessKey.ID, ginContext.GetHeader("Idempotency-Key"))
		if err != nil {
			return nil, metadata, parsed, invalidVideoRequestReason(err)
		}
		state := &videoRequestState{store: handler.videoTasks, create: create, createHash: hash}
		task, err := state.store.NewTask(accessKey.ID, *create, hash)
		if err != nil {
			return nil, metadata, parsed, &reasonVideoInternal
		}
		if recorder != nil {
			recorder.setClientModel(create.Model)
			recorder.setOperation(metadata.Operation)
			recorder.setVideoMetadata(observationForCreateTask(task))
		}
		task, created, err := state.store.Reserve(ginContext.Request.Context(), task)
		if err != nil {
			return nil, metadata, parsed, &reasonVideoInternal
		}
		if !created {
			if recorder != nil {
				recorder.setVideoMetadata(observationForExistingTask(task, "create"))
				recorder.completeLocalSuccess(http.StatusOK, task.UpstreamModel)
			}
			handler.writeVideoJSON(ginContext, http.StatusOK, video.CreateResponse(task))
			state.handled = true
			return state, metadata, parsed, nil
		}
		state.reservation, state.reserved = task, true
		return state, metadata, parsed, nil
	}

	if metadata.VideoTaskID == "" {
		return nil, metadata, parsed, &reasonVideoTaskNotFound
	}
	task, err := handler.videoTasks.Get(ginContext.Request.Context(), accessKey.ID, metadata.VideoTaskID)
	if errors.Is(err, video.ErrNotFound) {
		return nil, metadata, parsed, &reasonVideoTaskNotFound
	}
	if err != nil {
		return nil, metadata, parsed, &reasonVideoInternal
	}
	if len(accessKey.Filters.Models) > 0 {
		if _, allowed := accessKey.Filters.Models[task.ClientModel]; !allowed {
			return nil, metadata, parsed, &reasonVideoTaskNotFound
		}
	}
	group, ok := snapshot.Groups[task.GroupID]
	if !ok {
		return nil, metadata, parsed, &reasonVideoCredentialUnavailable
	}
	ref, ok := handler.registry.CredentialRef(task.CredentialID)
	if !ok || ref.GroupID != task.GroupID || !handler.videoCredentialAvailable(task.GroupID, task.CredentialID) {
		return nil, metadata, parsed, &reasonVideoCredentialUnavailable
	}
	if task.UpstreamTaskID == "" {
		return nil, metadata, parsed, &reasonVideoCredentialUnavailable
	}
	if _, supported := group.ResolvedTarget.Mode(protocol.OpenAIVideos, metadata.Operation); !supported {
		switch metadata.Operation {
		case execution.OperationVideosDelete:
			return nil, metadata, parsed, &reasonVideoCancelNotSupported
		default:
			return nil, metadata, parsed, &reasonVideoContentNotSupported
		}
	}
	state := &videoRequestState{store: handler.videoTasks, binding: task, pinned: true}
	phase := videoPhase(metadata.Operation)
	if recorder != nil {
		recorder.setClientModel(task.ClientModel)
		recorder.setOperation(metadata.Operation)
		recorder.setUsageApplicable(false)
		recorder.setVideoMetadata(observationForExistingTask(task, phase))
	}
	switch metadata.Operation {
	case execution.OperationVideosContent:
		state.handled = true
		updateDebugHeaders(ginContext.Writer.Header(), group.Name, 1)
		if failure := handler.handleVideoContent(ginContext, snapshot, accessKey, group, ref, task, recorder); failure != nil {
			return nil, metadata, parsed, failure
		}
		return state, metadata, parsed, nil
	case execution.OperationVideosDelete:
		state.delete = true
	case execution.OperationVideosRetrieve:
	default:
		return nil, metadata, parsed, &reasonVideoCredentialUnavailable
	}
	parsed.Method = ginContext.Request.Method
	parsed.Path = "/v1/videos/" + url.PathEscape(task.UpstreamTaskID)
	parsed.RawQuery = ""
	parsed.Body = nil
	if parsed.Header != nil {
		parsed.Header.Del("Content-Type")
		parsed.Header.Del("Content-Length")
	}
	return state, metadata, parsed, nil
}

func invalidVideoRequestReason(err error) *reason {
	value := reason{
		Status: http.StatusBadRequest, Code: "invalid_request_error",
		Message: "Invalid video request.",
	}
	var validation *video.ValidationError
	if errors.As(err, &validation) {
		value.Message = validation.Message
	}
	return &value
}

func (handler *Handler) videoCredentialAvailable(groupID, credentialID uint) bool {
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

func (handler *Handler) cleanupVideoReservation(state *videoRequestState) {
	if state == nil || handler == nil || handler.videoTasks == nil ||
		!state.reserved || state.finalized || state.reservation.ID == "" {
		return
	}
	if err := handler.videoTasks.Delete(context.Background(), state.reservation.ID); err != nil {
		utilsLogVideo(handler.logger, logrus.WarnLevel, map[string]any{
			"event": "video_task_reservation_cleanup_failed", "video_task_id": state.reservation.ID, "error": err.Error(),
		}, "Failed to remove an incomplete video task reservation")
	}
}

func (handler *Handler) finalizeVideoCreate(
	ctx context.Context,
	state *videoRequestState,
	selection scheduler.Selection,
	ref state.CredentialRef,
	result UpstreamResult,
	clientModel string,
	upstreamModel string,
) (UpstreamResult, *reason) {
	if state == nil || state.store == nil {
		return result, &reasonVideoInternal
	}
	response, err := video.ParseUpstreamResponse(result.Body)
	if err != nil {
		return result, &reasonUpstreamProtocol
	}
	upstreamTaskID := video.SelectUpstreamTaskID(string(selection.ChannelID), response)
	if upstreamTaskID == "" {
		return result, &reasonUpstreamProtocol
	}
	task := state.reservation
	if task.ID == "" {
		task, err = state.store.NewTask(selection.GroupID, *state.create, state.createHash)
		if err != nil {
			return result, &reasonVideoInternal
		}
		task, _, err = state.store.Reserve(ctx, task)
		if err != nil {
			return result, &reasonVideoInternal
		}
	}
	task.GroupID = selection.GroupID
	task.CredentialID = ref.ID
	task.UpstreamTaskID = upstreamTaskID
	task.UpstreamTaskAlias = response.TaskID
	task.UpstreamVideoID = response.VideoID
	task.UpstreamModel = upstreamModel
	task.ClientModel = clientModel
	task.Status = video.NormalizeStatus(response.Status)
	if strings.TrimSpace(response.Status) == "" {
		task.Status = video.StatusQueued
	}
	if response.Progress != nil {
		task.Progress = video.ClampProgress(*response.Progress)
	}
	if task.Status == video.StatusCompleted {
		task.Progress = 100
	}
	task.ResultURL = response.URL
	task.LastErrorCode = response.ErrorCode
	task.LastErrorMessage = response.ErrorMessage
	if err := state.store.UpdateCreateResult(ctx, task); err != nil {
		_ = state.store.Delete(ctx, task.ID)
		return result, &reasonVideoInternal
	}
	state.reservation = task
	state.finalized = true
	body, err := json.Marshal(video.CreateResponse(task))
	if err != nil {
		return result, &reasonVideoInternal
	}
	result.StatusCode = http.StatusOK
	result.Body = body
	result.ClassificationBody = body
	result.Header = videoJSONHeaders(result.Header)
	result.UpstreamReportedModel = response.Model
	return result, nil
}

func (handler *Handler) finalizeVideoResource(
	ctx context.Context,
	state *videoRequestState,
	result UpstreamResult,
) (UpstreamResult, *reason) {
	if result.StatusCode == http.StatusNotFound {
		return result, &reasonVideoTaskNotFound
	}
	if state == nil || state.store == nil {
		return result, &reasonVideoInternal
	}
	task := state.binding
	if result.StatusCode >= http.StatusOK && result.StatusCode < http.StatusMultipleChoices {
		if !state.delete && len(bytes.TrimSpace(result.Body)) > 0 {
			response, err := video.ParseUpstreamResponse(result.Body)
			if err != nil {
				return result, &reasonUpstreamProtocol
			}
			task = video.ApplyUpstreamObservation(task, response)
		}
	}
	var body []byte
	var err error
	if state.delete {
		task.Status = video.StatusCancelled
		body, err = json.Marshal(struct {
			ID      string `json:"id"`
			Object  string `json:"object"`
			Deleted bool   `json:"deleted"`
		}{ID: task.ID, Object: video.ObjectVideo, Deleted: true})
	} else {
		body, err = json.Marshal(video.TaskResponse(task, handler.now(), true))
	}
	if err != nil {
		return result, &reasonVideoInternal
	}
	if err := state.store.UpdateObservation(ctx, task); err != nil {
		return result, &reasonVideoInternal
	}
	state.binding = task
	if result.StatusCode < http.StatusOK || result.StatusCode >= http.StatusMultipleChoices {
		return result, nil
	}
	if state.delete {
		result.StatusCode = http.StatusOK
	}
	result.Body = body
	result.ClassificationBody = body
	result.Header = videoJSONHeaders(result.Header)
	return result, nil
}

func videoJSONHeaders(source http.Header) http.Header {
	headers := source.Clone()
	if headers == nil {
		headers = make(http.Header)
	}
	headers.Del("Content-Encoding")
	headers.Del("Content-Length")
	headers.Set("Content-Type", "application/json; charset=utf-8")
	return headers
}

func (handler *Handler) writeVideoJSON(ginContext *gin.Context, status int, value any) {
	body, err := json.Marshal(value)
	if err != nil {
		_ = handler.writeVideoError(ginContext, http.StatusInternalServerError, "internal_error", "server_error", "", "Video response could not be encoded.")
		return
	}
	if err := handler.writeBufferedResponse(ginContext, status, http.Header{
		"Content-Type": {"application/json; charset=utf-8"},
	}, body); err != nil {
		utilsLogVideo(handler.logger, logrus.WarnLevel, map[string]any{
			"event": "video_response_write_failed", "error": err.Error(),
		}, "Failed to write video response")
	}
}

func (handler *Handler) rememberVideoUpstreamError(ginContext *gin.Context, result UpstreamResult) {
	if result.ExecutionError != nil && result.ExecutionError.Summary != "" {
		ginContext.Set(videoUpstreamMessageKey, result.ExecutionError.Summary)
	}
	if result.UpstreamRequestID != "" {
		ginContext.Header("X-Request-Id", result.UpstreamRequestID)
	}
	if retryAfter := result.Header.Get("Retry-After"); retryAfter != "" {
		ginContext.Header("Retry-After", retryAfter)
	}
}

func (handler *Handler) completeVideoUpstreamFailure(
	ginContext *gin.Context,
	recorder *requestRecorder,
	operation execution.Operation,
	result UpstreamResult,
) {
	handler.rememberVideoUpstreamError(ginContext, result)
	value := videoUpstreamFailureReason(operation, result.StatusCode)
	handler.completeReason(ginContext, recorder, value)
}

func videoUpstreamFailureReason(operation execution.Operation, statusCode int) reason {
	switch statusCode {
	case http.StatusBadRequest:
		return reasonInvalidProtocolRequest
	case http.StatusNotFound:
		if operation == execution.OperationVideosCreate {
			return reasonVideoModelNotFound
		}
		return reasonVideoTaskNotFound
	case http.StatusTooManyRequests:
		return reasonUpstreamRateLimited
	case http.StatusNotImplemented, http.StatusMethodNotAllowed:
		if operation == execution.OperationVideosDelete {
			return reasonVideoCancelNotSupported
		}
		return reasonVideoContentNotSupported
	default:
		return reasonUpstreamProtocol
	}
}

func (handler *Handler) handleVideoContent(
	ginContext *gin.Context,
	snapshot *state.ConfigSnapshot,
	accessKey state.AccessKeyView,
	group state.GroupView,
	ref state.CredentialRef,
	task video.Task,
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
		return &reasonVideoCredentialUnavailable
	}
	decrypted, err := handler.encryption.Decrypt(encrypted)
	if err != nil {
		return &reasonVideoCredentialUnavailable
	}
	credential, err := normalizeChannelCredential(
		handler.channels, handler.subscriptions, group.ChannelID, group.ConnectionType, decrypted,
	)
	if err != nil {
		return &reasonVideoCredentialUnavailable
	}
	effectiveProxy, _, err := resolveAttemptProxy(handler.encryption, group.Proxy, ref)
	if err != nil {
		return &reasonVideoCredentialUnavailable
	}
	client, err := newVideoHTTPClient(effectiveProxy, group.Timeouts)
	if err != nil {
		return &reasonVideoCredentialUnavailable
	}
	defer client.CloseIdleConnections()
	ctx := ginContext.Request.Context()

	sourceURL, authenticated, task, failure := handler.videoContentSource(ctx, client, group, task, credential.apiKey)
	if failure != nil {
		handler.recordVideoContentAttempt(recorder, group, ref, task, 0, "", started, failure)
		return failure
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, sourceURL, nil)
	if err != nil {
		value := reasonUpstreamProtocol
		handler.recordVideoContentAttempt(recorder, group, ref, task, 0, "", started, &value)
		return &value
	}
	if authenticated {
		request.Header.Set("Authorization", "Bearer "+credential.apiKey)
		applyVideoHeaderRules(request.Header, group.HeaderRules, credential.apiKey)
	}
	copyVideoRangeHeaders(request.Header, ginContext.Request.Header)
	request.Header.Set("Accept-Encoding", "identity")
	response, err := client.Do(request)
	if err != nil {
		value := reasonUpstreamConnect
		if isTimeoutError(err) {
			value = reasonUpstreamTimeout
		}
		handler.recordVideoContentAttempt(recorder, group, ref, task, 0, "", started, &value)
		return &value
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound {
		handler.recordVideoContentAttempt(recorder, group, ref, task, response.StatusCode, response.Header.Get("X-Request-Id"), started, &reasonVideoTaskNotFound)
		return &reasonVideoTaskNotFound
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		body, _ := io.ReadAll(io.LimitReader(response.Body, maxVideoErrorBodyBytes))
		message := videoUpstreamErrorMessage(body)
		ginContext.Set(videoUpstreamMessageKey, message)
		value := reasonUpstreamProtocol
		if response.StatusCode == http.StatusTooManyRequests {
			value = reasonUpstreamRateLimited
		}
		handler.recordVideoContentAttempt(recorder, group, ref, task, response.StatusCode, response.Header.Get("X-Request-Id"), started, &value)
		return &value
	}

	copyVideoContentHeaders(ginContext.Writer.Header(), response.Header)
	ginContext.Status(response.StatusCode)
	ginContext.Writer.WriteHeaderNow()
	buffer := make([]byte, 64<<10)
	if _, err := io.CopyBuffer(ginContext.Writer, response.Body, buffer); err != nil {
		utilsLogVideo(handler.logger, logrus.WarnLevel, map[string]any{
			"event": "video_content_stream_failed", "video_task_id": task.ID, "error": err.Error(),
		}, "Video content stream ended early")
	}
	if flusher, ok := ginContext.Writer.(http.Flusher); ok {
		flusher.Flush()
	}
	handler.recordVideoContentAttempt(recorder, group, ref, task, response.StatusCode, response.Header.Get("X-Request-Id"), started, nil)
	if recorder != nil {
		recorder.updateVideoMetadata(task.Status, task.UpstreamTaskID)
	}
	return nil
}

func (handler *Handler) videoContentSource(
	ctx context.Context,
	client *http.Client,
	group state.GroupView,
	task video.Task,
	apiKey string,
) (sourceURL string, authenticated bool, updated video.Task, failure *reason) {
	if group.ChannelID == channel.Agnes {
		if task.Status == video.StatusCompleted && validVideoMediaURL(task.ResultURL) {
			return task.ResultURL, false, task, nil
		}
		retrieveURL, err := agnesVideoRetrieveURL(group, task)
		if err != nil {
			value := reasonVideoContentNotSupported
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
		parsed, err := video.ParseUpstreamResponse(body)
		if err != nil || !validVideoMediaURL(parsed.URL) {
			value := reasonUpstreamProtocol
			return "", false, task, &value
		}
		task = video.ApplyUpstreamObservation(task, parsed)
		if handler.videoTasks != nil {
			task.Status = video.StatusCompleted
			task.Progress = 100
			task.ResultURL = parsed.URL
			_ = handler.videoTasks.UpdateObservation(ctx, task)
		}
		return parsed.URL, false, task, nil
	}
	contentURL, err := openAIVideoContentURL(group, task)
	if err != nil {
		value := reasonVideoContentNotSupported
		return "", false, task, &value
	}
	return contentURL, true, task, nil
}

func (handler *Handler) recordVideoContentAttempt(
	recorder *requestRecorder,
	group state.GroupView,
	ref state.CredentialRef,
	task video.Task,
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

func newVideoHTTPClient(
	effective outboundproxy.Effective,
	timeouts state.TimeoutConfig,
) (*http.Client, error) {
	normalized, err := outboundproxy.NormalizeEffective(effective)
	if err != nil {
		return nil, fmt.Errorf("normalize video proxy: %w", err)
	}
	transport := &http.Transport{
		DialContext:           (&net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
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
	return &http.Client{Transport: transport, Timeout: timeout}, nil
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

func openAIVideoContentURL(group state.GroupView, task video.Task) (string, error) {
	resourceID := url.PathEscape(task.UpstreamTaskID)
	switch group.ResolvedTarget.ProviderKind {
	case channel.ProviderOpenAI:
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

func agnesVideoRetrieveURL(group state.GroupView, task video.Task) (string, error) {
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

func utilsLogVideo(logger *logrus.Logger, level logrus.Level, fields map[string]any, message string) {
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

func observationForCreateTask(task video.Task) *telemetry.VideoObservation {
	return &telemetry.VideoObservation{
		GatewayTaskID: task.ID, Model: task.ClientModel, Mode: task.Mode,
		Seconds: task.Seconds, Size: task.Size, AspectRatio: task.AspectRatio,
		Phase: "create", Status: task.Status,
	}
}

func observationForExistingTask(task video.Task, phase string) *telemetry.VideoObservation {
	return &telemetry.VideoObservation{
		GatewayTaskID: task.ID, UpstreamTaskID: task.UpstreamTaskID,
		Model: task.ClientModel, Mode: task.Mode, Seconds: task.Seconds,
		Size: task.Size, AspectRatio: task.AspectRatio,
		Phase: phase, Status: task.Status,
	}
}

func videoPhase(operation execution.Operation) string {
	switch operation {
	case execution.OperationVideosCreate:
		return "create"
	case execution.OperationVideosRetrieve:
		return "retrieve"
	case execution.OperationVideosContent:
		return "content"
	case execution.OperationVideosDelete:
		return "delete"
	default:
		return ""
	}
}

func (handler *Handler) videoResultHasTaskID(selection scheduler.Selection, result UpstreamResult) bool {
	response, err := video.ParseUpstreamResponse(result.Body)
	if err != nil {
		return false
	}
	return video.SelectUpstreamTaskID(string(selection.ChannelID), response) != ""
}
