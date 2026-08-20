package control

import (
	"fmt"
	"sort"

	"gorm.io/gorm"

	"gpt-load/internal/accessquota"
	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/pricing"
	"gpt-load/internal/storage/models"
)

type normalizedAccessKeyCostLimitRule struct {
	ID            uint
	Kind          accessquota.Kind
	LimitNanoUSD  int64
	PeriodSeconds int64
}

func normalizeAccessKeyCostLimitRules(
	field OptionalAccessKeyCostLimitRules,
	allowExistingIDs bool,
) ([]normalizedAccessKeyCostLimitRule, error) {
	if !field.Set {
		return []normalizedAccessKeyCostLimitRule{}, nil
	}
	result := make([]normalizedAccessKeyCostLimitRule, 0, len(field.Values))
	totalCount := 0
	periods := make(map[int64]struct{})
	ids := make(map[uint]struct{})
	for _, input := range field.Values {
		if input.ID != 0 && !allowExistingIDs {
			return nil, app_errors.ErrValidation
		}
		if input.ID != 0 {
			if _, duplicate := ids[input.ID]; duplicate {
				return nil, app_errors.ErrValidation
			}
			ids[input.ID] = struct{}{}
		}
		parsed, err := pricing.ParseUSD(input.LimitUSD)
		if err != nil || parsed <= 0 {
			return nil, app_errors.ErrValidation
		}
		rule := normalizedAccessKeyCostLimitRule{
			ID: input.ID, Kind: input.Kind, LimitNanoUSD: int64(parsed),
			PeriodSeconds: input.PeriodSeconds,
		}
		switch rule.Kind {
		case accessquota.KindTotal:
			totalCount++
			if totalCount > 1 || rule.PeriodSeconds != 0 {
				return nil, app_errors.ErrValidation
			}
		case accessquota.KindPeriodic:
			if rule.PeriodSeconds < accessquota.MinPeriodSeconds ||
				rule.PeriodSeconds > accessquota.MaxPeriodSeconds {
				return nil, app_errors.ErrValidation
			}
			if _, duplicate := periods[rule.PeriodSeconds]; duplicate {
				return nil, app_errors.ErrValidation
			}
			periods[rule.PeriodSeconds] = struct{}{}
		default:
			return nil, app_errors.ErrValidation
		}
		result = append(result, rule)
	}
	if len(periods) > accessquota.MaxPeriodicRules {
		return nil, app_errors.ErrValidation
	}
	sortNormalizedAccessKeyCostLimitRules(result)
	return result, nil
}

func sortNormalizedAccessKeyCostLimitRules(rules []normalizedAccessKeyCostLimitRule) {
	sort.Slice(rules, func(i, j int) bool {
		left, right := rules[i], rules[j]
		if left.Kind != right.Kind {
			return left.Kind == accessquota.KindTotal
		}
		if left.PeriodSeconds != right.PeriodSeconds {
			return left.PeriodSeconds < right.PeriodSeconds
		}
		return left.ID < right.ID
	})
}

func createAccessKeyCostLimitRules(
	tx *gorm.DB,
	accessKeyID uint,
	rules []normalizedAccessKeyCostLimitRule,
) ([]models.AccessKeyCostLimitRule, error) {
	created := make([]models.AccessKeyCostLimitRule, 0, len(rules))
	for _, definition := range rules {
		if definition.ID != 0 {
			return nil, app_errors.ErrValidation
		}
		rule := models.AccessKeyCostLimitRule{
			AccessKeyID: accessKeyID, Kind: models.AccessKeyCostLimitKind(definition.Kind),
			LimitNanoUSD: definition.LimitNanoUSD, PeriodSeconds: definition.PeriodSeconds,
			RuleRevision: 1,
		}
		if err := tx.Create(&rule).Error; err != nil {
			return nil, app_errors.ParseDBError(err)
		}
		state := models.AccessKeyCostLimitState{
			RuleID: rule.ID, RuleRevision: rule.RuleRevision, SnapshotVersion: 1,
		}
		if err := tx.Create(&state).Error; err != nil {
			return nil, app_errors.ParseDBError(err)
		}
		created = append(created, rule)
	}
	return created, nil
}

func reconcileAccessKeyCostLimitRules(
	tx *gorm.DB,
	accessKeyID uint,
	desired []normalizedAccessKeyCostLimitRule,
) ([]models.AccessKeyCostLimitRule, error) {
	var current []models.AccessKeyCostLimitRule
	if err := tx.Where("access_key_id = ?", accessKeyID).Order("id ASC").Find(&current).Error; err != nil {
		return nil, app_errors.ParseDBError(err)
	}
	currentByID := make(map[uint]models.AccessKeyCostLimitRule, len(current))
	for _, rule := range current {
		currentByID[rule.ID] = rule
	}
	desiredIDs := make(map[uint]struct{}, len(desired))
	for _, definition := range desired {
		if definition.ID == 0 {
			continue
		}
		currentRule, exists := currentByID[definition.ID]
		if !exists || accessquota.Kind(currentRule.Kind) != definition.Kind {
			return nil, app_errors.ErrValidation
		}
		desiredIDs[definition.ID] = struct{}{}
	}
	for _, rule := range current {
		if _, retained := desiredIDs[rule.ID]; retained {
			continue
		}
		if err := tx.Delete(&models.AccessKeyCostLimitRule{}, rule.ID).Error; err != nil {
			return nil, app_errors.ParseDBError(err)
		}
	}

	for _, definition := range desired {
		if definition.ID == 0 {
			if _, err := createAccessKeyCostLimitRules(
				tx, accessKeyID, []normalizedAccessKeyCostLimitRule{definition},
			); err != nil {
				return nil, err
			}
			continue
		}
		currentRule := currentByID[definition.ID]
		semanticsChanged := currentRule.PeriodSeconds != definition.PeriodSeconds
		updates := map[string]any{"limit_nano_usd": definition.LimitNanoUSD}
		if semanticsChanged {
			if currentRule.RuleRevision == ^uint64(0) {
				return nil, fmt.Errorf("advance access key cost limit rule %d revision: %w", definition.ID, app_errors.ErrInternalServer)
			}
			updates["kind"] = string(definition.Kind)
			updates["period_seconds"] = definition.PeriodSeconds
			updates["rule_revision"] = currentRule.RuleRevision + 1
		}
		if err := tx.Model(&models.AccessKeyCostLimitRule{}).
			Where("id = ? AND access_key_id = ?", definition.ID, accessKeyID).
			Updates(updates).Error; err != nil {
			return nil, app_errors.ParseDBError(err)
		}
		if semanticsChanged {
			stateUpdates := map[string]any{
				"rule_revision":        currentRule.RuleRevision + 1,
				"used_nano_usd":        int64(0),
				"window_started_at_ms": nil,
				"window_ends_at_ms":    nil,
				"window_generation":    uint64(0),
				"snapshot_version":     uint64(1),
			}
			result := tx.Model(&models.AccessKeyCostLimitState{}).
				Where("rule_id = ? AND rule_revision = ?", definition.ID, currentRule.RuleRevision).
				Updates(stateUpdates)
			if result.Error != nil {
				return nil, app_errors.ParseDBError(result.Error)
			}
			if result.RowsAffected != 1 {
				return nil, fmt.Errorf("reset access key cost limit rule %d state: %w", definition.ID, app_errors.ErrInternalServer)
			}
		}
	}
	return loadAccessKeyCostLimitRuleRows(tx, accessKeyID)
}

func loadAccessKeyCostLimitRuleRows(
	db *gorm.DB,
	accessKeyID uint,
) ([]models.AccessKeyCostLimitRule, error) {
	var rows []models.AccessKeyCostLimitRule
	if err := db.Where("access_key_id = ?", accessKeyID).
		Order("CASE WHEN kind = 'total' THEN 0 ELSE 1 END ASC, period_seconds ASC, id ASC").
		Find(&rows).Error; err != nil {
		return nil, app_errors.ParseDBError(err)
	}
	return rows, nil
}

func mapAccessKeyCostLimitRules(rows []models.AccessKeyCostLimitRule) []AccessKeyCostLimitRule {
	result := make([]AccessKeyCostLimitRule, 0, len(rows))
	for _, row := range rows {
		result = append(result, AccessKeyCostLimitRule{
			ID: row.ID, Kind: accessquota.Kind(row.Kind),
			LimitUSD:      pricing.FormatUSD(pricing.NanoUSD(row.LimitNanoUSD)),
			PeriodSeconds: row.PeriodSeconds,
		})
	}
	return result
}

func costLimitRuleRequestsForDigest(
	rules []normalizedAccessKeyCostLimitRule,
) []AccessKeyCostLimitRuleRequest {
	result := make([]AccessKeyCostLimitRuleRequest, 0, len(rules))
	for _, rule := range rules {
		result = append(result, AccessKeyCostLimitRuleRequest{
			Kind: rule.Kind, LimitUSD: pricing.FormatUSD(pricing.NanoUSD(rule.LimitNanoUSD)),
			PeriodSeconds: rule.PeriodSeconds,
		})
	}
	return result
}

func mapAccessKeyCostLimitStatus(view accessquota.View) AccessKeyCostLimitStatus {
	result := AccessKeyCostLimitStatus{
		ObservedAtMS:      view.ObservedAtMS,
		Allowed:           view.Allowed,
		Recoverable:       view.Recoverable,
		NextAvailableAtMS: cloneCostLimitMilliseconds(view.NextAvailableAtMS),
		Rules:             make([]AccessKeyCostLimitRuleStatus, 0, len(view.Rules)),
	}
	for _, rule := range view.Rules {
		result.Rules = append(result.Rules, AccessKeyCostLimitRuleStatus{
			ID: rule.ID, Kind: rule.Kind,
			LimitUSD:     pricing.FormatUSD(pricing.NanoUSD(rule.LimitNanoUSD)),
			UsedUSD:      pricing.FormatUSD(pricing.NanoUSD(rule.UsedNanoUSD)),
			RemainingUSD: pricing.FormatUSD(pricing.NanoUSD(rule.RemainingNanoUSD)),
			Status:       rule.Status, PeriodSeconds: rule.PeriodSeconds,
			WindowStartedAtMS: cloneCostLimitMilliseconds(rule.WindowStartedAtMS),
			WindowEndsAtMS:    cloneCostLimitMilliseconds(rule.WindowEndsAtMS),
		})
	}
	return result
}

func costLimitDefinitionsFromStatus(status AccessKeyCostLimitStatus) []AccessKeyCostLimitRule {
	rules := make([]AccessKeyCostLimitRule, 0, len(status.Rules))
	for _, rule := range status.Rules {
		rules = append(rules, AccessKeyCostLimitRule{
			ID: rule.ID, Kind: rule.Kind, LimitUSD: rule.LimitUSD,
			PeriodSeconds: rule.PeriodSeconds,
		})
	}
	return rules
}

func blockingCostLimitRuleStatuses(status AccessKeyCostLimitStatus) []AccessKeyCostLimitRuleStatus {
	result := make([]AccessKeyCostLimitRuleStatus, 0)
	for _, rule := range status.Rules {
		if rule.Status == accessquota.RuleStatusExhausted {
			result = append(result, rule)
		}
	}
	return result
}

func cloneCostLimitMilliseconds(value *int64) *int64 {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}
