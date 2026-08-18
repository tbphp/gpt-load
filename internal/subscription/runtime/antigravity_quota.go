package subscriptionruntime

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"

	"gpt-load/internal/antigravity"
)

// NormalizeAntigravityObservation creates the same provider-neutral snapshot
// shape used by other subscription channels. Google One AI credits are shown
// as a balance only; quota windows are copied from the upstream quota summary.
func NormalizeAntigravityObservation(email string, observation antigravity.AccountObservation) ([]byte, error) {
	result := quotaSnapshot{
		Plan:         quotaPlanSummary{Name: strings.TrimSpace(observation.PlanID)},
		Account:      &quotaAccountSummary{Email: strings.TrimSpace(email)},
		QuotaWindows: make([]quotaWindow, 0, len(observation.QuotaGroups)+1),
	}
	if credit := observation.GoogleOneAICredits; credit != nil {
		if math.IsNaN(credit.Amount) || math.IsInf(credit.Amount, 0) || credit.Amount < 0 ||
			math.IsNaN(credit.MinimumAmount) || math.IsInf(credit.MinimumAmount, 0) || credit.MinimumAmount < 0 {
			return nil, fmt.Errorf("Antigravity Google One AI credit is invalid")
		}
		remaining := credit.Amount
		state := "available"
		if credit.Amount < credit.MinimumAmount {
			state = "exhausted"
		}
		result.QuotaWindows = append(result.QuotaWindows, quotaWindow{
			ID: "google_one_ai", Label: "Google One AI", Scope: "credits", Unit: "credits",
			Remaining: &remaining, State: state,
		})
	}
	seenIDs := make(map[string]struct{}, len(result.QuotaWindows))
	for _, window := range result.QuotaWindows {
		seenIDs[window.ID] = struct{}{}
	}
	for _, group := range observation.QuotaGroups {
		groupName := strings.TrimSpace(group.DisplayName)
		if groupName == "" {
			groupName = "Antigravity"
		}
		for _, bucket := range group.Buckets {
			window, err := normalizeAntigravityQuotaBucket(groupName, bucket, seenIDs)
			if err != nil {
				return nil, err
			}
			result.QuotaWindows = append(result.QuotaWindows, window)
		}
	}
	if len(result.QuotaWindows) > 0 {
		result.QuotaWindows[0].IsPrimary = true
	}
	return json.Marshal(result)
}

func normalizeAntigravityQuotaBucket(
	groupName string,
	bucket antigravity.QuotaBucket,
	seenIDs map[string]struct{},
) (quotaWindow, error) {
	id := strings.TrimSpace(bucket.ID)
	if id == "" {
		return quotaWindow{}, fmt.Errorf("Antigravity quota bucket ID is missing")
	}
	if _, exists := seenIDs[id]; exists {
		id = groupName + ":" + id
	}
	if _, exists := seenIDs[id]; exists {
		return quotaWindow{}, fmt.Errorf("duplicate Antigravity quota bucket ID %q", id)
	}
	seenIDs[id] = struct{}{}
	if bucket.RemainingFraction == nil || math.IsNaN(*bucket.RemainingFraction) || math.IsInf(*bucket.RemainingFraction, 0) {
		return quotaWindow{}, fmt.Errorf("Antigravity quota bucket %q has no remaining fraction", id)
	}
	remainingFraction := math.Max(0, math.Min(1, *bucket.RemainingFraction))
	remaining := remainingFraction * 100
	used := 100 - remaining
	limit := 100.0
	utilization := used / 100
	state := "available"
	if remaining <= 0 {
		state = "exhausted"
	}
	label := strings.TrimSpace(bucket.DisplayName)
	if label == "" {
		label = id
	}
	if groupName != "" {
		label = groupName + " · " + label
	}
	window := quotaWindow{
		ID: id, Label: label, Scope: "account", Unit: "percent", State: state,
		Used: &used, Limit: &limit, Remaining: &remaining, Utilization: &utilization,
	}
	if seconds := antigravityQuotaWindowSeconds(bucket.Window); seconds > 0 {
		window.WindowSeconds = &seconds
	}
	if resetAt := antigravityQuotaResetAt(bucket.ResetTime); resetAt > 0 {
		window.ResetAtMS = &resetAt
	}
	return window, nil
}

func antigravityQuotaWindowSeconds(raw string) int64 {
	raw = strings.ToLower(strings.TrimSpace(raw))
	if raw == "weekly" {
		return 7 * 24 * 60 * 60
	}
	duration, err := time.ParseDuration(raw)
	if err != nil || duration <= 0 {
		return 0
	}
	return int64(duration / time.Second)
}

func antigravityQuotaResetAt(raw string) int64 {
	resetAt, err := time.Parse(time.RFC3339, strings.TrimSpace(raw))
	if err != nil {
		return 0
	}
	return resetAt.UnixMilli()
}
