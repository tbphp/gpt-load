package observation

type PlanLevel string

const (
	PlanLevelFree     PlanLevel = "free"
	PlanLevelStandard PlanLevel = "standard"
	PlanLevelPremium  PlanLevel = "premium"
	PlanLevelElite    PlanLevel = "elite"
)

const (
	QuotaLabelSession       = "session"
	QuotaLabelWeekly        = "weekly"
	QuotaLabelExtraUsage    = "extra_usage"
	QuotaLabelIncludedUsage = "included_usage"
	QuotaLabelPayAsYouGo    = "pay_as_you_go"
	QuotaLabelOAuthApps     = "oauth_apps"
)

type PlanSummary struct {
	Name  string    `json:"name,omitempty"`
	Level PlanLevel `json:"level,omitempty"`
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
	// SourceID 是提供方的额度来源标识，不是展示名称或请求模型。
	SourceID string `json:"source_id,omitempty"`
	// SourceName 仅用于 Codex WS 观测中尚未解析的上游额度名称，不持久化或对外返回。
	SourceName    string   `json:"-"`
	ID            string   `json:"id"`
	Label         string   `json:"label"`
	LabelKey      string   `json:"label_key,omitempty"`
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
	ExpiresAtMS *int64 `json:"expires_at_ms,omitempty"`
}

// CreditSummary 保存账号点数；余额使用字符串，保留上游小数及零余额。
type CreditSummary struct {
	Balance      string `json:"balance,omitempty"`
	HasCredits   *bool  `json:"has_credits,omitempty"`
	Unlimited    *bool  `json:"unlimited,omitempty"`
	ObservedAtMS *int64 `json:"observed_at_ms,omitempty"`
}

// SubscriptionSummary describes a billing period, independently of OAuth expiry.
// Failed refreshes retain the last confirmed values and their observation time.
type SubscriptionSummary struct {
	State        string `json:"state"`
	PeriodEndMS  *int64 `json:"period_end_ms,omitempty"`
	WillRenew    *bool  `json:"will_renew,omitempty"`
	ObservedAtMS *int64 `json:"observed_at_ms,omitempty"`
}

func MergeSubscription(previous, current *SubscriptionSummary) *SubscriptionSummary {
	if current == nil {
		return previous
	}
	if current.State != "error" || previous == nil {
		return current
	}
	merged := *previous
	merged.State = "error"
	return &merged
}

type Snapshot struct {
	Subscription          *SubscriptionSummary `json:"subscription,omitempty"`
	Plan                  PlanSummary          `json:"plan_summary"`
	Account               *AccountSummary      `json:"account_summary,omitempty"`
	QuotaWindows          []QuotaWindow        `json:"quota_windows"`
	Credits               *CreditSummary       `json:"credits,omitempty"`
	ResetCreditsAvailable *int64               `json:"reset_credits_available,omitempty"`
	ResetCredits          []ResetCredit        `json:"reset_credits,omitempty"`
}
