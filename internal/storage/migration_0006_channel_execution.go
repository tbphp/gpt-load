package storage

import (
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strings"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"gpt-load/internal/channel"
	"gpt-load/internal/storage/models"
)

func retainLegacyMigrationPosition(*gorm.DB) error { return nil }

type legacyExecutionGroup struct {
	ID              uint
	Name            string
	ProviderID      *string
	UpstreamURL     string
	Protocols       models.JSON
	Models          models.JSON
	Config          models.JSON
	WeightManual    *int
	ValidationModel *string
	Enabled         bool
	CreatedAtMS     int64
	UpdatedAtMS     int64
}

func (legacyExecutionGroup) TableName() string { return "groups" }

type legacyExecutionCredential struct {
	ID           uint
	GroupID      uint
	KeyValue     string
	KeyHash      string
	Status       string
	WeightManual *int
	CreatedAtMS  int64
	UpdatedAtMS  int64
}

// migrationCredential mirrors the final credential columns without the Group
// association. Following that association would try to migrate the legacy
// groups table before its channel fields have been backfilled.
type legacyExecutionPrice struct {
	ID                                     uint
	ModelID                                string
	InputPriceNanoUSDPerMillionTokens      *int64
	OutputPriceNanoUSDPerMillionTokens     *int64
	CacheReadPriceNanoUSDPerMillionTokens  *int64
	CacheWritePriceNanoUSDPerMillionTokens *int64
	ContextPriceTiers                      models.JSON
	IsManual                               bool
	CreatedAtMS                            int64
	UpdatedAtMS                            int64
}

type legacyRequestLogAttempt struct {
	RequestID       string
	Sequence        int
	CompletedAtMS   int64
	GroupID         uint
	GroupName       string
	KeyID           uint
	UpstreamModel   string
	StatusCode      int
	DurationMs      int64
	FailureCategory string
	Action          string
	WillRetry       bool
	ErrorCode       string
	ErrorSummary    string
	PricingReceipt  models.JSON
}

func (legacyRequestLogAttempt) TableName() string { return "request_log_attempts" }

type mappedExecutionGroup struct {
	legacyExecutionGroup
	channelID channel.ID
	params    json.RawMessage
	overrides json.RawMessage
}

type migrationGroup struct {
	ID              uint        `gorm:"primaryKey;autoIncrement"`
	Name            string      `gorm:"type:varchar(255);not null;uniqueIndex"`
	ChannelID       string      `gorm:"type:varchar(64);not null"`
	Params          models.JSON `gorm:"type:json;not null"`
	Models          models.JSON `gorm:"type:json;not null"`
	WeightManual    *int
	ValidationModel *string     `gorm:"type:varchar(255)"`
	Overrides       models.JSON `gorm:"type:json"`
	Enabled         bool        `gorm:"not null;default:true"`
	CreatedAtMS     int64       `gorm:"column:created_at_ms;not null;check:chk_group_created_at,created_at_ms >= 0"`
	UpdatedAtMS     int64       `gorm:"column:updated_at_ms;not null;check:chk_group_updated_at,updated_at_ms >= 0"`
}

func (migrationGroup) TableName() string { return "groups" }

func migrateChannelExecution(db *gorm.DB) error {
	if db == nil || db.Dialector == nil {
		return fmt.Errorf("migrate channel execution: database is unavailable")
	}
	if finalChannelExecutionSchema(db) {
		return nil
	}
	if !db.Migrator().HasTable("groups") {
		return fmt.Errorf("migrate channel execution: groups table is missing")
	}

	mappedGroups, err := loadAndMapLegacyExecutionGroups(db)
	if err != nil {
		return err
	}
	legacyCredentials, err := loadLegacyExecutionCredentials(db)
	if err != nil {
		return err
	}
	legacyPrices, err := loadLegacyExecutionPrices(db)
	if err != nil {
		return err
	}

	if db.Migrator().HasTable("upstream_keys") {
		if err := dropChannelMigrationTable(db, "upstream_keys"); err != nil {
			return fmt.Errorf("migrate channel execution: drop upstream_keys: %w", err)
		}
	}
	if err := migrateLegacyGroups(db, mappedGroups); err != nil {
		return err
	}
	if err := migrateLegacyCredentials(db, legacyCredentials); err != nil {
		return err
	}
	if err := migrateLegacyModelPrices(db, mappedGroups, legacyPrices); err != nil {
		return err
	}
	if err := prepareLegacyRequestAndUsageSchema(db); err != nil {
		return err
	}
	if err := createInitialV2Tables(db); err != nil {
		return fmt.Errorf("migrate channel execution: build final schema: %w", err)
	}
	if err := backfillChannelExecutionIdentity(db, mappedGroups); err != nil {
		return err
	}
	return nil
}

func finalChannelExecutionSchema(db *gorm.DB) bool {
	return db.Migrator().HasColumn("groups", "channel_id") &&
		db.Migrator().HasColumn("groups", "params") &&
		db.Migrator().HasTable("credentials") &&
		!db.Migrator().HasTable("upstream_keys") &&
		db.Migrator().HasColumn("model_prices", "channel_id") &&
		db.Migrator().HasColumn("request_log_attempts", "credential_id")
}

func loadAndMapLegacyExecutionGroups(db *gorm.DB) ([]mappedExecutionGroup, error) {
	if !db.Migrator().HasColumn("groups", "upstream_url") ||
		!db.Migrator().HasColumn("groups", "protocols") {
		return nil, fmt.Errorf("migrate channel execution: groups schema is partial")
	}
	var rows []legacyExecutionGroup
	if err := db.Table("groups").Order("id ASC").Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("migrate channel execution: load groups: %w", err)
	}
	registry := channel.NewRegistry()
	result := make([]mappedExecutionGroup, 0, len(rows))
	for _, row := range rows {
		channelID, params, err := mapLegacyExecutionChannel(registry, row)
		if err != nil {
			return nil, fmt.Errorf("migrate channel execution: group %d: %w", row.ID, err)
		}
		overrides := json.RawMessage(`{}`)
		if trimmed := strings.TrimSpace(string(row.Config)); trimmed != "" && trimmed != "null" {
			if !json.Valid(row.Config) {
				return nil, fmt.Errorf("migrate channel execution: group %d has invalid config", row.ID)
			}
			overrides = append(json.RawMessage(nil), row.Config...)
		}
		result = append(result, mappedExecutionGroup{
			legacyExecutionGroup: row,
			channelID:            channelID,
			params:               params,
			overrides:            overrides,
		})
	}
	return result, nil
}

func mapLegacyExecutionChannel(
	registry *channel.Registry,
	row legacyExecutionGroup,
) (channel.ID, json.RawMessage, error) {
	normalizedURL, err := normalizeLegacyChannelURL(row.UpstreamURL)
	if err != nil {
		return "", nil, err
	}
	if id, ok := fixedLegacyChannelURLs[normalizedURL]; ok {
		return id, json.RawMessage(`{}`), nil
	}
	var protocols []string
	if err := json.Unmarshal(row.Protocols, &protocols); err != nil || len(protocols) == 0 {
		return "", nil, fmt.Errorf("protocols must be a non-empty JSON array")
	}
	family := ""
	for _, item := range protocols {
		candidate := ""
		switch strings.TrimSpace(item) {
		case "openai-completions", "openai-responses":
			candidate = "openai"
		case "anthropic":
			candidate = "anthropic"
		case "gemini":
			candidate = "gemini"
		default:
			return "", nil, fmt.Errorf("unsupported protocol %q", item)
		}
		if family != "" && family != candidate {
			return "", nil, fmt.Errorf("multiple upstream protocol families require an explicit channel")
		}
		family = candidate
	}
	var channelID channel.ID
	switch family {
	case "openai":
		channelID = channel.OpenAICompatible
	case "anthropic":
		channelID = channel.AnthropicCompatible
	case "gemini":
		channelID = channel.GeminiCompatible
	default:
		return "", nil, fmt.Errorf("cannot infer channel")
	}
	encoded, err := json.Marshal(map[string]string{"base_url": normalizedURL})
	if err != nil {
		return "", nil, fmt.Errorf("encode channel params: %w", err)
	}
	resolved, err := registry.Resolve(channelID, encoded)
	if err != nil {
		return "", nil, fmt.Errorf("validate channel params: %w", err)
	}
	return channelID, append(json.RawMessage(nil), resolved.TargetConfig...), nil
}

func normalizeLegacyChannelURL(value string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || parsed == nil || parsed.Opaque != "" || parsed.User != nil || parsed.Host == "" ||
		(parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Fragment != "" {
		return "", fmt.Errorf("invalid upstream URL")
	}
	parsed.Scheme = strings.ToLower(parsed.Scheme)
	parsed.Host = strings.ToLower(parsed.Host)
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	parsed.RawPath = strings.TrimRight(parsed.RawPath, "/")
	parsed.ForceQuery = false
	return parsed.String(), nil
}

var fixedLegacyChannelURLs = map[string]channel.ID{
	"https://api.openai.com":                            channel.OpenAI,
	"https://api.openai.com/v1":                         channel.OpenAI,
	"https://api.anthropic.com":                         channel.Anthropic,
	"https://api.anthropic.com/v1":                      channel.Anthropic,
	"https://generativelanguage.googleapis.com":         channel.Gemini,
	"https://generativelanguage.googleapis.com/v1beta":  channel.Gemini,
	"https://api.deepseek.com/v1":                       channel.DeepSeek,
	"https://api.moonshot.cn/v1":                        channel.MoonshotAI,
	"https://api.siliconflow.cn/v1":                     channel.SiliconFlow,
	"https://open.bigmodel.cn/api/paas/v4":              channel.ZhipuAI,
	"https://dashscope.aliyuncs.com/compatible-mode/v1": channel.Alibaba,
	"https://ark.cn-beijing.volces.com/api/v3":          channel.Volcengine,
	"https://openrouter.ai/api/v1":                      channel.OpenRouter,
	"https://api.groq.com/openai/v1":                    channel.Groq,
	"https://api.x.ai/v1":                               channel.XAI,
}

func loadLegacyExecutionCredentials(db *gorm.DB) ([]legacyExecutionCredential, error) {
	if !db.Migrator().HasTable("upstream_keys") {
		return nil, nil
	}
	var rows []legacyExecutionCredential
	if err := db.Table("upstream_keys").Order("id ASC").Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("migrate channel execution: load upstream keys: %w", err)
	}
	return rows, nil
}

func migrateLegacyCredentials(db *gorm.DB, rows []legacyExecutionCredential) error {
	if err := db.AutoMigrate(&models.Credential{}); err != nil {
		return fmt.Errorf("migrate channel execution: create credentials: %w", err)
	}
	for _, row := range rows {
		credential := models.Credential{
			ID: row.ID, GroupID: row.GroupID, Data: row.KeyValue, Fingerprint: row.KeyHash,
			Status: models.CredentialStatus(row.Status), WeightManual: cloneMigrationWeight(row.WeightManual),
			CreatedAtMS: row.CreatedAtMS, UpdatedAtMS: row.UpdatedAtMS,
		}
		if err := db.Clauses(clause.OnConflict{DoNothing: true}).Create(&credential).Error; err != nil {
			return fmt.Errorf("migrate channel execution: copy credential %d: %w", row.ID, err)
		}
	}
	if err := resetMigrationIdentitySequence(db, "credentials"); err != nil {
		return err
	}
	return nil
}

func migrateLegacyGroups(db *gorm.DB, rows []mappedExecutionGroup) error {
	migrator := db.Migrator()
	const legacyTable = "groups_channel_legacy"
	if migrator.HasTable(legacyTable) {
		return fmt.Errorf("migrate channel execution: partial group migration")
	}
	if migrator.HasIndex(&legacyExecutionGroup{}, "idx_groups_name") {
		if err := dropChannelMigrationIndex(db, "groups", "idx_groups_name"); err != nil {
			return fmt.Errorf("migrate channel execution: drop legacy group name index: %w", err)
		}
	}
	if err := migrator.RenameTable("groups", legacyTable); err != nil {
		return fmt.Errorf("migrate channel execution: preserve groups: %w", err)
	}
	if err := preservePostgresLegacySequence(db, legacyTable); err != nil {
		return err
	}
	if err := db.AutoMigrate(&migrationGroup{}); err != nil {
		return fmt.Errorf("migrate channel execution: create channel groups: %w", err)
	}
	for _, row := range rows {
		group := migrationGroup{
			ID: row.ID, Name: row.Name, ChannelID: string(row.channelID), Params: append(models.JSON(nil), row.params...),
			Models: append(models.JSON(nil), row.Models...), WeightManual: cloneMigrationWeight(row.WeightManual),
			ValidationModel: row.ValidationModel, Overrides: append(models.JSON(nil), row.overrides...), Enabled: row.Enabled,
			CreatedAtMS: row.CreatedAtMS, UpdatedAtMS: row.UpdatedAtMS,
		}
		if err := db.Create(&group).Error; err != nil {
			return fmt.Errorf("migrate channel execution: copy group %d: %w", row.ID, err)
		}
	}
	if err := resetMigrationIdentitySequence(db, "groups"); err != nil {
		return err
	}
	if err := dropChannelMigrationTable(db, legacyTable); err != nil {
		return fmt.Errorf("migrate channel execution: drop legacy groups: %w", err)
	}
	return nil
}

func loadLegacyExecutionPrices(db *gorm.DB) ([]legacyExecutionPrice, error) {
	if !db.Migrator().HasTable("model_prices") {
		return nil, nil
	}
	var rows []legacyExecutionPrice
	if err := db.Table("model_prices").Order("model_id ASC").Order("id ASC").Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("migrate channel execution: load model prices: %w", err)
	}
	return rows, nil
}

func migrateLegacyModelPrices(
	db *gorm.DB,
	groups []mappedExecutionGroup,
	prices []legacyExecutionPrice,
) error {
	if db.Migrator().HasTable("model_prices_channel_legacy") {
		return fmt.Errorf("migrate channel execution: partial model price migration")
	}
	if db.Migrator().HasTable("model_prices") {
		if err := db.Migrator().RenameTable("model_prices", "model_prices_channel_legacy"); err != nil {
			return fmt.Errorf("migrate channel execution: preserve model prices: %w", err)
		}
		if err := preservePostgresLegacySequence(db, "model_prices_channel_legacy"); err != nil {
			return err
		}
	}
	if err := db.AutoMigrate(&models.ModelPrice{}); err != nil {
		return fmt.Errorf("migrate channel execution: create channel model prices: %w", err)
	}
	channelsByModel, allChannels, err := legacyPriceChannels(groups)
	if err != nil {
		return err
	}
	for _, price := range prices {
		channels := channelsByModel[price.ModelID]
		if len(channels) == 0 {
			channels = allChannels
		}
		if len(channels) == 0 {
			channels = []channel.ID{channel.OpenAI}
		}
		for index, channelID := range channels {
			id := uint(0)
			if index == 0 {
				id = price.ID
			}
			row := models.ModelPrice{
				ID: id, ChannelID: string(channelID), ModelID: price.ModelID,
				InputPriceNanoUSDPerMillionTokens:      cloneMigrationInt64(price.InputPriceNanoUSDPerMillionTokens),
				OutputPriceNanoUSDPerMillionTokens:     cloneMigrationInt64(price.OutputPriceNanoUSDPerMillionTokens),
				CacheReadPriceNanoUSDPerMillionTokens:  cloneMigrationInt64(price.CacheReadPriceNanoUSDPerMillionTokens),
				CacheWritePriceNanoUSDPerMillionTokens: cloneMigrationInt64(price.CacheWritePriceNanoUSDPerMillionTokens),
				ContextPriceTiers:                      append(models.JSON(nil), price.ContextPriceTiers...), IsManual: price.IsManual,
				CreatedAtMS: price.CreatedAtMS, UpdatedAtMS: price.UpdatedAtMS,
			}
			if err := db.Create(&row).Error; err != nil {
				return fmt.Errorf("migrate channel execution: copy price %s/%s: %w", channelID, price.ModelID, err)
			}
		}
	}
	if err := resetMigrationIdentitySequence(db, "model_prices"); err != nil {
		return err
	}
	if db.Migrator().HasTable("model_prices_channel_legacy") {
		if err := dropChannelMigrationTable(db, "model_prices_channel_legacy"); err != nil {
			return fmt.Errorf("migrate channel execution: drop legacy model prices: %w", err)
		}
	}
	return nil
}

func legacyPriceChannels(
	groups []mappedExecutionGroup,
) (map[string][]channel.ID, []channel.ID, error) {
	sets := make(map[string]map[channel.ID]struct{})
	allSet := make(map[channel.ID]struct{})
	for _, group := range groups {
		allSet[group.channelID] = struct{}{}
		var items []struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(group.Models, &items); err != nil {
			return nil, nil, fmt.Errorf("migrate channel execution: group %d has invalid models", group.ID)
		}
		for _, item := range items {
			modelID := strings.TrimSpace(item.ID)
			if modelID == "" {
				continue
			}
			if sets[modelID] == nil {
				sets[modelID] = make(map[channel.ID]struct{})
			}
			sets[modelID][group.channelID] = struct{}{}
		}
	}
	allChannels := sortedMigrationChannels(allSet)
	result := make(map[string][]channel.ID, len(sets))
	for modelID, set := range sets {
		result[modelID] = sortedMigrationChannels(set)
	}
	return result, allChannels, nil
}

func sortedMigrationChannels(set map[channel.ID]struct{}) []channel.ID {
	result := make([]channel.ID, 0, len(set))
	for id := range set {
		result = append(result, id)
	}
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result
}

func prepareLegacyRequestAndUsageSchema(db *gorm.DB) error {
	migrator := db.Migrator()
	if migrator.HasTable("request_log_attempts") && migrator.HasColumn("request_log_attempts", "key_id") {
		if err := rebuildLegacyRequestLogAttempts(db); err != nil {
			return err
		}
	}
	for table, index := range map[string]string{
		"usage_stats":               "idx_usage_stats_bucket_access_group_model",
		"usage_aggregation_journal": "idx_usage_aggregation_journal_identity",
	} {
		if migrator.HasTable(table) && migrator.HasIndex(table, index) {
			if err := dropChannelMigrationIndex(db, table, index); err != nil {
				return fmt.Errorf("migrate channel execution: drop %s: %w", index, err)
			}
		}
	}
	return nil
}

func rebuildLegacyRequestLogAttempts(db *gorm.DB) error {
	const legacyTable = "request_log_attempts_channel_legacy"
	migrator := db.Migrator()
	if migrator.HasTable(legacyTable) {
		return fmt.Errorf("migrate channel execution: partial request attempt migration")
	}
	var rows []legacyRequestLogAttempt
	if err := db.Table("request_log_attempts").Order("request_id ASC").Order("sequence ASC").Find(&rows).Error; err != nil {
		return fmt.Errorf("migrate channel execution: load request attempts: %w", err)
	}
	for _, index := range []string{
		"idx_request_log_attempts_error_completed_request",
		"idx_request_log_attempts_failure_completed_request",
		"idx_request_log_attempts_status_completed_request",
		"idx_request_log_attempts_model_completed_request",
		"idx_request_log_attempts_key_completed_request",
		"idx_request_log_attempts_group_completed_request",
	} {
		if migrator.HasIndex(&legacyRequestLogAttempt{}, index) {
			if err := dropChannelMigrationIndex(db, "request_log_attempts", index); err != nil {
				return fmt.Errorf("migrate channel execution: drop legacy attempt index %s: %w", index, err)
			}
		}
	}
	if err := dropLegacyAttemptForeignKey(db); err != nil {
		return err
	}
	if err := migrator.RenameTable("request_log_attempts", legacyTable); err != nil {
		return fmt.Errorf("migrate channel execution: preserve request attempts: %w", err)
	}
	if err := db.AutoMigrate(&models.RequestLogAttempt{}); err != nil {
		return fmt.Errorf("migrate channel execution: create request attempts: %w", err)
	}
	for _, row := range rows {
		action := row.Action
		switch action {
		case "cooldown_key":
			action = "cooldown_credential"
		case "fail_key":
			action = "fail_credential"
		}
		attempt := models.RequestLogAttempt{
			RequestID: row.RequestID, Sequence: row.Sequence, CompletedAtMS: row.CompletedAtMS,
			GroupID: row.GroupID, GroupName: row.GroupName, CredentialID: row.KeyID,
			UpstreamModel: row.UpstreamModel, StatusCode: row.StatusCode, DurationMs: row.DurationMs,
			FailureCategory: row.FailureCategory, Action: action, WillRetry: row.WillRetry,
			ErrorCode: row.ErrorCode, ErrorSummary: row.ErrorSummary,
			PricingReceipt: append(models.JSON(nil), row.PricingReceipt...),
		}
		if err := db.Omit("RequestLog").Create(&attempt).Error; err != nil {
			return fmt.Errorf("migrate channel execution: copy request attempt %s/%d: %w", row.RequestID, row.Sequence, err)
		}
	}
	if err := dropChannelMigrationTable(db, legacyTable); err != nil {
		return fmt.Errorf("migrate channel execution: drop legacy request attempts: %w", err)
	}
	return nil
}

func dropChannelMigrationTable(db *gorm.DB, table string) error {
	switch table {
	case "upstream_keys", "groups_channel_legacy", "model_prices_channel_legacy", "request_log_attempts_channel_legacy":
	default:
		return fmt.Errorf("unsupported migration table %q", table)
	}
	if !strings.EqualFold(db.Dialector.Name(), "mysql") {
		return db.Migrator().DropTable(table)
	}
	// The migration already runs on a pinned connection for the advisory lock.
	// GORM's MySQL DropTable opens another pinned connection internally, which
	// fails with gorm.ErrInvalidDB here. Execute the fixed-table drop on the
	// existing connection and always restore foreign-key checks.
	if err := db.Exec("SET FOREIGN_KEY_CHECKS = 0").Error; err != nil {
		return fmt.Errorf("disable foreign key checks: %w", err)
	}
	dropErr := db.Exec("DROP TABLE IF EXISTS ?", clause.Table{Name: table}).Error
	restoreErr := db.Exec("SET FOREIGN_KEY_CHECKS = 1").Error
	if dropErr != nil {
		if restoreErr != nil {
			return fmt.Errorf("drop table: %v; restore foreign key checks: %w", dropErr, restoreErr)
		}
		return dropErr
	}
	if restoreErr != nil {
		return fmt.Errorf("restore foreign key checks: %w", restoreErr)
	}
	return nil
}

func dropChannelMigrationIndex(db *gorm.DB, table, index string) error {
	allowed := map[string]map[string]struct{}{
		"groups": {
			"idx_groups_name": {},
		},
		"request_log_attempts": {
			"idx_request_log_attempts_error_completed_request":   {},
			"idx_request_log_attempts_failure_completed_request": {},
			"idx_request_log_attempts_status_completed_request":  {},
			"idx_request_log_attempts_model_completed_request":   {},
			"idx_request_log_attempts_key_completed_request":     {},
			"idx_request_log_attempts_group_completed_request":   {},
		},
		"usage_stats": {
			"idx_usage_stats_bucket_access_group_model": {},
		},
		"usage_aggregation_journal": {
			"idx_usage_aggregation_journal_identity": {},
		},
	}
	if _, ok := allowed[table][index]; !ok {
		return fmt.Errorf("unsupported migration index %s.%s", table, index)
	}
	if strings.EqualFold(db.Dialector.Name(), "mysql") {
		return db.Exec("ALTER TABLE ? DROP INDEX ?", clause.Table{Name: table}, clause.Column{Name: index}).Error
	}
	return db.Exec("DROP INDEX IF EXISTS ?", clause.Column{Name: index}).Error
}

func dropLegacyAttemptForeignKey(db *gorm.DB) error {
	switch strings.ToLower(db.Dialector.Name()) {
	case "sqlite":
		return nil
	case "mysql":
		if err := db.Exec("ALTER TABLE request_log_attempts DROP FOREIGN KEY fk_request_log_attempts_request_log").Error; err != nil {
			return fmt.Errorf("migrate channel execution: drop legacy attempt foreign key: %w", err)
		}
		return nil
	case "postgres", "postgresql":
		if err := db.Exec("ALTER TABLE request_log_attempts DROP CONSTRAINT IF EXISTS fk_request_log_attempts_request_log").Error; err != nil {
			return fmt.Errorf("migrate channel execution: drop legacy attempt foreign key: %w", err)
		}
		return nil
	default:
		return fmt.Errorf("migrate channel execution: unsupported database driver %q", db.Dialector.Name())
	}
}

func backfillChannelExecutionIdentity(db *gorm.DB, groups []mappedExecutionGroup) error {
	for _, group := range groups {
		for _, table := range []string{"request_logs", "request_log_attempts", "usage_stats", "usage_aggregation_journal"} {
			if !db.Migrator().HasTable(table) || !db.Migrator().HasColumn(table, "channel_id") {
				continue
			}
			if err := db.Table(table).Where("group_id = ?", group.ID).Update("channel_id", string(group.channelID)).Error; err != nil {
				return fmt.Errorf("migrate channel execution: backfill %s channel: %w", table, err)
			}
		}
	}
	if db.Migrator().HasTable("request_logs") && db.Migrator().HasTable("request_log_attempts") &&
		db.Migrator().HasColumn("request_logs", "credential_id") {
		type attemptIdentity struct {
			RequestID    string
			CredentialID uint
		}
		var attempts []attemptIdentity
		if err := db.Table("request_log_attempts").Select("request_id", "credential_id").Order("sequence ASC").Find(&attempts).Error; err != nil {
			return fmt.Errorf("migrate channel execution: load attempt identities: %w", err)
		}
		byRequest := make(map[string]uint)
		ambiguous := make(map[string]bool)
		for _, attempt := range attempts {
			if existing, seen := byRequest[attempt.RequestID]; seen && existing != attempt.CredentialID {
				ambiguous[attempt.RequestID] = true
				continue
			}
			byRequest[attempt.RequestID] = attempt.CredentialID
		}
		for requestID, credentialID := range byRequest {
			if ambiguous[requestID] || credentialID == 0 {
				continue
			}
			if err := db.Table("request_logs").Where("id = ?", requestID).Update("credential_id", credentialID).Error; err != nil {
				return fmt.Errorf("migrate channel execution: backfill request credential: %w", err)
			}
		}
	}
	return nil
}

func cloneMigrationWeight(value *int) *int {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func preservePostgresLegacySequence(db *gorm.DB, legacyTable string) error {
	if db == nil || db.Dialector == nil ||
		!strings.EqualFold(db.Dialector.Name(), "postgres") &&
			!strings.EqualFold(db.Dialector.Name(), "postgresql") {
		return nil
	}
	sequenceNames := map[string]string{
		"groups_channel_legacy":       "groups_channel_legacy_id_seq",
		"model_prices_channel_legacy": "model_prices_channel_legacy_id_seq",
	}
	legacySequence, ok := sequenceNames[legacyTable]
	if !ok {
		return fmt.Errorf("migrate channel execution: unsupported legacy sequence table %q", legacyTable)
	}
	statement := fmt.Sprintf(`DO $migration$
DECLARE sequence_name regclass;
BEGIN
  sequence_name := pg_get_serial_sequence('%s', 'id')::regclass;
  IF sequence_name IS NOT NULL THEN
    EXECUTE format('ALTER SEQUENCE %%s RENAME TO %%I', sequence_name, '%s');
  END IF;
END
$migration$;`, legacyTable, legacySequence)
	if err := db.Exec(statement).Error; err != nil {
		return fmt.Errorf("migrate channel execution: preserve %s sequence: %w", legacyTable, err)
	}
	return nil
}

func resetMigrationIdentitySequence(db *gorm.DB, table string) error {
	if db == nil || db.Dialector == nil ||
		!strings.EqualFold(db.Dialector.Name(), "postgres") &&
			!strings.EqualFold(db.Dialector.Name(), "postgresql") {
		return nil
	}
	if table != "groups" && table != "credentials" && table != "model_prices" {
		return fmt.Errorf("migrate channel execution: unsupported identity table %q", table)
	}
	statement := fmt.Sprintf(
		"SELECT setval(pg_get_serial_sequence('%s', 'id'), COALESCE(MAX(id), 1), COUNT(*) > 0) FROM %s",
		table,
		table,
	)
	if err := db.Exec(statement).Error; err != nil {
		return fmt.Errorf("migrate channel execution: reset %s identity sequence: %w", table, err)
	}
	return nil
}

func cloneMigrationInt64(value *int64) *int64 {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}
