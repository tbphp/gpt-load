package storage

import (
	"fmt"

	"gorm.io/gorm"
)

func applyMySQLMigration(db *gorm.DB, entry migration) error {
	marker := migrationResumeMarker(entry.ID)
	var markerCount int64
	if err := db.Model(&schemaMigration{}).Where("id = ?", marker).Count(&markerCount).Error; err != nil {
		return fmt.Errorf("inspect MySQL migration %s marker: %w", entry.ID, err)
	}
	if markerCount > 1 {
		return fmt.Errorf("apply MySQL migration %s: invalid resume marker count", entry.ID)
	}
	if markerCount == 0 {
		if err := db.Create(&schemaMigration{ID: marker}).Error; err != nil {
			return fmt.Errorf("record MySQL migration %s resume marker: %w", entry.ID, err)
		}
	} else if entry.ValidateRecoverable == nil {
		return fmt.Errorf("apply MySQL migration %s: interrupted migration has no recovery validation", entry.ID)
	} else if err := entry.ValidateRecoverable(db); err != nil {
		return fmt.Errorf("apply MySQL migration %s: unsafe interrupted migration: %w", entry.ID, err)
	}

	if err := entry.Up(db); err != nil {
		return fmt.Errorf("apply migration %s: %w", entry.ID, err)
	}
	if entry.Validate != nil {
		if err := entry.Validate(db); err != nil {
			return fmt.Errorf("validate migration %s: %w", entry.ID, err)
		}
	}
	if err := db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Delete(&schemaMigration{}, "id = ?", marker).Error; err != nil {
			return err
		}
		return tx.Create(&schemaMigration{ID: entry.ID}).Error
	}); err != nil {
		return fmt.Errorf("finalize MySQL migration %s: %w", entry.ID, err)
	}
	return nil
}

func migrationResumeMarker(id string) string {
	return id + "#building"
}
