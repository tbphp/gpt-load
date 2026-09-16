package control

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"testing"

	"github.com/gin-gonic/gin"

	"gpt-load/internal/platform/config"
	"gpt-load/internal/state"
	"gpt-load/internal/storage/models"
)

func TestModernGroupsPreservesModelAliasesAndZeroWeight(t *testing.T) {
	t.Parallel()
	fixture := newServiceFixture(t)
	weight := 0
	group := createGroupCollectionGroup(t, fixture, "zero weight", true, &weight)
	setGroupCollectionRoute(t, fixture, group, "", `[
		{"id":"upstream-a","alias":"public-a"},
		{"id":"upstream-b","alias":""},
		{"id":"upstream-c","alias":"public-c"},
		{"id":"upstream-d","alias":"public-d"}
	]`)
	publishGroupCollectionRuntime(t, fixture, nil)
	result, err := fixture.service.ListModernGroups(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Items) != 1 {
		t.Fatalf("got %d groups, want 1", len(result.Items))
	}
	item := result.Items[0]
	if item.Weight != 0 || item.Availability != "paused" {
		t.Errorf("zero weight context = %#v", item)
	}
	if item.ModelCount != 4 || !slices.Equal(item.ModelNames, []string{"public-a", "upstream-b", "public-c", "public-d"}) {
		t.Errorf("model names = %v, count = %d", item.ModelNames, item.ModelCount)
	}
}

func TestModernGroupsWorkspaceIncludesEveryGroupAndActionableContext(t *testing.T) {
	t.Parallel()
	initControlI18n(t)
	fixture := newServiceFixture(t)
	weight := 75
	ready := createGroupCollectionGroup(t, fixture, "serving", true, &weight)
	paused := createGroupCollectionGroup(t, fixture, "paused", false, nil)
	unconfigured := createGroupCollectionGroup(t, fixture, "no models", true, nil)
	setGroupCollectionRoute(t, fixture, unconfigured, "", `[]`)
	entries := []state.CredentialEntry{
		createGroupCollectionKey(t, fixture, ready.ID, models.CredentialStatusActive, nil),
		createGroupCollectionKey(t, fixture, paused.ID, models.CredentialStatusActive, nil),
		createGroupCollectionKey(t, fixture, unconfigured.ID, models.CredentialStatusActive, nil),
	}
	for i := range 21 {
		createGroupCollectionGroup(t, fixture, fmt.Sprintf("extra-%02d", i), false, nil)
	}
	publishGroupCollectionRuntime(t, fixture, entries)
	const hour = int64(1_700_000_000_000)
	createGroupCollectionUsageStat(t, fixture, ready.ID, hour, 9, "modern")
	engine := gin.New()
	NewServer(&config.Config{AuthKey: authTestKey}, fixture.service).RegisterRoutes(engine)
	result := performGroupCollectionRequest(engine, "/api/modern/groups", "Bearer "+authTestKey)
	if result.Code != http.StatusOK {
		t.Fatalf("workspace status = %d, want 200: %s", result.Code, result.Body.String())
	}
	var data struct {
		Items []struct {
			ID               uint   `json:"id"`
			Availability     string `json:"availability"`
			Weight           int    `json:"weight"`
			LastActiveHour   *int64 `json:"last_active_hour_ms"`
			LastHourRequests int64  `json:"last_active_hour_requests"`
		} `json:"items"`
	}
	if err := json.Unmarshal(decodeGroupCollectionSuccessData(t, result), &data); err != nil {
		t.Fatal(err)
	}
	if len(data.Items) != 24 {
		t.Fatalf("got %d groups, want all 24", len(data.Items))
	}
	for _, item := range data.Items {
		switch item.ID {
		case ready.ID:
			if item.Availability != "ready" || item.Weight != weight || item.LastActiveHour == nil || *item.LastActiveHour != hour || item.LastHourRequests != 9 {
				t.Fatalf("serving context = %#v", item)
			}
		case paused.ID:
			if item.Availability != "paused" || item.Weight != 50 || item.LastActiveHour != nil {
				t.Fatalf("paused context = %#v", item)
			}
		case unconfigured.ID:
			if item.Availability != "no_models" {
				t.Fatalf("setup reason = %s", item.Availability)
			}
		}
	}
	legacy := performGroupCollectionRequest(engine, "/api/groups", "Bearer "+authTestKey)
	if legacy.Code != http.StatusOK || len(decodeGroupCollectionSuccess(t, legacy).Items) != 20 {
		t.Fatal("legacy pagination changed")
	}
	unauthorized := performGroupCollectionRequest(engine, "/api/modern/groups", "")
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous status = %d", unauthorized.Code)
	}
}
