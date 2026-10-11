package embedded

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

var ErrClaudeResetCreditUnavailable = errors.New("Claude reset credit is unavailable")

type claudeResetCreditRequest struct {
	OrganizationID string `json:"organization_id"`
	GrantID        string `json:"grant_id"`
}

type ClaudeResetCreditResult struct {
	Result  string   `json:"result"`
	Reason  string   `json:"reason"`
	Cleared []string `json:"cleared"`
}

// PrepareClaudeResetCredit 读取上游指定的下一张卡，参数由调用方持久化后才能兑换。
func PrepareClaudeResetCredit(ctx context.Context, credential ClaudeCredential, options ClaudeOptions) ([]byte, error) {
	credential, err := canonicalClaudeCredential(credential)
	if err != nil {
		return nil, err
	}
	profile, err := fetchClaudeOAuthProfile(ctx, options, credential.AccessToken)
	if err != nil {
		return nil, err
	}
	if err := validateClaudeProfileIdentity(credential, profile); err != nil {
		return nil, err
	}
	if !validClaudeResetIdentifier(profile.Organization.UUID, 128, false) {
		return nil, fmt.Errorf("Claude reset organization is invalid")
	}
	payload, _, err := fetchClaudeUsagePayload(ctx, credential, options)
	if err != nil {
		return nil, err
	}
	resets := payload.CedarEmber
	if resets == nil || !resets.Eligible || !validClaudeResetIdentifier(resets.NextGrantID, 40, true) {
		return nil, ErrClaudeResetCreditUnavailable
	}
	if resets.CooldownUntil != nil && *resets.CooldownUntil != "" {
		until, err := time.Parse(time.RFC3339, *resets.CooldownUntil)
		if err != nil {
			return nil, fmt.Errorf("Claude reset cooldown is invalid")
		}
		if until.After(claudeNow(options)) {
			return nil, ErrClaudeResetCreditUnavailable
		}
	}
	for _, grant := range resets.Grants {
		if grant.ID != resets.NextGrantID || !grant.UsableNow || grant.Paused || grant.Remaining <= 0 {
			continue
		}
		if grant.ExpiresAt != nil && *grant.ExpiresAt != "" {
			expires, err := time.Parse(time.RFC3339, *grant.ExpiresAt)
			if err != nil || !expires.After(claudeNow(options)) {
				return nil, ErrClaudeResetCreditUnavailable
			}
		}
		return json.Marshal(claudeResetCreditRequest{OrganizationID: profile.Organization.UUID, GrantID: grant.ID})
	}
	return nil, ErrClaudeResetCreditUnavailable
}

// ConsumeClaudeResetCredit 只发送一次兑换请求，重试必须复用已保存的选卡参数和请求 ID。
func ConsumeClaudeResetCredit(ctx context.Context, credential ClaudeCredential, prepared []byte, requestID string, options ClaudeOptions) (ClaudeResetCreditResult, error) {
	credential, err := canonicalClaudeCredential(credential)
	if err != nil {
		return ClaudeResetCreditResult{}, err
	}
	var selection claudeResetCreditRequest
	if len(prepared) > 4096 || json.Unmarshal(prepared, &selection) != nil ||
		!validClaudeResetIdentifier(selection.OrganizationID, 128, false) ||
		!validClaudeResetIdentifier(selection.GrantID, 40, true) ||
		!validClaudeResetIdentifier(requestID, 64, false) {
		return ClaudeResetCreditResult{}, fmt.Errorf("Claude reset request is invalid")
	}
	if credential.OrganizationUUID != "" && credential.OrganizationUUID != selection.OrganizationID {
		return ClaudeResetCreditResult{}, ErrClaudeOrganizationIdentityChanged
	}
	payload, err := json.Marshal(struct {
		Program   string `json:"program"`
		GrantID   string `json:"grant_id"`
		RequestID string `json:"request_id"`
	}{Program: "cedar_ember", GrantID: selection.GrantID, RequestID: requestID})
	if err != nil {
		return ClaudeResetCreditResult{}, err
	}
	base := strings.TrimRight(strings.TrimSpace(options.ResetBaseURL), "/")
	if base == "" {
		base = "https://api.anthropic.com"
	}
	endpoint := base + "/api/organizations/" + selection.OrganizationID + "/reset_rate_limits"
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return ClaudeResetCreditResult{}, err
	}
	applyClaudeOAuthHeaders(request)
	request.Header.Set("Authorization", "Bearer "+credential.AccessToken)
	request.Header.Set("Anthropic-Beta", claudeBootstrapBeta)
	request.Header.Set("User-Agent", claudeUsageUserAgent)
	response, err := claudeOAuthClient(options).Do(request)
	if err != nil {
		return ClaudeResetCreditResult{}, err
	}
	defer func() { _ = response.Body.Close() }()
	body, err := readClaudeOAuthBody(response)
	if err != nil {
		return ClaudeResetCreditResult{}, err
	}
	defer clear(body)
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return ClaudeResetCreditResult{}, &ClaudeUpstreamHTTPError{StatusCode: response.StatusCode}
	}
	var result ClaudeResetCreditResult
	if decodeClaudeJSONObject(body, &result) != nil || len(result.Cleared) > maxClaudeScopedLimits {
		return ClaudeResetCreditResult{}, fmt.Errorf("Claude reset response is invalid")
	}
	return result, nil
}

func validClaudeResetIdentifier(value string, limit int, lowercase bool) bool {
	if value == "" || len(value) > limit {
		return false
	}
	for _, char := range value {
		if char >= 'a' && char <= 'z' || char >= '0' && char <= '9' || char == '-' || char == '_' || !lowercase && char >= 'A' && char <= 'Z' {
			continue
		}
		return false
	}
	return true
}
