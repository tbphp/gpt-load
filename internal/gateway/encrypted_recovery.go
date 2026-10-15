package gateway

import (
	"gpt-load/internal/codexrecovery"
	"gpt-load/internal/dialect"
	"gpt-load/internal/protocol"
	"gpt-load/internal/scheduler"
	"gpt-load/internal/state"
)

// encryptedContentRecoveryRetry pins one same-credential replay carrying a
// request body with rejected continuation state stripped. It is a one-shot
// budget per request; further cleanup replays are not attempted.
type encryptedContentRecoveryRetry struct {
	selection scheduler.Selection
	ref       state.CredentialRef
	body      []byte
}

// encryptedContentRecoverySignal reports the continuation-state rejection the
// upstream returned for this attempt, or the empty signal when the error is
// unrelated. It reads the structured evidence first and falls back to the
// rendered error summary.
func encryptedContentRecoverySignal(result UpstreamResult) codexrecovery.Signal {
	var code, summary string
	if result.ExecutionError != nil {
		code = result.ExecutionError.Code
		summary = result.ExecutionError.Summary
	}
	if summary == "" {
		summary = result.ErrorSummary
	}
	if signal := codexrecovery.ClassifyParts(code, summary); signal != "" {
		return signal
	}
	if signal := codexrecovery.Classify(summary); signal != "" {
		return signal
	}
	// Rendered summaries may omit a nested error code or truncate its message.
	// Inspect captured error payloads only on actual failure surfaces.
	if result.StatusCode >= 400 || result.ProviderErrorBeforeCommit {
		return codexrecovery.Classify(string(result.ClassificationBody))
	}
	return ""
}

// encryptedContentRecoveryApplies gates the recovery to the OpenAI Responses
// family before the response is committed to the client.
func encryptedContentRecoveryApplies(clientProtocol protocol.Protocol, committed bool) bool {
	return !committed && clientProtocol == protocol.OpenAIResponses
}

// withRequestBody returns a copy of request that carries body. It clones the
// header so the shared parsed request is never mutated.
func withRequestBody(request *dialect.ParsedRequest, body []byte) *dialect.ParsedRequest {
	cloned := *request
	cloned.Header = request.Header.Clone()
	cloned.Body = body
	return &cloned
}
