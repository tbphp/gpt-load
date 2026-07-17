package gateway

import (
	"context"
	"errors"
	"io"
	"math/rand"

	"gpt-load/internal/dialect"
	"gpt-load/internal/health"
	"gpt-load/internal/platform/encryption"
	"gpt-load/internal/protocol"
	"gpt-load/internal/scheduler"
	"gpt-load/internal/state"

	"github.com/gin-gonic/gin"
)

const maxAttempts = 3

type AttemptForwarder interface {
	Forward(context.Context, ForwardInput) UpstreamResult
}

type runtimeKeyRegistry interface {
	scheduler.KeySource
	ActiveEncryptedValue(keyID, expectedGroupID uint) (string, bool)
}

type DialectSet map[protocol.Protocol]dialect.Dialect

func NewDialectSet(openAI *dialect.OpenAI) DialectSet {
	dialects := make(DialectSet)
	if openAI != nil {
		dialects[protocol.OpenAI] = openAI
	}
	return dialects
}

type Handler struct {
	manager    *state.Manager
	registry   runtimeKeyRegistry
	encryption encryption.Service
	forwarder  AttemptForwarder
	dialects   DialectSet
	newRandom  func() *rand.Rand
}

func NewHandler(
	manager *state.Manager,
	registry *state.KeyRegistry,
	encryptionService encryption.Service,
	forwarder AttemptForwarder,
	dialects DialectSet,
) *Handler {
	return &Handler{
		manager: manager, registry: registry, encryption: encryptionService,
		forwarder: forwarder, dialects: dialects,
		newRandom: func() *rand.Rand { return rand.New(rand.NewSource(rand.Int63())) },
	}
}

func (handler *Handler) RegisterRoutes(engine *gin.Engine) {
	engine.POST("/v1/chat/completions", handler.Handle)
	engine.NoRoute(handler.Handle)
}

func (handler *Handler) Handle(ginContext *gin.Context) {
	snapshot := handler.manager.Current()
	accessKey, ok := authenticate(ginContext.Request, snapshot, handler.encryption)
	if !ok {
		writeReason(ginContext, reasonInvalidAccessKey)
		return
	}
	selectedRoute, ok := determineRoute(ginContext.Request.Method, ginContext.Request.URL.Path, ginContext.Request.Header)
	selectedDialect, dialectReady := handler.dialects[selectedRoute.Protocol]
	if !ok || !dialectReady || selectedRoute.Kind != endpointChat {
		writeReason(ginContext, reasonEndpointNotFound)
		return
	}

	body, err := io.ReadAll(ginContext.Request.Body)
	if err != nil {
		writeReason(ginContext, reasonCannotExtractModel)
		return
	}
	parsed := &dialect.ParsedRequest{
		Method:   ginContext.Request.Method,
		Path:     ginContext.Request.URL.Path,
		RawQuery: ginContext.Request.URL.RawQuery,
		Header:   ginContext.Request.Header.Clone(),
		Body:     body,
	}
	model, _, err := selectedDialect.ExtractModel(parsed)
	if err != nil {
		writeReason(ginContext, reasonCannotExtractModel)
		return
	}

	iterator := scheduler.New(snapshot, handler.registry, scheduler.Query{
		Protocol: selectedRoute.Protocol, ExternalModel: model, AccessKey: accessKey,
	}, handler.newRandom())
	handler.executeAttempts(ginContext, iterator, selectedDialect, parsed)
}

func (handler *Handler) executeAttempts(
	ginContext *gin.Context,
	iterator *scheduler.Iterator,
	selectedDialect dialect.Dialect,
	parsed *dialect.ParsedRequest,
) {
	var lastResponse *UpstreamResult
	var lastTransport *UpstreamResult
	attempts := 0
	for attempts < maxAttempts {
		selection, err := iterator.Next()
		if errors.Is(err, scheduler.ErrExhausted) {
			break
		}
		if err != nil {
			break
		}
		encrypted, ok := handler.registry.ActiveEncryptedValue(selection.KeyID, selection.GroupID)
		if !ok {
			continue
		}
		apiKey, err := handler.encryption.Decrypt(encrypted)
		if err != nil {
			continue
		}

		attempts++
		result := handler.forwarder.Forward(ginContext.Request.Context(), ForwardInput{
			Dialect: selectedDialect, Group: selection.Group, APIKey: apiKey, Request: parsed,
		})
		verdict := health.Judge(selectedDialect, health.Attempt{
			StatusCode: result.StatusCode, Body: result.ClassificationBody,
			Err: result.Err, RequestWritten: result.RequestWritten,
		})
		if result.HasResponse() {
			copied := result
			lastResponse = &copied
			if verdict.Retryable {
				continue
			}
			writeUpstreamResponse(ginContext, result)
			return
		}
		if errors.Is(result.Err, context.Canceled) {
			return
		}
		if verdict.Retryable {
			copied := result
			lastTransport = &copied
			continue
		}
		if isTimeoutError(result.Err) {
			writeReason(ginContext, reasonUpstreamTimeout)
		} else {
			writeReason(ginContext, reasonUpstreamConnect)
		}
		return
	}

	if lastResponse != nil {
		writeUpstreamResponse(ginContext, *lastResponse)
		return
	}
	if lastTransport != nil {
		writeReason(ginContext, reasonUpstreamConnect)
		return
	}
	writeReason(ginContext, reasonNoCandidate)
}

func writeUpstreamResponse(ginContext *gin.Context, result UpstreamResult) {
	for name, values := range result.Header {
		for _, value := range values {
			ginContext.Writer.Header().Add(name, value)
		}
	}
	ginContext.Status(result.StatusCode)
	_, _ = ginContext.Writer.Write(result.Body)
}
