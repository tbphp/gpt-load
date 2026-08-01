package control

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"gpt-load/internal/health"
	"gpt-load/internal/platform/config"
	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/state"
	"gpt-load/internal/storage/models"
)

func TestRestoreGroupKeyClearsProblemStateAndReturnsItem(t *testing.T) {
	for _, test := range []struct {
		name string
		seed func(*state.KeyRegistry, uint, time.Time) bool
	}{
		{
			name: "cooldown",
			seed: func(registry *state.KeyRegistry, keyID uint, now time.Time) bool {
				return registry.SetCooldown(keyID, now.Add(time.Minute))
			},
		},
		{
			name: "blacklisted",
			seed: func(registry *state.KeyRegistry, keyID uint, _ time.Time) bool {
				return registry.SetBlacklisted(keyID)
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newServiceFixture(t)
			now := time.Date(2026, time.August, 1, 12, 0, 0, 0, time.UTC)
			fixture.service.now = func() time.Time { return now }
			group := validControlGroup("restore-" + test.name)
			if err := fixture.db.Create(group).Error; err != nil {
				t.Fatal(err)
			}
			row := seedManagedUpstreamKey(t, fixture, group.ID, "provider-secret-restore", models.UpstreamKeyStatusActive, nil)
			if !test.seed(fixture.registry, row.ID, now) {
				t.Fatal("seed runtime problem state")
			}
			for range 4 {
				if _, ok := fixture.registry.IncrFailure(row.ID); !ok {
					t.Fatal("seed Registry failure")
				}
			}
			for index := 0; index < 8; index++ {
				fixture.stats.RecordSuccess(row.ID, now.Add(-2*time.Minute+time.Duration(index)*time.Second))
			}
			for index := 0; index < 4; index++ {
				fixture.stats.RecordFailure(row.ID, health.FailureCategoryInvalidKey, http.StatusUnauthorized, now.Add(-time.Minute+time.Duration(index)*time.Second))
			}

			got, err := fixture.service.RestoreGroupKey(t.Context(), group.ID, row.ID)
			if err != nil {
				t.Fatalf("RestoreGroupKey() error = %v", err)
			}
			if got.ID != row.ID || got.Mask != "prov****tore" || got.ConfiguredStatus != "active" ||
				got.EffectiveStatus != "available" || got.WeightMode != "auto" || got.Weight == nil || *got.Weight != 64 ||
				got.RecentSuccessCount != 8 || got.RecentFailureCount != 4 || got.ConsecutiveFailureCount != 0 ||
				got.LastFailureCategory != "ambiguous" || got.LastStatusCode != nil || got.CooldownUntilMS != nil ||
				got.Recovery != (GroupKeyRecoveryResponse{Mode: "none"}) {
				t.Fatalf("restored item = %#v", got)
			}
			view, exists := findRuntimeKey(fixture.registry.Snapshot(), row.ID)
			if !exists || view.Blacklisted || !view.CooldownUntil.IsZero() || view.FailureCount != 0 || view.WeightAuto != 64 {
				t.Fatalf("restored Registry view = %#v, exists=%t", view, exists)
			}
			if stats := fixture.stats.Snapshot(row.ID, now); stats != (health.KeyStats{Success: 8, Failure: 4}) {
				t.Fatalf("restored stats = %#v", stats)
			}
		})
	}
}

func TestRestoreGroupKeyRejectsNormalAndDisabledWithoutMutation(t *testing.T) {
	for _, status := range []models.UpstreamKeyStatus{
		models.UpstreamKeyStatusActive,
		models.UpstreamKeyStatusDisabled,
	} {
		t.Run(string(status), func(t *testing.T) {
			fixture := newServiceFixture(t)
			group := validControlGroup("restore-invalid-" + string(status))
			if err := fixture.db.Create(group).Error; err != nil {
				t.Fatal(err)
			}
			row := seedManagedUpstreamKey(t, fixture, group.ID, "provider-secret-invalid", status, nil)
			before := fixture.registry.Snapshot()

			_, err := fixture.service.RestoreGroupKey(t.Context(), group.ID, row.ID)
			if !errors.Is(err, app_errors.ErrInvalidKeyState) {
				t.Fatalf("RestoreGroupKey() error = %#v, want INVALID_KEY_STATE", err)
			}
			if after := fixture.registry.Snapshot(); len(after) != len(before) || after[0] != before[0] {
				t.Fatalf("Registry after rejection = %#v, want %#v", after, before)
			}
		})
	}
}

func TestRestoreGroupKeyReturnsPreciseNotFound(t *testing.T) {
	fixture := newServiceFixture(t)
	group := validControlGroup("restore-not-found")
	other := validControlGroup("restore-not-found-other")
	if err := fixture.db.Create(group).Error; err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.Create(other).Error; err != nil {
		t.Fatal(err)
	}
	row := seedManagedUpstreamKey(t, fixture, other.ID, "provider-secret-other", models.UpstreamKeyStatusActive, nil)

	for _, test := range []struct {
		name    string
		groupID uint
		keyID   uint
		want    string
	}{
		{name: "group", groupID: group.ID + other.ID + 1000, keyID: row.ID, want: "group"},
		{name: "key", groupID: group.ID, keyID: row.ID, want: "key"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := fixture.service.RestoreGroupKey(t.Context(), test.groupID, test.keyID)
			var notFound *controlResourceNotFoundError
			if !errors.As(err, &notFound) || notFound.resource != test.want {
				t.Fatalf("RestoreGroupKey() error = %#v, want %s not found", err, test.want)
			}
		})
	}
}

func TestRestoreGroupKeyAllowsFailureAfterRestorePoint(t *testing.T) {
	fixture := newServiceFixture(t)
	now := time.Date(2026, time.August, 1, 12, 0, 0, 0, time.UTC)
	fixture.service.now = func() time.Time { return now }
	group := validControlGroup("restore-new-failure")
	if err := fixture.db.Create(group).Error; err != nil {
		t.Fatal(err)
	}
	row := seedManagedUpstreamKey(t, fixture, group.ID, "provider-secret-new-failure", models.UpstreamKeyStatusActive, nil)
	if !fixture.registry.SetBlacklisted(row.ID) {
		t.Fatal("seed blacklist")
	}
	if _, err := fixture.service.RestoreGroupKey(t.Context(), group.ID, row.ID); err != nil {
		t.Fatal(err)
	}

	if _, ok := fixture.registry.IncrFailure(row.ID); !ok || !fixture.registry.SetBlacklisted(row.ID) {
		t.Fatal("record post-restore Registry failure")
	}
	fixture.stats.RecordFailure(row.ID, health.FailureCategoryInvalidKey, http.StatusUnauthorized, now.Add(time.Second))

	view, exists := findRuntimeKey(fixture.registry.Snapshot(), row.ID)
	if !exists || !view.Blacklisted || view.FailureCount != 1 {
		t.Fatalf("post-restore Registry view = %#v, exists=%t", view, exists)
	}
	stats := fixture.stats.Snapshot(row.ID, now.Add(time.Second))
	if stats.Failure != 1 || stats.ConsecutiveFailure != 1 || stats.LastFailureCategory != health.FailureCategoryInvalidKey {
		t.Fatalf("post-restore stats = %#v", stats)
	}
}

type postRestoreFailureCoordinator struct {
	after func()
	calls int
}

type recoverBeforeRestoreCoordinator struct {
	registry  *state.KeyRegistry
	stats     *health.StatsStore
	ref       state.KeyRef
	recovered bool
}

func (coordinator *recoverBeforeRestoreCoordinator) Do(_ uint, restore func()) {
	coordinator.recovered = coordinator.registry.RecoverIfMatch(
		coordinator.ref,
		state.DefaultWeight,
	)
	if coordinator.recovered {
		coordinator.stats.Reset(coordinator.ref.ID)
	}
	restore()
}

func TestRestoreGroupKeyRejectsWhenValidationRecoveryWinsCoordinator(t *testing.T) {
	fixture := newServiceFixture(t)
	now := time.Date(2026, time.August, 1, 12, 15, 0, 0, time.UTC)
	fixture.service.now = func() time.Time { return now }
	group := validControlGroup("restore-validation-wins")
	if err := fixture.db.Create(group).Error; err != nil {
		t.Fatal(err)
	}
	row := seedManagedUpstreamKey(t, fixture, group.ID, "provider-secret-validation-wins", models.UpstreamKeyStatusActive, nil)
	if !fixture.registry.SetBlacklisted(row.ID) {
		t.Fatal("seed blacklist")
	}
	refs := fixture.registry.BlacklistedKeys()
	if len(refs) != 1 {
		t.Fatalf("blacklisted refs = %#v, want one", refs)
	}
	coordinator := &recoverBeforeRestoreCoordinator{
		registry: fixture.registry,
		stats:    fixture.stats,
		ref:      refs[0],
	}
	fixture.service.mutations = coordinator

	_, err := fixture.service.RestoreGroupKey(t.Context(), group.ID, row.ID)
	if !coordinator.recovered {
		t.Fatal("validation recovery did not complete before restore callback")
	}
	if !errors.Is(err, app_errors.ErrInvalidKeyState) {
		t.Fatalf("RestoreGroupKey() error = %#v, want INVALID_KEY_STATE", err)
	}
	view, exists := findRuntimeKey(fixture.registry.Snapshot(), row.ID)
	if !exists || view.Blacklisted || view.FailureCount != 0 || view.WeightAuto != state.DefaultWeight {
		t.Fatalf("validation-won Registry view = %#v, exists=%t", view, exists)
	}
}

type beforeRestoreCoordinator struct {
	before func()
}

func (coordinator *beforeRestoreCoordinator) Do(_ uint, restore func()) {
	coordinator.before()
	restore()
}

func TestRestoreGroupKeyEvaluatesCooldownAtCoordinatorLinearizationPoint(t *testing.T) {
	fixture := newServiceFixture(t)
	before := time.Date(2026, time.August, 1, 12, 20, 0, 0, time.UTC)
	current := before
	fixture.service.now = func() time.Time { return current }
	group := validControlGroup("restore-expired-at-linearization")
	if err := fixture.db.Create(group).Error; err != nil {
		t.Fatal(err)
	}
	row := seedManagedUpstreamKey(t, fixture, group.ID, "provider-secret-expired-linearization", models.UpstreamKeyStatusActive, nil)
	if !fixture.registry.SetCooldown(row.ID, before.Add(time.Second)) {
		t.Fatal("seed cooldown")
	}
	fixture.service.mutations = &beforeRestoreCoordinator{before: func() {
		current = before.Add(2 * time.Second)
	}}

	_, err := fixture.service.RestoreGroupKey(t.Context(), group.ID, row.ID)
	if !errors.Is(err, app_errors.ErrInvalidKeyState) {
		t.Fatalf("RestoreGroupKey() error = %#v, want INVALID_KEY_STATE", err)
	}
	view, exists := findRuntimeKey(fixture.registry.Snapshot(), row.ID)
	if !exists || !view.CooldownUntil.Equal(before.Add(time.Second)) {
		t.Fatalf("expired cooldown view = %#v, exists=%t", view, exists)
	}
}

func (coordinator *postRestoreFailureCoordinator) Do(_ uint, mutate func()) {
	coordinator.calls++
	mutate()
	coordinator.after()
}

func TestRestoreGroupKeySerializesNewFailureAfterCompleteRestore(t *testing.T) {
	fixture := newServiceFixture(t)
	now := time.Date(2026, time.August, 1, 12, 30, 0, 0, time.UTC)
	fixture.service.now = func() time.Time { return now }
	group := validControlGroup("restore-linearized-failure")
	if err := fixture.db.Create(group).Error; err != nil {
		t.Fatal(err)
	}
	row := seedManagedUpstreamKey(t, fixture, group.ID, "provider-secret-linearized", models.UpstreamKeyStatusActive, nil)
	if !fixture.registry.SetBlacklisted(row.ID) {
		t.Fatal("seed blacklist")
	}
	coordinator := &postRestoreFailureCoordinator{after: func() {
		if _, ok := fixture.registry.IncrFailure(row.ID); !ok || !fixture.registry.SetBlacklisted(row.ID) {
			t.Fatal("record serialized post-restore Registry failure")
		}
		fixture.stats.RecordFailure(row.ID, health.FailureCategoryInvalidKey, http.StatusUnauthorized, now.Add(time.Second))
	}}
	fixture.service.mutations = coordinator

	if _, err := fixture.service.RestoreGroupKey(t.Context(), group.ID, row.ID); err != nil {
		t.Fatal(err)
	}
	if coordinator.calls != 1 {
		t.Fatalf("mutation coordinator calls = %d, want 1", coordinator.calls)
	}
	view, exists := findRuntimeKey(fixture.registry.Snapshot(), row.ID)
	if !exists || !view.Blacklisted || view.FailureCount != 1 {
		t.Fatalf("serialized post-restore Registry view = %#v, exists=%t", view, exists)
	}
	stats := fixture.stats.Snapshot(row.ID, now.Add(time.Second))
	if stats.Failure != 1 || stats.ConsecutiveFailure != 1 || stats.LastFailureCategory != health.FailureCategoryInvalidKey {
		t.Fatalf("serialized post-restore stats = %#v", stats)
	}
}

func TestRestoreGroupKeyHTTPStrictBodyAndStateError(t *testing.T) {
	initControlI18n(t)
	fixture := newServiceFixture(t)
	now := time.Date(2026, time.August, 1, 12, 45, 0, 0, time.UTC)
	fixture.service.now = func() time.Time { return now }
	group := validControlGroup("restore-http")
	if err := fixture.db.Create(group).Error; err != nil {
		t.Fatal(err)
	}
	cooldown := seedManagedUpstreamKey(t, fixture, group.ID, "provider-secret-restore-http", models.UpstreamKeyStatusActive, nil)
	normal := seedManagedUpstreamKey(t, fixture, group.ID, "provider-secret-normal-http", models.UpstreamKeyStatusActive, nil)
	if !fixture.registry.SetCooldown(cooldown.ID, now.Add(time.Minute)) {
		t.Fatal("seed cooldown")
	}
	engine := gin.New()
	NewServer(&config.Config{AuthKey: groupKeyHTTPAuth}, fixture.service).RegisterRoutes(engine)

	success := serveGroupKeyHTTPRequest(t, engine, http.MethodPost, fmt.Sprintf("/api/groups/%d/keys/%d/restore", group.ID, cooldown.ID), "", groupKeyHTTPAuth, "en-US")
	if success.Code != http.StatusOK || !bytes.Contains(success.Body.Bytes(), []byte(`"effective_status":"available"`)) {
		t.Fatalf("restore success = %d %s, want available item", success.Code, success.Body.String())
	}
	invalidBody := serveGroupKeyHTTPRequest(t, engine, http.MethodPost, fmt.Sprintf("/api/groups/%d/keys/%d/restore", group.ID, normal.ID), `{"unexpected":true}`, groupKeyHTTPAuth, "en-US")
	if invalidBody.Code != http.StatusBadRequest {
		t.Fatalf("restore invalid body = %d %s, want 400", invalidBody.Code, invalidBody.Body.String())
	}
	invalidState := serveGroupKeyHTTPRequest(t, engine, http.MethodPost, fmt.Sprintf("/api/groups/%d/keys/%d/restore", group.ID, normal.ID), "", groupKeyHTTPAuth, "en-US")
	if invalidState.Code != http.StatusConflict || !bytes.Contains(invalidState.Body.Bytes(), []byte(`"code":"INVALID_KEY_STATE"`)) {
		t.Fatalf("restore invalid state = %d %s, want INVALID_KEY_STATE", invalidState.Code, invalidState.Body.String())
	}
}

func TestBatchGroupKeysEnableDisableAndDelete(t *testing.T) {
	for _, action := range []GroupKeyBatchAction{
		GroupKeyBatchEnable,
		GroupKeyBatchDisable,
		GroupKeyBatchDelete,
	} {
		t.Run(string(action), func(t *testing.T) {
			fixture := newServiceFixture(t)
			now := time.Date(2026, time.August, 1, 13, 0, 0, 0, time.UTC)
			fixture.service.now = func() time.Time { return now }
			group := validControlGroup("batch-" + string(action))
			if err := fixture.db.Create(group).Error; err != nil {
				t.Fatal(err)
			}
			initialStatus := models.UpstreamKeyStatusActive
			if action == GroupKeyBatchEnable {
				initialStatus = models.UpstreamKeyStatusDisabled
			}
			first := seedManagedUpstreamKey(t, fixture, group.ID, "provider-secret-batch-first", initialStatus, nil)
			second := seedManagedUpstreamKey(t, fixture, group.ID, "provider-secret-batch-second", initialStatus, nil)
			untouched := seedManagedUpstreamKey(t, fixture, group.ID, "provider-secret-batch-third", models.UpstreamKeyStatusActive, nil)
			fixture.stats.RecordFailure(first.ID, health.FailureCategoryInvalidKey, http.StatusUnauthorized, now)
			fixture.stats.RecordSuccess(second.ID, now)

			got, err := fixture.service.BatchGroupKeys(t.Context(), group.ID, GroupKeyBatchRequest{
				Action: action, KeyIDs: []uint{second.ID, first.ID},
			})
			if err != nil {
				t.Fatalf("BatchGroupKeys() error = %v", err)
			}
			if len(got.AffectedIDs) != 2 || got.AffectedIDs[0] != first.ID || got.AffectedIDs[1] != second.ID {
				t.Fatalf("affected ids = %v, want sorted [%d %d]", got.AffectedIDs, first.ID, second.ID)
			}

			var rows []models.UpstreamKey
			if err := fixture.db.Where("group_id = ?", group.ID).Order("id ASC").Find(&rows).Error; err != nil {
				t.Fatal(err)
			}
			views := fixture.registry.Snapshot()
			switch action {
			case GroupKeyBatchEnable:
				if len(rows) != 3 || rows[0].Status != models.UpstreamKeyStatusActive || rows[1].Status != models.UpstreamKeyStatusActive {
					t.Fatalf("enabled DB rows = %#v", rows)
				}
				if got.Summary != (GroupKeySummaryResponse{Total: 3, Available: 3}) {
					t.Fatalf("enabled summary = %#v", got.Summary)
				}
			case GroupKeyBatchDisable:
				if len(rows) != 3 || rows[0].Status != models.UpstreamKeyStatusDisabled || rows[1].Status != models.UpstreamKeyStatusDisabled {
					t.Fatalf("disabled DB rows = %#v", rows)
				}
				if got.Summary != (GroupKeySummaryResponse{Total: 3, Available: 1, Disabled: 2}) {
					t.Fatalf("disabled summary = %#v", got.Summary)
				}
			case GroupKeyBatchDelete:
				if len(rows) != 1 || rows[0].ID != untouched.ID || len(views) != 1 || views[0].ID != untouched.ID {
					t.Fatalf("delete DB rows / Registry views = %#v / %#v", rows, views)
				}
				if got.Summary != (GroupKeySummaryResponse{Total: 1, Available: 1}) {
					t.Fatalf("delete summary = %#v", got.Summary)
				}
				if stats := fixture.stats.Snapshot(first.ID, now); stats != (health.KeyStats{}) {
					t.Fatalf("deleted first stats = %#v", stats)
				}
				if stats := fixture.stats.Snapshot(second.ID, now); stats != (health.KeyStats{}) {
					t.Fatalf("deleted second stats = %#v", stats)
				}
			}
		})
	}
}

func TestBatchGroupKeysRejectsInvalidRequestsWithoutMutation(t *testing.T) {
	for _, test := range []struct {
		name    string
		action  GroupKeyBatchAction
		ids     func(first, second, other uint) []uint
		wantErr *app_errors.APIError
	}{
		{name: "unknown action", action: "archive", ids: func(first, _, _ uint) []uint { return []uint{first} }, wantErr: app_errors.ErrValidation},
		{name: "empty", action: GroupKeyBatchDisable, ids: func(_, _, _ uint) []uint { return nil }, wantErr: app_errors.ErrValidation},
		{name: "duplicate", action: GroupKeyBatchDisable, ids: func(first, _, _ uint) []uint { return []uint{first, first} }, wantErr: app_errors.ErrValidation},
		{name: "cross group", action: GroupKeyBatchDisable, ids: func(first, _, other uint) []uint { return []uint{first, other} }, wantErr: app_errors.ErrResourceNotFound},
		{name: "missing", action: GroupKeyBatchDisable, ids: func(first, _, _ uint) []uint { return []uint{first, 999999} }, wantErr: app_errors.ErrResourceNotFound},
		{name: "over limit", action: GroupKeyBatchDisable, ids: func(_, _, _ uint) []uint {
			ids := make([]uint, 101)
			for index := range ids {
				ids[index] = uint(index + 1)
			}
			return ids
		}, wantErr: app_errors.ErrValidation},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newServiceFixture(t)
			group := validControlGroup("batch-reject-" + test.name)
			otherGroup := validControlGroup("batch-reject-other-" + test.name)
			if err := fixture.db.Create(group).Error; err != nil {
				t.Fatal(err)
			}
			if err := fixture.db.Create(otherGroup).Error; err != nil {
				t.Fatal(err)
			}
			first := seedManagedUpstreamKey(t, fixture, group.ID, "provider-secret-reject-first", models.UpstreamKeyStatusActive, nil)
			second := seedManagedUpstreamKey(t, fixture, group.ID, "provider-secret-reject-second", models.UpstreamKeyStatusActive, nil)
			other := seedManagedUpstreamKey(t, fixture, otherGroup.ID, "provider-secret-reject-other", models.UpstreamKeyStatusActive, nil)
			before := fixture.registry.Snapshot()

			_, err := fixture.service.BatchGroupKeys(t.Context(), group.ID, GroupKeyBatchRequest{Action: test.action, KeyIDs: test.ids(first.ID, second.ID, other.ID)})
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("BatchGroupKeys() error = %#v, want %s", err, test.wantErr.Code)
			}
			if after := fixture.registry.Snapshot(); len(after) != len(before) {
				t.Fatalf("Registry length after rejection = %d, want %d", len(after), len(before))
			} else {
				for index := range after {
					if after[index] != before[index] {
						t.Fatalf("Registry after rejection = %#v, want %#v", after, before)
					}
				}
			}
			var persisted []models.UpstreamKey
			if err := fixture.db.Where("group_id = ?", group.ID).Order("id ASC").Find(&persisted).Error; err != nil {
				t.Fatal(err)
			}
			if len(persisted) != 2 || persisted[0].Status != models.UpstreamKeyStatusActive || persisted[1].Status != models.UpstreamKeyStatusActive {
				t.Fatalf("DB rows after rejection = %#v", persisted)
			}
		})
	}
}

func TestBatchGroupKeysDBAndRegistryFailuresAreAtomic(t *testing.T) {
	t.Run("Registry validation before DB", func(t *testing.T) {
		fixture := newServiceFixture(t)
		group := validControlGroup("batch-registry-failure")
		if err := fixture.db.Create(group).Error; err != nil {
			t.Fatal(err)
		}
		first := seedManagedUpstreamKey(t, fixture, group.ID, "provider-secret-registry-first", models.UpstreamKeyStatusActive, nil)
		second := seedManagedUpstreamKey(t, fixture, group.ID, "provider-secret-registry-second", models.UpstreamKeyStatusActive, nil)
		fixture.registry.RemoveKey(second.ID)

		_, err := fixture.service.BatchGroupKeys(t.Context(), group.ID, GroupKeyBatchRequest{Action: GroupKeyBatchDisable, KeyIDs: []uint{first.ID, second.ID}})
		if err == nil {
			t.Fatal("BatchGroupKeys() error = nil, want Registry mismatch")
		}
		var rows []models.UpstreamKey
		if err := fixture.db.Where("group_id = ?", group.ID).Order("id ASC").Find(&rows).Error; err != nil {
			t.Fatal(err)
		}
		if len(rows) != 2 || rows[0].Status != models.UpstreamKeyStatusActive || rows[1].Status != models.UpstreamKeyStatusActive {
			t.Fatalf("DB rows after Registry failure = %#v", rows)
		}
	})

	t.Run("DB failure before Registry", func(t *testing.T) {
		fixture := newServiceFixture(t)
		group := validControlGroup("batch-db-failure")
		if err := fixture.db.Create(group).Error; err != nil {
			t.Fatal(err)
		}
		first := seedManagedUpstreamKey(t, fixture, group.ID, "provider-secret-db-first", models.UpstreamKeyStatusActive, nil)
		second := seedManagedUpstreamKey(t, fixture, group.ID, "provider-secret-db-second", models.UpstreamKeyStatusActive, nil)
		if err := fixture.db.Exec(`CREATE TRIGGER reject_batch_status BEFORE UPDATE OF status ON upstream_keys BEGIN SELECT RAISE(ABORT, 'blocked'); END`).Error; err != nil {
			t.Fatal(err)
		}

		_, err := fixture.service.BatchGroupKeys(t.Context(), group.ID, GroupKeyBatchRequest{Action: GroupKeyBatchDisable, KeyIDs: []uint{first.ID, second.ID}})
		if !errors.Is(err, app_errors.ErrDatabase) {
			t.Fatalf("BatchGroupKeys() error = %#v, want DATABASE_ERROR", err)
		}
		for _, view := range fixture.registry.Snapshot() {
			if view.GroupID == group.ID && view.Status != state.KeyStatusActive {
				t.Fatalf("Registry view after DB failure = %#v", view)
			}
		}
		var rows []models.UpstreamKey
		if err := fixture.db.Where("group_id = ?", group.ID).Order("id ASC").Find(&rows).Error; err != nil {
			t.Fatal(err)
		}
		if len(rows) != 2 || rows[0].Status != models.UpstreamKeyStatusActive || rows[1].Status != models.UpstreamKeyStatusActive {
			t.Fatalf("DB rows after DB failure = %#v", rows)
		}
	})
}

func TestBatchGroupKeysRegistryApplyFailureRestoresDBAndRegistry(t *testing.T) {
	for _, action := range []GroupKeyBatchAction{
		GroupKeyBatchDisable,
		GroupKeyBatchDelete,
	} {
		t.Run(string(action), func(t *testing.T) {
			fixture := newServiceFixture(t)
			group := validControlGroup("batch-apply-failure-" + string(action))
			if err := fixture.db.Create(group).Error; err != nil {
				t.Fatal(err)
			}
			first := seedManagedUpstreamKey(
				t, fixture, group.ID, "provider-secret-apply-first", models.UpstreamKeyStatusActive, nil,
			)
			second := seedManagedUpstreamKey(
				t, fixture, group.ID, "provider-secret-apply-second", models.UpstreamKeyStatusActive, nil,
			)
			before := fixture.registry.Snapshot()
			apply := fixture.service.applyBatchRegistryMutation
			fixture.service.applyBatchRegistryMutation = func(
				groupID uint,
				keyIDs []uint,
				action GroupKeyBatchAction,
			) error {
				if !fixture.registry.RemoveKey(second.ID) {
					t.Fatal("remove Registry key before batch apply")
				}
				return apply(groupID, keyIDs, action)
			}

			_, err := fixture.service.BatchGroupKeys(t.Context(), group.ID, GroupKeyBatchRequest{
				Action: action,
				KeyIDs: []uint{first.ID, second.ID},
			})
			if !errors.Is(err, app_errors.ErrInternalServer) {
				t.Fatalf("BatchGroupKeys() error = %#v, want INTERNAL_SERVER_ERROR", err)
			}

			var rows []models.UpstreamKey
			if err := fixture.db.Where("group_id = ?", group.ID).Order("id ASC").Find(&rows).Error; err != nil {
				t.Fatal(err)
			}
			if len(rows) != 2 || rows[0].Status != models.UpstreamKeyStatusActive ||
				rows[1].Status != models.UpstreamKeyStatusActive {
				t.Fatalf("DB rows after Registry apply failure = %#v, want old state", rows)
			}
			after := fixture.registry.Snapshot()
			if !reflect.DeepEqual(after, before) {
				t.Fatalf("Registry after apply failure = %#v, want %#v", after, before)
			}
		})
	}
}

func TestBatchGroupKeysCompensationRestoresSelectedRuntimeGenerationExactly(t *testing.T) {
	for _, test := range []struct {
		name       string
		action     GroupKeyBatchAction
		failApply  bool
		triggerSQL string
	}{
		{
			name: "disable apply failure", action: GroupKeyBatchDisable, failApply: true,
		},
		{
			name: "delete apply failure", action: GroupKeyBatchDelete, failApply: true,
		},
		{
			name:       "disable DB failure",
			action:     GroupKeyBatchDisable,
			triggerSQL: `CREATE TRIGGER reject_exact_status BEFORE UPDATE OF status ON upstream_keys BEGIN SELECT RAISE(ABORT, 'blocked'); END`,
		},
		{
			name:       "delete DB failure",
			action:     GroupKeyBatchDelete,
			triggerSQL: `CREATE TRIGGER reject_exact_delete BEFORE DELETE ON upstream_keys BEGIN SELECT RAISE(ABORT, 'blocked'); END`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newServiceFixture(t)
			now := time.Date(2026, time.August, 1, 15, 0, 0, 0, time.UTC)
			group := validControlGroup("batch-exact-compensation-" + test.name)
			if err := fixture.db.Create(group).Error; err != nil {
				t.Fatal(err)
			}
			first := seedManagedUpstreamKey(
				t, fixture, group.ID, "provider-secret-exact-first", models.UpstreamKeyStatusActive, nil,
			)
			second := seedManagedUpstreamKey(
				t, fixture, group.ID, "provider-secret-exact-second", models.UpstreamKeyStatusActive, nil,
			)
			unselected := seedManagedUpstreamKey(
				t, fixture, group.ID, "provider-secret-exact-unselected", models.UpstreamKeyStatusActive, nil,
			)
			for _, keyID := range []uint{first.ID, second.ID} {
				if !fixture.registry.SetCooldown(keyID, now.Add(time.Hour)) ||
					!fixture.registry.SetAutoWeight(keyID, 37) {
					t.Fatalf("seed selected runtime key %d", keyID)
				}
				if _, ok := fixture.registry.IncrFailure(keyID); !ok {
					t.Fatalf("seed selected failure key %d", keyID)
				}
				if !fixture.registry.SetBlacklisted(keyID) {
					t.Fatalf("seed selected blacklist key %d", keyID)
				}
			}
			beforeViews := map[uint]state.KeyRuntimeView{}
			beforeGenerations := map[uint]uint64{}
			for _, keyID := range []uint{first.ID, second.ID} {
				view, exists := findRuntimeKey(fixture.registry.Snapshot(), keyID)
				if !exists {
					t.Fatalf("selected key %d missing before batch", keyID)
				}
				beforeViews[keyID] = view
				beforeGenerations[keyID] = blacklistedGeneration(t, fixture.registry, keyID)
				if beforeGenerations[keyID] == 0 {
					t.Fatalf("selected key %d generation = 0 before batch", keyID)
				}
			}

			if test.failApply {
				apply := fixture.service.applyBatchRegistryMutation
				fixture.service.applyBatchRegistryMutation = func(
					groupID uint,
					keyIDs []uint,
					action GroupKeyBatchAction,
				) error {
					if err := apply(groupID, keyIDs, action); err != nil {
						return err
					}
					if _, ok := fixture.registry.IncrFailure(unselected.ID); !ok ||
						!fixture.registry.SetBlacklisted(unselected.ID) {
						t.Fatal("mutate unselected runtime key during apply")
					}
					return errors.New("injected batch Registry apply failure")
				}
			} else if err := fixture.db.Exec(test.triggerSQL).Error; err != nil {
				t.Fatal(err)
			}

			_, err := fixture.service.BatchGroupKeys(t.Context(), group.ID, GroupKeyBatchRequest{
				Action: test.action,
				KeyIDs: []uint{first.ID, second.ID},
			})
			if test.failApply {
				if !errors.Is(err, app_errors.ErrInternalServer) {
					t.Fatalf("BatchGroupKeys() error = %#v, want INTERNAL_SERVER_ERROR", err)
				}
			} else if !errors.Is(err, app_errors.ErrDatabase) {
				t.Fatalf("BatchGroupKeys() error = %#v, want DATABASE_ERROR", err)
			}

			for _, keyID := range []uint{first.ID, second.ID} {
				afterView, exists := findRuntimeKey(fixture.registry.Snapshot(), keyID)
				if !exists || !reflect.DeepEqual(afterView, beforeViews[keyID]) {
					t.Fatalf("selected key %d after compensation = %#v, %t; want %#v", keyID, afterView, exists, beforeViews[keyID])
				}
				if generation := blacklistedGeneration(t, fixture.registry, keyID); generation != beforeGenerations[keyID] {
					t.Fatalf("selected key %d generation = %d, want %d", keyID, generation, beforeGenerations[keyID])
				}
			}
			if test.failApply {
				unselectedView, exists := findRuntimeKey(fixture.registry.Snapshot(), unselected.ID)
				if !exists || !unselectedView.Blacklisted || unselectedView.FailureCount != 1 ||
					blacklistedGeneration(t, fixture.registry, unselected.ID) == 0 {
					t.Fatalf("unselected runtime update was overwritten: %#v, exists=%t", unselectedView, exists)
				}
			}
		})
	}
}

func blacklistedGeneration(t *testing.T, registry *state.KeyRegistry, keyID uint) uint64 {
	t.Helper()
	for _, ref := range registry.BlacklistedKeys() {
		if ref.ID == keyID {
			return ref.FailureGeneration
		}
	}
	t.Fatalf("blacklisted key %d not found", keyID)
	return 0
}

type heldGroupKeyMutationCoordinator struct {
	mu            sync.Mutex
	manyAttempted chan struct{}
	manyOnce      sync.Once
}

func (coordinator *heldGroupKeyMutationCoordinator) Do(_ uint, mutate func()) {
	coordinator.mu.Lock()
	defer coordinator.mu.Unlock()
	mutate()
}

func (coordinator *heldGroupKeyMutationCoordinator) DoMany(_ []uint, mutate func()) {
	coordinator.manyOnce.Do(func() { close(coordinator.manyAttempted) })
	coordinator.mu.Lock()
	defer coordinator.mu.Unlock()
	mutate()
}

func TestBatchGroupKeysDeleteWaitsForInflightMutationThenResetsStats(t *testing.T) {
	fixture := newServiceFixture(t)
	now := time.Date(2026, time.August, 1, 14, 0, 0, 0, time.UTC)
	fixture.service.now = func() time.Time { return now }
	group := validControlGroup("batch-delete-inflight")
	if err := fixture.db.Create(group).Error; err != nil {
		t.Fatal(err)
	}
	row := seedManagedUpstreamKey(t, fixture, group.ID, "provider-secret-delete-inflight", models.UpstreamKeyStatusActive, nil)
	coordinator := &heldGroupKeyMutationCoordinator{manyAttempted: make(chan struct{})}
	fixture.service.mutations = coordinator
	holderEntered := make(chan struct{})
	releaseHolder := make(chan struct{})
	holderDone := make(chan struct{})
	go func() {
		coordinator.Do(row.ID, func() {
			if _, ok := fixture.registry.IncrFailure(row.ID); !ok {
				t.Error("in-flight Registry failure did not start")
			}
			close(holderEntered)
			<-releaseHolder
			fixture.stats.RecordFailure(row.ID, health.FailureCategoryInvalidKey, http.StatusUnauthorized, now)
		})
		close(holderDone)
	}()
	<-holderEntered

	type batchResult struct {
		response GroupKeyBatchResponse
		err      error
	}
	batchDone := make(chan batchResult, 1)
	go func() {
		response, err := fixture.service.BatchGroupKeys(t.Context(), group.ID, GroupKeyBatchRequest{
			Action: GroupKeyBatchDelete,
			KeyIDs: []uint{row.ID},
		})
		batchDone <- batchResult{response: response, err: err}
	}()

	select {
	case <-coordinator.manyAttempted:
		select {
		case result := <-batchDone:
			t.Fatalf("batch delete completed while mutation held key: %#v", result)
		default:
		}
	case result := <-batchDone:
		t.Fatalf("batch delete bypassed per-key coordinator: %#v", result)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for batch delete to reach per-key coordinator")
	}

	close(releaseHolder)
	<-holderDone
	result := <-batchDone
	if result.err != nil {
		t.Fatalf("BatchGroupKeys() error = %v", result.err)
	}
	if len(result.response.AffectedIDs) != 1 || result.response.AffectedIDs[0] != row.ID {
		t.Fatalf("batch response = %#v", result.response)
	}
	if _, exists := fixture.registry.EncryptedValue(row.ID); exists {
		t.Fatal("deleted key remains in Registry")
	}
	if stats := fixture.stats.Snapshot(row.ID, now); stats != (health.KeyStats{}) {
		t.Fatalf("deleted key stats = %#v, want zero", stats)
	}
}

func TestBatchGroupKeysDeleteFailsClosedWithoutMultiKeyCoordinator(t *testing.T) {
	fixture := newServiceFixture(t)
	group := validControlGroup("batch-delete-no-multi-coordinator")
	if err := fixture.db.Create(group).Error; err != nil {
		t.Fatal(err)
	}
	row := seedManagedUpstreamKey(t, fixture, group.ID, "provider-secret-no-multi", models.UpstreamKeyStatusActive, nil)
	fixture.service.mutations = &postRestoreFailureCoordinator{after: func() {}}

	_, err := fixture.service.BatchGroupKeys(t.Context(), group.ID, GroupKeyBatchRequest{
		Action: GroupKeyBatchDelete,
		KeyIDs: []uint{row.ID},
	})
	if !errors.Is(err, app_errors.ErrInternalServer) {
		t.Fatalf("BatchGroupKeys() error = %#v, want INTERNAL_SERVER_ERROR", err)
	}
	var count int64
	if err := fixture.db.Model(&models.UpstreamKey{}).Where("id = ?", row.ID).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("persisted key count = %d, want 1", count)
	}
	if _, exists := fixture.registry.EncryptedValue(row.ID); !exists {
		t.Fatal("Registry key removed without multi-key coordinator")
	}
}

func TestBatchGroupKeysHTTPStrictJSONAndSecretFreeResponse(t *testing.T) {
	initControlI18n(t)
	fixture := newServiceFixture(t)
	group := validControlGroup("batch-http")
	if err := fixture.db.Create(group).Error; err != nil {
		t.Fatal(err)
	}
	row := seedManagedUpstreamKey(t, fixture, group.ID, "provider-secret-http", models.UpstreamKeyStatusActive, nil)
	engine := gin.New()
	NewServer(&config.Config{AuthKey: groupKeyHTTPAuth}, fixture.service).RegisterRoutes(engine)
	path := fmt.Sprintf("/api/groups/%d/keys/batch", group.ID)

	for _, body := range []string{
		`{"action":"disable","key_ids":[],"unexpected":true}`,
		`{"action":"disable","key_ids":[]} {}`,
		`{"action":"disable","key_ids":null}`,
	} {
		recorder := serveGroupKeyHTTPRequest(t, engine, http.MethodPost, path, body, groupKeyHTTPAuth, "en-US")
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("POST body %s = %d %s, want 400", body, recorder.Code, recorder.Body.String())
		}
	}

	body := fmt.Sprintf(`{"action":"disable","key_ids":[%d]}`, row.ID)
	recorder := serveGroupKeyHTTPRequest(t, engine, http.MethodPost, path, body, groupKeyHTTPAuth, "en-US")
	if recorder.Code != http.StatusOK {
		t.Fatalf("POST = %d %s, want 200", recorder.Code, recorder.Body.String())
	}
	var envelope struct {
		Code int                   `json:"code"`
		Data GroupKeyBatchResponse `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Code != 0 || len(envelope.Data.AffectedIDs) != 1 || envelope.Data.AffectedIDs[0] != row.ID || envelope.Data.Summary.Disabled != 1 {
		t.Fatalf("batch envelope = %#v", envelope)
	}
	for _, forbidden := range [][]byte{[]byte("provider-secret-http"), []byte(row.KeyValue), []byte("key_value"), []byte("key_hash"), []byte("mask")} {
		if bytes.Contains(recorder.Body.Bytes(), forbidden) {
			t.Fatalf("response exposes %q: %s", forbidden, recorder.Body.String())
		}
	}
}
