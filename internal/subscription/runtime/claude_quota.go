package subscriptionruntime

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"gpt-load/internal/claude"
)

func NormalizeClaudeObservation(observation claude.AccountObservation) ([]byte, error) {
	account, err := normalizeClaudeAccount(observation.Profile, observation.Usage.ExtraUsage)
	if err != nil {
		return nil, err
	}
	result := quotaSnapshot{
		Plan:         quotaPlanSummary{Name: claudePlanName(observation.Profile)},
		Account:      account,
		QuotaWindows: make([]quotaWindow, 0, 7+len(observation.Usage.Limits)),
	}
	seenWindowIDs := make(map[string]int, cap(result.QuotaWindows))

	for _, candidate := range []struct {
		id            string
		label         string
		windowSeconds int64
		primary       bool
		value         *claude.UsageWindow
	}{
		{id: "five_hour", label: "5h", windowSeconds: 5 * 60 * 60, primary: true, value: observation.Usage.FiveHour},
		{id: "seven_day", label: "7d", windowSeconds: 7 * 24 * 60 * 60, value: observation.Usage.SevenDay},
		{id: "seven_day_oauth_apps", label: "7d · OAuth apps", windowSeconds: 7 * 24 * 60 * 60, value: observation.Usage.SevenDayOAuthApps},
		{id: "seven_day_opus", label: "7d · Opus", windowSeconds: 7 * 24 * 60 * 60, value: observation.Usage.SevenDayOpus},
		{id: "seven_day_sonnet", label: "7d · Sonnet", windowSeconds: 7 * 24 * 60 * 60, value: observation.Usage.SevenDaySonnet},
		{id: "cinder_cove", label: "Cinder Cove", value: observation.Usage.CinderCove},
	} {
		if candidate.value == nil {
			continue
		}
		window, err := normalizeClaudePercentageWindow(
			candidate.id,
			candidate.label,
			claudeWindowScope(candidate.id),
			candidate.windowSeconds,
			candidate.primary,
			candidate.value.Utilization,
			candidate.value.ResetsAt,
		)
		if err != nil {
			return nil, err
		}
		appendClaudeQuotaWindow(&result.QuotaWindows, seenWindowIDs, window)
	}

	if extra := observation.Usage.ExtraUsage; extra != nil {
		window, err := normalizeClaudeExtraUsage(extra)
		if err != nil {
			return nil, err
		}
		appendClaudeQuotaWindow(&result.QuotaWindows, seenWindowIDs, window)
	}
	for _, limit := range observation.Usage.Limits {
		window, err := normalizeClaudeScopedLimit(limit)
		if err != nil {
			return nil, err
		}
		if duplicateClaudeStandardWindow(result.QuotaWindows, window) {
			continue
		}
		appendClaudeQuotaWindow(&result.QuotaWindows, seenWindowIDs, window)
	}

	sort.SliceStable(result.QuotaWindows, func(i, j int) bool {
		left, right := result.QuotaWindows[i], result.QuotaWindows[j]
		if left.IsPrimary != right.IsPrimary {
			return left.IsPrimary
		}
		leftSeconds, rightSeconds := int64(math.MaxInt64), int64(math.MaxInt64)
		if left.WindowSeconds != nil {
			leftSeconds = *left.WindowSeconds
		}
		if right.WindowSeconds != nil {
			rightSeconds = *right.WindowSeconds
		}
		if leftSeconds != rightSeconds {
			return leftSeconds < rightSeconds
		}
		return left.ID < right.ID
	})
	return json.Marshal(result)
}

func duplicateClaudeStandardWindow(existing []quotaWindow, candidate quotaWindow) bool {
	targetID := ""
	switch safeID(candidate.Label) {
	case "session":
		targetID = "five_hour"
	case "weekly":
		targetID = "seven_day"
	default:
		return false
	}
	for _, window := range existing {
		if window.ID == targetID && equalOptionalFloat(window.Utilization, candidate.Utilization) &&
			equalOptionalInt64(window.ResetAtMS, candidate.ResetAtMS) {
			return true
		}
	}
	return false
}

func equalOptionalFloat(left, right *float64) bool {
	return left == nil && right == nil || left != nil && right != nil && *left == *right
}

func equalOptionalInt64(left, right *int64) bool {
	return left == nil && right == nil || left != nil && right != nil && *left == *right
}

func appendClaudeQuotaWindow(windows *[]quotaWindow, seen map[string]int, window quotaWindow) {
	base, candidate := window.ID, window.ID
	for suffix := 2; seen[candidate] > 0; suffix++ {
		candidate = fmt.Sprintf("%s_%d", base, suffix)
	}
	seen[candidate] = 1
	window.ID = candidate
	*windows = append(*windows, window)
}

func normalizeClaudeAccount(profile claude.AccountProfile, extraUsage *claude.ExtraUsage) (*quotaAccountSummary, error) {
	result := &quotaAccountSummary{
		DisplayName: strings.TrimSpace(profile.DisplayName), Email: strings.TrimSpace(profile.Email),
		OrganizationName:          strings.TrimSpace(profile.OrganizationName),
		OrganizationType:          strings.TrimSpace(profile.OrganizationType),
		OrganizationRole:          strings.TrimSpace(profile.OrganizationRole),
		WorkspaceRole:             strings.TrimSpace(profile.WorkspaceRole),
		OrganizationRateLimitTier: strings.TrimSpace(profile.OrganizationRateLimitTier),
		UserRateLimitTier:         strings.TrimSpace(profile.UserRateLimitTier),
		SeatTier:                  strings.TrimSpace(profile.SeatTier), BillingType: strings.TrimSpace(profile.BillingType),
		ExtraUsageEnabled: cloneBoolPointer(profile.ExtraUsageEnabled),
	}
	if extraUsage != nil {
		if extraUsage.Enabled != nil {
			result.ExtraUsageEnabled = cloneBoolPointer(extraUsage.Enabled)
		}
		if extraUsage.DisabledReason != nil {
			result.ExtraUsageDisabledReason = strings.TrimSpace(*extraUsage.DisabledReason)
		}
	}
	var err error
	if result.AccountCreatedAtMS, err = claudeTimeMilliseconds("account creation", profile.AccountCreatedAt); err != nil {
		return nil, err
	}
	if result.SubscriptionCreatedAtMS, err = claudeTimeMilliseconds("subscription creation", profile.SubscriptionCreatedAt); err != nil {
		return nil, err
	}
	return result, nil
}

func claudePlanName(profile claude.AccountProfile) string {
	for _, value := range []string{profile.OrganizationType, profile.SeatTier} {
		normalized := strings.NewReplacer("-", "_", " ", "_").Replace(strings.ToLower(strings.TrimSpace(value)))
		switch {
		case strings.Contains(normalized, "enterprise"):
			return "Claude Enterprise"
		case strings.Contains(normalized, "team"):
			return "Claude Team"
		case strings.Contains(normalized, "max"):
			return "Claude Max"
		case strings.Contains(normalized, "pro"):
			return "Claude Pro"
		case strings.Contains(normalized, "free"):
			return "Claude Free"
		}
	}
	return ""
}

func claudeWindowScope(id string) string {
	switch id {
	case "five_hour", "seven_day":
		return "account"
	case "seven_day_opus", "seven_day_sonnet":
		return "model"
	default:
		return "surface"
	}
}

func normalizeClaudePercentageWindow(
	id, label, scope string,
	windowSeconds int64,
	primary bool,
	percent *float64,
	reset *string,
) (quotaWindow, error) {
	result := quotaWindow{
		ID: id, Label: label, Scope: scope, Unit: "percent", State: "unknown", IsPrimary: primary,
	}
	if windowSeconds > 0 {
		result.WindowSeconds = floatlessIntPointer(windowSeconds)
	}
	if percent != nil {
		if math.IsNaN(*percent) || math.IsInf(*percent, 0) || *percent < 0 || *percent > 100 {
			return quotaWindow{}, fmt.Errorf("Claude %s utilization is invalid", id)
		}
		used, limit, remaining, utilization := *percent, 100.0, math.Max(0, 100-*percent), *percent/100
		result.Used, result.Limit, result.Remaining, result.Utilization = &used, &limit, &remaining, &utilization
		result.State = "available"
		if *percent >= 100 {
			result.State = "exhausted"
		}
	}
	resetAtMS, err := claudeOptionalResetMilliseconds(id, reset)
	if err != nil {
		return quotaWindow{}, err
	}
	result.ResetAtMS = resetAtMS
	return result, nil
}

func normalizeClaudeExtraUsage(extra *claude.ExtraUsage) (quotaWindow, error) {
	result := quotaWindow{ID: "extra_usage", Label: "Extra usage", Scope: "extra_usage", Unit: "credits", State: "unknown"}
	if extra.Currency != nil && strings.TrimSpace(*extra.Currency) != "" {
		result.Unit = strings.ToUpper(strings.TrimSpace(*extra.Currency))
	}
	if extra.MonthlyLimit != nil {
		limit := *extra.MonthlyLimit
		if math.IsNaN(limit) || math.IsInf(limit, 0) || limit < 0 {
			return quotaWindow{}, fmt.Errorf("Claude extra usage limit is invalid")
		}
		result.Limit = &limit
	}
	if extra.UsedCredits != nil {
		used := *extra.UsedCredits
		if math.IsNaN(used) || math.IsInf(used, 0) || used < 0 {
			return quotaWindow{}, fmt.Errorf("Claude extra usage used credits are invalid")
		}
		result.Used = &used
	}
	if result.Limit != nil && result.Used != nil {
		remaining := math.Max(0, *result.Limit-*result.Used)
		result.Remaining = &remaining
	}
	if extra.Utilization != nil {
		if math.IsNaN(*extra.Utilization) || math.IsInf(*extra.Utilization, 0) || *extra.Utilization < 0 || *extra.Utilization > 100 {
			return quotaWindow{}, fmt.Errorf("Claude extra usage utilization is invalid")
		}
		utilization := *extra.Utilization / 100
		result.Utilization = &utilization
	} else if result.Limit != nil && *result.Limit > 0 && result.Used != nil {
		utilization := math.Min(1, *result.Used / *result.Limit)
		result.Utilization = &utilization
	}
	if extra.Enabled != nil && *extra.Enabled && (result.Limit != nil || result.Utilization != nil) {
		result.State = "available"
		if result.Remaining != nil && *result.Remaining <= 0 || result.Utilization != nil && *result.Utilization >= 1 {
			result.State = "exhausted"
		}
	}
	return result, nil
}

func normalizeClaudeScopedLimit(limit claude.ScopedLimit) (quotaWindow, error) {
	kind, group := safeID(limit.Kind), safeID(limit.Group)
	if kind == "" || group == "" {
		return quotaWindow{}, fmt.Errorf("Claude scoped limit identifier is invalid")
	}
	id := kind + "_" + group
	label := strings.TrimSpace(limit.ModelDisplayName)
	scope := "model"
	if label == "" {
		label = strings.TrimSpace(limit.SurfaceDisplayName)
		scope = "surface"
	}
	if label == "" {
		label = strings.TrimSpace(limit.Group)
	}
	if label == "" {
		label = id
	}
	window, err := normalizeClaudePercentageWindow(id, label, scope, 0, false, &limit.Percent, limit.ResetsAt)
	if err != nil {
		return quotaWindow{}, err
	}
	return window, nil
}

func claudeOptionalResetMilliseconds(field string, value *string) (*int64, error) {
	if value == nil || strings.TrimSpace(*value) == "" {
		return nil, nil
	}
	parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(*value))
	if err != nil {
		return nil, fmt.Errorf("Claude %s reset time is invalid", field)
	}
	result := parsed.UnixMilli()
	return &result, nil
}

func claudeTimeMilliseconds(field, value string) (*int64, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, nil
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return nil, fmt.Errorf("Claude %s time is invalid", field)
	}
	result := parsed.UnixMilli()
	return &result, nil
}

func cloneBoolPointer(value *bool) *bool {
	if value == nil {
		return nil
	}
	result := *value
	return &result
}

func boolPointerValue(value bool) *bool { return &value }

func floatlessIntPointer(value int64) *int64 { return &value }
