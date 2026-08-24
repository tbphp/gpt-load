package claude

import (
	"context"
	"net/http"

	cpaembedded "github.com/router-for-me/CLIProxyAPI/v7/gptload-embedded/embedded"
)

type Model struct {
	ID          string
	DisplayName string
	Description string
}

type AccountProfile struct {
	DisplayName               string
	Email                     string
	AccountUUID               string
	AccountCreatedAt          string
	OrganizationUUID          string
	OrganizationName          string
	OrganizationType          string
	OrganizationRole          string
	WorkspaceRole             string
	OrganizationRateLimitTier string
	UserRateLimitTier         string
	SeatTier                  string
	BillingType               string
	SubscriptionCreatedAt     string
	ExtraUsageEnabled         *bool
}

type UsageWindow struct {
	Utilization *float64
	ResetsAt    *string
}

type ExtraUsage struct {
	Enabled        *bool
	MonthlyLimit   *float64
	UsedCredits    *float64
	Utilization    *float64
	Currency       *string
	DisabledReason *string
}

type ScopedLimit struct {
	Kind               string
	Group              string
	Percent            float64
	ResetsAt           *string
	ModelDisplayName   string
	SurfaceDisplayName string
}

type Usage struct {
	FiveHour          *UsageWindow
	SevenDay          *UsageWindow
	SevenDayOAuthApps *UsageWindow
	SevenDayOpus      *UsageWindow
	SevenDaySonnet    *UsageWindow
	CinderCove        *UsageWindow
	ExtraUsage        *ExtraUsage
	Limits            []ScopedLimit
}

type AccountObservation struct {
	Profile           AccountProfile
	Usage             Usage
	Header            http.Header
	AccountObserved   bool
	UsageObserved     bool
	IncompleteSources []string
}

func ListModels(ctx context.Context, credential Credential) ([]Model, error) {
	options, err := claudeOptions(ctx)
	if err != nil {
		return nil, err
	}
	models, err := cpaembedded.DiscoverClaudeModels(ctx, credentialToBridge(credential), options)
	if err != nil {
		return nil, normalizeAuthorizationError(err)
	}
	result := make([]Model, 0, len(models))
	for _, model := range models {
		result = append(result, Model{
			ID: model.ID, DisplayName: model.DisplayName, Description: model.Description,
		})
	}
	return result, nil
}

func ObserveAccount(ctx context.Context, credential Credential) (AccountObservation, error) {
	options, err := claudeOptions(ctx)
	if err != nil {
		return AccountObservation{}, err
	}
	observed, err := cpaembedded.ObserveClaudeAccount(ctx, credentialToBridge(credential), options)
	if err != nil {
		return AccountObservation{}, normalizeAuthorizationError(err)
	}
	return accountObservationFromBridge(observed), nil
}

func accountObservationFromBridge(value cpaembedded.ClaudeAccountObservation) AccountObservation {
	profile := value.Profile
	result := AccountObservation{
		Profile: AccountProfile{
			DisplayName: profile.DisplayName, Email: profile.Email,
			AccountUUID: profile.AccountUUID, AccountCreatedAt: profile.AccountCreatedAt,
			OrganizationUUID: profile.OrganizationUUID, OrganizationName: profile.OrganizationName,
			OrganizationType: profile.OrganizationType, OrganizationRole: profile.OrganizationRole,
			WorkspaceRole:             profile.WorkspaceRole,
			OrganizationRateLimitTier: profile.OrganizationRateLimitTier,
			UserRateLimitTier:         profile.UserRateLimitTier, SeatTier: profile.SeatTier,
			BillingType: profile.BillingType, SubscriptionCreatedAt: profile.SubscriptionCreatedAt,
			ExtraUsageEnabled: cloneBool(profile.ExtraUsageEnabled),
		},
		Header:            value.Header.Clone(),
		AccountObserved:   value.AccountObserved,
		UsageObserved:     value.UsageObserved,
		IncompleteSources: append([]string(nil), value.IncompleteSources...),
	}
	result.Usage = usageFromBridge(value.Usage)
	return result
}

func usageFromBridge(value cpaembedded.ClaudeUsage) Usage {
	result := Usage{
		FiveHour:          usageWindowFromBridge(value.FiveHour),
		SevenDay:          usageWindowFromBridge(value.SevenDay),
		SevenDayOAuthApps: usageWindowFromBridge(value.SevenDayOAuthApps),
		SevenDayOpus:      usageWindowFromBridge(value.SevenDayOpus),
		SevenDaySonnet:    usageWindowFromBridge(value.SevenDaySonnet),
		CinderCove:        usageWindowFromBridge(value.CinderCove),
	}
	if value.ExtraUsage != nil {
		result.ExtraUsage = &ExtraUsage{
			Enabled:        cloneBool(value.ExtraUsage.Enabled),
			MonthlyLimit:   cloneFloat(value.ExtraUsage.MonthlyLimit),
			UsedCredits:    cloneFloat(value.ExtraUsage.UsedCredits),
			Utilization:    cloneFloat(value.ExtraUsage.Utilization),
			Currency:       cloneString(value.ExtraUsage.Currency),
			DisabledReason: cloneString(value.ExtraUsage.DisabledReason),
		}
	}
	result.Limits = make([]ScopedLimit, 0, len(value.Limits))
	for _, limit := range value.Limits {
		result.Limits = append(result.Limits, ScopedLimit{
			Kind: limit.Kind, Group: limit.Group, Percent: limit.Percent,
			ResetsAt: cloneString(limit.ResetsAt), ModelDisplayName: limit.ModelDisplayName,
			SurfaceDisplayName: limit.SurfaceDisplayName,
		})
	}
	return result
}

func usageWindowFromBridge(value *cpaembedded.ClaudeUsageWindow) *UsageWindow {
	if value == nil {
		return nil
	}
	return &UsageWindow{Utilization: cloneFloat(value.Utilization), ResetsAt: cloneString(value.ResetsAt)}
}

func cloneFloat(value *float64) *float64 {
	if value == nil {
		return nil
	}
	result := *value
	return &result
}

func cloneString(value *string) *string {
	if value == nil {
		return nil
	}
	result := *value
	return &result
}

func cloneBool(value *bool) *bool {
	if value == nil {
		return nil
	}
	result := *value
	return &result
}
