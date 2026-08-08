package control

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"gpt-load/internal/platform/config"
	"gpt-load/internal/protocol"
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

func TestProjectModelsHTTPScopesAccessKeyFiltersAndRelationships(t *testing.T) {
	initControlI18n(t)
	fixture := newServiceFixture(t)
	allowedOpenAI := createPriceTestGroup(t, fixture.db, models.Group{
		Name: "allowed-openai", UpstreamURL: "https://openai.example/v1",
		Protocols: models.JSON(`["openai-completions"]`),
		Models: models.JSON(`[
			{"id":"upstream-a","alias":"client-a"},
			{"id":"private-hidden","alias":"client-hidden"},
			{"id":"upstream-shared","alias":"shared"}
		]`),
		Config: models.JSON(`{}`), Enabled: true,
	})
	allowedAnthropic := createPriceTestGroup(t, fixture.db, models.Group{
		Name: "allowed-anthropic", UpstreamURL: "https://anthropic.example",
		Protocols: models.JSON(`["anthropic"]`),
		Models: models.JSON(`[
			{"id":"private-client-b","alias":"client-b"},
			{"id":"upstream-shared","alias":"shared"}
		]`),
		Config: models.JSON(`{}`), Enabled: true,
	})
	createPriceTestGroup(t, fixture.db, models.Group{
		Name: "private-gemini", UpstreamURL: "https://gemini.example",
		Protocols: models.JSON(`["gemini"]`),
		Models:    models.JSON(`[{"id":"private-gemini-model","alias":"gemini-private"}]`),
		Config:    models.JSON(`{}`), Enabled: true,
	})
	disabled := createPriceTestGroup(t, fixture.db, models.Group{
		Name: "private-disabled", UpstreamURL: "https://disabled.example/v1",
		Protocols: models.JSON(`["openai-completions"]`),
		Models:    models.JSON(`[{"id":"private-disabled-model","alias":"client-a"}]`),
		Config:    models.JSON(`{}`), Enabled: false,
	})
	if err := fixture.db.Model(&disabled).Update("enabled", false).Error; err != nil {
		t.Fatalf("disable private Group: %v", err)
	}
	mustEnsureInitialPrices(t, fixture)
	created, err := fixture.service.CreateAccessKey(t.Context(), AccessKeyCreateRequest{
		Name: "model viewer",
		Filters: &AccessKeyFilters{
			Groups: []uint{allowedOpenAI.ID, allowedAnthropic.ID},
			Protocols: []protocol.Protocol{
				protocol.OpenAICompletions,
				protocol.Anthropic,
			},
			Models: []string{"client-a", "shared"},
		},
	})
	if err != nil {
		t.Fatalf("CreateAccessKey() error = %v", err)
	}
	engine := gin.New()
	NewServer(&config.Config{AuthKey: authTestKey}, fixture.service).RegisterRoutes(engine)

	recorder := serveAuthRequest(
		engine,
		"/api/models?group_status=all&page_size=100",
		"192.0.2.90:1234",
		"Bearer "+created.Key,
		nil,
	)
	if recorder.Code != http.StatusOK {
		t.Fatalf("AccessKey models = %d %s, want 200", recorder.Code, recorder.Body.String())
	}
	var envelope struct {
		Data ProjectModelListResponse `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode AccessKey models: %v", err)
	}
	if envelope.Data.Summary.ClientModelCount != 2 ||
		envelope.Data.Summary.UpstreamModelCount != 2 ||
		envelope.Data.Summary.PriceCount != 2 ||
		len(envelope.Data.Items) != 2 ||
		envelope.Data.Items[0].ClientModel != "client-a" ||
		envelope.Data.Items[1].ClientModel != "shared" {
		t.Fatalf("AccessKey model response = %#v", envelope.Data)
	}
	shared := envelope.Data.Items[1]
	if len(shared.Protocols) != 2 || len(shared.UpstreamModels) != 1 ||
		len(shared.UpstreamModels[0].RouteGroups) != 2 ||
		len(shared.UpstreamModels[0].AffectedGroups) != 2 {
		t.Fatalf("AccessKey shared model = %#v", shared)
	}
	for _, privateValue := range []string{
		"client-hidden", "client-b", "gemini-private", "private-gemini",
		"private-disabled", "private-hidden", "private-client-b",
		"private-gemini-model", "private-disabled-model",
	} {
		if strings.Contains(recorder.Body.String(), privateValue) {
			t.Fatalf("AccessKey models expose %q: %s", privateValue, recorder.Body.String())
		}
	}
}
