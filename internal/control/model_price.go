package control

import (
	"context"
	"fmt"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/pricing"
	"gpt-load/internal/storage/models"
)

type ModelPriceInput struct {
	Pattern       string
	UncachedInput *float64
	CacheRead     *float64
	CacheWrite5M  *float64
	CacheWrite1H  *float64
	Output        *float64
}

func (s *Service) UpsertModelPrice(ctx context.Context, input ModelPriceInput) error {
	if _, err := pricing.Compile([]pricing.Rule{modelPriceInputRule(input)}); err != nil {
		return app_errors.ErrValidation
	}
	return s.writePriceTable(ctx, func(tx *gorm.DB) error {
		now := s.now()
		row := models.ModelPrice{
			Pattern:           input.Pattern,
			InputPrice:        input.UncachedInput,
			OutputPrice:       input.Output,
			CacheReadPrice:    input.CacheRead,
			CacheWrite5MPrice: input.CacheWrite5M,
			CacheWrite1HPrice: input.CacheWrite1H,
			Source:            string(pricing.SourceUser),
			UpdatedAt:         now,
		}
		err := tx.Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "pattern"}},
			DoUpdates: clause.Assignments(map[string]any{
				"input_price":          input.UncachedInput,
				"output_price":         input.Output,
				"cache_read_price":     input.CacheRead,
				"cache_write_5m_price": input.CacheWrite5M,
				"cache_write_1h_price": input.CacheWrite1H,
				"source":               string(pricing.SourceUser),
				"updated_at":           now,
			}),
		}).Create(&row).Error
		if err != nil {
			return fmt.Errorf("persist model price: %w", app_errors.ParseDBError(err))
		}
		return nil
	})
}

func (s *Service) ResetModelPrice(ctx context.Context, pattern string) error {
	return s.writePriceTable(ctx, func(tx *gorm.DB) error {
		if err := tx.Where("pattern = ?", pattern).Delete(&models.ModelPrice{}).Error; err != nil {
			return fmt.Errorf("delete model price: %w", app_errors.ParseDBError(err))
		}
		return nil
	})
}

func (s *Service) writePriceTable(
	ctx context.Context,
	mutate func(*gorm.DB) error,
) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	var table *pricing.Table
	err := s.withControlTransaction(ctx, func(tx *gorm.DB) error {
		if err := mutate(tx); err != nil {
			return err
		}
		var err error
		table, err = loadPriceTable(ctx, tx)
		return err
	})
	if err != nil {
		return err
	}
	s.priceRuntime.Publish(table)
	return nil
}

func loadPriceTable(ctx context.Context, tx *gorm.DB) (*pricing.Table, error) {
	var rows []models.ModelPrice
	if err := tx.WithContext(ctx).Order("id ASC").Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("load persisted model prices: %w", app_errors.ParseDBError(err))
	}

	rules := pricing.BuiltinRules()
	for _, row := range rows {
		if row.Source != string(pricing.SourceUser) {
			return nil, fmt.Errorf(
				"validate persisted model price source: %w",
				app_errors.ErrInternalServer,
			)
		}
		rules = append(rules, modelPriceRule(row))
	}
	table, err := pricing.Compile(rules)
	if err != nil {
		return nil, fmt.Errorf("compile model prices: %w", app_errors.ErrInternalServer)
	}
	return table, nil
}

func modelPriceInputRule(input ModelPriceInput) pricing.Rule {
	return pricing.Rule{
		Pattern: input.Pattern,
		Prices: pricing.Prices{
			UncachedInput: priceFromPointer(input.UncachedInput),
			CacheRead:     priceFromPointer(input.CacheRead),
			CacheWrite5M:  priceFromPointer(input.CacheWrite5M),
			CacheWrite1H:  priceFromPointer(input.CacheWrite1H),
			Output:        priceFromPointer(input.Output),
		},
		Source: pricing.SourceUser,
	}
}

func modelPriceRule(row models.ModelPrice) pricing.Rule {
	return pricing.Rule{
		Pattern: row.Pattern,
		Prices: pricing.Prices{
			UncachedInput: priceFromPointer(row.InputPrice),
			CacheRead:     priceFromPointer(row.CacheReadPrice),
			CacheWrite5M:  priceFromPointer(row.CacheWrite5MPrice),
			CacheWrite1H:  priceFromPointer(row.CacheWrite1HPrice),
			Output:        priceFromPointer(row.OutputPrice),
		},
		Source:    pricing.SourceUser,
		SourceURL: "",
		UpdatedAt: row.UpdatedAt,
	}
}

func priceFromPointer(value *float64) pricing.Price {
	if value == nil {
		return pricing.Price{}
	}
	return pricing.Price{Value: *value, Set: true}
}
