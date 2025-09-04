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
	"time"

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
		os.Exit(1)
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
	db              *gorm.DB
	configManager   types.ConfigManager
	cacheStore      store.Store
	fromKey         string
	toKey           string
	backupTableName string
}

// NewMigrateKeysCommand creates a new migration command
func NewMigrateKeysCommand(db *gorm.DB, configManager types.ConfigManager, cacheStore store.Store, fromKey, toKey string) *MigrateKeysCommand {
	backupTableName := fmt.Sprintf("api_keys_migration_backup_%s", time.Now().Format("20060102_150405"))
	return &MigrateKeysCommand{
		db:              db,
		configManager:   configManager,
		cacheStore:      cacheStore,
		fromKey:         fromKey,
		toKey:           toKey,
		backupTableName: backupTableName,
	}
}

// Execute performs the key migration
func (cmd *MigrateKeysCommand) Execute() error {
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

	// 3. Create backup table and migrate data
	if err := cmd.createBackupTableAndMigrate(); err != nil {
		return fmt.Errorf("data migration failed: %w", err)
	}

	// 4. Verify backup table data integrity
	if err := cmd.verifyBackupTable(); err != nil {
		logrus.Errorf("Data verification failed, backup table %s preserved for manual inspection: %v", cmd.backupTableName, err)
		return fmt.Errorf("data verification failed: %w", err)
	}

	// 5. Atomic table switch
	if err := cmd.atomicTableSwitch(); err != nil {
		logrus.Errorf("Table switch failed, backup table %s preserved for manual recovery: %v", cmd.backupTableName, err)
		return fmt.Errorf("table switch failed: %w", err)
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

	// Get current encryption service
	var currentService encryption.Service
	var err error

	if cmd.fromKey != "" {
		// Use fromKey to create encryption service for verification
		currentService, err = cmd.createEncryptionService(cmd.fromKey)
	} else {
		// Use currently configured encryption key
		currentService, err = encryption.NewService(cmd.configManager)
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
		if err := cmd.db.Offset(offset).Limit(migrationBatchSize).Find(&keys).Error; err != nil {
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
		logrus.Infof("Verified %d/%d keys", offset, totalCount)
	}

	if failedCount > 0 {
		return fmt.Errorf("found %d keys that cannot be decrypted, please check current ENCRYPTION_KEY configuration", failedCount)
	}

	logrus.Info("Pre-check passed, all keys verified successfully")
	return nil
}

// createBackupTableAndMigrate creates backup table and performs migration
func (cmd *MigrateKeysCommand) createBackupTableAndMigrate() error {
	logrus.Info("Creating backup table and starting migration...")

	// 1. Create backup table
	if err := cmd.createBackupTable(); err != nil {
		return err
	}

	// 2. Create old and new encryption services
	oldService, newService, err := cmd.createMigrationServices()
	if err != nil {
		return err
	}

	// 3. Get all keys in backup table for migration
	var totalCount int64
	if err := cmd.db.Table(cmd.backupTableName).Count(&totalCount).Error; err != nil {
		return fmt.Errorf("failed to get backup table key count: %w", err)
	}

	if totalCount == 0 {
		logrus.Info("No keys to migrate")
		return nil
	}

	logrus.Infof("Starting migration of %d keys...", totalCount)

	// 4. Process migration in batches
	offset := 0
	migratedCount := 0

	for {
		var keys []models.APIKey
		if err := cmd.db.Table(cmd.backupTableName).Offset(offset).Limit(migrationBatchSize).Find(&keys).Error; err != nil {
			return fmt.Errorf("failed to get backup table key data: %w", err)
		}

		if len(keys) == 0 {
			break
		}

		// Process current batch in transaction
		if err := cmd.db.Transaction(func(tx *gorm.DB) error {
			return cmd.processBatch(tx, keys, oldService, newService)
		}); err != nil {
			return fmt.Errorf("failed to process batch data: %w", err)
		}

		migratedCount += len(keys)
		offset += migrationBatchSize
		logrus.Infof("Migrated %d/%d keys", migratedCount, totalCount)
	}

	logrus.Info("Key migration completed")
	return nil
}

// createBackupTable creates backup table
func (cmd *MigrateKeysCommand) createBackupTable() error {
	// Drop potentially existing old backup table
	cmd.db.Exec("DROP TABLE IF EXISTS " + cmd.backupTableName)

	// Use universal syntax to create backup table and copy data (one step)
	sql := fmt.Sprintf("CREATE TABLE %s AS SELECT * FROM api_keys", cmd.backupTableName)
	if err := cmd.db.Exec(sql).Error; err != nil {
		return fmt.Errorf("failed to create backup table: %w", err)
	}

	logrus.Infof("Backup table %s created successfully", cmd.backupTableName)
	return nil
}

// createMigrationServices creates old and new encryption services for migration
func (cmd *MigrateKeysCommand) createMigrationServices() (oldService, newService encryption.Service, err error) {
	// Create old encryption service (for decryption)
	if cmd.fromKey != "" {
		oldService, err = cmd.createEncryptionService(cmd.fromKey)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to create old encryption service: %w", err)
		}
	} else {
		// Disable encryption scenario: use current configured encryption service
		oldService, err = encryption.NewService(cmd.configManager)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to create current encryption service: %w", err)
		}
	}

	// Create new encryption service (for encryption)
	if cmd.toKey != "" {
		newService, err = cmd.createEncryptionService(cmd.toKey)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to create new encryption service: %w", err)
		}
	} else {
		// Disable encryption scenario: use noop service
		newService, err = cmd.createEncryptionService("")
		if err != nil {
			return nil, nil, fmt.Errorf("failed to create noop encryption service: %w", err)
		}
	}

	return oldService, newService, nil
}

// createEncryptionService creates encryption service with specified key
func (cmd *MigrateKeysCommand) createEncryptionService(key string) (encryption.Service, error) {
	// Create temporary config manager
	tempConfig := &tempConfigManager{
		configManager: cmd.configManager,
		encryptionKey: key,
	}
	return encryption.NewService(tempConfig)
}

// processBatch processes a batch of key migrations
func (cmd *MigrateKeysCommand) processBatch(tx *gorm.DB, keys []models.APIKey, oldService, newService encryption.Service) error {
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

		// 3. Update key in backup table
		if err := tx.Table(cmd.backupTableName).Where("id = ?", key.ID).Update("key_value", encrypted).Error; err != nil {
			return fmt.Errorf("failed to update key ID %d: %w", key.ID, err)
		}
	}

	return nil
}

// verifyBackupTable verifies backup table data integrity
func (cmd *MigrateKeysCommand) verifyBackupTable() error {
	logrus.Info("Verifying backup table data integrity...")

	// Create new encryption service for verification
	var newService encryption.Service
	var err error

	if cmd.toKey != "" {
		newService, err = cmd.createEncryptionService(cmd.toKey)
	} else {
		newService, err = cmd.createEncryptionService("")
	}

	if err != nil {
		return fmt.Errorf("failed to create verification encryption service: %w", err)
	}

	// Verify all keys in backup table can be decrypted correctly
	var totalCount int64
	if err := cmd.db.Table(cmd.backupTableName).Count(&totalCount).Error; err != nil {
		return fmt.Errorf("failed to get backup table key count: %w", err)
	}

	if totalCount == 0 {
		return nil
	}

	offset := 0
	verifiedCount := 0

	for {
		var keys []models.APIKey
		if err := cmd.db.Table(cmd.backupTableName).Offset(offset).Limit(migrationBatchSize).Find(&keys).Error; err != nil {
			return fmt.Errorf("failed to get backup table key data: %w", err)
		}

		if len(keys) == 0 {
			break
		}

		for _, key := range keys {
			_, err := newService.Decrypt(key.KeyValue)
			if err != nil {
				return fmt.Errorf("backup table key ID %d verification failed: %w", key.ID, err)
			}
		}

		verifiedCount += len(keys)
		offset += migrationBatchSize
		logrus.Infof("Verified %d/%d keys", verifiedCount, totalCount)
	}

	logrus.Info("Backup table data verification passed")
	return nil
}

// atomicTableSwitch performs atomic table name switching
func (cmd *MigrateKeysCommand) atomicTableSwitch() error {
	logrus.Info("Executing atomic table switch...")

	dbType := cmd.db.Dialector.Name()
	
	switch dbType {
	case "mysql":
		// MySQL supports simultaneous renaming of multiple tables (atomic operation)
		sql := fmt.Sprintf("RENAME TABLE api_keys TO api_keys_old, %s TO api_keys", cmd.backupTableName)
		if err := cmd.db.Exec(sql).Error; err != nil {
			return fmt.Errorf("MySQL table switch failed: %w", err)
		}
	case "postgres", "sqlite":
		// PostgreSQL and SQLite need step-by-step operations, but ensure atomicity within transaction
		if err := cmd.db.Transaction(func(tx *gorm.DB) error {
			if err := tx.Exec("ALTER TABLE api_keys RENAME TO api_keys_old").Error; err != nil {
				return fmt.Errorf("failed to rename original table: %w", err)
			}
			sql := fmt.Sprintf("ALTER TABLE %s RENAME TO api_keys", cmd.backupTableName)
			if err := tx.Exec(sql).Error; err != nil {
				return fmt.Errorf("failed to rename backup table: %w", err)
			}
			return nil
		}); err != nil {
			return fmt.Errorf("%s table switch failed: %w", dbType, err)
		}
	default:
		return fmt.Errorf("unsupported database type: %s", dbType)
	}

	logrus.Info("Table switch successful")
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

// tempConfigManager temporary config manager for creating encryption service with specified key
type tempConfigManager struct {
	configManager types.ConfigManager
	encryptionKey string
}

func (t *tempConfigManager) GetEncryptionKey() string {
	return t.encryptionKey
}

// Implement other methods of types.ConfigManager interface, delegating to original config manager
func (t *tempConfigManager) IsMaster() bool {
	return t.configManager.IsMaster()
}

func (t *tempConfigManager) GetAuthConfig() types.AuthConfig {
	return t.configManager.GetAuthConfig()
}

func (t *tempConfigManager) GetCORSConfig() types.CORSConfig {
	return t.configManager.GetCORSConfig()
}

func (t *tempConfigManager) GetPerformanceConfig() types.PerformanceConfig {
	return t.configManager.GetPerformanceConfig()
}

func (t *tempConfigManager) GetLogConfig() types.LogConfig {
	return t.configManager.GetLogConfig()
}

func (t *tempConfigManager) GetDatabaseConfig() types.DatabaseConfig {
	return t.configManager.GetDatabaseConfig()
}

func (t *tempConfigManager) GetEffectiveServerConfig() types.ServerConfig {
	return t.configManager.GetEffectiveServerConfig()
}

func (t *tempConfigManager) GetRedisDSN() string {
	return t.configManager.GetRedisDSN()
}

func (t *tempConfigManager) Validate() error {
	return t.configManager.Validate()
}

func (t *tempConfigManager) DisplayServerConfig() {
	t.configManager.DisplayServerConfig()
}

func (t *tempConfigManager) ReloadConfig() error {
	return t.configManager.ReloadConfig()
}