package commands

import (
	"flag"
	"fmt"
	"gpt-load/internal/container"
	"gpt-load/internal/encryption"
	"gpt-load/internal/models"
	"gpt-load/internal/store"
	"gpt-load/internal/types"
	"gpt-load/internal/utils"
	"os"
	"strings"

	"github.com/sirupsen/logrus"
	"gorm.io/gorm"
)

// RunMigrateKeys handles the migrate-keys command entry point
func RunMigrateKeys(args []string) {
	// Parse migrate-keys subcommand parameters
	migrateCmd := flag.NewFlagSet("migrate-keys", flag.ExitOnError)
	fromKey := migrateCmd.String("from", "", "Source encryption key (for decrypting existing data)")
	toKey := migrateCmd.String("to", "", "Target encryption key (for encrypting new data)")

	// Set custom usage message
	migrateCmd.Usage = func() {
		fmt.Println("GPT-Load Key Migration Tool")
		fmt.Println()
		fmt.Println("Usage:")
		fmt.Println("  Enable encryption: gpt-load migrate-keys --to new-key")
		fmt.Println("  Disable encryption: gpt-load migrate-keys --from old-key")
		fmt.Println("  Change key: gpt-load migrate-keys --from old-key --to new-key")
		fmt.Println()
		fmt.Println("Arguments:")
		migrateCmd.PrintDefaults()
		fmt.Println()
		fmt.Println("⚠️  Important Notes:")
		fmt.Println("  1. Always backup database before migration")
		fmt.Println("  2. Stop service during migration")
		fmt.Println("  3. Restart service after migration completes")
	}

	// Parse parameters
	if err := migrateCmd.Parse(args); err != nil {
		logrus.Fatalf("Parameter parsing failed: %v", err)
	}

	// Check if help should be displayed
	if len(args) == 0 || (*fromKey == "" && *toKey == "") {
		migrateCmd.Usage()
		os.Exit(0)
	}

	// Build dependency injection container
	cont, err := container.BuildContainer()
	if err != nil {
		logrus.Fatalf("Failed to build container: %v", err)
	}

	// Initialize global logger
	if err := cont.Invoke(func(configManager types.ConfigManager) {
		utils.SetupLogger(configManager)
	}); err != nil {
		logrus.Fatalf("Failed to setup logger: %v", err)
	}

	// Execute migration command
	if err := cont.Invoke(func(db *gorm.DB, configManager types.ConfigManager, cacheStore store.Store) {
		migrateKeysCmd := NewMigrateKeysCommand(db, configManager, cacheStore, *fromKey, *toKey)
		if err := migrateKeysCmd.Execute(); err != nil {
			logrus.Fatalf("Key migration failed: %v", err)
		}
	}); err != nil {
		logrus.Fatalf("Failed to execute migration: %v", err)
	}

	logrus.Info("Key migration command completed")
}

// Migration batch size configuration
const migrationBatchSize = 1000

// MigrateKeysCommand handles encryption key migration
type MigrateKeysCommand struct {
	db            *gorm.DB
	configManager types.ConfigManager
	cacheStore    store.Store
	fromKey       string
	toKey         string
}

// NewMigrateKeysCommand creates a new migration command
func NewMigrateKeysCommand(db *gorm.DB, configManager types.ConfigManager, cacheStore store.Store, fromKey, toKey string) *MigrateKeysCommand {
	return &MigrateKeysCommand{
		db:            db,
		configManager: configManager,
		cacheStore:    cacheStore,
		fromKey:       fromKey,
		toKey:         toKey,
	}
}

// Execute performs the key migration
func (cmd *MigrateKeysCommand) Execute() error {
	// pre. Database migration and repair
	if err := cmd.db.AutoMigrate(&models.APIKey{}); err != nil {
		return fmt.Errorf("database auto-migration failed: %w", err)
	}

	// 1. Validate parameters and get scenario
	scenario, err := cmd.validateAndGetScenario()
	if err != nil {
		return fmt.Errorf("parameter validation failed: %w", err)
	}

	logrus.Infof("Starting key migration, scenario: %s", scenario)

	// 2. Pre-check - verify current keys can decrypt all data
	if err := cmd.preCheck(); err != nil {
		return fmt.Errorf("pre-check failed: %w", err)
	}

	// 3. Migrate data to temporary columns
	if err := cmd.createBackupTableAndMigrate(); err != nil {
		return fmt.Errorf("data migration failed: %w", err)
	}

	// 4. Verify temporary columns data integrity
	if err := cmd.verifyTempColumns(); err != nil {
		logrus.Errorf("Data verification failed: %v", err)
		return fmt.Errorf("data verification failed: %w", err)
	}

	// 5. Switch columns atomically
	if err := cmd.switchColumns(); err != nil {
		logrus.Errorf("Column switch failed: %v", err)
		return fmt.Errorf("column switch failed: %w", err)
	}

	// 6. Clear cache
	if err := cmd.clearCache(); err != nil {
		logrus.Warnf("Cache cleanup failed, recommend manual service restart: %v", err)
	}

	// 7. Clean up old table
	if err := cmd.cleanupOldTable(); err != nil {
		logrus.Warnf("Old table cleanup failed, can manually clean up api_keys_old table: %v", err)
	}

	logrus.Info("Key migration completed successfully!")
	logrus.Info("Recommend restarting service to ensure all cached data is loaded correctly")

	return nil
}

// validateAndGetScenario validates parameters and returns migration scenario
func (cmd *MigrateKeysCommand) validateAndGetScenario() (string, error) {
	hasFrom := cmd.fromKey != ""
	hasTo := cmd.toKey != ""

	switch {
	case !hasFrom && hasTo:
		// Enable encryption
		utils.ValidatePasswordStrength(cmd.toKey, "new encryption key")
		return "enable encryption", nil
	case hasFrom && !hasTo:
		// Disable encryption
		return "disable encryption", nil
	case hasFrom && hasTo:
		// Change encryption key
		if cmd.fromKey == cmd.toKey {
			return "", fmt.Errorf("new and old keys cannot be the same")
		}
		utils.ValidatePasswordStrength(cmd.toKey, "new encryption key")
		return "change encryption key", nil
	default:
		return "", fmt.Errorf("must specify --from or --to parameter, or both")
	}
}

// preCheck verifies if current data can be processed correctly
func (cmd *MigrateKeysCommand) preCheck() error {
	logrus.Info("Executing pre-check...")

	// Get current encryption service based on parameters only
	var currentService encryption.Service
	var err error

	if cmd.fromKey != "" {
		// Use fromKey to create encryption service for verification
		currentService, err = encryption.NewService(cmd.fromKey)
	} else {
		// Enable encryption scenario: data should be unencrypted
		// Use noop service to verify data is not encrypted
		currentService, err = encryption.NewService("")
	}

	if err != nil {
		return fmt.Errorf("failed to create current encryption service: %w", err)
	}

	// Check number of keys in database
	var totalCount int64
	if err := cmd.db.Model(&models.APIKey{}).Count(&totalCount).Error; err != nil {
		return fmt.Errorf("failed to get total key count: %w", err)
	}

	if totalCount == 0 {
		logrus.Info("No key data in database, skipping pre-check")
		return nil
	}

	logrus.Infof("Starting validation of %d keys...", totalCount)

	// Batch verify all keys can be decrypted correctly
	offset := 0
	failedCount := 0

	for {
		var keys []models.APIKey
		if err := cmd.db.Order("id").Offset(offset).Limit(migrationBatchSize).Find(&keys).Error; err != nil {
			return fmt.Errorf("failed to get key data: %w", err)
		}

		if len(keys) == 0 {
			break
		}

		for _, key := range keys {
			_, err := currentService.Decrypt(key.KeyValue)
			if err != nil {
				logrus.Errorf("Key ID %d decryption failed: %v", key.ID, err)
				failedCount++
			}
		}

		offset += migrationBatchSize
		// Ensure we don't display more than total count
		actualVerified := offset
		if int64(offset) > totalCount {
			actualVerified = int(totalCount)
		}
		logrus.Infof("Verified %d/%d keys", actualVerified, totalCount)
	}

	if failedCount > 0 {
		return fmt.Errorf("found %d keys that cannot be decrypted, please check the --from parameter", failedCount)
	}

	logrus.Info("Pre-check passed, all keys verified successfully")
	return nil
}

// createBackupTableAndMigrate performs migration using temporary columns
func (cmd *MigrateKeysCommand) createBackupTableAndMigrate() error {
	logrus.Info("Starting key migration using temporary columns...")

	// 1. Clean up any existing temporary columns from previous failed attempts
	if err := cmd.cleanupTempColumns(); err != nil {
		logrus.WithError(err).Warn("Failed to cleanup temporary columns, continuing anyway")
	}

	// 2. Add temporary columns
	if err := cmd.addTempColumns(); err != nil {
		return fmt.Errorf("failed to add temporary columns: %w", err)
	}

	// 3. Create old and new encryption services
	oldService, newService, err := cmd.createMigrationServices()
	if err != nil {
		return err
	}

	// 4. Get total count to migrate
	var totalCount int64
	if err := cmd.db.Model(&models.APIKey{}).Count(&totalCount).Error; err != nil {
		return fmt.Errorf("failed to get key count: %w", err)
	}

	if totalCount == 0 {
		logrus.Info("No keys to migrate")
		return nil
	}

	logrus.Infof("Starting migration of %d keys...", totalCount)

	// 5. Process migration in batches (using WHERE condition instead of OFFSET)
	processedCount := 0

	for {
		var keys []models.APIKey
		// Query only records that haven't been processed yet
		if err := cmd.db.Where("key_value_new IS NULL OR key_value_new = ''").Order("id").Limit(migrationBatchSize).Find(&keys).Error; err != nil {
			return fmt.Errorf("failed to get key data: %w", err)
		}

		if len(keys) == 0 {
			break
		}

		// Process current batch
		if err := cmd.processBatchToTempColumns(keys, oldService, newService); err != nil {
			return fmt.Errorf("failed to process batch data: %w", err)
		}

		processedCount += len(keys)
		logrus.Infof("Processed %d/%d keys", processedCount, totalCount)
	}

	logrus.Info("Data migration to temporary columns completed")
	return nil
}

// cleanupTempColumns removes any existing temporary columns from previous failed attempts
func (cmd *MigrateKeysCommand) cleanupTempColumns() error {
	dbType := cmd.db.Dialector.Name()

	// Check if temporary columns exist
	var columnExists bool
	switch dbType {
	case "mysql":
		var count int64
		cmd.db.Raw("SELECT COUNT(*) FROM information_schema.COLUMNS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'api_keys' AND COLUMN_NAME IN ('key_value_new', 'key_hash_new')").Count(&count)
		columnExists = count > 0
	case "postgres":
		var count int64
		cmd.db.Raw("SELECT COUNT(*) FROM information_schema.columns WHERE table_name = 'api_keys' AND column_name IN ('key_value_new', 'key_hash_new')").Count(&count)
		columnExists = count > 0
	case "sqlite":
		// SQLite doesn't have information_schema, use PRAGMA
		var columns []struct{ Name string }
		cmd.db.Raw("PRAGMA table_info(api_keys)").Scan(&columns)
		for _, col := range columns {
			if col.Name == "key_value_new" || col.Name == "key_hash_new" {
				columnExists = true
				break
			}
		}
	}

	if columnExists {
		logrus.Info("Found existing temporary columns, removing...")
		// Drop columns based on database type
		switch dbType {
		case "sqlite":
			// SQLite doesn't support DROP COLUMN before version 3.35.0
			// Need to recreate table without these columns
			return cmd.dropColumnsSQLite()
		case "postgres":
			// PostgreSQL supports DROP COLUMN IF EXISTS
			if err := cmd.db.Exec("ALTER TABLE api_keys DROP COLUMN IF EXISTS key_value_new").Error; err != nil {
				logrus.WithError(err).Warn("Failed to drop key_value_new column")
			}
			if err := cmd.db.Exec("ALTER TABLE api_keys DROP COLUMN IF EXISTS key_hash_new").Error; err != nil {
				logrus.WithError(err).Warn("Failed to drop key_hash_new column")
			}
		case "mysql":
			// MySQL doesn't support IF EXISTS in DROP COLUMN, ignore errors
			cmd.db.Exec("ALTER TABLE api_keys DROP COLUMN key_value_new")
			cmd.db.Exec("ALTER TABLE api_keys DROP COLUMN key_hash_new")
		}
	}

	return nil
}

// addTempColumns adds temporary columns for migration
func (cmd *MigrateKeysCommand) addTempColumns() error {
	logrus.Info("Adding temporary columns for migration...")

	dbType := cmd.db.Dialector.Name()

	// Add temporary columns with database-specific syntax
	switch dbType {
	case "mysql":
		// MySQL uses ADD without COLUMN keyword
		if err := cmd.db.Exec("ALTER TABLE api_keys ADD key_value_new TEXT").Error; err != nil {
			return fmt.Errorf("failed to add key_value_new column: %w", err)
		}
		if err := cmd.db.Exec("ALTER TABLE api_keys ADD key_hash_new VARCHAR(255)").Error; err != nil {
			return fmt.Errorf("failed to add key_hash_new column: %w", err)
		}
	default:
		// PostgreSQL and SQLite use ADD COLUMN
		if err := cmd.db.Exec("ALTER TABLE api_keys ADD COLUMN key_value_new TEXT").Error; err != nil {
			return fmt.Errorf("failed to add key_value_new column: %w", err)
		}
		if err := cmd.db.Exec("ALTER TABLE api_keys ADD COLUMN key_hash_new VARCHAR(255)").Error; err != nil {
			return fmt.Errorf("failed to add key_hash_new column: %w", err)
		}
	}

	return nil
}

// createMigrationServices creates old and new encryption services for migration
func (cmd *MigrateKeysCommand) createMigrationServices() (oldService, newService encryption.Service, err error) {
	// Create old encryption service (for decryption) based on parameters only
	if cmd.fromKey != "" {
		// Decrypt with specified key
		oldService, err = encryption.NewService(cmd.fromKey)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to create old encryption service: %w", err)
		}
	} else {
		// Enable encryption scenario: data should be unencrypted
		// Use noop service (empty key means no encryption)
		oldService, err = encryption.NewService("")
		if err != nil {
			return nil, nil, fmt.Errorf("failed to create noop encryption service for source: %w", err)
		}
	}

	// Create new encryption service (for encryption) based on parameters only
	if cmd.toKey != "" {
		// Encrypt with specified key
		newService, err = encryption.NewService(cmd.toKey)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to create new encryption service: %w", err)
		}
	} else {
		// Disable encryption scenario: data should be unencrypted
		// Use noop service (empty key means no encryption)
		newService, err = encryption.NewService("")
		if err != nil {
			return nil, nil, fmt.Errorf("failed to create noop encryption service for target: %w", err)
		}
	}

	return oldService, newService, nil
}

// processBatchToTempColumns processes a batch of keys and writes to temporary columns
func (cmd *MigrateKeysCommand) processBatchToTempColumns(keys []models.APIKey, oldService, newService encryption.Service) error {
	// Process all keys in a single transaction
	return cmd.db.Transaction(func(tx *gorm.DB) error {
		for _, key := range keys {
			// 1. Decrypt using old service
			decrypted, err := oldService.Decrypt(key.KeyValue)
			if err != nil {
				return fmt.Errorf("key ID %d decryption failed: %w", key.ID, err)
			}

			// 2. Encrypt using new service
			encrypted, err := newService.Encrypt(decrypted)
			if err != nil {
				return fmt.Errorf("key ID %d encryption failed: %w", key.ID, err)
			}

			// 3. Generate new hash using new service
			newHash := newService.Hash(decrypted)

			// 4. Update temporary columns
			updates := map[string]any{
				"key_value_new": encrypted,
				"key_hash_new":  newHash,
			}

			if err := tx.Model(&models.APIKey{}).Where("id = ?", key.ID).Updates(updates).Error; err != nil {
				return fmt.Errorf("failed to update key ID %d: %w", key.ID, err)
			}
		}
		return nil
	})
}

// dropColumnsSQLite handles dropping columns in SQLite by recreating the table
func (cmd *MigrateKeysCommand) dropColumnsSQLite() error {
	// SQLite doesn't support DROP COLUMN, need to recreate table
	return cmd.db.Transaction(func(tx *gorm.DB) error {
		// Create new table without temporary columns
		if err := tx.Exec(`
			CREATE TABLE api_keys_temp AS
			SELECT id, key_value, key_hash, group_id, status, request_count,
			       failure_count, last_used_at, created_at, updated_at
			FROM api_keys
		`).Error; err != nil {
			return fmt.Errorf("failed to create temp table: %w", err)
		}

		// Drop original table
		if err := tx.Exec("DROP TABLE api_keys").Error; err != nil {
			return fmt.Errorf("failed to drop original table: %w", err)
		}

		// Rename temp table
		if err := tx.Exec("ALTER TABLE api_keys_temp RENAME TO api_keys").Error; err != nil {
			return fmt.Errorf("failed to rename temp table: %w", err)
		}

		return nil
	})
}

// verifyTempColumns verifies temporary columns data integrity
func (cmd *MigrateKeysCommand) verifyTempColumns() error {
	logrus.Info("Verifying temporary columns data integrity...")

	// Create new encryption service for verification
	var newService encryption.Service
	var err error

	if cmd.toKey != "" {
		newService, err = encryption.NewService(cmd.toKey)
	} else {
		newService, err = encryption.NewService("")
	}

	if err != nil {
		return fmt.Errorf("failed to create verification encryption service: %w", err)
	}

	// Get total count
	var totalCount int64
	if err := cmd.db.Model(&models.APIKey{}).Count(&totalCount).Error; err != nil {
		return fmt.Errorf("failed to get key count: %w", err)
	}

	if totalCount == 0 {
		return nil
	}

	// Verify temporary columns have been populated
	var migratedCount int64
	if err := cmd.db.Model(&models.APIKey{}).Where("key_value_new IS NOT NULL AND key_value_new != ''").Count(&migratedCount).Error; err != nil {
		return fmt.Errorf("failed to count migrated keys: %w", err)
	}

	if migratedCount != totalCount {
		return fmt.Errorf("migration incomplete: %d/%d keys migrated", migratedCount, totalCount)
	}

	// Verify a sample of keys can be decrypted correctly
	verifiedCount := 0
	for {
		var keys []struct {
			ID          uint
			KeyValueNew string `gorm:"column:key_value_new"`
		}

		if err := cmd.db.Table("api_keys").Select("id, key_value_new").Where("key_value_new IS NOT NULL").Order("id").Limit(100).Offset(verifiedCount).Scan(&keys).Error; err != nil {
			return fmt.Errorf("failed to get keys for verification: %w", err)
		}

		if len(keys) == 0 {
			break
		}

		for _, key := range keys {
			_, err := newService.Decrypt(key.KeyValueNew)
			if err != nil {
				return fmt.Errorf("key ID %d verification failed: invalid temporary column data: %w", key.ID, err)
			}
		}

		verifiedCount += len(keys)
		if verifiedCount >= int(totalCount) || verifiedCount >= 1000 { // Verify max 1000 keys for performance
			break
		}
	}

	logrus.Infof("Verified %d keys successfully", verifiedCount)
	return nil
}

// switchColumns performs atomic column switching
func (cmd *MigrateKeysCommand) switchColumns() error {
	logrus.Info("Switching to new columns...")

	dbType := cmd.db.Dialector.Name()

	return cmd.db.Transaction(func(tx *gorm.DB) error {
		switch dbType {
		case "sqlite":
			// SQLite requires table recreation
			return cmd.switchColumnsSQLite(tx)
		case "mysql":
			// MySQL version-specific handling
			// 1. Drop old columns
			if err := tx.Exec("ALTER TABLE api_keys DROP COLUMN key_value").Error; err != nil {
				return fmt.Errorf("failed to drop key_value column: %w", err)
			}
			if err := tx.Exec("ALTER TABLE api_keys DROP COLUMN key_hash").Error; err != nil {
				return fmt.Errorf("failed to drop key_hash column: %w", err)
			}

			// 2. Check MySQL version for rename syntax
			var version string
			tx.Raw("SELECT VERSION()").Scan(&version)
			
			// MySQL 8.0+ supports RENAME COLUMN, MySQL 5.x needs CHANGE
			if strings.Contains(version, "5.") {
				// MySQL 5.x: use CHANGE syntax
				if err := tx.Exec("ALTER TABLE api_keys CHANGE key_value_new key_value TEXT").Error; err != nil {
					return fmt.Errorf("failed to rename key_value_new: %w", err)
				}
				if err := tx.Exec("ALTER TABLE api_keys CHANGE key_hash_new key_hash VARCHAR(255)").Error; err != nil {
					return fmt.Errorf("failed to rename key_hash_new: %w", err)
				}
			} else {
				// MySQL 8.0+: use RENAME COLUMN
				if err := tx.Exec("ALTER TABLE api_keys RENAME COLUMN key_value_new TO key_value").Error; err != nil {
					return fmt.Errorf("failed to rename key_value_new: %w", err)
				}
				if err := tx.Exec("ALTER TABLE api_keys RENAME COLUMN key_hash_new TO key_hash").Error; err != nil {
					return fmt.Errorf("failed to rename key_hash_new: %w", err)
				}
			}
		case "postgres":
			// PostgreSQL supports standard column operations
			// 1. Drop old columns
			if err := tx.Exec("ALTER TABLE api_keys DROP COLUMN key_value").Error; err != nil {
				return fmt.Errorf("failed to drop key_value column: %w", err)
			}
			if err := tx.Exec("ALTER TABLE api_keys DROP COLUMN key_hash").Error; err != nil {
				return fmt.Errorf("failed to drop key_hash column: %w", err)
			}

			// 2. Rename new columns
			if err := tx.Exec("ALTER TABLE api_keys RENAME COLUMN key_value_new TO key_value").Error; err != nil {
				return fmt.Errorf("failed to rename key_value_new: %w", err)
			}
			if err := tx.Exec("ALTER TABLE api_keys RENAME COLUMN key_hash_new TO key_hash").Error; err != nil {
				return fmt.Errorf("failed to rename key_hash_new: %w", err)
			}
		}
		return nil
	})
}

// switchColumnsSQLite handles column switching for SQLite
func (cmd *MigrateKeysCommand) switchColumnsSQLite(tx *gorm.DB) error {
	// SQLite doesn't support DROP COLUMN or RENAME COLUMN in older versions
	// Need to recreate table

	// 1. Create new table with correct structure
	if err := tx.Exec(`
		CREATE TABLE api_keys_new (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			key_value TEXT,
			key_hash VARCHAR(255),
			group_id INTEGER,
			status VARCHAR(20),
			request_count INTEGER DEFAULT 0,
			failure_count INTEGER DEFAULT 0,
			last_used_at DATETIME,
			created_at DATETIME,
			updated_at DATETIME
		)
	`).Error; err != nil {
		return fmt.Errorf("failed to create new table: %w", err)
	}

	// 2. Copy data from temporary columns to new table
	if err := tx.Exec(`
		INSERT INTO api_keys_new (id, key_value, key_hash, group_id, status,
		                         request_count, failure_count, last_used_at, created_at, updated_at)
		SELECT id, key_value_new, key_hash_new, group_id, status,
		       request_count, failure_count, last_used_at, created_at, updated_at
		FROM api_keys
	`).Error; err != nil {
		return fmt.Errorf("failed to copy data: %w", err)
	}

	// 3. Drop old table
	if err := tx.Exec("DROP TABLE api_keys").Error; err != nil {
		return fmt.Errorf("failed to drop old table: %w", err)
	}

	// 4. Rename new table
	if err := tx.Exec("ALTER TABLE api_keys_new RENAME TO api_keys").Error; err != nil {
		return fmt.Errorf("failed to rename new table: %w", err)
	}

	return nil
}

// clearCache cleans cache
func (cmd *MigrateKeysCommand) clearCache() error {
	logrus.Info("Starting cache cleanup...")

	if cmd.cacheStore == nil {
		logrus.Info("No cache storage configured, skipping cache cleanup")
		return nil
	}

	logrus.Info("Executing cache cleanup...")
	if err := cmd.cacheStore.FlushDB(); err != nil {
		return fmt.Errorf("cache cleanup failed: %w", err)
	}

	logrus.Info("Cache cleanup successful")
	return nil
}

// cleanupOldTable cleans up old table
func (cmd *MigrateKeysCommand) cleanupOldTable() error {
	logrus.Info("Cleaning up old table...")
	if err := cmd.db.Exec("DROP TABLE IF EXISTS api_keys_old").Error; err != nil {
		return err
	}
	logrus.Info("Old table cleanup successful")
	return nil
}
