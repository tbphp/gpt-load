package control

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strconv"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"gpt-load/internal/health"
	"gpt-load/internal/platform/config"
	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/storage/models"
)

func TestGroupKeyCollectionQuery(t *testing.T) {
	initControlI18n(t)
	fixture := newServiceFixture(t)
	group := validControlGroup("key-collection-query")
	if err := fixture.db.Create(group).Error; err != nil {
		t.Fatal(err)
	}
	engine := gin.New()
	NewServer(&config.Config{AuthKey: groupKeyHTTPAuth}, fixture.service).RegisterRoutes(engine)
	path := fmt.Sprintf("/api/groups/%d/keys", group.ID)

	for _, test := range []struct {
		name       string
		query      string
		wantCode   int
		wantObject bool
	}{
		{name: "empty", wantCode: http.StatusOK, wantObject: true},
		{name: "mask search", query: "?q=sk-gl-%2A%2A%2A%2Aabcd", wantCode: http.StatusOK, wantObject: true},
		{name: "available", query: "?status=available", wantCode: http.StatusOK, wantObject: true},
		{name: "cooldown", query: "?status=cooldown", wantCode: http.StatusOK, wantObject: true},
		{name: "blacklisted", query: "?status=blacklisted", wantCode: http.StatusOK, wantObject: true},
		{name: "disabled", query: "?status=disabled", wantCode: http.StatusOK, wantObject: true},
		{name: "minimum page", query: "?page=1", wantCode: http.StatusOK, wantObject: true},
		{name: "page size 20", query: "?page_size=20", wantCode: http.StatusOK, wantObject: true},
		{name: "page size 50", query: "?page_size=50", wantCode: http.StatusOK, wantObject: true},
		{name: "page size 100", query: "?page_size=100", wantCode: http.StatusOK, wantObject: true},
		{name: "duplicate parameter", query: "?page=1&page=2", wantCode: http.StatusBadRequest},
		{name: "unknown parameter", query: "?unexpected=value", wantCode: http.StatusBadRequest},
		{name: "unknown status", query: "?status=unknown", wantCode: http.StatusBadRequest},
		{name: "zero page", query: "?page=0", wantCode: http.StatusBadRequest},
		{name: "unsupported page size", query: "?page_size=21", wantCode: http.StatusBadRequest},
	} {
		t.Run(test.name, func(t *testing.T) {
			recorder := serveGroupKeyHTTPRequest(
				t, engine, http.MethodGet, path+test.query, "", groupKeyHTTPAuth, "en-US",
			)
			if recorder.Code != test.wantCode {
				t.Fatalf("GET %s = %d %s, want %d", test.query, recorder.Code, recorder.Body.String(), test.wantCode)
			}
			if !test.wantObject {
				return
			}
			var envelope struct {
				Code int             `json:"code"`
				Data json.RawMessage `json:"data"`
			}
			if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
				t.Fatal(err)
			}
			if envelope.Code != 0 || len(envelope.Data) == 0 || envelope.Data[0] != '{' {
				t.Fatalf("collection envelope = %s, want object data", recorder.Body.String())
			}
		})
	}

	unauthenticated := serveGroupKeyHTTPRequest(
		t, engine, http.MethodGet, path+"?unexpected=value", "", "wrong-auth-key", "en-US",
	)
	if unauthenticated.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated request = %d %s, want %d before query handling", unauthenticated.Code, unauthenticated.Body.String(), http.StatusUnauthorized)
	}
}

func TestListGroupKeysCollection(t *testing.T) {
	fixture := newServiceFixture(t)
	now := time.Date(2026, time.August, 1, 10, 0, 0, 0, time.UTC)
	fixture.service.now = func() time.Time { return now }
	group := validControlGroup("key-collection")
	if err := fixture.db.Create(group).Error; err != nil {
		t.Fatal(err)
	}

	manualWeight := 73
	blacklistedOne := seedManagedUpstreamKey(t, fixture, group.ID, "provider-secret-0001", models.UpstreamKeyStatusActive, nil)
	blacklistedTwo := seedManagedUpstreamKey(t, fixture, group.ID, "provider-secret-0002", models.UpstreamKeyStatusActive, nil)
	cooldownOne := seedManagedUpstreamKey(t, fixture, group.ID, "provider-secret-0003", models.UpstreamKeyStatusActive, nil)
	cooldownTwo := seedManagedUpstreamKey(t, fixture, group.ID, "provider-secret-0004", models.UpstreamKeyStatusActive, nil)
	availableAuto := seedManagedUpstreamKey(t, fixture, group.ID, "provider-secret-0005", models.UpstreamKeyStatusActive, nil)
	availableManual := seedManagedUpstreamKey(t, fixture, group.ID, "provider-secret-0006", models.UpstreamKeyStatusActive, &manualWeight)
	disabledOne := seedManagedUpstreamKey(t, fixture, group.ID, "provider-secret-0007", models.UpstreamKeyStatusDisabled, nil)
	disabledTwo := seedManagedUpstreamKey(t, fixture, group.ID, "provider-secret-0008", models.UpstreamKeyStatusDisabled, nil)

	if !fixture.registry.SetBlacklisted(blacklistedOne.ID) || !fixture.registry.SetBlacklisted(blacklistedTwo.ID) ||
		!fixture.registry.SetCooldown(cooldownOne.ID, now.Add(2*time.Minute)) ||
		!fixture.registry.SetCooldown(cooldownTwo.ID, now.Add(time.Minute)) ||
		!fixture.registry.SetAutoWeight(availableAuto.ID, 61) {
		t.Fatal("seed runtime state")
	}
	fixture.stats.RecordSuccess(blacklistedOne.ID, now.Add(-2*time.Minute))
	fixture.stats.RecordFailure(blacklistedOne.ID, health.FailureCategoryRateLimited, http.StatusTooManyRequests, now.Add(-time.Minute))
	fixture.stats.RecordFailure(blacklistedOne.ID, health.FailureCategoryInvalidKey, http.StatusUnauthorized, now)

	response, err := fixture.service.ListGroupKeys(t.Context(), group.ID, GroupKeyCollectionQuery{Page: 1, PageSize: 20})
	if err != nil {
		t.Fatalf("ListGroupKeys() error = %v", err)
	}
	if response.ObservedAtMS != now.UnixMilli() || response.StatsWindowSeconds != 300 {
		t.Fatalf("observation metadata = %#v", response)
	}
	if response.Summary != (GroupKeySummaryResponse{Total: 8, Available: 2, Cooldown: 2, Blacklisted: 2, Disabled: 2}) {
		t.Fatalf("summary = %#v", response.Summary)
	}
	wantIDs := []uint{
		blacklistedOne.ID, blacklistedTwo.ID,
		cooldownOne.ID, cooldownTwo.ID,
		availableAuto.ID, availableManual.ID,
		disabledOne.ID, disabledTwo.ID,
	}
	gotIDs := make([]uint, len(response.Items))
	for index, item := range response.Items {
		gotIDs[index] = item.ID
	}
	if !reflect.DeepEqual(gotIDs, wantIDs) {
		t.Fatalf("sorted ids = %v, want %v", gotIDs, wantIDs)
	}

	blacklisted := response.Items[0]
	if blacklisted.Mask != "sk-gl-****0001" || blacklisted.ConfiguredStatus != "active" ||
		blacklisted.EffectiveStatus != "blacklisted" || blacklisted.Weight != nil ||
		blacklisted.RecentSuccessCount != 1 || blacklisted.RecentFailureCount != 2 ||
		blacklisted.ConsecutiveFailureCount != 2 || blacklisted.LastFailureCategory != "invalid_key" ||
		blacklisted.LastStatusCode == nil || *blacklisted.LastStatusCode != http.StatusUnauthorized ||
		blacklisted.CooldownUntilMS != nil || blacklisted.Recovery.Mode != "probe" ||
		!blacklisted.Recovery.Automatic || blacklisted.Recovery.AtMS != nil {
		t.Fatalf("blacklisted item = %#v", blacklisted)
	}
	cooldown := response.Items[2]
	if cooldown.EffectiveStatus != "cooldown" || cooldown.Weight != nil || cooldown.CooldownUntilMS == nil ||
		*cooldown.CooldownUntilMS != now.Add(2*time.Minute).UnixMilli() || cooldown.Recovery.Mode != "cooldown" ||
		!cooldown.Recovery.Automatic || cooldown.Recovery.AtMS == nil || *cooldown.Recovery.AtMS != *cooldown.CooldownUntilMS {
		t.Fatalf("cooldown item = %#v", cooldown)
	}
	auto := response.Items[4]
	if auto.EffectiveStatus != "available" || auto.WeightMode != "auto" || auto.Weight == nil || *auto.Weight != 61 ||
		auto.Recovery.Mode != "none" || auto.Recovery.Automatic || auto.Recovery.AtMS != nil {
		t.Fatalf("automatic available item = %#v", auto)
	}
	manual := response.Items[5]
	if manual.EffectiveStatus != "available" || manual.WeightMode != "manual" || manual.Weight == nil || *manual.Weight != manualWeight {
		t.Fatalf("manual available item = %#v", manual)
	}
	disabled := response.Items[6]
	if disabled.ConfiguredStatus != "disabled" || disabled.EffectiveStatus != "disabled" || disabled.Weight != nil ||
		disabled.Recovery.Mode != "manual" || disabled.Recovery.Automatic || disabled.Recovery.AtMS != nil {
		t.Fatalf("disabled item = %#v", disabled)
	}

	blacklistedStatus := "blacklisted"
	filtered, err := fixture.service.ListGroupKeys(t.Context(), group.ID, GroupKeyCollectionQuery{
		Status: &blacklistedStatus, Page: 1, PageSize: 20,
	})
	if err != nil {
		t.Fatalf("ListGroupKeys(status) error = %v", err)
	}
	if filtered.Summary != response.Summary || filtered.Pagination.TotalItems != 2 || filtered.Pagination.TotalPages != 1 ||
		len(filtered.Items) != 2 || filtered.Items[0].ID != blacklistedOne.ID || filtered.Items[1].ID != blacklistedTwo.ID {
		t.Fatalf("filtered response = %#v", filtered)
	}

	matched, err := fixture.service.ListGroupKeys(t.Context(), group.ID, GroupKeyCollectionQuery{
		Query: "SK-GL-****0006", Page: 1, PageSize: 20,
	})
	if err != nil {
		t.Fatalf("ListGroupKeys(q) error = %v", err)
	}
	if len(matched.Items) != 1 || matched.Items[0].ID != availableManual.ID {
		t.Fatalf("mask query response = %#v", matched)
	}

	outOfRange, err := fixture.service.ListGroupKeys(t.Context(), group.ID, GroupKeyCollectionQuery{Page: 2, PageSize: 20})
	if err != nil {
		t.Fatalf("ListGroupKeys(out of range) error = %v", err)
	}
	if outOfRange.Pagination.TotalItems != 8 || outOfRange.Pagination.TotalPages != 1 || len(outOfRange.Items) != 0 || outOfRange.Items == nil {
		t.Fatalf("out of range response = %#v", outOfRange)
	}

	encoded, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"provider-secret-", "key_value", "key_hash", "ciphertext", blacklistedOne.KeyValue, blacklistedOne.KeyHash} {
		if bytes.Contains(encoded, []byte(forbidden)) {
			t.Fatalf("collection response exposes %q: %s", forbidden, encoded)
		}
	}
}

func TestListGroupKeysCollectionFailsAtomicallyWhenMaskingCannotDecrypt(t *testing.T) {
	fixture := newServiceFixture(t)
	group := validControlGroup("key-collection-decrypt")
	if err := fixture.db.Create(group).Error; err != nil {
		t.Fatal(err)
	}
	first := seedManagedUpstreamKey(t, fixture, group.ID, "provider-secret-first", models.UpstreamKeyStatusActive, nil)
	second := seedManagedUpstreamKey(t, fixture, group.ID, "provider-secret-second", models.UpstreamKeyStatusActive, nil)
	if err := fixture.db.Model(&models.UpstreamKey{}).Where("id = ?", second.ID).Update("key_value", "not-valid-ciphertext").Error; err != nil {
		t.Fatal(err)
	}

	response, err := fixture.service.ListGroupKeys(t.Context(), group.ID, GroupKeyCollectionQuery{Page: 1, PageSize: 20})
	if !errors.Is(err, app_errors.ErrInternalServer) {
		t.Fatalf("ListGroupKeys() error = %v, want internal error", err)
	}
	if !reflect.DeepEqual(response, GroupKeyCollectionResponse{}) {
		t.Fatalf("ListGroupKeys() response = %#v, want zero response", response)
	}
	if first.ID == 0 {
		t.Fatal("first key was not persisted")
	}
}

func TestListGroupKeysCollectionReturnsEmptyItemsForOverflowingOffset(t *testing.T) {
	fixture := newServiceFixture(t)
	group := validControlGroup("key-collection-overflow")
	if err := fixture.db.Create(group).Error; err != nil {
		t.Fatal(err)
	}
	seedManagedUpstreamKey(t, fixture, group.ID, "provider-secret-overflow", models.UpstreamKeyStatusActive, nil)

	overflowPage := 1 + (1 << (strconv.IntSize - 2))
	query, apiErr := parseGroupKeyCollectionQuery(fmt.Sprintf("page=%d&page_size=20", overflowPage))
	if apiErr != nil {
		t.Fatalf("parseGroupKeyCollectionQuery() error = %v", apiErr)
	}
	response, err := fixture.service.ListGroupKeys(t.Context(), group.ID, query)
	if err != nil {
		t.Fatalf("ListGroupKeys() error = %v", err)
	}
	if response.Pagination != (GroupKeyPaginationResponse{
		Page: overflowPage, PageSize: 20, TotalItems: 1, TotalPages: 1,
	}) || response.Items == nil || len(response.Items) != 0 {
		t.Fatalf("overflow page response = %#v, want empty out-of-range page", response)
	}
}
