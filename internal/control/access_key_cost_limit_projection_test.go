package control

import (
	"testing"
	"time"

	"gpt-load/internal/accessquota"
)

func TestAccessKeyCostLimitRuntimeProjectionIsSharedByCollectionHomeAndHealth(t *testing.T) {
	t.Parallel()
	fixture := newServiceFixture(t)
	created, err := fixture.service.CreateAccessKey(t.Context(), AccessKeyCreateRequest{
		Name: "limited",
		CostLimitRules: OptionalAccessKeyCostLimitRules{Set: true, Values: []AccessKeyCostLimitRuleRequest{
			{Kind: accessquota.KindTotal, LimitUSD: "100"},
			{Kind: accessquota.KindPeriodic, LimitUSD: "20", PeriodSeconds: 300},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_787_184_000, 0).UTC()
	fixture.service.now = func() time.Time { return now }
	ticket, decision := fixture.accessQuota.Admit(created.ID, now)
	if !decision.Allowed {
		t.Fatalf("Admit() = %#v", decision)
	}
	fixture.accessQuota.Complete(ticket, 100_000_000_000)

	collection, err := fixture.service.ListAccessKeyCollection(
		t.Context(),
		AccessKeyCollectionQuery{Page: 1, PageSize: 20},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(collection.Items) != 1 || collection.Items[0].CostLimitStatus == nil ||
		collection.Items[0].CostLimitStatus.Allowed ||
		len(collection.Items[0].CostLimitStatus.Rules) != 2 {
		t.Fatalf("collection cost limit projection = %#v", collection.Items)
	}

	home, err := fixture.service.ReadAccessKeyHomeBase(t.Context(), now.UnixMilli(), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if home.CurrentAccessKey == nil || home.CurrentAccessKey.CostLimitStatus == nil ||
		home.CurrentAccessKey.CostLimitStatus.Allowed ||
		home.CurrentAccessKey.Status != "active" {
		t.Fatalf("current access key projection = %#v", home.CurrentAccessKey)
	}

	health, err := fixture.service.RuntimeHealth()
	if err != nil {
		t.Fatal(err)
	}
	if len(health.BlockedAccessKeys) != 1 || health.BlockedAccessKeys[0].AccessKeyID != created.ID ||
		health.BlockedAccessKeys[0].Recoverable || len(health.BlockedAccessKeys[0].BlockingRules) != 2 {
		t.Fatalf("blocked access keys = %#v", health.BlockedAccessKeys)
	}
	if health.Counts != (healthCountsResponse{}) {
		t.Fatalf("business quota block changed credential health counts = %#v", health.Counts)
	}
}
