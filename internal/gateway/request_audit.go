package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"

	"gpt-load/internal/jev"
	"gpt-load/internal/requestaudit"
	"gpt-load/internal/state"
)

var reasonAuditBlocked = reason{http.StatusForbidden, "request_audit_blocked", "Request blocked by a guardrail rule."}

// 在业务外发前审查最终内容。自动模型的沿用/预热不构成护栏通过证明。
func (h *Handler) checkRequestAudit(ctx context.Context, snapshot *state.ConfigSnapshot, key state.AccessKeyView, body []byte, recorder *requestRecorder, admit func() *reason) *reason {
	cfg := snapshot.RequestAudit
	if !cfg.Applies(key.ID) || recorder == nil || len(bytes.TrimSpace(body)) == 0 {
		return nil
	}
	// 业务重试不重复未完成的审查，也不覆盖首次失败原因；新请求仍可重新审查。
	if recorder.audit != nil && (recorder.audit.Status == "incomplete" || recorder.audit.Reason == "content_truncated") {
		return nil
	}
	doc, incomplete := requestaudit.Extract(body)
	if incomplete == "" && (len(doc.Units) == 0 || recorder.auditCache[doc.Digest]) {
		return nil
	}
	if recorder.audit == nil {
		recorder.audit = &requestaudit.Result{Status: "passed", Findings: []requestaudit.Finding{}, Calls: []jev.Observation{}}
	}
	result := recorder.audit
	// 审查异常只记录未完成并放行，不能缓存为通过；只有明确命中拦截规则才拒绝。
	allowIncomplete := func(cause string) *reason {
		result.Status, result.Reason = "incomplete", cause
		return nil
	}
	if incomplete != "" {
		return allowIncomplete(incomplete)
	}
	group := snapshot.Groups[snapshot.Jev.GroupID]
	// 规则动作/名称变更不必重审；JEV 模型、组及其路由变化不能沿用旧证明。
	namespace, err := json.Marshal([]any{key.ID, recorder.protocol, snapshot.RequestRedaction.Rules(), snapshot.Jev, group.ChannelID, group.Params, group.Models})
	if err != nil {
		return allowIncomplete("internal_error")
	}
	review := h.guardrails.Prepare(namespace, snapshot.Jev.Model, doc, cfg.Rules, h.now())
	recordFindings := func() {
		for _, finding := range review.Findings {
			found := false
			for _, old := range result.Findings {
				found = found || old.RuleID == finding.RuleID
			}
			if !found {
				result.Findings = append(result.Findings, finding)
			}
		}
		if review.Status != "passed" {
			result.Status = review.Status
		}
		if review.Reason != "" {
			result.Reason = review.Reason
		}
	}
	// 已知命中先记录。有效拦截无需再调用 JEV，告警也不能被后续失败抹掉。
	recordFindings()
	if review.Status == "blocked" {
		return &reasonAuditBlocked
	}
	if review.Reason != "" {
		return allowIncomplete(review.Reason)
	}
	if len(review.Payload) != 0 {
		if recorder.auditCalled {
			return allowIncomplete("content_changed")
		}
		if failure := admit(); failure != nil {
			return failure
		}
		recorder.auditCalled = true
		call, upstream := h.executeJevDecision(ctx, snapshot, key, review.Payload, true)
		result.Calls = append(result.Calls, call)
		if ctx.Err() != nil {
			return allowIncomplete("canceled")
		}
		if call.Reason != "" {
			return allowIncomplete(call.Reason)
		}
		if reason := review.Resolve(upstream.Body, h.now()); reason != "" {
			return allowIncomplete(reason)
		}
	}
	recordFindings()
	if review.Status == "blocked" {
		return &reasonAuditBlocked
	}
	if review.Reason != "" {
		return nil
	}
	if recorder.auditCache == nil {
		recorder.auditCache = map[[32]byte]bool{}
	}
	recorder.auditCache[doc.Digest] = true
	return nil
}
