package observation

type PlanSummary struct {
	Name string `json:"name,omitempty"`
}

type AccountSummary struct {
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

type QuotaWindow struct {
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

type ResetCredit struct {
	ExpiresAtMS int64 `json:"expires_at_ms"`
}

type Snapshot struct {
	Plan                  PlanSummary     `json:"plan_summary"`
	Account               *AccountSummary `json:"account_summary,omitempty"`
	QuotaWindows          []QuotaWindow   `json:"quota_windows"`
	ResetCreditsAvailable *int64          `json:"reset_credits_available,omitempty"`
	ResetCredits          []ResetCredit   `json:"reset_credits,omitempty"`
}
