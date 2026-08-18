package grok

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"

	providerobservation "gpt-load/internal/subscription/providers/observation"
)

const (
	quotaScopeAccount = "account"
	quotaScopeSurface = "surface"
	quotaScopeCredits = "credits"
)

func NormalizeObservation(email string, observation AccountObservation) ([]byte, error) {
	result := providerobservation.Snapshot{
		Plan:         grokPlan(observation),
		Account:      &providerobservation.AccountSummary{Email: strings.TrimSpace(email)},
		QuotaWindows: make([]providerobservation.QuotaWindow, 0, 4+len(observation.Billing.ProductUsage)),
	}
	_, periodEnd, windowSeconds, err := grokPeriod(
		observation.Billing.PeriodStart,
		observation.Billing.PeriodEnd,
	)
	if err != nil {
		return nil, err
	}
	if observation.AccountQuotaObserved && (observation.Billing.UsagePercent != nil || periodEnd != nil) {
		window, err := grokPercentWindow(
			"weekly", providerobservation.WindowLabel("Weekly", windowSeconds), quotaScopeAccount,
			observation.Billing.UsagePercent, periodEnd, windowSeconds,
		)
		if err != nil {
			return nil, err
		}
		window.IsPrimary = true
		result.QuotaWindows = append(result.QuotaWindows, window)
	}
	seenProducts := make(map[string]int, len(observation.Billing.ProductUsage))
	for index, product := range observation.Billing.ProductUsage {
		name := grokProductName(product.Product)
		if name == "" {
			return nil, fmt.Errorf("Grok product usage name is invalid")
		}
		id := providerobservation.SafeID(product.Product)
		if id == "" {
			id = fmt.Sprintf("product-%d", index+1)
		}
		seenProducts[id]++
		if seenProducts[id] > 1 {
			id = fmt.Sprintf("%s-%d", id, seenProducts[id])
		}
		window, err := grokPercentWindow(
			id, providerobservation.WindowLabel(name, windowSeconds), quotaScopeSurface,
			product.UsagePercent, periodEnd, windowSeconds,
		)
		if err != nil {
			return nil, err
		}
		result.QuotaWindows = append(result.QuotaWindows, window)
	}
	_, billingEnd, billingSeconds, err := grokPeriod(
		observation.Billing.BillingPeriodStart,
		observation.Billing.BillingPeriodEnd,
	)
	if err != nil {
		return nil, err
	}
	if observation.CreditQuotaObserved {
		if window := grokCreditWindow(
			"included_usage", "Included usage", observation.Billing.UsedCents,
			observation.Billing.MonthlyLimitCents, billingEnd, billingSeconds,
		); window != nil {
			result.QuotaWindows = append(result.QuotaWindows, *window)
		}
		if window := grokCreditWindow(
			"pay_as_you_go", "Pay as you go", observation.Billing.OnDemandUsedCents,
			observation.Billing.OnDemandCapCents, billingEnd, billingSeconds,
		); window != nil {
			result.QuotaWindows = append(result.QuotaWindows, *window)
		}
	}
	if len(result.QuotaWindows) > 0 && !result.QuotaWindows[0].IsPrimary {
		result.QuotaWindows[0].IsPrimary = true
	}
	return json.Marshal(result)
}

func grokProductName(value string) string {
	switch providerobservation.SafeID(value) {
	case "grokbuild", "grok-build":
		return "Grok Build"
	case "grokchat", "grok-chat":
		return "Grok Chat"
	default:
		return providerobservation.DisplayName(value)
	}
}

func grokPlan(observation AccountObservation) providerobservation.PlanSummary {
	if value := observation.Billing.MonthlyLimitCents; value != nil {
		switch *value {
		case 0:
			if observation.Billing.OnDemandCapCents == nil || *observation.Billing.OnDemandCapCents <= 0 {
				return providerobservation.PlanSummary{Name: "Free", Level: providerobservation.PlanLevelFree}
			}
			return providerobservation.PlanSummary{Name: "Grok Paid", Level: providerobservation.PlanLevelStandard}
		case 15000:
			return providerobservation.PlanSummary{Name: "SuperGrok", Level: providerobservation.PlanLevelPremium}
		case 150000:
			return providerobservation.PlanSummary{Name: "SuperGrok Heavy", Level: providerobservation.PlanLevelElite}
		default:
			if *value > 0 {
				return providerobservation.PlanSummary{Name: "Grok Paid", Level: providerobservation.PlanLevelStandard}
			}
		}
	}
	if observation.Tier != nil {
		if *observation.Tier >= 1 {
			return providerobservation.PlanSummary{Name: "Grok Paid", Level: providerobservation.PlanLevelStandard}
		}
		return providerobservation.PlanSummary{Name: "Free", Level: providerobservation.PlanLevelFree}
	}
	return providerobservation.PlanSummary{}
}

func grokPercentWindow(
	id, label, scope string,
	usedPercent *float64,
	resetAt *int64,
	windowSeconds int64,
) (providerobservation.QuotaWindow, error) {
	window := providerobservation.QuotaWindow{ID: id, Label: label, Scope: scope, Unit: "percent", State: "unknown"}
	if usedPercent != nil {
		if math.IsNaN(*usedPercent) || math.IsInf(*usedPercent, 0) || *usedPercent < 0 || *usedPercent > 100 {
			return providerobservation.QuotaWindow{}, fmt.Errorf("Grok utilization is invalid")
		}
		used, limit, remaining := *usedPercent, 100.0, 100-*usedPercent
		utilization := *usedPercent / 100
		window.Used, window.Limit, window.Remaining, window.Utilization = &used, &limit, &remaining, &utilization
		window.State = "available"
		if remaining <= 0 {
			window.State = "exhausted"
		}
	}
	if resetAt != nil {
		window.ResetAtMS = resetAt
	}
	if windowSeconds > 0 {
		window.WindowSeconds = &windowSeconds
	}
	return window, nil
}

func grokCreditWindow(
	id, label string,
	usedCents, limitCents *float64,
	resetAt *int64,
	windowSeconds int64,
) *providerobservation.QuotaWindow {
	if usedCents == nil && limitCents == nil && resetAt == nil {
		return nil
	}
	window := &providerobservation.QuotaWindow{ID: id, Label: label, Scope: quotaScopeCredits, Unit: "usd", State: "unknown"}
	if usedCents != nil {
		used := *usedCents / 100
		window.Used = &used
	}
	if limitCents != nil {
		limit := *limitCents / 100
		window.Limit = &limit
		remaining := limit
		if window.Used != nil {
			remaining = math.Max(0, limit-*window.Used)
		}
		window.Remaining = &remaining
		window.State = "available"
		if limit > 0 && window.Used != nil {
			utilization := math.Min(1, *window.Used/limit)
			window.Utilization = &utilization
			if remaining <= 0 {
				window.State = "exhausted"
			}
		}
	}
	if resetAt != nil {
		window.ResetAtMS = resetAt
	}
	if windowSeconds > 0 {
		window.WindowSeconds = &windowSeconds
	}
	return window
}

func grokPeriod(startRaw, endRaw string) (*int64, *int64, int64, error) {
	var startMS, endMS *int64
	if value := strings.TrimSpace(startRaw); value != "" {
		parsed, err := time.Parse(time.RFC3339, value)
		if err != nil {
			return nil, nil, 0, fmt.Errorf("Grok period start is invalid")
		}
		ms := parsed.UnixMilli()
		startMS = &ms
	}
	if value := strings.TrimSpace(endRaw); value != "" {
		parsed, err := time.Parse(time.RFC3339, value)
		if err != nil {
			return nil, nil, 0, fmt.Errorf("Grok period end is invalid")
		}
		ms := parsed.UnixMilli()
		endMS = &ms
	}
	seconds := int64(0)
	if startMS != nil && endMS != nil && *endMS > *startMS {
		seconds = (*endMS - *startMS) / 1000
	}
	return startMS, endMS, seconds, nil
}
