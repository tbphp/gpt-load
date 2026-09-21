package gateway

import (
	"context"
	"crypto/sha256"
	"net/http"

	"gpt-load/internal/execution"
	"gpt-load/internal/jev"
	"gpt-load/internal/requestaudit"
	"gpt-load/internal/state"
)

var reasonAuditBlocked = reason{http.StatusForbidden, "request_audit_blocked", "Request blocked by an audit rule."}
var reasonAuditIncomplete = reason{http.StatusServiceUnavailable, "request_audit_incomplete", "Request audit could not be completed."}

// 只在单次客户端请求内复用相同完整内容；新一轮工具结果不会沿用旧结论。
func (h *Handler) checkRequestAudit(ctx context.Context, snapshot *state.ConfigSnapshot, key state.AccessKeyView, operation execution.Operation, body []byte, recorder *requestRecorder, admit func() *reason, semantic bool) *reason {
	cfg := snapshot.RequestAudit
	if !cfg.Applies(key.ID) || recorder == nil {
		return nil
	}
	started := h.now()
	content, findings, incomplete := requestaudit.Inspect(body)
	if !cfg.LocalSecrets {
		findings = nil
	}
	if incomplete == "content_too_large" && !cfg.SemanticEnabled {
		incomplete = ""
	}
	if operation != execution.OperationChatCompletion && operation != execution.OperationResponsesCreate {
		incomplete = "unsupported_operation"
	}
	digest := sha256.Sum256(append([]byte(string(operation)+":"), content...))
	if recorder.auditCache[digest] {
		return nil
	}
	matched := len(findings) > 0
	if !semantic && cfg.SemanticEnabled && !matched && incomplete == "" {
		return nil
	}
	if recorder.audit == nil {
		recorder.audit = &requestaudit.Result{Mode: cfg.Mode, Status: "passed", Findings: []requestaudit.Finding{}, Calls: []jev.Observation{}}
	}
	result := recorder.audit
	result.Checks++
	defer func() { result.DurationMs += max(0, h.now().Sub(started).Milliseconds()) }()
	if !matched && incomplete == "" && cfg.SemanticEnabled {
		if failure := admit(); failure != nil {
			return failure
		}
		call, upstream := h.executeJevDecision(ctx, snapshot, key, requestaudit.BuildRequest(snapshot.Jev.Model, content, cfg.Rules), true)
		result.Calls = append(result.Calls, call)
		incomplete = call.Reason
		if incomplete == "" {
			findings, incomplete = requestaudit.Interpret(upstream.Body, cfg.Rules)
			for _, finding := range findings {
				matched = matched || finding.Status == "matched"
			}
		}
	}
	result.Findings = append(result.Findings, findings...)
	if matched {
		result.Status = "matched"
		result.Reason = "rule_matched"
	} else if incomplete != "" && result.Status != "matched" {
		result.Status = "incomplete"
		result.Reason = incomplete
	}
	if ctx.Err() != nil {
		return &reasonAuditIncomplete
	}
	if cfg.Mode == "enforce" {
		if matched {
			return &reasonAuditBlocked
		}
		if incomplete != "" {
			return &reasonAuditIncomplete
		}
	}
	if recorder.auditCache == nil {
		recorder.auditCache = map[[32]byte]bool{}
	}
	recorder.auditCache[digest] = true
	return nil
}
