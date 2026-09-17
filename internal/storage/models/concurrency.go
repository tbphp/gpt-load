package models

// Absence of a policy means inheritance. A stored zero explicitly disables
// that scope's limit. Account subjects survive OAuth token rotation and are
// shared by all memberships of the same subscription account.
type ConcurrencyPolicy struct {
	Subject        string `gorm:"type:varchar(255);primaryKey;not null"`
	MaxConcurrency int64  `gorm:"type:bigint;not null;check:chk_concurrency_limit,max_concurrency >= 0 AND max_concurrency <= 1000000"`
}
