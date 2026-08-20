package models

type AccessKeyCostLimitKind string

const (
	AccessKeyCostLimitKindTotal    AccessKeyCostLimitKind = "total"
	AccessKeyCostLimitKindPeriodic AccessKeyCostLimitKind = "periodic"

	AccessKeyCostLimitMinPeriodSeconds int64 = 60
	AccessKeyCostLimitMaxPeriodSeconds int64 = 365 * 24 * 60 * 60
)

// AccessKeyCostLimitRule stores one immutable-definition revision for an AccessKey limit.
type AccessKeyCostLimitRule struct {
	ID            uint                   `gorm:"primaryKey;autoIncrement"`
	AccessKeyID   uint                   `gorm:"not null;uniqueIndex:idx_access_key_cost_limit_rules_identity,priority:1"`
	Kind          AccessKeyCostLimitKind `gorm:"type:varchar(16);not null;uniqueIndex:idx_access_key_cost_limit_rules_identity,priority:2;check:chk_ak_cost_rule_kind,kind IN ('total','periodic')"`
	LimitNanoUSD  int64                  `gorm:"column:limit_nano_usd;not null;check:chk_ak_cost_rule_limit,limit_nano_usd > 0"`
	PeriodSeconds int64                  `gorm:"not null;default:0;uniqueIndex:idx_access_key_cost_limit_rules_identity,priority:3;check:chk_ak_cost_rule_period,(kind = 'total' AND period_seconds = 0) OR (kind = 'periodic' AND period_seconds BETWEEN 60 AND 31536000)"`
	RuleRevision  uint64                 `gorm:"not null;default:1;check:chk_ak_cost_rule_revision,rule_revision > 0"`
	AccessKey     *AccessKey             `gorm:"foreignKey:AccessKeyID;references:ID;constraint:OnUpdate:CASCADE,OnDelete:CASCADE"`
	CreatedAtMS   int64                  `gorm:"column:created_at_ms;not null;autoCreateTime:milli;check:chk_ak_cost_rule_created_at,created_at_ms >= 0"`
	UpdatedAtMS   int64                  `gorm:"column:updated_at_ms;not null;autoUpdateTime:milli;check:chk_ak_cost_rule_updated_at,updated_at_ms >= 0"`
}

func (AccessKeyCostLimitRule) TableName() string {
	return "access_key_cost_limit_rules"
}

// AccessKeyCostLimitState is the latest restart checkpoint for one rule.
type AccessKeyCostLimitState struct {
	RuleID            uint                    `gorm:"primaryKey;not null"`
	RuleRevision      uint64                  `gorm:"not null;check:chk_ak_cost_state_revision,rule_revision > 0"`
	UsedNanoUSD       int64                   `gorm:"column:used_nano_usd;not null;default:0;check:chk_ak_cost_state_used,used_nano_usd >= 0"`
	WindowStartedAtMS *int64                  `gorm:"column:window_started_at_ms;check:chk_ak_cost_state_window,(window_started_at_ms IS NULL AND window_ends_at_ms IS NULL) OR (window_started_at_ms IS NOT NULL AND window_ends_at_ms IS NOT NULL AND window_started_at_ms >= 0 AND window_ends_at_ms > window_started_at_ms)"`
	WindowEndsAtMS    *int64                  `gorm:"column:window_ends_at_ms"`
	WindowGeneration  uint64                  `gorm:"not null;default:0"`
	SnapshotVersion   uint64                  `gorm:"not null;default:1;check:chk_ak_cost_state_version,snapshot_version > 0"`
	Rule              *AccessKeyCostLimitRule `gorm:"foreignKey:RuleID;references:ID;constraint:OnUpdate:CASCADE,OnDelete:CASCADE"`
	UpdatedAtMS       int64                   `gorm:"column:updated_at_ms;not null;autoUpdateTime:milli;check:chk_ak_cost_state_updated_at,updated_at_ms >= 0"`
}

func (AccessKeyCostLimitState) TableName() string {
	return "access_key_cost_limit_states"
}
