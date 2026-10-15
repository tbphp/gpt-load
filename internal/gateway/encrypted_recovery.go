package gateway

import (
	"gpt-load/internal/codexrecovery"
	"gpt-load/internal/dialect"
	"gpt-load/internal/protocol"
	"gpt-load/internal/scheduler"
	"gpt-load/internal/state"
)

// encryptedContentRecoveryRetry pins one same-credential replay carrying a
// request body with the rejected encrypted state stripped. It is a one-shot
// budget per request: the encrypted continuation context does not change
// between attempts, so a second replay cannot succeed.
type encryptedContentRecoveryRetry struct {
	selection scheduler.Selection
	ref       state.CredentialRef
	body      []byte
}

// encryptedContentRecoverySignal reports the encrypted-content rejection the
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
	return codexrecovery.Classify(summary)
}

// encryptedContentRecoveryApplies gates the recovery to the OpenAI Responses
// family, where encrypted continuation state is account-bound.
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
