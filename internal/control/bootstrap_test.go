package control

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log"
	"strings"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"gpt-load/internal/platform/encryption"
	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/pricing"
	"gpt-load/internal/state"
	"gpt-load/internal/storage/models"
)

const bootstrapMarkerForTest = models.InternalSystemSettingPrefix + "bootstrap.default_access_key.v1"

var errBootstrapEncryption = errors.New("forced bootstrap encryption failure")

func TestEnsureInitialStatePublishesBuiltinPricesWithAndWithoutMarker(t *testing.T) {
	t.Run("fresh database", func(t *testing.T) {
		fixture := newServiceFixture(t)
		beforeRevision := fixture.manager.Current().Revision

		if err := fixture.service.EnsureInitialState(context.Background()); err != nil {
			t.Fatalf("EnsureInitialState() error = %v", err)
		}

		assertPublishedPrice(t, fixture.priceRuntime, "gpt-4o", pricing.SourceBuiltin, 2_500_000_000)
		if got := fixture.manager.Current().Revision; got != beforeRevision {
			t.Fatalf("ConfigSnapshot revision = %d, want unchanged %d", got, beforeRevision)
		}
		assertModelPriceCount(t, fixture, 0)
	})

	t.Run("existing marker and user row", func(t *testing.T) {
		fixture := newServiceFixture(t)
		zero := int64(0)
		output := int64(12_000_000_000)
		if err := fixture.db.Create(&models.SystemSetting{
			Key: bootstrapMarkerForTest, Value: "true",
		}).Error; err != nil {
			t.Fatalf("create marker: %v", err)
		}
		if err := fixture.db.Create(&models.ModelPrice{
			Pattern:                            "gpt-*",
			InputPriceNanoUSDPerMillionTokens:  &zero,
			OutputPriceNanoUSDPerMillionTokens: &output,
			Source:                             "user",
		}).Error; err != nil {
			t.Fatalf("create user ModelPrice: %v", err)
		}
		beforeRevision := fixture.manager.Current().Revision

		if err := fixture.service.EnsureInitialState(context.Background()); err != nil {
			t.Fatalf("EnsureInitialState() error = %v", err)
		}

		assertPublishedPrice(t, fixture.priceRuntime, "gpt-4o", pricing.SourceUser, 0)
		if got := fixture.manager.Current().Revision; got != beforeRevision {
			t.Fatalf("ConfigSnapshot revision = %d, want unchanged %d", got, beforeRevision)
		}
		assertModelPriceCount(t, fixture, 1)
	})

	t.Run("second service on same file", func(t *testing.T) {
		first, dsn := newFileServiceFixture(t)
		if err := first.service.EnsureInitialState(context.Background()); err != nil {
			t.Fatalf("first EnsureInitialState() error = %v", err)
		}

		second := newServiceFixtureWithDSN(t, dsn)
		if second.priceRuntime.Load() != nil {
			t.Fatal("second PriceRuntime unexpectedly initialized before bootstrap")
		}
		beforeRevision := second.manager.Current().Revision
		if err := second.service.EnsureInitialState(context.Background()); err != nil {
			t.Fatalf("second EnsureInitialState() error = %v", err)
		}

		assertPublishedPrice(t, second.priceRuntime, "gpt-4o", pricing.SourceBuiltin, 2_500_000_000)
		if got := second.manager.Current().Revision; got != beforeRevision {
			t.Fatalf("ConfigSnapshot revision = %d, want unchanged %d", got, beforeRevision)
		}
	})
}

func TestEnsureInitialStateRejectsInvalidPersistedModelPrice(t *testing.T) {
	fixture := newServiceFixture(t)
	value := int64(1_000_000_000)
	if err := fixture.db.Create(&models.ModelPrice{
		Pattern:                           "invalid?pattern",
		InputPriceNanoUSDPerMillionTokens: &value,
		Source:                            "user",
	}).Error; err != nil {
		t.Fatalf("create invalid persisted ModelPrice: %v", err)
	}

	err := fixture.service.EnsureInitialState(context.Background())
	if err == nil {
		t.Fatal("EnsureInitialState() error = nil, want invalid price error")
	}
	if fixture.priceRuntime.Load() != nil {
		t.Fatal("failed bootstrap published a PriceTable")
	}
	assertAccessKeyCount(t, fixture, 0)
	assertBootstrapMarkerCount(t, fixture, 0)
}

func TestEnsureInitialStateRejectsNonUserPersistedModelPrice(t *testing.T) {
	fixture := newServiceFixture(t)
	fixture.service.random = bytes.NewReader(make([]byte, 16))

	const (
		privatePattern = "private-pattern-value"
		privateSource  = "private-source-value"
	)
	if err := fixture.db.Exec("PRAGMA ignore_check_constraints = ON").Error; err != nil {
		t.Fatalf("enable ignored CHECK constraints: %v", err)
	}
	t.Cleanup(func() {
		if err := fixture.db.Exec("PRAGMA ignore_check_constraints = OFF").Error; err != nil {
			t.Errorf("restore CHECK constraints: %v", err)
		}
	})
	if err := fixture.db.Exec(`
			INSERT INTO model_prices (
				pattern,
				output_price_nano_usd_per_million_tokens,
				source,
				created_at_ms,
				updated_at_ms
			)
			VALUES (?, ?, ?, ?, ?)
		`,
		privatePattern,
		int64(73_456_125_000_000),
		privateSource,
		time.Now().UnixMilli(),
		time.Now().UnixMilli(),
	).Error; err != nil {
		t.Fatalf("insert invalid persisted ModelPrice source: %v", err)
	}
	if err := fixture.db.Exec("PRAGMA ignore_check_constraints = OFF").Error; err != nil {
		t.Fatalf("restore CHECK constraints: %v", err)
	}

	err := fixture.service.EnsureInitialState(context.Background())
	if !errors.Is(err, app_errors.ErrInternalServer) {
		t.Fatalf("EnsureInitialState() error = %v, want internal error", err)
	}
	for _, privateValue := range []string{privatePattern, privateSource, "73456125000000"} {
		if strings.Contains(err.Error(), privateValue) {
			t.Fatalf("EnsureInitialState() error leaked persisted value %q: %v", privateValue, err)
		}
	}
	if fixture.priceRuntime.Load() != nil {
		t.Fatal("failed bootstrap published a PriceTable")
	}
	assertAccessKeyCount(t, fixture, 0)
	assertBootstrapMarkerCount(t, fixture, 0)
	assertModelPriceCount(t, fixture, 1)
}

func TestEnsureInitialStateRollsBackBootstrapWhenPriceCompileFails(t *testing.T) {
	fixture := newServiceFixture(t)
	fixture.service.random = bytes.NewReader(make([]byte, 16))
	unset := models.ModelPrice{Pattern: "unset-price", Source: "user"}
	if err := fixture.db.Create(&unset).Error; err != nil {
		t.Fatalf("create invalid persisted ModelPrice: %v", err)
	}

	err := fixture.service.EnsureInitialState(context.Background())
	if err == nil {
		t.Fatal("EnsureInitialState() error = nil, want price compile failure")
	}
	assertAccessKeyCount(t, fixture, 0)
	assertBootstrapMarkerCount(t, fixture, 0)
	if fixture.priceRuntime.Load() != nil {
		t.Fatal("failed bootstrap published a PriceTable")
	}
}

func TestEnsureInitialStateCreatesDefaultAccessKeyAndMarker(t *testing.T) {
	fixture := newServiceFixture(t)
	fixture.service.random = bytes.NewReader([]byte{
		0x00, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07,
		0x08, 0x09, 0x0a, 0x0b, 0x0c, 0x0d, 0x0e, 0x0f,
	})
	beforeRevision := fixture.manager.Current().Revision

	if err := fixture.service.EnsureInitialState(context.Background()); err != nil {
		t.Fatalf("EnsureInitialState() error = %v", err)
	}

	var row models.AccessKey
	if err := fixture.db.First(&row).Error; err != nil {
		t.Fatalf("query default AccessKey: %v", err)
	}
	plaintext, err := fixture.encryption.Decrypt(row.KeyValue)
	if err != nil {
		t.Fatalf("Decrypt(default key) error = %v", err)
	}
	if !strings.HasPrefix(plaintext, "sk-gl-") || len(plaintext) != len("sk-gl-")+32 {
		t.Fatal("default plaintext shape is invalid")
	}
	if _, err := hex.DecodeString(strings.TrimPrefix(plaintext, "sk-gl-")); err != nil {
		t.Fatalf("default plaintext suffix is not hex: %v", err)
	}
	if row.KeyHash != fixture.encryption.Hash(plaintext) {
		t.Fatal("default access key hash does not match plaintext")
	}
	if row.Name != "Default" || row.Status != string(state.AccessKeyStatusActive) ||
		row.RPMLimit != 0 {
		t.Fatalf("default row = %#v", row)
	}
	var filters AccessKeyFilters
	if err := json.Unmarshal(row.Filters, &filters); err != nil {
		t.Fatalf("decode default filters: %v", err)
	}
	if filters.Groups == nil || filters.Protocols == nil || filters.Models == nil ||
		len(filters.Groups) != 0 || len(filters.Protocols) != 0 || len(filters.Models) != 0 {
		t.Fatalf("default filters = %#v, want three non-nil empty slices", filters)
	}

	var marker models.SystemSetting
	if err := fixture.db.First(&marker, "key = ?", bootstrapMarkerForTest).Error; err != nil {
		t.Fatalf("query bootstrap marker: %v", err)
	}
	if marker.Key != bootstrapMarkerForTest || marker.Value != "true" {
		t.Fatalf("bootstrap marker = %#v, want exact JSON true", marker)
	}
	if got := fixture.manager.Current().Revision; got != beforeRevision {
		t.Fatalf("snapshot revision = %d, want unchanged %d", got, beforeRevision)
	}
	if len(fixture.manager.Current().AccessKeysByHash) != 0 {
		t.Fatal("EnsureInitialState() published a runtime snapshot")
	}
}

func TestEnsureInitialStateWithExistingAccessKeyOnlyWritesMarker(t *testing.T) {
	fixture := newServiceFixture(t)
	const plaintext = "gl-existing-access-key"
	ciphertext, err := fixture.encryption.Encrypt(plaintext)
	if err != nil {
		t.Fatalf("Encrypt(existing key) error = %v", err)
	}
	existing := models.AccessKey{
		Name:      "Existing",
		KeyValue:  ciphertext,
		KeyHash:   fixture.encryption.Hash(plaintext),
		KeySuffix: "0000",
		Status:    string(state.AccessKeyStatusActive),
		Filters:   models.JSON(`{"groups":[],"protocols":[],"models":[]}`),
	}
	if err := fixture.db.Create(&existing).Error; err != nil {
		t.Fatalf("create existing AccessKey: %v", err)
	}

	if err := fixture.service.EnsureInitialState(context.Background()); err != nil {
		t.Fatalf("EnsureInitialState() error = %v", err)
	}

	var rows []models.AccessKey
	if err := fixture.db.Order("id ASC").Find(&rows).Error; err != nil {
		t.Fatalf("query AccessKeys: %v", err)
	}
	if len(rows) != 1 || rows[0].ID != existing.ID {
		t.Fatalf("AccessKeys = %#v, want only existing row", rows)
	}
	decrypted, err := fixture.encryption.Decrypt(rows[0].KeyValue)
	if err != nil || decrypted != plaintext {
		t.Fatalf("existing credential = %q, %v, want unchanged", decrypted, err)
	}
	assertBootstrapMarkerCount(t, fixture, 1)
}

func TestEnsureInitialStateDoesNotLogExpectedMarkerMissAsError(t *testing.T) {
	fixture := newServiceFixture(t)
	var logs bytes.Buffer
	fixture.service.db = fixture.db.Session(&gorm.Session{
		Logger: logger.New(log.New(&logs, "\r\n", log.LstdFlags), logger.Config{
			SlowThreshold:        200 * time.Millisecond,
			LogLevel:             logger.Warn,
			ParameterizedQueries: true,
			Colorful:             true,
		}),
	})

	if err := fixture.service.EnsureInitialState(context.Background()); err != nil {
		t.Fatalf("EnsureInitialState() error = %v", err)
	}
	if strings.Contains(logs.String(), "record not found") {
		t.Fatalf("expected marker miss was logged as an error: %q", logs.String())
	}
}

func TestEnsureInitialStateIsIdempotentWhenMarkerExists(t *testing.T) {
	fixture := newServiceFixture(t)
	if err := fixture.db.Create(&models.SystemSetting{
		Key: bootstrapMarkerForTest, Value: "true",
	}).Error; err != nil {
		t.Fatalf("create bootstrap marker: %v", err)
	}
	if err := fixture.db.Exec("DROP TABLE access_keys").Error; err != nil {
		t.Fatalf("drop AccessKey table: %v", err)
	}

	if err := fixture.service.EnsureInitialState(context.Background()); err != nil {
		t.Fatalf("EnsureInitialState() with marker error = %v", err)
	}
	assertBootstrapMarkerCount(t, fixture, 1)
}

func TestEnsureInitialStateDoesNotRecreateDeletedFinalKey(t *testing.T) {
	fixture := newServiceFixture(t)
	fixture.service.random = bytes.NewReader(make([]byte, 16))
	if err := fixture.service.EnsureInitialState(context.Background()); err != nil {
		t.Fatalf("first EnsureInitialState() error = %v", err)
	}
	if err := fixture.db.Where("1 = 1").Delete(&models.AccessKey{}).Error; err != nil {
		t.Fatalf("delete final AccessKey: %v", err)
	}

	if err := fixture.service.EnsureInitialState(context.Background()); err != nil {
		t.Fatalf("second EnsureInitialState() error = %v", err)
	}
	assertAccessKeyCount(t, fixture, 0)
	assertBootstrapMarkerCount(t, fixture, 1)
}

func TestEnsureInitialStateRollsBackWhenEncryptionFails(t *testing.T) {
	fixture := newServiceFixture(t)
	fixture.service.random = bytes.NewReader(make([]byte, 16))
	fixture.service.encryption = bootstrapFailingEncryptService{Service: fixture.encryption}

	err := fixture.service.EnsureInitialState(context.Background())
	if !errors.Is(err, errBootstrapEncryption) {
		t.Fatalf("EnsureInitialState() error = %v, want encryption failure", err)
	}
	assertAccessKeyCount(t, fixture, 0)
	assertBootstrapMarkerCount(t, fixture, 0)
}

func TestEnsureInitialStateRollsBackAccessKeyWhenMarkerWriteFails(t *testing.T) {
	fixture := newServiceFixture(t)
	fixture.service.random = bytes.NewReader(make([]byte, 16))
	if err := fixture.db.Exec(`
		CREATE TRIGGER reject_bootstrap_marker
		BEFORE INSERT ON system_settings
		WHEN NEW.key = '_internal.bootstrap.default_access_key.v1'
		BEGIN
		  SELECT RAISE(ABORT, 'marker rejected');
		END;
	`).Error; err != nil {
		t.Fatalf("create marker rejection trigger: %v", err)
	}

	if err := fixture.service.EnsureInitialState(context.Background()); err == nil {
		t.Fatal("EnsureInitialState() error = nil, want marker rejection")
	}
	assertAccessKeyCount(t, fixture, 0)
	assertBootstrapMarkerCount(t, fixture, 0)
}

func TestEnsureInitialStateDoesNotLogPlaintext(t *testing.T) {
	fixture := newServiceFixture(t)
	randomBytes := bytes.Repeat([]byte{0xab}, 16)
	fixture.service.random = bytes.NewReader(randomBytes)
	expectedPlaintext := "sk-gl-" + hex.EncodeToString(randomBytes)

	var logs bytes.Buffer
	logger := logrus.StandardLogger()
	previousOutput, previousFormatter, previousLevel := logger.Out, logger.Formatter, logger.GetLevel()
	logrus.SetOutput(&logs)
	logrus.SetFormatter(&logrus.JSONFormatter{DisableTimestamp: true})
	logrus.SetLevel(logrus.DebugLevel)
	t.Cleanup(func() {
		logrus.SetOutput(previousOutput)
		logrus.SetFormatter(previousFormatter)
		logrus.SetLevel(previousLevel)
	})

	if err := fixture.service.EnsureInitialState(context.Background()); err != nil {
		t.Fatalf("EnsureInitialState() error = %v", err)
	}
	if strings.Contains(logs.String(), expectedPlaintext) {
		t.Fatalf("bootstrap logs exposed plaintext: %q", logs.String())
	}
}

type bootstrapFailingEncryptService struct {
	encryption.Service
}

func (bootstrapFailingEncryptService) Encrypt(string) (string, error) {
	return "", errBootstrapEncryption
}

func assertAccessKeyCount(t *testing.T, fixture serviceFixture, want int64) {
	t.Helper()
	var count int64
	if err := fixture.db.Model(&models.AccessKey{}).Count(&count).Error; err != nil {
		t.Fatalf("count AccessKeys: %v", err)
	}
	if count != want {
		t.Fatalf("AccessKey count = %d, want %d", count, want)
	}
}

func assertBootstrapMarkerCount(t *testing.T, fixture serviceFixture, want int64) {
	t.Helper()
	var count int64
	if err := fixture.db.Model(&models.SystemSetting{}).
		Where("key = ?", bootstrapMarkerForTest).Count(&count).Error; err != nil {
		t.Fatalf("count bootstrap marker: %v", err)
	}
	if count != want {
		t.Fatalf("bootstrap marker count = %d, want %d", count, want)
	}
}

func assertModelPriceCount(t *testing.T, fixture serviceFixture, want int64) {
	t.Helper()
	var count int64
	if err := fixture.db.Model(&models.ModelPrice{}).Count(&count).Error; err != nil {
		t.Fatalf("count ModelPrice rows: %v", err)
	}
	if count != want {
		t.Fatalf("ModelPrice count = %d, want %d", count, want)
	}
}

func assertPublishedPrice(
	t *testing.T,
	runtime *PriceRuntime,
	model string,
	wantSource pricing.Source,
	wantInput pricing.NanoUSD,
) {
	t.Helper()
	table := runtime.Load()
	if table == nil {
		t.Fatal("PriceRuntime.Load() = nil")
	}
	rule, ok := table.Match(model)
	if !ok {
		t.Fatalf("PriceTable.Match(%q) missed", model)
	}
	if rule.Source != wantSource || !rule.Prices.UncachedInput.Set ||
		rule.Prices.UncachedInput.NanoUSDPerMillion != wantInput {
		t.Fatalf("PriceTable.Match(%q) = %+v", model, rule)
	}
}
