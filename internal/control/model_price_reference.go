package control

import "gpt-load/internal/catalog"

// ModelPriceCatalogReferenceDTO 描述固定目录来源，缺失时仍使用已保存的最后有效价格。
type ModelPriceCatalogReferenceDTO struct {
	ProviderID   string `json:"provider_id"`
	ProviderName string `json:"provider_name"`
	ModelID      string `json:"model_id"`
	ModelName    string `json:"model_name"`
	Available    bool   `json:"available"`
}

func lookupCatalogPrice(snapshot *catalog.Snapshot, providerID, modelID string) (catalog.Model, bool) {
	if snapshot == nil {
		return catalog.Model{}, false
	}
	model, exists := snapshot.Providers[providerID].Models[modelID]
	if !exists || model.Cost == nil {
		return model, false
	}
	cost := model.Cost
	return model, cost.Prices.Input.Set || cost.Prices.Output.Set || cost.Prices.CacheRead.Set ||
		cost.Prices.CacheWrite.Set || len(cost.ContextTiers) > 0 || len(cost.ModePrices) > 0
}

func projectCatalogPriceReference(snapshot *catalog.Snapshot, providerID, modelID string) *ModelPriceCatalogReferenceDTO {
	if providerID == "" {
		return nil
	}
	model, available := lookupCatalogPrice(snapshot, providerID, modelID)
	name := model.Name
	if name == "" {
		name = modelID
	}
	return &ModelPriceCatalogReferenceDTO{
		ProviderID: providerID, ProviderName: pricingSourceName(snapshot, providerID, providerID),
		ModelID: modelID, ModelName: name, Available: available,
	}
}
