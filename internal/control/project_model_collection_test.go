package control

import (
	"testing"

	"gpt-load/internal/storage/models"
)

func TestProjectModelsUseOnePricePerUpstreamAndDetailShowsAllRelationships(t *testing.T) {
	fixture := newServiceFixture(t)
	createPriceTestGroup(t, fixture.db, models.Group{
		Name: "enabled", UpstreamURL: "https://enabled.example/v1", Protocols: models.JSON(`["openai-completions"]`),
		Models: models.JSON(`[{"id":"upstream-x","alias":"client-a"},{"id":"upstream-x","alias":"client-b"}]`), Config: models.JSON(`{}`), Enabled: true,
	})
	createPriceTestGroup(t, fixture.db, models.Group{
		Name: "disabled", UpstreamURL: "https://disabled.example/v1", Protocols: models.JSON(`["anthropic"]`),
		Models: models.JSON(`[{"id":"upstream-x","alias":"client-a"}]`), Config: models.JSON(`{}`), Enabled: false,
	})
	mustEnsureInitialPrices(t, fixture)
	all, err := fixture.service.ListProjectModels(t.Context(), ProjectModelListQuery{
		GroupStatus: ProjectModelGroupStatusAll, PricingStatus: ProjectModelPricingStatusAll, Page: 1, PageSize: 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(all.Items) != 2 || all.Items[0].ClientModel != "client-a" || all.Items[1].ClientModel != "client-b" {
		t.Fatalf("client model overview = %#v", all.Items)
	}
	upstream := all.Items[0].UpstreamModels[0]
	if upstream.ModelID != "upstream-x" || upstream.Price.ModelID != "upstream-x" || len(upstream.RouteGroups) != 2 || len(upstream.AffectedGroups) != 2 {
		t.Fatalf("upstream overview = %#v", upstream)
	}
	detail, err := fixture.service.GetUpstreamModelDetail(t.Context(), upstream.Price.ID)
	if err != nil {
		t.Fatal(err)
	}
	if detail.ModelID != "upstream-x" || detail.ClientModelCount != 2 || detail.GroupCount != 2 || len(detail.Associations) != 3 {
		t.Fatalf("upstream detail = %#v", detail)
	}
	if detail.Associations[0].ClientModel != "client-a" || detail.Associations[0].Group.Name != "disabled" {
		t.Fatalf("detail associations are not stable/unfiltered: %#v", detail.Associations)
	}
}
