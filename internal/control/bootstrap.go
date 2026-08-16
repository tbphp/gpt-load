package control

import (
	"context"
	"fmt"

	"gorm.io/gorm"

	"gpt-load/internal/catalog"
	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/pricing"
	"gpt-load/internal/storage/models"

	"github.com/sirupsen/logrus"
)

const defaultAccessKeyMarker = models.InternalSystemSettingPrefix + "bootstrap.default_access_key.v1"

func (s *Service) EnsureInitialState(ctx context.Context) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	var catalogSnapshot *catalog.Snapshot
	if s.catalogRuntime != nil {
		catalogSnapshot = s.catalogRuntime.Load()
	}

	var priceTable *pricing.Table
	err := s.withControlTransaction(ctx, func(tx *gorm.DB) error {
		nowMS := s.now().UnixMilli()
		if err := tx.Model(&models.Credential{}).
			Where("auth_state = ?", models.CredentialAuthStateRefreshing).
			Updates(map[string]any{
				"auth_state":      models.CredentialAuthStateOutcomeUnknown,
				"auth_error_code": "refresh_interrupted", "updated_at_ms": nowMS,
			}).Error; err != nil {
			return app_errors.ParseDBError(err)
		}
		if err := tx.Model(&models.CredentialStage{}).
			Where("status = ?", models.CredentialStageExchanging).
			Updates(map[string]any{
				"status":            models.CredentialStageOutcomeUnknown,
				"encrypted_payload": "", "oauth_state_hash": nil,
				"error_code": "authorization_exchange_interrupted", "updated_at_ms": nowMS,
			}).Error; err != nil {
			return app_errors.ParseDBError(err)
		}
		var marker models.SystemSetting
		query := tx.Where(&models.SystemSetting{Key: defaultAccessKeyMarker}).
			Limit(1).Find(&marker)
		if query.Error != nil {
			return app_errors.ParseDBError(query.Error)
		}
		if query.RowsAffected == 0 {
			var count int64
			if err := tx.Model(&models.AccessKey{}).Count(&count).Error; err != nil {
				return app_errors.ParseDBError(err)
			}
			if count == 0 {
				filters, err := normalizeAccessKeyFilters(nil)
				if err != nil {
					return fmt.Errorf("build default access key filters: %w", err)
				}
				row, _, err := s.newAccessKeyRow("Default", filters, 0)
				if err != nil {
					return err
				}
				if err := tx.Create(&row).Error; err != nil {
					return app_errors.ParseDBError(err)
				}
			}

			marker = models.SystemSetting{
				Key:   defaultAccessKeyMarker,
				Value: "true",
			}
			if err := tx.Create(&marker).Error; err != nil {
				return app_errors.ParseDBError(err)
			}
		}

		if catalogSnapshot != nil {
			if err := reconcileCatalogAutomaticPrices(tx, catalogSnapshot); err != nil {
				return err
			}
		}
		if err := reconcileReferencedPrices(tx, catalogSnapshot); err != nil {
			return err
		}
		if err := cleanupUnreferencedAutomaticPrices(tx); err != nil {
			return err
		}
		var err error
		priceTable, err = loadPriceTable(ctx, tx, catalogSnapshot)
		if err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("ensure initial control state: %w", err)
	}
	s.priceRuntime.Publish(priceTable)
	logrus.WithField("event", "startup.model_prices_publish").Info("model prices published")
	return nil
}
