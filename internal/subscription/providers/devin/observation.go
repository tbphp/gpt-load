package devin

import (
	"encoding/json"
	"fmt"
	"time"

	cpaembedded "github.com/router-for-me/CLIProxyAPI/v8/gptload-embedded/embedded"

	providerobservation "gpt-load/internal/subscription/providers/observation"
)

func normalizeObservation(value cpaembedded.DevinAccountObservation) ([]byte, error) {
	result := providerobservation.Snapshot{
		Plan:         providerobservation.PlanSummary{Name: value.Credential.Plan},
		Account:      &providerobservation.AccountSummary{Email: value.Credential.Email, DisplayName: value.Credential.UserName},
		QuotaWindows: make([]providerobservation.QuotaWindow, 0, 2),
	}
	if !value.PlanEnd.IsZero() {
		end := value.PlanEnd.UnixMilli()
		result.Subscription = &providerobservation.SubscriptionSummary{State: "confirmed", PeriodEndMS: &end}
	}
	for _, item := range []struct {
		id, label string
		seconds   int64
		remaining *int64
		reset     time.Time
	}{
		{"daily", "24h", 86400, value.DailyRemaining, value.DailyResetAt},
		{"weekly", "7d", 604800, value.WeeklyRemaining, value.WeeklyResetAt},
	} {
		seconds := item.seconds
		window := providerobservation.QuotaWindow{ID: item.id, Label: item.label, Scope: item.id, Unit: "percent", State: "unknown", WindowSeconds: &seconds, IsPrimary: item.id == "daily"}
		if item.id == "weekly" {
			window.LabelKey = providerobservation.QuotaLabelWeekly
		}
		if !item.reset.IsZero() {
			reset := item.reset.UnixMilli()
			window.ResetAtMS = &reset
		}
		if item.remaining != nil {
			if *item.remaining < 0 || *item.remaining > 100 {
				return nil, fmt.Errorf("invalid Devin remaining percentage")
			}
			remaining, limit := float64(*item.remaining), float64(100)
			used := limit - remaining
			utilization := used / limit
			window.Remaining, window.Limit, window.Used, window.Utilization = &remaining, &limit, &used, &utilization
			window.State = "available"
			if remaining == 0 {
				window.State = "exhausted"
			}
		}
		result.QuotaWindows = append(result.QuotaWindows, window)
	}
	return json.Marshal(result)
}
