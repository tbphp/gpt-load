package subscriptionruntime

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"

	"gpt-load/internal/antigravity"
)

// NormalizeAntigravityObservation creates the same provider-neutral snapshot
// shape used by other subscription channels. Google One AI credits are shown
// as a balance only; no total, utilization, reset window, or payment action is
// inferred from the upstream payload.
func NormalizeAntigravityObservation(email string, observation antigravity.AccountObservation) ([]byte, error) {
	result := quotaSnapshot{
		Plan:         quotaPlanSummary{Name: strings.TrimSpace(observation.PlanID)},
		Account:      &quotaAccountSummary{Email: strings.TrimSpace(email)},
		QuotaWindows: []quotaWindow{},
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
	return json.Marshal(result)
}
