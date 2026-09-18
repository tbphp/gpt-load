package control

import (
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"gpt-load/internal/channel"
	"gpt-load/internal/outboundproxy"
)

// 核对后端公开 JSON 字段与经典版读取契约，不执行前端或浏览器测试。
// 包含 omitempty 字段，防止只在有账号、额度、错误等数据时才暴露兼容回归。
func TestSharedAPIResponseFieldsMatchClassicContracts(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		resource string
		fields   string
		response any
	}{
		{"channels", "listFields", ChannelListResponse{}},
		{"channels", "channelFields", ChannelListItem{}},
		{"channels", "fieldFields", channel.FieldDescriptor{}},
		{"channels", "connectionFields", channel.ConnectionDescriptor{}},
		{"channels", "capabilityFields", channel.CapabilityDescriptor{}},
		{"channels", "noticeFields", channel.NoticeDescriptor{}},
		{"channels", "routeFields", channel.RouteDescriptor{}},
		{"groups", "groupCollectionFields", GroupCollectionResponse{}},
		{"groups", "groupCollectionSummaryFields", GroupCollectionSummary{}},
		{"groups", "groupCollectionItemFields", GroupCollectionItem{}},
		{"groups", "groupCollectionPaginationFields", GroupCollectionPagination{}},
		{"groups", "credentialCountFields", GroupCollectionCredentialCounts{}},
		{"groups", "groupSummaryFields", GroupSummaryResponse{}},
		{"groups", "groupSettingsFields", GroupSettingsResponse{}},
		{"groups", "runtimeSettingFields", GroupEffectiveConfigResponse{}},
		{"groups", "groupModelsFields", GroupModelsResponse{}},
		{"groups", "groupModelItemFields", GroupModelResponse{}},
		{"groups", "groupOptionFields", GroupOption{}},
		{"credentials", "credentialCollectionFields", CredentialCollectionResponse{}},
		{"credentials", "credentialSummaryFields", CredentialSummaryResponse{}},
		{"credentials", "credentialItemFields", CredentialItemResponse{}},
		{"credentials", "credentialDetailFields", CredentialDetailResponse{}},
		{"credentials", "credentialDailyUsageFields", CredentialDailyUsageResponse{}},
		{"credentials", "credentialRecoveryFields", CredentialRecoveryResponse{}},
		{"credentials", "credentialPaginationFields", CredentialPaginationResponse{}},
		{"credentials", "credentialBatchFields", CredentialBatchResponse{}},
		{"credentials", "credentialTestResultFields", CredentialProbeResponse{}},
		{"credentials", "accountFields", CredentialAccountResponse{}},
		{"credentials", "observationFields", CredentialObservationResponse{}},
		{"credentials", "observationSnapshotFields", CredentialObservationSnapshot{}},
		{"credentials", "planFields", ObservationPlanSummary{}},
		{"credentials", "observationAccountFields", ObservationAccountSummary{}},
		{"credentials", "quotaWindowFields", ObservationQuotaWindow{}},
		{"credentials", "observedWindowUsageFields", ObservationWindowUsage{}},
		{"credentials", "resetCreditFields", ObservationResetCredit{}},
		{"credentials", "resetCreditConsumeFields", ResetCreditConsumeResponse{}},
		{"credential-stages", "stageFields", CredentialStageResult{}},
		{"credential-stages", "accountFields", CredentialStageAccount{}},
		{"access-keys", "metadataFields", AccessKeyMetadata{}},
		{"access-keys", "optionFields", AccessKeyOption{}},
		{"access-keys", "collectionFields", AccessKeyCollectionResponse{}},
		{"access-keys", "collectionSummaryFields", AccessKeyCollectionSummary{}},
		{"access-keys", "collectionItemFields", AccessKeyCollectionItem{}},
		{"access-keys", "collectionPaginationFields", AccessKeyCollectionPagination{}},
		{"access-keys", "costLimitRuleFields", AccessKeyCostLimitRule{}},
		{"access-keys", "costLimitRuleStatusFields", AccessKeyCostLimitRuleStatus{}},
		{"access-keys", "costLimitStatusFields", AccessKeyCostLimitStatus{}},
		{"home", "homeBaseFields", homeResponse{}},
		{"home", "inventoryFields", HomeInventory{}},
		{"home", "accessKeyFields", HomeAccessKey{}},
		{"home", "statisticsFields", homeStatisticsResponse{}},
		{"home", "summaryFields", homeStatisticsAggregateResponse{}},
		{"home", "trendPointFields", homeStatisticsSeriesResponse{}},
		{"home", "rankingsFields", homeStatisticsRankingsResponse{}},
		{"home", "statisticsRefFields", homeStatisticsRefResponse{}},
		{"home", "modelRankingFields", homeModelRankingResponse{}},
		{"home", "groupRankingFields", homeGroupRankingResponse{}},
		{"home", "accessKeyRankingFields", homeAccessKeyRankingResponse{}},
		{"home", "subscriptionAccountsFields", HomeSubscriptionAccountsResponse{}},
		{"home", "subscriptionAccountFields", HomeSubscriptionAccountResponse{}},
		{"home", "subscriptionCapabilitiesFields", channel.CapabilityDescriptor{}},
		{"health", "healthFields", runtimeHealthResponse{}},
		{"health", "countFields", healthCountsResponse{}},
		{"health", "problemCredentialFields", healthProblemCredentialResponse{}},
		{"health", "quotaCredentialFields", healthQuotaCredentialResponse{}},
		{"health", "expiringResetCreditFields", healthExpiringResetCreditResponse{}},
		{"health", "blockedAccessKeyFields", healthAccessKeyCostLimitResponse{}},
		{"health", "requestLogFields", requestLogHealthResponse{}},
		{"request-logs", "itemFields", requestLogItemResponse{}},
		{"usage", "aggregateFields", usageAggregateResponse{}},
		{"usage", "distributionAggregateFields", usageDistributionAggregateResponse{}},
		{"usage", "reportFields", usageResponse{}},
		{"models", "collectionFields", ProjectModelListResponse{}},
		{"models", "clientModelFields", ProjectModelDTO{}},
		{"models", "upstreamModelFields", ProjectUpstreamModelDTO{}},
		{"models", "upstreamDetailFields", UpstreamModelDetailDTO{}},
		{"models", "associationFields", UpstreamModelAssociationDTO{}},
		{"models", "catalogReferenceFields", ProjectModelCatalogReferenceDTO{}},
		{"models", "groupFields", ProjectModelGroupDTO{}},
		{"models", "catalogModelFields", ProjectModelCatalogDTO{}},
		{"models", "modalitiesFields", ProjectModelModalitiesDTO{}},
		{"models", "limitsFields", ProjectModelLimitsDTO{}},
		{"models", "capabilitiesFields", ProjectModelCapabilitiesDTO{}},
		{"models", "paginationFields", ProjectModelPaginationDTO{}},
		{"models", "summaryFields", ProjectModelSummaryDTO{}},
		{"models", "catalogFields", ProjectModelCatalogStatusDTO{}},
		{"model-prices", "collectionFields", ModelPriceListResponse{}},
		{"model-prices", "paginationFields", ModelPricePaginationDTO{}},
		{"model-prices", "itemFields", ModelPriceDTO{}},
		{"model-prices", "priceFields", PriceSlotsDTO{}},
		{"model-prices", "contextTierFields", ModelPriceContextTierDTO{}},
		{"model-prices", "modeScheduleFields", ModelPriceScheduleDTO{}},
		{"providers", "modelCandidateFields", ModelCandidate{}},
		{"providers", "catalogSyncStatusFields", CatalogSyncStatus{}},
		{"settings", "settingsFields", SettingsResponse{}},
		{"settings", "settingsValueFields", SettingsValuesResponse{}},
		{"proxy", "proxyViewFields", outboundproxy.View{}},
		{"system-update", "systemUpdateFields", systemUpdateResponse{}},
		{"system-update", "releaseUpdateFields", releaseUpdateResponse{}},
	} {
		t.Run(test.resource+"/"+test.fields, func(t *testing.T) {
			path := filepath.Join("web", "src", "frontends", "classic", "app", "resources", test.resource+".ts")
			allowed := readUIContractArray(t, readUIContractFile(t, path), test.fields)
			for _, field := range reflect.VisibleFields(reflect.TypeOf(test.response)) {
				name := strings.Split(field.Tag.Get("json"), ",")[0]
				if name == "-" || !field.IsExported() || (field.Anonymous && name == "") {
					continue
				}
				if name == "" {
					name = field.Name
				}
				if !allowed[name] {
					t.Errorf("shared API %T field %q is rejected by classic %s", test.response, name, test.fields)
				}
			}
		})
	}
}

func TestSharedAPIOperationsMatchBothUIContracts(t *testing.T) {
	t.Parallel()
	content := readUIContractFile(t, filepath.Join("internal", "execution", "contracts.go"))
	operations := regexp.MustCompile(`Operation\w+\s+Operation\s*=\s*"([^"]+)"`).FindAllStringSubmatch(content, -1)
	if len(operations) == 0 {
		t.Fatal("execution operation contract is empty")
	}
	for _, test := range []struct {
		path  string
		array string
	}{
		{"frontends/classic/app/resources/channels.ts", "operations"},
		{"frontends/classic/app/resources/request-logs.ts", "operations"},
		{"frontends/modern/api/logs.ts", "logOperations"},
	} {
		t.Run(test.path, func(t *testing.T) {
			allowed := readUIContractArray(t, readUIContractFile(t, filepath.Join("web", "src", test.path)), test.array)
			for _, operation := range operations {
				if !allowed[operation[1]] {
					t.Errorf("shared API operation %q is rejected by %s", operation[1], test.path)
				}
			}
		})
	}
}

func readUIContractFile(t *testing.T, path string) string {
	t.Helper()
	content, err := os.ReadFile(filepath.Join("..", "..", path))
	if err != nil {
		t.Fatal(err)
	}
	return string(content)
}

func readUIContractArray(t *testing.T, content, name string) map[string]bool {
	t.Helper()
	pattern := `(?s)\bconst\s+` + regexp.QuoteMeta(name) + `\s*=\s*\[(.*?)\]\s*as const`
	match := regexp.MustCompile(pattern).FindStringSubmatch(content)
	if match == nil {
		t.Fatalf("UI contract array %q not found", name)
	}
	values := make(map[string]bool)
	for _, value := range regexp.MustCompile(`'([^']+)'|\.\.\.(\w+)`).FindAllStringSubmatch(match[1], -1) {
		if value[1] != "" {
			values[value[1]] = true
			continue
		}
		for field := range readUIContractArray(t, content, value[2]) {
			values[field] = true
		}
	}
	return values
}
