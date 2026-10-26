package control

import (
	"fmt"
	"testing"

	"gpt-load/internal/channel"
	"gpt-load/internal/storage/models"
)

func TestProjectModelPricingMethodQuery(t *testing.T) {
	t.Parallel()
	for _, value := range []string{"all", "automatic", "manual", "linked"} {
		query, err := parseProjectModelListQuery("pricing_method="+value+"&pricing_status=pending", false)
		if err != nil || string(query.PricingMethod) != value || query.PricingStatus != ProjectModelPricingStatusPending {
			t.Fatalf("pricing_method=%s: query=%#v, error=%v", value, query, err)
		}
	}
	for _, raw := range []string{"pricing_method=", "pricing_method=unknown", "pricing_method=manual&pricing_method=linked"} {
		if _, err := parseProjectModelListQuery(raw, false); err == nil {
			t.Fatalf("accepted invalid query %q", raw)
		}
	}
}

func TestProjectModelsCombinePricingMethodAndStatusBeforePagination(t *testing.T) {
	t.Parallel()
	fixture := newServiceFixture(t)
	price := int64(1_000_000_000)
	rows := []models.ModelPrice{
		{ChannelID: string(channel.OpenAICompatible), ModelID: "automatic-priced", InputPriceNanoUSDPerMillionTokens: &price},
		{ChannelID: string(channel.OpenAICompatible), ModelID: "automatic-pending"},
		{ChannelID: string(channel.OpenAICompatible), ModelID: "manual-priced", IsManual: true, InputPriceNanoUSDPerMillionTokens: &price},
		{ChannelID: string(channel.OpenAICompatible), ModelID: "manual-unpriced", IsManual: true},
	}
	for index := range rows {
		if err := fixture.db.Create(&rows[index]).Error; err != nil {
			t.Fatal(err)
		}
	}
	rows = append(rows,
		models.ModelPrice{ChannelID: string(channel.OpenAICompatible), ModelID: "linked-priced", SourcePriceID: &rows[0].ID, InputPriceNanoUSDPerMillionTokens: &price},
		models.ModelPrice{ChannelID: string(channel.OpenAICompatible), ModelID: "linked-pending", SourcePriceID: &rows[3].ID},
	)
	for index := 4; index < len(rows); index++ {
		if err := fixture.db.Create(&rows[index]).Error; err != nil {
			t.Fatal(err)
		}
	}
	for index, prefix := range []string{"automatic", "manual", "linked"} {
		createPriceTestGroup(t, fixture.db, models.Group{
			Name: "method-" + prefix, ChannelID: string(channel.OpenAICompatible), Enabled: true,
			Overrides: models.JSON(`{}`),
			Models:    models.JSON(fmt.Sprintf(`[{"id":%q,"alias":"shared-client"},{"id":%q}]`, rows[index*2].ModelID, rows[index*2+1].ModelID)),
		})
	}
	cases := []struct {
		method ProjectModelPricingMethod
		status ProjectModelPricingStatus
		models map[string]bool
	}{
		{ProjectModelPricingMethodAutomatic, ProjectModelPricingStatusAll, map[string]bool{"automatic-priced": true, "automatic-pending": true}},
		{ProjectModelPricingMethodManual, ProjectModelPricingStatusAll, map[string]bool{"manual-priced": true, "manual-unpriced": true}},
		{ProjectModelPricingMethodLinked, ProjectModelPricingStatusAll, map[string]bool{"linked-priced": true, "linked-pending": true}},
		{ProjectModelPricingMethodAutomatic, ProjectModelPricingStatusPending, map[string]bool{"automatic-pending": true}},
		{ProjectModelPricingMethodManual, ProjectModelPricingStatusPending, map[string]bool{}},
		{ProjectModelPricingMethodManual, ProjectModelPricingStatusConfigured, map[string]bool{"manual-priced": true, "manual-unpriced": true}},
		{ProjectModelPricingMethodLinked, ProjectModelPricingStatusPending, map[string]bool{"linked-pending": true}},
		{ProjectModelPricingMethodLinked, ProjectModelPricingStatusConfigured, map[string]bool{"linked-priced": true}},
	}
	for _, test := range cases {
		t.Run(string(test.method)+"/"+string(test.status), func(t *testing.T) {
			seen := make(map[string]bool)
			for page := int64(1); page <= int64(max(1, len(test.models))); page++ {
				result, err := fixture.service.ListProjectModels(t.Context(), ProjectModelListQuery{
					GroupStatus: ProjectModelGroupStatusEnabled, PricingStatus: test.status, PricingMethod: test.method, Page: page, PageSize: 1,
				})
				if err != nil || result.Pagination.TotalItems != int64(len(test.models)) {
					t.Fatalf("filtered count=%d, expected=%d, error=%v", result.Pagination.TotalItems, len(test.models), err)
				}
				for _, item := range result.Items {
					if len(item.UpstreamModels) != 1 {
						t.Fatalf("card retained other pricing methods: %#v", item)
					}
					for _, upstream := range item.UpstreamModels {
						if !test.models[upstream.ModelID] || seen[upstream.ModelID] {
							t.Fatalf("unexpected or duplicated upstream: %s", upstream.ModelID)
						}
						seen[upstream.ModelID] = true
					}
				}
			}
			if len(seen) != len(test.models) {
				t.Fatalf("filtered models=%v, expected=%v", seen, test.models)
			}
		})
	}
}
