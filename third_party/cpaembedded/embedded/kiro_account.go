package embedded

import (
	"context"
	"net/http"
	"strings"
)

// KiroUsageMeter is one credit/resource meter observed on the account.
type KiroUsageMeter struct {
	DisplayName        string
	Unit               string
	CurrentUsage       float64
	UsageLimit         float64
	UsageLimitExplicit bool
	PercentageUsed     float64
	ResetDate          string
}

// KiroUsageObservation aggregates the credit/resource meters for one account.
type KiroUsageObservation struct {
	Meters []KiroUsageMeter
}

// KiroAccountObservation is GPT-Load's normalized, projection-safe account
// observation for a Kiro subscription. It is deliberately sparse and free of
// arbitrary billing assumptions: Kiro exposes quota only through the local
// desktop mirror, so that is what this observes.
type KiroAccountObservation struct {
	Usage KiroUsageObservation
	// ModelID is the account's last selected model, when readable locally.
	ModelID string
	// Header is preserved for parity with other providers; it is unused by Kiro.
	Header http.Header
	// AccountObserved reports whether the local account mirror was reachable.
	AccountObserved bool
	// AccountQuotaObserved reports whether at least one quota meter was found.
	AccountQuotaObserved bool
	// CreditQuotaObserved reports whether the primary credit meter was found.
	CreditQuotaObserved bool
	// LoadedViaFreecode reports whether the observation came from the local
	// desktop mirror that freecode manages (self-exploration).
	LoadedViaFreecode bool
	IncompleteSources []string
}

// ObserveKiroAccount self-explores the running machine for the Kiro account
// managed by the Kiro desktop app / AWS SSO cache. Unlike cloud account
// introspection (which Kiro does not expose publicly), the quota mirror is
// read directly from the local app state, matching what freecode observes.
func ObserveKiroAccount(ctx context.Context, credential KiroCredential, options KiroOptions) (KiroAccountObservation, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	discovery, err := DiscoverKiroLocal()
	if err != nil {
		return KiroAccountObservation{IncompleteSources: []string{"local_mirror"}}, ErrKiroAccountObservationUnavailable
	}
	observation := KiroAccountObservation{
		Header:            http.Header{},
		AccountObserved:   discovery.TokenFound || discovery.Usage != nil,
		LoadedViaFreecode: true,
		IncompleteSources: []string{},
	}
	if discovery.Usage != nil {
		observation.ModelID = strings.TrimSpace(discovery.Usage.ModelID)
		meters := make([]KiroUsageMeter, 0, len(discovery.Usage.Breaks))
		for _, brk := range discovery.Usage.Breaks {
			meters = append(meters, KiroUsageMeter{
				DisplayName: brk.DisplayName, Unit: brk.Unit,
				CurrentUsage: brk.CurrentUsage, UsageLimit: brk.UsageLimit,
				UsageLimitExplicit: brk.UsageLimitExplicit, PercentageUsed: brk.PercentageUsed,
				ResetDate: brk.ResetDate,
			})
		}
		observation.Usage.Meters = meters
		observation.AccountQuotaObserved = len(meters) > 0
		observation.CreditQuotaObserved = hasKiroCreditMeter(meters)
	}
	return observation, nil
}

func hasKiroCreditMeter(meters []KiroUsageMeter) bool {
	for _, meter := range meters {
		kind := strings.ToLower(strings.TrimSpace(meter.Unit))
		if kind == "invocations" || kind == "credits" {
			return true
		}
	}
	return false
}
