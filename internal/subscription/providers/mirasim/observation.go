package mirasim

import (
	"encoding/json"
	"strconv"
	"strings"

	providerobservation "gpt-load/internal/subscription/providers/observation"
)

const (
	quotaScopeAccount = "account"
	quotaScopeSurface = "surface"
)

func NormalizeObservation(email, plan string, limits Limits) ([]byte, error) {
	summary := providerobservation.Snapshot{
		Plan:         planSummary(plan, limits.Paid),
		Account:      &providerobservation.AccountSummary{Email: strings.TrimSpace(email)},
		QuotaWindows: make([]providerobservation.QuotaWindow, 0, len(limits.Windows)),
	}
	seen := map[string]int{}
	for _, window := range limits.Windows {
		id := providerobservation.SafeID(window.Name)
		if id == "" {
			continue
		}
		seen[id]++
		if seen[id] > 1 {
			id = id + "-" + strconv.Itoa(seen[id])
		}
		scope := quotaScopeAccount
		if window.ModelScoped {
			scope = quotaScopeSurface
		}
		used := window.Used
		limit := window.Budget
		var utilization *float64
		state := "ok"
		if limit > 0 {
			value := used / limit * 100
			if value < 0 {
				value = 0
			}
			if value > 100 {
				value = 100
			}
			utilization = &value
			switch {
			case value >= 100:
				state = "exhausted"
			case value >= 80:
				state = "warning"
			}
		}
		quota := providerobservation.QuotaWindow{
			ID: id, Label: window.Name, Scope: scope, Unit: "count",
			Used: &used, Limit: &limit, Utilization: utilization, State: state,
		}
		if window.ResetAt != nil {
			reset := window.ResetAt.UTC().UnixMilli()
			quota.ResetAtMS = &reset
		}
		if !window.ModelScoped && !hasPrimary(summary.QuotaWindows) {
			quota.IsPrimary = true
		}
		summary.QuotaWindows = append(summary.QuotaWindows, quota)
	}
	if len(summary.QuotaWindows) > 0 && !hasPrimary(summary.QuotaWindows) {
		summary.QuotaWindows[0].IsPrimary = true
	}
	return json.Marshal(summary)
}

func planSummary(plan string, paid *bool) providerobservation.PlanSummary {
	name := strings.TrimSpace(plan)
	level := providerobservation.PlanLevelStandard
	if paid != nil && !*paid || strings.Contains(strings.ToLower(name), "free") {
		level = providerobservation.PlanLevelFree
	}
	if name == "" {
		if level == providerobservation.PlanLevelFree {
			name = "free"
		} else {
			name = "paid"
		}
	}
	return providerobservation.PlanSummary{Name: name, Level: level}
}

func hasPrimary(windows []providerobservation.QuotaWindow) bool {
	for _, window := range windows {
		if window.IsPrimary {
			return true
		}
	}
	return false
}
