package models

import (
	"time"

	"gorm.io/datatypes"
)

// AccessKey is an encrypted client credential and its persisted access policy.
type AccessKey struct {
	ID               uint           `gorm:"primaryKey;autoIncrement"`
	Name             string         `gorm:"type:varchar(255);not null"`
	KeyValue         string         `gorm:"type:text;not null"`
	KeyHash          string         `gorm:"type:varchar(128);not null;uniqueIndex"`
	Status           string         `gorm:"type:varchar(32);not null;default:'active';index"`
	Filters          datatypes.JSON `gorm:"type:json"`
	RPMLimit         int64          `gorm:"not null;default:0"`
	DailyCostLimit   float64        `gorm:"not null;default:0"`
	MonthlyCostLimit float64        `gorm:"not null;default:0"`
	CreatedAt        time.Time
	UpdatedAt        time.Time
}
