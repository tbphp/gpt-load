package codex

import (
	"bytes"
	"encoding/json"
	"errors"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	providerobservation "gpt-load/internal/subscription/providers/observation"
)

type quotaPlanSummary = providerobservation.PlanSummary
type quotaAccountSummary = providerobservation.AccountSummary
type quotaWindow = providerobservation.QuotaWindow
type resetCredit = providerobservation.ResetCredit
type quotaSnapshot = providerobservation.Snapshot

// NormalizeQuota converts Codex provider payloads into the canonical,
// provider-neutral observation contract consumed by the control plane.
func NormalizeQuota(primary, details []byte) ([]byte, error) {
	decoder := json.NewDecoder(bytes.NewReader(primary))
	decoder.UseNumber()
	var payload map[string]any
	if err := decoder.Decode(&payload); err != nil || payload == nil {
		return nil, errors.New("invalid subscription quota observation")
	}
	result := quotaSnapshot{QuotaWindows: []quotaWindow{}}
	result.Plan = codexPlan(cleanString(firstValue(payload, "plan_type", "planType")))
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
			windows := normalizeRateWindows(rate, providerobservation.SafeID(name)+"-", name)
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
		item := quotaWindow{
			ID: prefix + name, Label: windowLabel(window, name, scope), LabelKey: codexWindowLabelKey(name, scope),
			Scope: scope, Unit: "percent", State: "unknown",
		}
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

func codexWindowLabelKey(fallback, scope string) string {
	if scope != "account" {
		return ""
	}
	switch fallback {
	case "primary":
		return providerobservation.QuotaLabelSession
	case "secondary":
		return providerobservation.QuotaLabelWeekly
	default:
		return ""
	}
}

func windowLabel(window map[string]any, fallback, scope string) string {
	seconds, _ := integer(firstValue(window, "limit_window_seconds", "limitWindowSeconds"))
	if scope == "account" {
		if period := providerobservation.PeriodLabel(seconds); period != "" {
			return period
		}
		subject := providerobservation.DisplayName(fallback)
		switch seconds {
		case 5 * 60 * 60:
			subject = "Session"
		case 7 * 24 * 60 * 60:
			subject = "Weekly"
		default:
			switch fallback {
			case "primary":
				subject = "Session"
			case "secondary":
				subject = "Weekly"
			}
		}
		return providerobservation.WindowLabel(subject, seconds)
	}
	subject := providerobservation.DisplayName(scope)
	if subject == "" {
		subject = providerobservation.DisplayName(fallback)
	}
	return providerobservation.WindowLabel(subject, seconds)
}

func codexPlan(value string) quotaPlanSummary {
	normalized := strings.NewReplacer("_", "", "-", "", " ", "").Replace(strings.ToLower(strings.TrimSpace(value)))
	switch normalized {
	case "pro":
		return quotaPlanSummary{Name: "Pro 20x", Level: providerobservation.PlanLevelElite}
	case "prolite":
		return quotaPlanSummary{Name: "Pro 5x", Level: providerobservation.PlanLevelPremium}
	case "free":
		return quotaPlanSummary{Name: "Free", Level: providerobservation.PlanLevelFree}
	case "plus":
		return quotaPlanSummary{Name: "Plus", Level: providerobservation.PlanLevelStandard}
	case "edu", "education":
		return quotaPlanSummary{Name: "Education", Level: providerobservation.PlanLevelStandard}
	case "team", "business":
		return quotaPlanSummary{Name: providerobservation.DisplayName(normalized), Level: providerobservation.PlanLevelPremium}
	case "enterprise":
		return quotaPlanSummary{Name: "Enterprise", Level: providerobservation.PlanLevelElite}
	default:
		return quotaPlanSummary{Name: providerobservation.DisplayName(value)}
	}
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
