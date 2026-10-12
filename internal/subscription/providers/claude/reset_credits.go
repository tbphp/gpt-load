package claude

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	cpaembedded "github.com/router-for-me/CLIProxyAPI/v8/gptload-embedded/embedded"

	"gpt-load/internal/channel/modules"
	"gpt-load/internal/channel/spec"
	subscriptionruntime "gpt-load/internal/subscription/runtime"
)

type claudeResetCreditAction struct{ *claudeDriver }

func (claudeResetCreditAction) ID() spec.ActionID { return modules.ClaudeResetCreditAction }

func (claudeResetCreditAction) PrepareResetCredit(ctx context.Context, credential subscriptionruntime.Credential, target subscriptionruntime.Target) (json.RawMessage, error) {
	value, options, err := claudeResetContext(ctx, credential, target)
	if err != nil {
		return nil, err
	}
	request, err := cpaembedded.PrepareClaudeResetCredit(ctx, credentialToBridge(value), options)
	return request, normalizeClaudeResetError(err)
}

func (claudeResetCreditAction) Consume(ctx context.Context, credential subscriptionruntime.Credential, target subscriptionruntime.Target, request subscriptionruntime.ResetCreditRequest) (subscriptionruntime.ResetCreditResult, error) {
	value, options, err := claudeResetContext(ctx, credential, target)
	if err != nil {
		return subscriptionruntime.ResetCreditResult{}, err
	}
	result, err := cpaembedded.ConsumeClaudeResetCredit(ctx, credentialToBridge(value), request.Data, request.ID, options)
	if err != nil {
		return subscriptionruntime.ResetCreditResult{}, normalizeClaudeResetError(err)
	}
	return normalizeClaudeResetResult(result)
}

func normalizeClaudeResetResult(result cpaembedded.ClaudeResetCreditResult) (subscriptionruntime.ResetCreditResult, error) {
	// 上游无法确认扣卡或重置结果时，保留原操作，禁止按普通拒绝再次扣卡。
	if result.Reason == "stamp_indeterminate" || result.Reason == "reset_unconfirmed" {
		return subscriptionruntime.ResetCreditResult{}, errors.New("Claude reset outcome is unknown")
	}
	switch result.Result {
	case "reset":
		return subscriptionruntime.ResetCreditResult{Status: "succeeded", WindowsReset: len(result.Cleared)}, nil
	case "already_used", "ineligible":
		// already_used 只说明卡已用完，不能当作本次重置成功。
		return subscriptionruntime.ResetCreditResult{}, &subscriptionruntime.UpstreamHTTPError{StatusCode: http.StatusConflict}
	case "not_limited":
		return subscriptionruntime.ResetCreditResult{}, &subscriptionruntime.UpstreamHTTPError{StatusCode: http.StatusBadRequest}
	case "cooldown":
		return subscriptionruntime.ResetCreditResult{}, &subscriptionruntime.UpstreamHTTPError{StatusCode: http.StatusTooManyRequests}
	case "unavailable":
		return subscriptionruntime.ResetCreditResult{}, errors.New("Claude reset outcome is unknown")
	default:
		return subscriptionruntime.ResetCreditResult{}, errors.New("Claude reset response is invalid")
	}
}

func claudeResetContext(ctx context.Context, credential subscriptionruntime.Credential, target subscriptionruntime.Target) (Credential, cpaembedded.ClaudeOptions, error) {
	value, err := ParseCredentialJSON(credential.Canonical())
	if err != nil {
		return Credential{}, cpaembedded.ClaudeOptions{}, err
	}
	root, err := target.BaseURL()
	if err != nil {
		return Credential{}, cpaembedded.ClaudeOptions{}, err
	}
	options, err := claudeAPIOptions(ctx, root)
	return value, options, err
}

func normalizeClaudeResetError(err error) error {
	if errors.Is(err, cpaembedded.ErrClaudeResetCreditUnavailable) {
		return &subscriptionruntime.UpstreamHTTPError{StatusCode: http.StatusConflict}
	}
	var upstream *cpaembedded.ClaudeUpstreamHTTPError
	if errors.As(err, &upstream) {
		return &subscriptionruntime.UpstreamHTTPError{StatusCode: upstream.StatusCode}
	}
	return err
}
