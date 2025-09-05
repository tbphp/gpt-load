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

	// 7. Clean up temporary table
	if err := cmd.dropTempTable(); err != nil {
		logrus.Warnf("Temporary table cleanup failed, can manually drop temp_migration table: %v", err)
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

	// Critical check: if enabling encryption (fromKey is empty), ensure data is not already encrypted
	if cmd.fromKey == "" && cmd.toKey != "" {
		if err := cmd.detectIfAlreadyEncrypted(); err != nil {
			return err
		}
	}

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

// detectIfAlreadyEncrypted checks if data is already encrypted to prevent double encryption
func (cmd *MigrateKeysCommand) detectIfAlreadyEncrypted() error {
	logrus.Info("Detecting if data is already encrypted...")

	// Sample check
	var sampleKeys []models.APIKey
	if err := cmd.db.Limit(20).Where("key_hash IS NOT NULL AND key_hash != ''").Find(&sampleKeys).Error; err != nil {
		return fmt.Errorf("failed to fetch sample keys: %w", err)
	}

	if len(sampleKeys) == 0 {
		logrus.Info("No keys found in database, safe to proceed")
		return nil
	}

	// 1. Hash consistency check
	// If data is unencrypted, key_hash should equal SHA256(key_value)
	hashConsistentCount := 0
	noopService, err := encryption.NewService("") // SHA256 service for unencrypted data
	if err != nil {
		return fmt.Errorf("failed to create noop service: %w", err)
	}

	for _, key := range sampleKeys {
		// For unencrypted data: key_hash should match SHA256(key_value)
		expectedHash := noopService.Hash(key.KeyValue)
		if expectedHash == key.KeyHash {
			hashConsistentCount++
		}
	}

	// 2. Analyze results
	if hashConsistentCount == len(sampleKeys) {
		// All hashes match SHA256(key_value) - data is unencrypted
		logrus.Info("Hash check passed: Data appears to be unencrypted (SHA256 hashes match)")
		return nil // Safe to proceed with encryption
	}

	if hashConsistentCount == 0 {
		// No hashes match SHA256(key_value) - data is already encrypted!

		// 3. Further check: can we decrypt with target key?
		if cmd.toKey != "" {
			targetService, err := encryption.NewService(cmd.toKey)
			if err != nil {
				return fmt.Errorf("failed to create target encryption service: %w", err)
			}

			canDecryptCount := 0
			for _, key := range sampleKeys {
				decrypted, err := targetService.Decrypt(key.KeyValue)
				if err == nil {
					// Verify hash matches
					expectedHash := targetService.Hash(decrypted)
					if expectedHash == key.KeyHash {
						canDecryptCount++
					}
				}
			}

			if canDecryptCount > 0 {
				return fmt.Errorf(
					"CRITICAL: Data is already encrypted with the target key! %d/%d keys can be decrypted with target key",
					canDecryptCount,
					len(sampleKeys),
				)
			}
		}

		return fmt.Errorf(
			"CRITICAL: Data appears to be already encrypted! 0/%d keys have matching SHA256 hashes (expected for unencrypted data)",
			len(sampleKeys),
		)
	}

	// Partial match - inconsistent data state
	return fmt.Errorf(
		"WARNING: Inconsistent data state detected! %d/%d keys appear unencrypted (SHA256 hash matches), %d/%d keys appear encrypted (SHA256 hash doesn't match)",
		hashConsistentCount,
		len(sampleKeys),
		len(sampleKeys)-hashConsistentCount,
		len(sampleKeys),
	)
}

// createBackupTableAndMigrate performs migration using temporary table
func (cmd *MigrateKeysCommand) createBackupTableAndMigrate() error {
	logrus.Info("Starting key migration using temporary table...")

	// 1. Create temporary table
	if err := cmd.createTempTable(); err != nil {
		return fmt.Errorf("failed to create temporary table: %w", err)
	}

	// 2. Create old and new encryption services
	oldService, newService, err := cmd.createMigrationServices()
	if err != nil {
		return err
	}

	// 3. Get total count to migrate
	var totalCount int64
	if err := cmd.db.Model(&models.APIKey{}).Count(&totalCount).Error; err != nil {
		return fmt.Errorf("failed to get key count: %w", err)
	}

	if totalCount == 0 {
		logrus.Info("No keys to migrate")
		return nil
	}

	logrus.Infof("Starting migration of %d keys...", totalCount)

	// 4. Process migration in batches
	processedCount := 0
	lastID := uint(0)

	for {
		var keys []models.APIKey
		// Use ID-based pagination for stable results
		if err := cmd.db.Where("id > ?", lastID).Order("id").Limit(migrationBatchSize).Find(&keys).Error; err != nil {
			return fmt.Errorf("failed to get key data: %w", err)
		}

		if len(keys) == 0 {
			break
		}

		// Process current batch to temp table
		if err := cmd.processBatchToTempTable(keys, oldService, newService); err != nil {
			return fmt.Errorf("failed to process batch data: %w", err)
		}

		processedCount += len(keys)
		lastID = keys[len(keys)-1].ID
		logrus.Infof("Processed %d/%d keys", processedCount, totalCount)
	}

	logrus.Info("Data migration to temporary table completed")
	return nil
}

// createTempTable creates a temporary table for migration
func (cmd *MigrateKeysCommand) createTempTable() error {
	logrus.Info("Creating temporary migration table...")

	// Drop existing temp table if exists
	if err := cmd.db.Exec("DROP TABLE IF EXISTS temp_migration").Error; err != nil {
		logrus.WithError(err).Warn("Failed to drop existing temp table, continuing anyway")
	}

	dbType := cmd.db.Dialector.Name()
	var createTableSQL string

	// Use database-specific syntax for better compatibility
	switch dbType {
	case "mysql":
		createTableSQL = `
			CREATE TABLE temp_migration (
				id BIGINT PRIMARY KEY,
				key_value_new TEXT,
				key_hash_new VARCHAR(255)
			)
		`
	case "postgres":
		createTableSQL = `
			CREATE TABLE temp_migration (
				id BIGINT PRIMARY KEY,
				key_value_new TEXT,
				key_hash_new VARCHAR(255)
			)
		`
	case "sqlite":
		// SQLite uses INTEGER for primary key
		createTableSQL = `
			CREATE TABLE temp_migration (
				id INTEGER PRIMARY KEY,
				key_value_new TEXT,
				key_hash_new VARCHAR(255)
			)
		`
	default:
		// Fallback to generic syntax
		createTableSQL = `
			CREATE TABLE temp_migration (
				id INTEGER PRIMARY KEY,
				key_value_new TEXT,
				key_hash_new VARCHAR(255)
			)
		`
	}

	// Create temp table with minimal structure
	if err := cmd.db.Exec(createTableSQL).Error; err != nil {
		return fmt.Errorf("failed to create temp_migration table: %w", err)
	}

	// Create index for better UPDATE performance (not needed for PRIMARY KEY but helps with JOIN)
	// Skip index creation since id is already PRIMARY KEY which creates an implicit index

	return nil
}

// dropTempTable removes the temporary migration table
func (cmd *MigrateKeysCommand) dropTempTable() error {
	logrus.Info("Dropping temporary migration table...")

	if err := cmd.db.Exec("DROP TABLE IF EXISTS temp_migration").Error; err != nil {
		return fmt.Errorf("failed to drop temp_migration table: %w", err)
	}

	logrus.Info("Temporary table dropped successfully")
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

// processBatchToTempTable processes a batch of keys and writes to temporary table
func (cmd *MigrateKeysCommand) processBatchToTempTable(keys []models.APIKey, oldService, newService encryption.Service) error {
	// Prepare batch data for insertion
	type TempMigration struct {
		ID          uint   `gorm:"primaryKey"`
		KeyValueNew string `gorm:"column:key_value_new"`
		KeyHashNew  string `gorm:"column:key_hash_new"`
	}

	var tempRecords []TempMigration

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

		tempRecords = append(tempRecords, TempMigration{
			ID:          key.ID,
			KeyValueNew: encrypted,
			KeyHashNew:  newHash,
		})
	}

	// Insert batch into temp table in a transaction
	return cmd.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Table("temp_migration").Create(&tempRecords).Error; err != nil {
			return fmt.Errorf("failed to insert batch into temp_migration: %w", err)
		}
		return nil
	})
}

// verifyTempColumns verifies temporary table data integrity
func (cmd *MigrateKeysCommand) verifyTempColumns() error {
	logrus.Info("Verifying temporary table data integrity...")

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

	// Verify temporary table has been populated
	var migratedCount int64
	if err := cmd.db.Table("temp_migration").Count(&migratedCount).Error; err != nil {
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

		if err := cmd.db.Table("temp_migration").Select("id, key_value_new").Order("id").Limit(100).Offset(verifiedCount).Scan(&keys).Error; err != nil {
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

// switchColumns performs atomic update from temporary table to original table
func (cmd *MigrateKeysCommand) switchColumns() error {
	logrus.Info("Updating original table from temporary table...")

	dbType := cmd.db.Dialector.Name()

	return cmd.db.Transaction(func(tx *gorm.DB) error {
		var updateSQL string

		switch dbType {
		case "mysql":
			// MySQL uses JOIN syntax for cross-table UPDATE
			updateSQL = `
				UPDATE api_keys a
				INNER JOIN temp_migration t ON a.id = t.id
				SET a.key_value = t.key_value_new,
				    a.key_hash = t.key_hash_new
			`

		case "postgres":
			// PostgreSQL uses FROM clause for cross-table UPDATE
			updateSQL = `
				UPDATE api_keys
				SET key_value = t.key_value_new,
				    key_hash = t.key_hash_new
				FROM temp_migration t
				WHERE api_keys.id = t.id
			`

		case "sqlite":
			// SQLite uses subquery for cross-table UPDATE (compatible with all versions)
			updateSQL = `
				UPDATE api_keys
				SET key_value = (SELECT key_value_new FROM temp_migration WHERE temp_migration.id = api_keys.id),
				    key_hash = (SELECT key_hash_new FROM temp_migration WHERE temp_migration.id = api_keys.id)
				WHERE EXISTS (SELECT 1 FROM temp_migration WHERE temp_migration.id = api_keys.id)
			`

		default:
			return fmt.Errorf("unsupported database type: %s", dbType)
		}

		logrus.Infof("Executing cross-table UPDATE for %s...", dbType)
		if err := tx.Exec(updateSQL).Error; err != nil {
			return fmt.Errorf("failed to update api_keys from temp_migration: %w", err)
		}

		logrus.Info("Successfully updated original table with migrated data")
		return nil
	})
}

// clearCache cleans cache
func (cmd *MigrateKeysCommand) clearCache() error {
	logrus.Info("Starting cache cleanup...")

	if cmd.cacheStore == nil {
		logrus.Info("No cache storage configured, skipping cache cleanup")
		return nil
	}

	logrus.Info("Executing cache cleanup...")
	if err := cmd.cacheStore.Clear(); err != nil {
		return fmt.Errorf("cache cleanup failed: %w", err)
	}

	logrus.Info("Cache cleanup successful")
	return nil
}
