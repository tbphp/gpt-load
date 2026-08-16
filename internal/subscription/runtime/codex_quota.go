package subscriptionruntime

import (
	"bytes"
	"encoding/json"
	"errors"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"
)

type quotaPlanSummary struct {
	Name string `json:"name,omitempty"`
}

type quotaAccountSummary struct {
	DisplayName               string `json:"display_name,omitempty"`
	Email                     string `json:"email,omitempty"`
	OrganizationName          string `json:"organization_name,omitempty"`
	OrganizationType          string `json:"organization_type,omitempty"`
	OrganizationRole          string `json:"organization_role,omitempty"`
	WorkspaceRole             string `json:"workspace_role,omitempty"`
	OrganizationRateLimitTier string `json:"organization_rate_limit_tier,omitempty"`
	UserRateLimitTier         string `json:"user_rate_limit_tier,omitempty"`
	SeatTier                  string `json:"seat_tier,omitempty"`
	BillingType               string `json:"billing_type,omitempty"`
	ExtraUsageEnabled         *bool  `json:"extra_usage_enabled,omitempty"`
	ExtraUsageDisabledReason  string `json:"extra_usage_disabled_reason,omitempty"`
	AccountCreatedAtMS        *int64 `json:"account_created_at_ms,omitempty"`
	SubscriptionCreatedAtMS   *int64 `json:"subscription_created_at_ms,omitempty"`
}

type quotaWindow struct {
	ID            string   `json:"id"`
	Label         string   `json:"label"`
	Scope         string   `json:"scope"`
	Unit          string   `json:"unit"`
	Used          *float64 `json:"used,omitempty"`
	Limit         *float64 `json:"limit,omitempty"`
	Remaining     *float64 `json:"remaining,omitempty"`
	Utilization   *float64 `json:"utilization,omitempty"`
	ResetAtMS     *int64   `json:"reset_at_ms,omitempty"`
	WindowSeconds *int64   `json:"window_seconds,omitempty"`
	ModelIDs      []string `json:"model_ids,omitempty"`
	State         string   `json:"state"`
	IsPrimary     bool     `json:"is_primary,omitempty"`
}

type resetCredit struct {
	ExpiresAtMS int64 `json:"expires_at_ms"`
}

type quotaSnapshot struct {
	Plan                  quotaPlanSummary     `json:"plan_summary"`
	Account               *quotaAccountSummary `json:"account_summary,omitempty"`
	QuotaWindows          []quotaWindow        `json:"quota_windows"`
	ResetCreditsAvailable *int64               `json:"reset_credits_available,omitempty"`
	ResetCredits          []resetCredit        `json:"reset_credits,omitempty"`
}

// NormalizeCodexQuota converts Codex provider payloads into the canonical,
// provider-neutral observation contract consumed by the control plane.
func NormalizeCodexQuota(primary, details []byte) ([]byte, error) {
	decoder := json.NewDecoder(bytes.NewReader(primary))
	decoder.UseNumber()
	var payload map[string]any
	if err := decoder.Decode(&payload); err != nil || payload == nil {
		return nil, errors.New("invalid subscription quota observation")
	}
	result := quotaSnapshot{QuotaWindows: []quotaWindow{}}
	result.Plan.Name = cleanString(firstValue(payload, "plan_type", "planType"))
	if credits, ok := object(firstValue(payload, "rate_limit_reset_credits", "rateLimitResetCredits")); ok {
		if value, ok := integer(firstValue(credits, "available_count", "availableCount")); ok {
			result.ResetCreditsAvailable = &value
		}
	}
	if rate, ok := object(firstValue(payload, "rate_limit", "rateLimit")); ok {
		result.QuotaWindows = append(result.QuotaWindows, normalizeRateWindows(rate, "", "account")...)
	}
	if additional, ok := firstValue(payload, "additional_rate_limits", "additionalRateLimits").([]any); ok {
		for index, rawEntry := range additional {
			entry, ok := object(rawEntry)
			if !ok {
				continue
			}
			name := cleanString(firstValue(entry, "limit_name", "limitName", "metered_feature", "meteredFeature"))
			if name == "" {
				name = "additional-" + strconv.Itoa(index+1)
			}
			rate, ok := object(firstValue(entry, "rate_limit", "rateLimit"))
			if !ok {
				continue
			}
			windows := normalizeRateWindows(rate, safeID(name)+"-", name)
			modelIDs := stringsFrom(firstValue(entry, "model_ids", "modelIds"))
			for windowIndex := range windows {
				windows[windowIndex].ModelIDs = append([]string(nil), modelIDs...)
			}
			result.QuotaWindows = append(result.QuotaWindows, windows...)
		}
	}
	sort.SliceStable(result.QuotaWindows, func(i, j int) bool {
		left, right := result.QuotaWindows[i], result.QuotaWindows[j]
		if (left.State == "exhausted") != (right.State == "exhausted") {
			return left.State == "exhausted"
		}
		leftUsed, rightUsed := -1.0, -1.0
		if left.Utilization != nil {
			leftUsed = *left.Utilization
		}
		if right.Utilization != nil {
			rightUsed = *right.Utilization
		}
		if leftUsed != rightUsed {
			return leftUsed > rightUsed
		}
		leftReset, rightReset := int64(math.MaxInt64), int64(math.MaxInt64)
		if left.ResetAtMS != nil {
			leftReset = *left.ResetAtMS
		}
		if right.ResetAtMS != nil {
			rightReset = *right.ResetAtMS
		}
		if leftReset != rightReset {
			return leftReset < rightReset
		}
		return left.ID < right.ID
	})
	if len(result.QuotaWindows) > 0 {
		result.QuotaWindows[0].IsPrimary = true
	}
	if count, credits, present, err := normalizeResetCreditDetails(details); err == nil {
		if count != nil {
			result.ResetCreditsAvailable = count
		}
		if present {
			result.ResetCredits = credits
		}
	}
	return json.Marshal(result)
}

func normalizeRateWindows(rate map[string]any, prefix, scope string) []quotaWindow {
	result := make([]quotaWindow, 0, 2)
	for _, name := range []string{"primary", "secondary"} {
		window, ok := object(firstValue(rate, name+"_window", name+"Window"))
		if !ok {
			continue
		}
		item := quotaWindow{ID: prefix + name, Label: windowLabel(window, name, scope), Scope: scope, Unit: "percent", State: "unknown"}
		if used, ok := number(firstValue(window, "used_percent", "usedPercent")); ok {
			used = math.Max(0, math.Min(100, used))
			limit, remaining, utilization := 100.0, 100-used, used/100
			item.Used, item.Limit, item.Remaining, item.Utilization = &used, &limit, &remaining, &utilization
			if used >= 100 {
				item.State = "exhausted"
			} else {
				item.State = "available"
			}
		}
		if allowed, ok := firstValue(rate, "allowed").(bool); ok && !allowed {
			item.State = "exhausted"
		}
		if reached, ok := firstValue(rate, "limit_reached", "limitReached").(bool); ok && reached {
			item.State = "exhausted"
		}
		if seconds, ok := integer(firstValue(window, "limit_window_seconds", "limitWindowSeconds")); ok {
			item.WindowSeconds = &seconds
		}
		if reset, ok := integer(firstValue(window, "reset_at", "resetAt")); ok {
			value := reset * 1000
			item.ResetAtMS = &value
		}
		result = append(result, item)
	}
	return result
}

func windowLabel(window map[string]any, fallback, scope string) string {
	if value := cleanString(firstValue(window, "label", "display_name", "displayName")); value != "" {
		return value
	}
	if seconds, ok := integer(firstValue(window, "limit_window_seconds", "limitWindowSeconds")); ok {
		duration := strconv.FormatInt(seconds, 10) + "s"
		switch seconds {
		case 5 * 60 * 60:
			duration = "5h"
		case 7 * 24 * 60 * 60:
			duration = "7d"
		}
		if scope != "account" {
			return scope + " · " + duration
		}
		return duration
	}
	if scope != "account" {
		return scope + " · " + fallback
	}
	return fallback
}

type resetCreditDetail struct {
	ExpiresAt      string `json:"expires_at"`
	ExpiresAtCamel string `json:"expiresAt"`
	ResetType      string `json:"reset_type"`
	ResetTypeCamel string `json:"resetType"`
	Status         string `json:"status"`
}

type resetCreditEnvelope struct {
	AvailableCount        json.RawMessage `json:"available_count"`
	AvailableCountCamel   json.RawMessage `json:"availableCount"`
	Credits               json.RawMessage `json:"credits"`
	RateLimitResetCredits json.RawMessage `json:"rate_limit_reset_credits"`
	Items                 json.RawMessage `json:"items"`
	Data                  json.RawMessage `json:"data"`
}

func normalizeResetCreditDetails(raw []byte) (*int64, []resetCredit, bool, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil, nil, false, nil
	}
	var details []*resetCreditDetail
	var count *int64
	present := false
	if trimmed[0] == '[' {
		if err := json.Unmarshal(trimmed, &details); err != nil {
			return nil, nil, false, err
		}
		present = true
	} else {
		var envelope resetCreditEnvelope
		if err := json.Unmarshal(trimmed, &envelope); err != nil {
			return nil, nil, false, err
		}
		count = resetCount(envelope.AvailableCount, envelope.AvailableCountCamel)
		for _, candidate := range []json.RawMessage{envelope.Credits, envelope.RateLimitResetCredits, envelope.Items, envelope.Data} {
			candidate = bytes.TrimSpace(candidate)
			if len(candidate) == 0 || bytes.Equal(candidate, []byte("null")) {
				continue
			}
			if err := json.Unmarshal(candidate, &details); err != nil {
				return count, nil, false, err
			}
			present = true
			break
		}
	}
	available := int64(0)
	credits := make([]resetCredit, 0, len(details))
	for _, detail := range details {
		if detail == nil {
			continue
		}
		resetType := strings.TrimSpace(detail.ResetType)
		if resetType == "" {
			resetType = strings.TrimSpace(detail.ResetTypeCamel)
		}
		if resetType != "" && !strings.EqualFold(resetType, "codex_rate_limits") {
			continue
		}
		if status := strings.TrimSpace(detail.Status); status != "" && !strings.EqualFold(status, "available") {
			continue
		}
		available++
		expires := strings.TrimSpace(detail.ExpiresAt)
		if expires == "" {
			expires = strings.TrimSpace(detail.ExpiresAtCamel)
		}
		parsed, err := time.Parse(time.RFC3339, expires)
		if err == nil {
			credits = append(credits, resetCredit{ExpiresAtMS: parsed.UnixMilli()})
		}
	}
	if count == nil && present {
		count = &available
	}
	sort.Slice(credits, func(i, j int) bool { return credits[i].ExpiresAtMS < credits[j].ExpiresAtMS })
	return count, credits, present, nil
}

func resetCount(values ...json.RawMessage) *int64 {
	for _, value := range values {
		trimmed := bytes.TrimSpace(value)
		if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
			continue
		}
		var count int64
		if trimmed[0] == '"' {
			var text string
			if json.Unmarshal(trimmed, &text) != nil {
				continue
			}
			parsed, err := strconv.ParseInt(strings.TrimSpace(text), 10, 64)
			if err != nil {
				continue
			}
			count = parsed
		} else if json.Unmarshal(trimmed, &count) != nil {
			continue
		}
		if count >= 0 {
			return &count
		}
	}
	return nil
}

func firstValue(values map[string]any, keys ...string) any {
	for _, key := range keys {
		if value, ok := values[key]; ok {
			return value
		}
	}
	return nil
}

func object(value any) (map[string]any, bool) {
	result, ok := value.(map[string]any)
	return result, ok
}
func cleanString(value any) string { result, _ := value.(string); return strings.TrimSpace(result) }

func number(value any) (float64, bool) {
	switch typed := value.(type) {
	case json.Number:
		result, err := typed.Float64()
		return result, err == nil
	case float64:
		return typed, true
	case int:
		return float64(typed), true
	default:
		return 0, false
	}
}

func integer(value any) (int64, bool) {
	result, ok := number(value)
	if !ok || math.Trunc(result) != result || result < 0 || result > math.MaxInt64 {
		return 0, false
	}
	return int64(result), true
}

func stringsFrom(value any) []string {
	values, ok := value.([]any)
	if !ok {
		return nil
	}
	result := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, raw := range values {
		value, ok := raw.(string)
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, duplicate := seen[value]; duplicate {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func safeID(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var result strings.Builder
	for _, character := range value {
		if character >= 'a' && character <= 'z' || character >= '0' && character <= '9' {
			result.WriteRune(character)
		} else if result.Len() > 0 && !strings.HasSuffix(result.String(), "-") {
			result.WriteByte('-')
		}
	}
	return strings.Trim(result.String(), "-")
}
