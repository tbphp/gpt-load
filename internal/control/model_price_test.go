package control

import (
	"context"
	"errors"
	"math"
	"strings"
	"sync"
	"testing"

	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/pricing"
	"gpt-load/internal/storage/models"
)

func TestPriceRuntimePublishesImmutableTablesAtomically(t *testing.T) {
	runtime := NewPriceRuntime()
	if runtime.Load() != nil {
		t.Fatal("new PriceRuntime.Load() is non-nil")
	}
	runtime.Publish(nil)
	if runtime.Load() != nil {
		t.Fatal("Publish(nil) changed the runtime")
	}

	oldTable := mustCompilePriceTable(t, pricing.Rule{
		Pattern: "atomic-model",
		Prices: pricing.Prices{
			UncachedInput: pricing.Price{Value: 1, Set: true},
			Output:        pricing.Price{Value: 2, Set: true},
		},
		Source: pricing.SourceUser,
	})
	newTable := mustCompilePriceTable(t, pricing.Rule{
		Pattern: "atomic-model",
		Prices: pricing.Prices{
			UncachedInput: pricing.Price{Value: 10, Set: true},
			Output:        pricing.Price{Value: 20, Set: true},
		},
		Source: pricing.SourceUser,
	})
	runtime.Publish(oldTable)

	const readerCount = 16
	start := make(chan struct{})
	stop := make(chan struct{})
	errs := make(chan string, readerCount)
	var readers sync.WaitGroup
	for range readerCount {
		readers.Add(1)
		go func() {
			defer readers.Done()
			<-start
			for {
				select {
				case <-stop:
					return
				default:
				}
				table := runtime.Load()
				rule, ok := table.Match("atomic-model")
				if !ok {
					errs <- "published table did not match"
					return
				}
				input := rule.Prices.UncachedInput.Value
				output := rule.Prices.Output.Value
				if (input != 1 || output != 2) && (input != 10 || output != 20) {
					errs <- "reader observed a partial table"
					return
				}
			}
		}()
	}
	close(start)
	for range 1000 {
		runtime.Publish(newTable)
		runtime.Publish(oldTable)
	}
	runtime.Publish(newTable)
	close(stop)
	readers.Wait()
	close(errs)
	for message := range errs {
		t.Error(message)
	}

	if runtime.Load() != newTable {
		t.Fatal("Load() did not return the last complete table")
	}
}

func mustCompilePriceTable(t *testing.T, rules ...pricing.Rule) *pricing.Table {
	t.Helper()
	table, err := pricing.Compile(rules)
	if err != nil {
		t.Fatalf("pricing.Compile() error = %v", err)
	}
	return table
}

func TestUpsertModelPricePublishesCompleteUserOverride(t *testing.T) {
	fixture := newServiceFixture(t)
	mustEnsureInitialPrices(t, fixture)

	input, cacheRead, cacheWrite5M, cacheWrite1H, output := 9.1, 9.2, 9.3, 9.4, 9.5
	err := fixture.service.UpsertModelPrice(context.Background(), ModelPriceInput{
		Pattern:       "gpt-*",
		UncachedInput: &input,
		CacheRead:     &cacheRead,
		CacheWrite5M:  &cacheWrite5M,
		CacheWrite1H:  &cacheWrite1H,
		Output:        &output,
	})
	if err != nil {
		t.Fatalf("UpsertModelPrice() error = %v", err)
	}

	table := fixture.priceRuntime.Load()
	rule, ok := table.Match("gpt-4o")
	if !ok {
		t.Fatal("published PriceTable missed gpt-4o")
	}
	wantPrices := pricing.Prices{
		UncachedInput: pricing.Price{Value: input, Set: true},
		CacheRead:     pricing.Price{Value: cacheRead, Set: true},
		CacheWrite5M:  pricing.Price{Value: cacheWrite5M, Set: true},
		CacheWrite1H:  pricing.Price{Value: cacheWrite1H, Set: true},
		Output:        pricing.Price{Value: output, Set: true},
	}
	if rule.Source != pricing.SourceUser || rule.Pattern != "gpt-*" ||
		rule.Prices != wantPrices {
		t.Fatalf("published override = %+v, want prices %+v", rule, wantPrices)
	}

	var rows []models.ModelPrice
	if err := fixture.db.Find(&rows).Error; err != nil {
		t.Fatalf("query ModelPrice rows: %v", err)
	}
	if len(rows) != 1 || rows[0].Source != string(pricing.SourceUser) {
		t.Fatalf("persisted ModelPrice rows = %+v", rows)
	}
}

func TestUpsertModelPriceWritesNullAndExplicitZero(t *testing.T) {
	fixture := newServiceFixture(t)
	mustEnsureInitialPrices(t, fixture)

	first := 1.0
	if err := fixture.service.UpsertModelPrice(context.Background(), ModelPriceInput{
		Pattern:       "round-trip-model",
		UncachedInput: &first,
		CacheRead:     nil,
		CacheWrite5M:  &first,
		CacheWrite1H:  nil,
		Output:        &first,
	}); err != nil {
		t.Fatalf("first UpsertModelPrice() error = %v", err)
	}

	zero := 0.0
	output := 2.0
	if err := fixture.service.UpsertModelPrice(context.Background(), ModelPriceInput{
		Pattern:       "round-trip-model",
		UncachedInput: nil,
		CacheRead:     &zero,
		CacheWrite5M:  nil,
		CacheWrite1H:  &zero,
		Output:        &output,
	}); err != nil {
		t.Fatalf("second UpsertModelPrice() error = %v", err)
	}

	var row models.ModelPrice
	if err := fixture.db.Where("pattern = ?", "round-trip-model").Take(&row).Error; err != nil {
		t.Fatalf("query round-trip ModelPrice: %v", err)
	}
	if row.InputPrice != nil || row.CacheWrite5MPrice != nil {
		t.Fatalf("nullable columns = input:%v cache_write_5m:%v, want nil", row.InputPrice, row.CacheWrite5MPrice)
	}
	if row.CacheReadPrice == nil || *row.CacheReadPrice != 0 ||
		row.CacheWrite1HPrice == nil || *row.CacheWrite1HPrice != 0 ||
		row.OutputPrice == nil || *row.OutputPrice != output {
		t.Fatalf("explicit numeric columns = %+v", row)
	}

	rule, ok := fixture.priceRuntime.Load().Match("round-trip-model")
	if !ok || rule.Prices.UncachedInput.Set || rule.Prices.CacheWrite5M.Set ||
		!rule.Prices.CacheRead.Set || rule.Prices.CacheRead.Value != 0 ||
		!rule.Prices.CacheWrite1H.Set || rule.Prices.CacheWrite1H.Value != 0 {
		t.Fatalf("published nullable/zero prices = %+v, matched=%v", rule.Prices, ok)
	}
}

func TestUpsertModelPriceRejectsInvalidInputBeforePersistence(t *testing.T) {
	one := 1.0
	nan := math.NaN()
	positiveInfinity := math.Inf(1)
	negativeInfinity := math.Inf(-1)
	negative := -1.0
	tests := []struct {
		name  string
		input ModelPriceInput
	}{
		{
			name: "NaN with another valid price",
			input: ModelPriceInput{
				Pattern:   "private-invalid-pattern",
				CacheRead: &nan,
				Output:    &one,
			},
		},
		{
			name: "positive infinity",
			input: ModelPriceInput{
				Pattern: "private-invalid-pattern",
				Output:  &positiveInfinity,
			},
		},
		{
			name: "negative infinity",
			input: ModelPriceInput{
				Pattern: "private-invalid-pattern",
				Output:  &negativeInfinity,
			},
		},
		{
			name: "negative price",
			input: ModelPriceInput{
				Pattern: "private-invalid-pattern",
				Output:  &negative,
			},
		},
		{
			name:  "all prices unset",
			input: ModelPriceInput{Pattern: "private-invalid-pattern"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newServiceFixture(t)
			mustEnsureInitialPrices(t, fixture)
			beforeTable := fixture.priceRuntime.Load()

			err := fixture.service.UpsertModelPrice(context.Background(), test.input)
			if err != app_errors.ErrValidation {
				t.Fatalf("UpsertModelPrice() error = %v, want fixed validation error", err)
			}
			if strings.Contains(err.Error(), test.input.Pattern) {
				t.Fatalf("UpsertModelPrice() error leaked input pattern: %v", err)
			}
			if fixture.priceRuntime.Load() != beforeTable {
				t.Fatal("invalid input changed PriceRuntime")
			}
			assertModelPriceCount(t, fixture, 0)
		})
	}
}

func TestUpsertModelPriceFailureRollsBackAndKeepsRuntime(t *testing.T) {
	t.Run("invalid input", func(t *testing.T) {
		fixture := newServiceFixture(t)
		mustEnsureInitialPrices(t, fixture)
		beforeTable := fixture.priceRuntime.Load()

		value := 1.0
		err := fixture.service.UpsertModelPrice(context.Background(), ModelPriceInput{
			Pattern: "invalid?pattern", UncachedInput: &value,
		})
		if err == nil {
			t.Fatal("UpsertModelPrice() error = nil, want validation failure")
		}
		if fixture.priceRuntime.Load() != beforeTable {
			t.Fatal("invalid input changed PriceRuntime")
		}
		assertModelPriceCount(t, fixture, 0)
	})

	t.Run("trigger rejection", func(t *testing.T) {
		fixture := newServiceFixture(t)
		mustEnsureInitialPrices(t, fixture)
		beforeTable := fixture.priceRuntime.Load()
		if err := fixture.db.Exec(`
			CREATE TRIGGER reject_model_price
			BEFORE INSERT ON model_prices
			BEGIN
			  SELECT RAISE(ABORT, 'model price rejected');
			END;
		`).Error; err != nil {
			t.Fatalf("create rejection trigger: %v", err)
		}

		value := 1.0
		err := fixture.service.UpsertModelPrice(context.Background(), ModelPriceInput{
			Pattern: "rejected-model", Output: &value,
		})
		if !errors.Is(err, app_errors.ErrDatabase) {
			t.Fatalf("UpsertModelPrice() error = %v, want database error", err)
		}
		if fixture.priceRuntime.Load() != beforeTable {
			t.Fatal("trigger rejection changed PriceRuntime")
		}
		assertModelPriceCount(t, fixture, 0)
	})

	t.Run("commit failure", func(t *testing.T) {
		fixture, dsn := newFileServiceFixture(t)
		mustEnsureInitialPrices(t, fixture)
		beforeTable := fixture.priceRuntime.Load()
		releaseReader := holdRollbackJournalReadLock(t, fixture.db, dsn)

		value := 1.0
		err := fixture.service.UpsertModelPrice(context.Background(), ModelPriceInput{
			Pattern: "commit-failure-model", Output: &value,
		})
		if !errors.Is(err, app_errors.ErrDatabase) {
			t.Fatalf("UpsertModelPrice() error = %v, want database error", err)
		}
		releaseReader()
		if fixture.priceRuntime.Load() != beforeTable {
			t.Fatal("failed commit changed PriceRuntime")
		}
		assertModelPriceCount(t, fixture, 0)
	})
}

func TestResetModelPriceRestoresBuiltinAndIsIdempotent(t *testing.T) {
	fixture := newServiceFixture(t)
	mustEnsureInitialPrices(t, fixture)

	input, output := 99.0, 100.0
	if err := fixture.service.UpsertModelPrice(context.Background(), ModelPriceInput{
		Pattern: "gpt-4o", UncachedInput: &input, Output: &output,
	}); err != nil {
		t.Fatalf("UpsertModelPrice() error = %v", err)
	}
	assertPublishedPrice(t, fixture.priceRuntime, "gpt-4o", pricing.SourceUser, input)

	if err := fixture.service.ResetModelPrice(context.Background(), "gpt-4o"); err != nil {
		t.Fatalf("ResetModelPrice() error = %v", err)
	}
	assertPublishedPrice(t, fixture.priceRuntime, "gpt-4o", pricing.SourceBuiltin, 2.5)
	assertModelPriceCount(t, fixture, 0)

	beforeTable := fixture.priceRuntime.Load()
	if err := fixture.service.ResetModelPrice(context.Background(), "missing-pattern"); err != nil {
		t.Fatalf("idempotent ResetModelPrice() error = %v", err)
	}
	if fixture.priceRuntime.Load() == nil || fixture.priceRuntime.Load() == beforeTable {
		t.Fatal("idempotent reset did not publish the precompiled complete table")
	}
	assertPublishedPrice(t, fixture.priceRuntime, "gpt-4o", pricing.SourceBuiltin, 2.5)
	assertModelPriceCount(t, fixture, 0)
}

func TestModelPriceWritesDoNotPublishConfigSnapshot(t *testing.T) {
	fixture := newServiceFixture(t)
	mustEnsureInitialPrices(t, fixture)
	beforeSnapshot := fixture.manager.Current()
	value := 3.0

	if err := fixture.service.UpsertModelPrice(context.Background(), ModelPriceInput{
		Pattern: "snapshot-model", Output: &value,
	}); err != nil {
		t.Fatalf("UpsertModelPrice() error = %v", err)
	}
	if fixture.manager.Current() != beforeSnapshot {
		t.Fatal("UpsertModelPrice() published ConfigSnapshot")
	}
	if err := fixture.service.ResetModelPrice(context.Background(), "snapshot-model"); err != nil {
		t.Fatalf("ResetModelPrice() error = %v", err)
	}
	if fixture.manager.Current() != beforeSnapshot {
		t.Fatal("ResetModelPrice() published ConfigSnapshot")
	}
}

func TestModelPriceConcurrentReadersSeeCompleteTables(t *testing.T) {
	fixture := newServiceFixture(t)
	mustEnsureInitialPrices(t, fixture)
	input, output := 40.0, 80.0
	override := ModelPriceInput{
		Pattern: "gpt-4o", UncachedInput: &input, Output: &output,
	}

	const readerCount = 8
	start := make(chan struct{})
	stop := make(chan struct{})
	errs := make(chan string, readerCount)
	var readers sync.WaitGroup
	for range readerCount {
		readers.Add(1)
		go func() {
			defer readers.Done()
			<-start
			for {
				select {
				case <-stop:
					return
				default:
				}
				rule, ok := fixture.priceRuntime.Load().Match("gpt-4o")
				if !ok {
					errs <- "concurrent Match missed gpt-4o"
					return
				}
				prices := rule.Prices
				builtin := rule.Source == pricing.SourceBuiltin &&
					prices.UncachedInput.Value == 2.5 && prices.Output.Value == 10
				user := rule.Source == pricing.SourceUser &&
					prices.UncachedInput.Value == input && prices.Output.Value == output
				if !builtin && !user {
					errs <- "concurrent Match observed partial prices"
					return
				}
			}
		}()
	}
	close(start)
	for range 25 {
		if err := fixture.service.UpsertModelPrice(context.Background(), override); err != nil {
			t.Fatalf("concurrent UpsertModelPrice() error = %v", err)
		}
		if err := fixture.service.ResetModelPrice(context.Background(), override.Pattern); err != nil {
			t.Fatalf("concurrent ResetModelPrice() error = %v", err)
		}
	}
	close(stop)
	readers.Wait()
	close(errs)
	for message := range errs {
		t.Error(message)
	}
}

func mustEnsureInitialPrices(t *testing.T, fixture serviceFixture) {
	t.Helper()
	if err := fixture.service.EnsureInitialState(context.Background()); err != nil {
		t.Fatalf("EnsureInitialState() error = %v", err)
	}
	if fixture.priceRuntime.Load() == nil {
		t.Fatal("EnsureInitialState() did not publish PriceTable")
	}
}
