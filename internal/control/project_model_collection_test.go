package control

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"gpt-load/internal/catalog"
	"gpt-load/internal/channel"
	"gpt-load/internal/platform/config"
	"gpt-load/internal/pricing"
	"gpt-load/internal/protocol"
	"gpt-load/internal/storage/models"
)

func TestProjectModelsSeparateSameUpstreamModelByChannelAndDetail(t *testing.T) {
	fixture := newServiceFixture(t)
	createPriceTestGroup(t, fixture.db, models.Group{
		Name: "enabled", ChannelID: string(channel.OpenAICompatible), Params: models.JSON(`{"base_url":"https://enabled.example/v1"}`),
		Models: models.JSON(`[{"id":"upstream-x","alias":"client-a"},{"id":"upstream-x","alias":"client-b"}]`), Overrides: models.JSON(`{}`), Enabled: true,
	})
	createPriceTestGroup(t, fixture.db, models.Group{
		Name: "disabled", ChannelID: string(channel.Anthropic), Params: models.JSON(`{"base_url":"https://disabled.example"}`),
		Models: models.JSON(`[{"id":"upstream-x","alias":"client-a"}]`), Overrides: models.JSON(`{}`), Enabled: false,
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
	if len(all.Items[0].UpstreamModels) != 2 || len(all.Items[1].UpstreamModels) != 1 {
		t.Fatalf("channel-scoped upstream overview = %#v", all.Items)
	}
	upstream := all.Items[1].UpstreamModels[0]
	if upstream.ModelID != "upstream-x" || upstream.Price.ModelID != "upstream-x" ||
		upstream.Price.ChannelID != "openai_compatible" ||
		len(upstream.RouteGroups) != 1 || len(upstream.AffectedGroups) != 1 {
		t.Fatalf("OpenAI-compatible upstream overview = %#v", upstream)
	}
	for _, group := range append(upstream.RouteGroups, upstream.AffectedGroups...) {
		if got := string(group.Params); got != `{"base_url":"https://enabled.example/v1"}` {
			t.Fatalf("admin route Group params = %s", got)
		}
	}
	encodedPrice, err := json.Marshal(upstream.Price)
	if err != nil {
		t.Fatalf("encode upstream price: %v", err)
	}
	var priceWire map[string]any
	if err := json.Unmarshal(encodedPrice, &priceWire); err != nil {
		t.Fatalf("decode upstream price: %v", err)
	}
	if priceWire["channel_name"] != "OpenAI Compatible" {
		t.Fatalf("upstream price channel name = %#v", priceWire["channel_name"])
	}
	detail, err := fixture.service.GetUpstreamModelDetail(t.Context(), upstream.Price.ID)
	if err != nil {
		t.Fatal(err)
	}
	if detail.ModelID != "upstream-x" || detail.Price.ChannelID != "openai_compatible" ||
		detail.Price.ChannelName != "OpenAI Compatible" ||
		detail.ClientModelCount != 2 || detail.GroupCount != 1 || len(detail.Associations) != 2 {
		t.Fatalf("upstream detail = %#v", detail)
	}
	if detail.Associations[0].ClientModel != "client-a" || detail.Associations[0].Group.Name != "enabled" {
		t.Fatalf("detail associations are not stable/unfiltered: %#v", detail.Associations)
	}
	encoded, err := json.Marshal(upstream.RouteGroups[0])
	if err != nil {
		t.Fatalf("encode route Group: %v", err)
	}
	var wire map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &wire); err != nil {
		t.Fatalf("decode route Group wire: %v", err)
	}
	for _, field := range []string{"channel_id", "params", "client_protocols"} {
		if _, ok := wire[field]; !ok {
			t.Fatalf("route Group wire misses %q: %s", field, encoded)
		}
	}
	for _, field := range []string{"provider_id", "upstream_url", "protocols"} {
		if _, ok := wire[field]; ok {
			t.Fatalf("route Group wire exposes legacy %q: %s", field, encoded)
		}
	}
}

func TestProjectModelsHTTPScopesAccessKeyFiltersAndRelationships(t *testing.T) {
	initControlI18n(t)
	fixture := newServiceFixture(t)
	allowedOpenAI := createPriceTestGroup(t, fixture.db, models.Group{
		Name: "allowed-openai", ChannelID: string(channel.OpenAICompatible), Params: models.JSON(`{"base_url":"https://openai.example/v1"}`),
		Models: models.JSON(`[
			{"id":"upstream-a","alias":"client-a"},
			{"id":"private-hidden","alias":"client-hidden"},
			{"id":"upstream-shared","alias":"shared"}
		]`),
		Overrides: models.JSON(`{}`), Enabled: true,
	})
	allowedAzure := createPriceTestGroup(t, fixture.db, models.Group{
		Name: "allowed-azure", ChannelID: string(channel.AzureOpenAI), Params: models.JSON(`{"endpoint":"https://azure.example"}`),
		Models: models.JSON(`[
			{"id":"private-client-b","alias":"client-b"},
			{"id":"upstream-shared","alias":"shared"}
		]`),
		Overrides: models.JSON(`{}`), Enabled: true,
	})
	allowedVertex := createPriceTestGroup(t, fixture.db, models.Group{
		Name: "allowed-vertex", ChannelID: string(channel.GoogleVertex),
		Params:    models.JSON(`{"project_id":"project-sentinel","location":"us-central1"}`),
		Models:    models.JSON(`[{"id":"upstream-shared","alias":"shared"}]`),
		Overrides: models.JSON(`{}`), Enabled: true,
	})
	createPriceTestGroup(t, fixture.db, models.Group{
		Name: "private-gemini", ChannelID: string(channel.Gemini), Params: models.JSON(`{"base_url":"https://gemini.example/v1beta"}`),
		Models:    models.JSON(`[{"id":"private-gemini-model","alias":"gemini-private"}]`),
		Overrides: models.JSON(`{}`), Enabled: true,
	})
	disabled := createPriceTestGroup(t, fixture.db, models.Group{
		Name: "private-disabled", ChannelID: string(channel.OpenAICompatible), Params: models.JSON(`{"base_url":"https://disabled.example/v1"}`),
		Models:    models.JSON(`[{"id":"private-disabled-model","alias":"client-a"}]`),
		Overrides: models.JSON(`{}`), Enabled: false,
	})
	if err := fixture.db.Model(&disabled).Update("enabled", false).Error; err != nil {
		t.Fatalf("disable private Group: %v", err)
	}
	mustEnsureInitialPrices(t, fixture)
	created, err := fixture.service.CreateAccessKey(t.Context(), AccessKeyCreateRequest{
		Name: "model viewer",
		Filters: &AccessKeyFilters{
			Groups: []uint{allowedOpenAI.ID, allowedAzure.ID, allowedVertex.ID},
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
		envelope.Data.Summary.UpstreamModelCount != 4 ||
		envelope.Data.Summary.PriceCount != 4 ||
		len(envelope.Data.Items) != 2 ||
		envelope.Data.Items[0].ClientModel != "client-a" ||
		envelope.Data.Items[1].ClientModel != "shared" {
		t.Fatalf("AccessKey model response = %#v", envelope.Data)
	}
	shared := envelope.Data.Items[1]
	if len(shared.Protocols) != 2 || len(shared.UpstreamModels) != 3 {
		t.Fatalf("AccessKey shared model = %#v", shared)
	}
	for _, upstream := range shared.UpstreamModels {
		if len(upstream.RouteGroups) != 1 || len(upstream.AffectedGroups) != 1 {
			t.Fatalf("channel-scoped shared upstream = %#v", upstream)
		}
		for _, group := range append(upstream.RouteGroups, upstream.AffectedGroups...) {
			if got := string(group.Params); got != `{}` {
				t.Fatalf("AccessKey route Group params = %s, want empty object", got)
			}
		}
	}
	for _, privateValue := range []string{
		"base_url", "https://openai.example/v1",
		"endpoint", "https://azure.example",
		"project_id", "project-sentinel", "location", "us-central1",
		"client-hidden", "client-b", "gemini-private", "private-gemini",
		"private-disabled", "private-hidden", "private-client-b",
		"private-gemini-model", "private-disabled-model",
	} {
		if strings.Contains(recorder.Body.String(), privateValue) {
			t.Fatalf("AccessKey models expose %q: %s", privateValue, recorder.Body.String())
		}
	}
}

func TestProjectModelCatalogReferenceUsesTheRecordedPriceProviderAndSource(t *testing.T) {
	snapshot := &catalog.Snapshot{Providers: map[string]catalog.Provider{
		"openai": {ID: "openai", Models: map[string]catalog.Model{
			"shared": {
				ID: "shared", Name: "OpenAI metadata",
				Cost: &catalog.ModelCost{Prices: pricing.Prices{Input: priceTestValue(1)}},
			},
		}},
		"anthropic": {ID: "anthropic", Models: map[string]catalog.Model{
			"shared": {
				ID: "shared", Name: "Anthropic priced metadata",
				Cost: &catalog.ModelCost{Prices: pricing.Prices{Input: priceTestValue(2)}},
			},
		}},
	}}

	tests := []struct {
		name          string
		channelID     channel.ID
		providerID    string
		matchSource   ModelPriceMatchSource
		wantSource    string
		wantModelName string
	}{
		{
			name: "channel catalog provider", channelID: channel.OpenAI,
			providerID: "openai", matchSource: ModelPriceMatchSourceChannelCatalogProvider,
			wantSource: "actual_provider", wantModelName: "OpenAI metadata",
		},
		{
			name: "priority fallback", channelID: channel.OpenAICompatible,
			providerID: "openai", matchSource: ModelPriceMatchSourceProviderPriorityFallback,
			wantSource: "reference_provider", wantModelName: "OpenAI metadata",
		},
		{
			name: "recorded provider wins over catalog priority", channelID: channel.OpenAICompatible,
			providerID: "anthropic", matchSource: ModelPriceMatchSourceProviderPriorityFallback,
			wantSource: "reference_provider", wantModelName: "Anthropic priced metadata",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			reference := projectModelCatalogReference(
				ModelPriceDTO{MatchedProviderID: &test.providerID, MatchSource: &test.matchSource},
				pricing.Identity{ChannelID: string(test.channelID), ModelID: "shared"},
				snapshot,
			)
			if reference == nil || reference.ProviderID != test.providerID ||
				reference.Source != test.wantSource || reference.Model.Name != test.wantModelName {
				t.Fatalf("catalog reference = %#v", reference)
			}
		})
	}
}
